package main

import (
	"context"
	"fmt"
	"log"

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

	// Start EMSC WebSocket consumer client
	emscClient := ingest.NewEMSCClient(tuiLog)
	go emscClient.Start(ctx, eventChan)

	// Start Funvisis HTML scraper polling loop
	funvisisErrHandler := func(err error) {
		tuiLog("Funvisis scraper warning: %v", err)
	}
	funvisisScraper := ingest.NewFunvisisScraper(tuiLog, funvisisErrHandler)
	go funvisisScraper.Start(ctx, eventChan)

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
				stats.SwarmQueueLen = len(swarmQueue.GetEvents())

				// 4. Update TUI events list
				select {
				case tuiChan <- tui.MsgSismo(s):
				default:
				}

				// 5. Trigger notifications for Critical and Pre-Alert events
				if level == alert.LevelCritical || level == alert.LevelPreAlert {
					tuiLog("CRITICAL/PRE-ALERT detected: Dispatching notification for %s...", s.Location)
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
