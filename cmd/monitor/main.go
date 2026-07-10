package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/api"
	"sismo-monitor/internal/config"
	"sismo-monitor/internal/ingest"
	"sismo-monitor/internal/tui"
)

func main() {
	// Load configuration
	cfg := config.Load()

	// Parent context for all background goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Channel to send updates (sismos, logs, stats) to the TUI
	tuiChan := make(chan tea.Msg, 1000)

	// Thread-safe logging function mapping to TUI logs
	tuiLog := func(format string, args ...interface{}) {
		msg := fmt.Sprintf(format, args...)
		select {
		case tuiChan <- tui.MsgLog(msg):
		default:
		}
	}

	tuiLog("System initializing...")

	// Internal sismo channel for workers to push to the coordinator
	eventChan := make(chan alert.Sismo, 200)

	// Initialize Alert Engine
	swarmQueue := alert.NewSwarmQueue()
	notifier := alert.NewPushoverNotifier(cfg.PushoverAppToken, cfg.PushoverUserKey, tuiLog)

	// Start rate-limited notification loop
	go notifier.Start(ctx)

	// Initialize Gap Analyzer
	gapAnalyzer := alert.NewGapAnalyzer("data/sismos_historicos.json")
	if err := gapAnalyzer.Load(); err != nil {
		tuiLog("Failed to load historical gaps database: %v", err)
	}

	// Seed historical database if empty
	histWorker := ingest.NewUSGSHistoricalWorker(cfg.USGSHistoricalURL, tuiLog)
	dbFile := "data/sismos_historicos.json"
	needsSeed := false
	if fi, err := os.Stat(dbFile); os.IsNotExist(err) || fi.Size() <= 4 {
		needsSeed = true
	}
	if needsSeed {
		tuiLog("Historical database empty, seeding from USGS...")
		twoYearsAgo := time.Now().AddDate(-2, 0, 0)
		histSismos, err := histWorker.Fetch(ctx, twoYearsAgo)
		if err != nil {
			tuiLog("Error fetching historical data: %v", err)
		} else {
			gapAnalyzer.SetSismos(histSismos)
			if err := gapAnalyzer.Save(); err != nil {
				tuiLog("Failed to save historical data: %v", err)
			} else {
				tuiLog("Successfully seeded %d historical events.", len(histSismos))
			}
		}
	}

	// Start EMSC WebSocket consumer client
	emscClient := ingest.NewEMSCClient(tuiLog)
	go emscClient.Start(ctx, eventChan)

	// Start Funvisis HTML scraper polling loop
	funvisisErrHandler := func(err error) {
		tuiLog("Funvisis scraper warning: %v", err)
	}
	funvisisScraper := ingest.NewFunvisisScraper(tuiLog, funvisisErrHandler)
	go funvisisScraper.Start(ctx, eventChan)

	// Start USGS real-time client
	usgsClient := ingest.NewUSGSClient(cfg.USGSRealtimeURL, tuiLog, func(err error) {
		tuiLog("USGS client warning: %v", err)
	})
	go usgsClient.Start(ctx, eventChan)

	// Start HTTP API Simulation server
	simServer := api.NewSimulationServer(cfg.Port, eventChan, tuiLog)
	go func() {
		if err := simServer.Start(ctx); err != nil {
			tuiLog("Simulation HTTP server failed: %v", err)
		}
	}()

	// Statistics state
	stats := tui.MsgStats{}

	// Coordinator processing loop
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case s := <-eventChan:
				// 1. Classify threat level
				level := alert.ClassifyDanger(s)

				// 2. Check swarm condition
				isSwarm := swarmQueue.AddAndCheck(s)

				// 2.b Check seismic gap/instability condition
				isInstability, err := gapAnalyzer.Add(s)
				if err != nil {
					tuiLog("Error adding to gap analyzer: %v", err)
				}

				// 3. Update stats counter
				stats.TotalEvents++
				if s.Distance <= 300.0 {
					stats.LocalEvents++
				}
				switch s.Source {
				case "EMSC":
					stats.EmscEvents++
				case "Funvisis":
					stats.FunvisisCount++
				case "USGS":
					stats.USGSEvents++
				case "Simulation":
					stats.SimEvents++
				}
				switch level {
				case alert.LevelInfo:
					stats.InfoCount++
				case alert.LevelPreAlert:
					stats.PreAlertCount++
				case alert.LevelCritical:
					stats.CriticalCount++
				}
				if isInstability {
					stats.InstabilityCount++
					level = alert.LevelInstability
				}

				stats.SwarmQueueLen = len(swarmQueue.GetEvents())
				stats.USGSPolls = usgsClient.GetStatsCount()
				stats.ActiveGaps = len(gapAnalyzer.GetActiveLockSegments(time.Now()))

				// 4. Update TUI events list
				select {
				case tuiChan <- tui.MsgSismo(s):
				default:
				}

				// 5. Trigger notifications for Critical, Pre-Alert, and Instability events
				if level == alert.LevelCritical || level == alert.LevelPreAlert || level == alert.LevelInstability {
					tuiLog("%s detected: Dispatching notification for %s...", level, s.Location)
					_ = notifier.Notify(ctx, alert.Alert{Sismo: s, Level: level})
				}

				// 6. Trigger swarm notification if condition is met
				if isSwarm {
					stats.SwarmCount++
					tuiLog("SWARM DETECTED: >= 5 events with M >= 3.0 under 300km in last 6 hours!")
					_ = notifier.Notify(ctx, alert.Alert{Sismo: s, Level: alert.LevelSwarm})
				}

				// 7. Update TUI stats panel
				select {
				case tuiChan <- stats:
				default:
				}
			}
		}
	}()

	tuiLog("System running. Press 't' to inject critical alert, 's' to inject swarm.")

	// Start Bubbletea program (takes over terminal)
	p := tea.NewProgram(tui.NewModel(tuiChan, cfg.Port), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("Error running TUI dashboard: %v", err)
	}

	// Shutdown other goroutines when TUI exits
	cancel()
	tuiLog("System shutting down...")
}
