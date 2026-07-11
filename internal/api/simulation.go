package api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
)

// SimulationServer exposes an HTTP endpoint to inject mock seismic events.
type SimulationServer struct {
	port        string
	out         chan<- alert.Sismo
	logger      func(string, ...interface{})
	gapAnalyzer *alert.GapAnalyzer
}

// NewSimulationServer initializes a SimulationServer.
func NewSimulationServer(port string, out chan<- alert.Sismo, gapAnalyzer *alert.GapAnalyzer, logger func(string, ...interface{})) *SimulationServer {
	return &SimulationServer{
		port:        port,
		out:         out,
		logger:      logger,
		gapAnalyzer: gapAnalyzer,
	}
}

// TestAlertPayload defines the fields accepted by POST /test-alert.
type TestAlertPayload struct {
	Magnitude       float64 `json:"magnitude"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	Depth           float64 `json:"depth"`
	Location        string  `json:"location"`
	GridCell        string  `json:"grid_cell"`
	InstabilityTest bool    `json:"instability_test"`
}

// Start starts the HTTP server. It listens for requests and shuts down gracefully when the context is cancelled.
func (s *SimulationServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/test-alert", s.handleTestAlert)

	server := &http.Server{
		Addr:    "127.0.0.1:" + s.port,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		s.log("Simulation HTTP server shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	s.log("Simulation HTTP server started on port %s", s.port)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *SimulationServer) handleTestAlert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed. Use POST.", http.StatusMethodNotAllowed)
		return
	}

	var p TestAlertPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if p.InstabilityTest {
		// 0. Purge prior simulation events from the gap analyzer to prevent
		//    false-positive cascades on repeated instability test runs.
		if s.gapAnalyzer != nil {
			s.gapAnalyzer.PurgeSimulationEvents()
		}

		// 1. Inject 5 historical events 100 days ago to lock the cell G_0_0
		pastTime := time.Now().AddDate(0, 0, -100)
		for i := 0; i < 5; i++ {
			sismoHist := alert.Sismo{
				ID:        fmt.Sprintf("sim-hist-%d-%d", pastTime.Unix(), i),
				Source:    "Simulation",
				Magnitude: 3.0,
				Depth:     10.0,
				Latitude:  10.60,
				Longitude: -66.93,
				Location:  "La Guaira Port (Hist)",
				Time:      pastTime.Add(time.Duration(i) * time.Minute),
				Distance:  1.1,
				GridCell:  "G_0_0",
			}
			s.out <- sismoHist
		}

		// Brief sleep to yield and ensure TUI/GapAnalyzer processes them
		time.Sleep(50 * time.Millisecond)

		// 2. Inject 3 recent events in G_0_0 to trigger the Instability Swarm
		now := time.Now()
		var lastSismo alert.Sismo
		for i := 0; i < 3; i++ {
			lastSismo = alert.Sismo{
				ID:        fmt.Sprintf("sim-inst-%d-%d", now.Unix(), i),
				Source:    "Simulation",
				Magnitude: 2.5 + float64(i)*0.1,
				Depth:     10.0,
				Latitude:  10.60,
				Longitude: -66.93,
				Location:  fmt.Sprintf("La Guaira Port (Instability %c)", 'A'+i),
				Time:      now.Add(time.Duration(i) * time.Second),
				Distance:  1.1,
				GridCell:  "G_0_0",
			}
			s.out <- lastSismo
		}

		s.log("Instability simulation triggered (5 hist, 3 swarm events injected).")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"sismo":  lastSismo,
		})
		return
	}

	if p.Magnitude <= 0 {
		http.Error(w, "Magnitude must be greater than 0", http.StatusBadRequest)
		return
	}

	now := time.Now()
	hashInput := fmt.Sprintf("sim-%d-%f-%f", now.UnixNano(), p.Latitude, p.Longitude)
	hasher := md5.New()
	hasher.Write([]byte(hashInput))
	eventID := "sim-" + hex.EncodeToString(hasher.Sum(nil))[:8]

	dist := geo.DistanceToLaGuaira(p.Latitude, p.Longitude)
	loc := p.Location
	if loc == "" {
		loc = "Simulation Center (Test)"
	}

	gridCell := p.GridCell
	if gridCell == "" {
		gridCell = geo.GetGridCell(p.Latitude, p.Longitude)
	}

	sismo := alert.Sismo{
		ID:        eventID,
		Source:    "Simulation",
		Magnitude: p.Magnitude,
		Depth:     p.Depth,
		Latitude:  p.Latitude,
		Longitude: p.Longitude,
		Location:  loc,
		Time:      now,
		Distance:  dist,
		GridCell:  gridCell,
	}

	select {
	case s.out <- sismo:
		s.log("Simulated event injected: Mag %.1f Mw, Dist %.1f km, Loc: %s", sismo.Magnitude, sismo.Distance, sismo.Location)
	default:
		s.log("Failed to inject simulation: event queue is full.")
		http.Error(w, "Event queue full", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"sismo":  sismo,
	})
}

func (s *SimulationServer) log(format string, args ...interface{}) {
	if s.logger != nil {
		s.logger(format, args...)
	}
}
