package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Notifier defines the interface for dispatching alerts.
type Notifier interface {
	Notify(ctx context.Context, alert Alert) error
	Start(ctx context.Context)
}

// PushoverNotifier implements Notifier with rate-limiting and Pushover API integration.
type PushoverNotifier struct {
	appToken          string
	userKey           string
	apiURL            string
	client            *http.Client
	queue             chan Alert
	logger            func(string, ...interface{})
	rateLimitInterval time.Duration
}

// NewPushoverNotifier initializes a PushoverNotifier. If token or user key is empty,
// it attempts to read from PUSHOVER_APP_TOKEN and PUSHOVER_USER_KEY.
func NewPushoverNotifier(appToken, userKey string, logger func(string, ...interface{})) *PushoverNotifier {
	if appToken == "" {
		appToken = os.Getenv("PUSHOVER_APP_TOKEN")
	}
	if userKey == "" {
		userKey = os.Getenv("PUSHOVER_USER_KEY")
	}

	return &PushoverNotifier{
		appToken:          appToken,
		userKey:           userKey,
		apiURL:            "https://api.pushover.net/1/messages.json",
		client:            &http.Client{Timeout: 10 * time.Second},
		queue:             make(chan Alert, 100),
		logger:            logger,
		rateLimitInterval: 10 * time.Second,
	}
}

// Notify queues an alert for rate-limited delivery.
func (n *PushoverNotifier) Notify(ctx context.Context, alert Alert) error {
	select {
	case n.queue <- alert:
		if n.logger != nil {
			n.logger("Alert queued for Pushover: %s (Level: %s, Mag: %.1f)", alert.Sismo.Location, alert.Level, alert.Sismo.Magnitude)
		}
		return nil
	default:
		return fmt.Errorf("pushover queue full, dropping alert")
	}
}

// Start runs the notifier consumption loop. It processes alerts from the queue,
// enforcing a rate limit of at least 10 seconds between API dispatches (configurable via rateLimitInterval).
func (n *PushoverNotifier) Start(ctx context.Context) {
	if n.logger != nil {
		n.logger("Pushover client started. AppToken configured: %t, UserKey configured: %t", n.appToken != "", n.userKey != "")
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
				n.logger("Error sending Pushover notification: %v", err)
			}
			lastSent = time.Now()
		}
	}
}

func (n *PushoverNotifier) send(alert Alert) error {
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

	if n.appToken == "" || n.userKey == "" {
		if n.logger != nil {
			var etas string
			if alert.Sismo.Distance > 0 {
				etas = etaText
			}
			n.logger("[MOCK PUSHOVER ALERT] Level: %s | Mag: %.1f | Location: %s | Dist: %.1fkm%s (Credentials missing, notification skipped)",
				alert.Level, alert.Sismo.Magnitude, alert.Sismo.Location, alert.Sismo.Distance, strings.ReplaceAll(etas, "\n", " | "))
		}
		return nil
	}

	apiURL := n.apiURL
	var title string
	var message string

	if alert.Level == LevelInstability {
		title = "Alerta Especial: Inestabilidad Cortical"
		message = fmt.Sprintf("Alerta Especial: Actividad sísmica detectada en segmento previamente bloqueado. Posible deslizamiento acelerado en desarrollo (Falla de San Sebastián/Boconó, cuadrante %s).\n\nMagnitude: %.1f Mw\nDepth: %.1f km\nDistance: %.1f km\nLocation: %s\nTime: %s%s",
			alert.Sismo.GridCell,
			alert.Sismo.Magnitude,
			alert.Sismo.Depth,
			alert.Sismo.Distance,
			alert.Sismo.Location,
			alert.Sismo.Time.Format("2006-01-02 15:04:05"),
			etaText,
		)
	} else {
		title = fmt.Sprintf("Seismic Alert Venezuelan Region: %s", alert.Level)
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

	data := url.Values{}
	data.Set("token", n.appToken)
	data.Set("user", n.userKey)
	data.Set("title", title)
	data.Set("message", message)

	priority := "0"
	if alert.Level == LevelInstability {
		priority = "2"
		data.Set("retry", "30")
		data.Set("expire", "3600")
	} else if alert.Level == LevelCritical {
		priority = "1"
	} else if alert.Level == LevelPreAlert || alert.Level == LevelSwarm {
		priority = "0"
	} else {
		priority = "-1"
	}
	data.Set("priority", priority)

	req, err := http.NewRequest("POST", apiURL, bytes.NewBufferString(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errData map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errData)
		return fmt.Errorf("pushover API returned status %d: %v", resp.StatusCode, errData)
	}

	if n.logger != nil {
		n.logger("Pushover alert sent successfully: %s (Mag: %.1f)", alert.Sismo.Location, alert.Sismo.Magnitude)
	}
	return nil
}
