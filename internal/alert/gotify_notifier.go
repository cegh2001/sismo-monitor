package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// GotifyNotifier implements Notifier with rate-limiting and Gotify API integration.
type GotifyNotifier struct {
	serverURL         string
	appToken          string
	client            *http.Client
	queue             chan Alert
	logger            func(string, ...interface{})
	rateLimitInterval time.Duration
}

// NewGotifyNotifier initializes a GotifyNotifier.
func NewGotifyNotifier(serverURL, appToken string, logger func(string, ...interface{})) *GotifyNotifier {
	if serverURL == "" {
		serverURL = "http://localhost:8383"
	}
	serverURL = strings.TrimRight(serverURL, "/")
	return &GotifyNotifier{
		serverURL:         serverURL,
		appToken:          appToken,
		client:            &http.Client{Timeout: 10 * time.Second},
		queue:             make(chan Alert, 100),
		logger:            logger,
		rateLimitInterval: 10 * time.Second,
	}
}

// Notify queues an alert for rate-limited delivery.
func (n *GotifyNotifier) Notify(ctx context.Context, alert Alert) error {
	select {
	case n.queue <- alert:
		if n.logger != nil {
			n.logger("Alert queued for Gotify: %s (Level: %s, Mag: %.1f)", alert.Sismo.Location, alert.Level, alert.Sismo.Magnitude)
		}
		return nil
	default:
		return fmt.Errorf("gotify queue full, dropping alert")
	}
}

// SendNow dispatches an alert immediately, bypassing the queue.
func (n *GotifyNotifier) SendNow(alert Alert) error {
	return n.send(alert)
}

// Start runs the consumption loop for Gotify notifications.
func (n *GotifyNotifier) Start(ctx context.Context) {
	if n.logger != nil {
		n.logger("Gotify client started. ServerURL: %s, AppToken configured: %t", n.serverURL, n.appToken != "")
	}

	var lastSent time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-n.queue:
			elapsed := time.Since(lastSent)
			if elapsed < n.rateLimitInterval {
				remaining := n.rateLimitInterval - elapsed
				select {
				case <-ctx.Done():
					return
				case <-time.After(remaining):
				}
			}

			err := n.send(alert)
			if err != nil && n.logger != nil {
				n.logger("Error sending Gotify notification: %v", err)
			}
			lastSent = time.Now()
		}
	}
}

type gotifyMessagePayload struct {
	Title    string                 `json:"title"`
	Message  string                 `json:"message"`
	Priority int                    `json:"priority"`
	Extras   map[string]interface{} `json:"extras,omitempty"`
}

func (n *GotifyNotifier) send(alert Alert) error {
	var etaText string
	if alert.Sismo.Distance > 0 {
		now := time.Now()
		pArrival := alert.Sismo.PWaveArrivalTime()
		sArrival := alert.Sismo.SWaveArrivalTime()

		pStr := "ya llegó"
		if pArrival.After(now) {
			pStr = fmt.Sprintf("en %ds", int(pArrival.Sub(now).Seconds()))
		}
		sStr := "ya llegó"
		if sArrival.After(now) {
			sStr = fmt.Sprintf("en %ds", int(sArrival.Sub(now).Seconds()))
		}
		etaText = fmt.Sprintf("\n[EEW] ETA Onda P: %s | ETA Onda S: %s", pStr, sStr)
	}

	if n.serverURL == "" || n.appToken == "" {
		isSimulated := strings.Contains(alert.Sismo.Source, "Simulation") ||
			alert.Sismo.Source == "TUI-Manual" ||
			strings.HasPrefix(alert.Sismo.ID, "sim-")
		if n.logger != nil && isSimulated {
			var etas string
			if alert.Sismo.Distance > 0 {
				etas = etaText
			}
			n.logger("[MOCK GOTIFY ALERT] Level: %s | Mag: %.1f | Location: %s | Dist: %.1fkm%s (Credentials missing, notification skipped)",
				alert.Level, alert.Sismo.Magnitude, alert.Sismo.Location, alert.Sismo.Distance, strings.ReplaceAll(etas, "\n", " | "))
		}
		return nil
	}

	var title string
	var message string
	priority := 5

	if alert.EarlyWarning {
		title = fmt.Sprintf("⚠️ [EARLY WARNING] M%.1f - %s", alert.Sismo.Magnitude, alert.Sismo.Location)
		priority = 9
		if alert.Body != "" {
			message = alert.Body
		} else {
			message = fmt.Sprintf("Magnitude: %.1f Mw\nLocation: %s\n— pending classification",
				alert.Sismo.Magnitude, alert.Sismo.Location)
		}
	} else if alert.Level == LevelInstability {
		title = "🚨 Alerta Especial: Inestabilidad Cortical"
		priority = 10
		faultName := GetFaultName(alert.Sismo.Latitude, alert.Sismo.Longitude)
		message = fmt.Sprintf("Alerta Especial: Actividad sísmica detectada en segmento previamente bloqueado. Posible deslizamiento acelerado en desarrollo (%s, cuadrante %s).\n\nMagnitude: %.1f Mw\nDepth: %.1f km\nDistance: %.1f km\nLocation: %s\nTime: %s%s",
			faultName,
			alert.Sismo.GridCell,
			alert.Sismo.Magnitude,
			alert.Sismo.Depth,
			alert.Sismo.Distance,
			alert.Sismo.Location,
			alert.Sismo.Time.Format("2006-01-02 15:04:05"),
			etaText,
		)
	} else {
		switch alert.Level {
		case LevelCritical:
			title = fmt.Sprintf("🔴 Seismic Alert: %s", alert.Level)
			priority = 8
		case LevelPreAlert, LevelSwarm:
			title = fmt.Sprintf("🟠 Seismic Alert: %s", alert.Level)
			priority = 6
		default:
			title = fmt.Sprintf("🟢 Seismic Alert: %s", alert.Level)
			priority = 4
		}

		message = fmt.Sprintf("Level: %s\nMagnitude: %.1f Mw\nDepth: %.1f km\nDistance: %.1f km\nLocation: %s\nTime: %s%s",
			alert.Level,
			alert.Sismo.Magnitude,
			alert.Sismo.Depth,
			alert.Sismo.Distance,
			alert.Sismo.Location,
			alert.Sismo.Time.Format("2006-01-02 15:04:05"),
			etaText,
		)
	}

	payload := gotifyMessagePayload{
		Title:    title,
		Message:  message,
		Priority: priority,
		Extras: map[string]interface{}{
			"client::display": map[string]string{
				"contentType": "text/plain",
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	targetURL := fmt.Sprintf("%s/message", n.serverURL)
	req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", n.appToken)

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gotify API returned status code %d", resp.StatusCode)
	}

	if n.logger != nil {
		n.logger("Gotify alert sent: %s (Mag: %.1f, Level: %s)", alert.Sismo.Location, alert.Sismo.Magnitude, alert.Level)
	}
	return nil
}

// SendSynthesisReport dispatches an LLM narrative report from Gemma via Gotify with Markdown formatting.
func (n *GotifyNotifier) SendSynthesisReport(report SynthesisResponse) error {
	if n.serverURL == "" || n.appToken == "" {
		return fmt.Errorf("gotify server URL or app token missing")
	}

	title := fmt.Sprintf("[Gemma] %s", report.ReportType)
	if report.Summary != "" {
		title = fmt.Sprintf("[Gemma] %s — %s", report.ReportType, report.Summary)
		if len(title) > 250 {
			title = title[:247] + "..."
		}
	}

	var msgBuilder strings.Builder
	msgBuilder.WriteString(report.Body)

	if len(report.Citations) > 0 {
		msgBuilder.WriteString("\n\n**Fuentes verificadas:**\n")
		for _, c := range report.Citations {
			msgBuilder.WriteString(fmt.Sprintf("- [%s](%s)\n", c.Title, c.URL))
		}
	}

	priority := 5
	if report.ReportType == ReportConfirmacion {
		priority = 8
	}

	payload := gotifyMessagePayload{
		Title:    title,
		Message:  msgBuilder.String(),
		Priority: priority,
		Extras: map[string]interface{}{
			"client::display": map[string]string{
				"contentType": "text/markdown",
			},
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	targetURL := fmt.Sprintf("%s/message", n.serverURL)
	req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Gotify-Key", n.appToken)

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gotify API returned status code %d", resp.StatusCode)
	}

	if n.logger != nil {
		n.logger("Gotify Gemma report sent: %s (%s)", report.ReportType, report.ModelUsed)
	}
	return nil
}
