package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Notifier defines the interface for dispatching alerts.
type Notifier interface {
	Notify(ctx context.Context, alert Alert) error
	Start(ctx context.Context)
}

// PushoverNotifier implements Notifier with rate-limiting and Pushover API integration.
type PushoverNotifier struct {
	appToken string
	userKey  string
	apiURL   string
	client   *http.Client
	queue    chan Alert
	logger   func(string, ...interface{})
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
		appToken: appToken,
		userKey:  userKey,
		apiURL:   "https://api.pushover.net/1/messages.json",
		client:   &http.Client{Timeout: 10 * time.Second},
		queue:    make(chan Alert, 100),
		logger:   logger,
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
// enforcing a rate limit of at least 10 seconds between API dispatches.
func (n *PushoverNotifier) Start(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	if n.logger != nil {
		n.logger("Pushover client started. AppToken configured: %t, UserKey configured: %t", n.appToken != "", n.userKey != "")
	}

	for {
		select {
		case <-ctx.Done():
			return
		case alert := <-n.queue:
			err := n.send(alert)
			if err != nil && n.logger != nil {
				n.logger("Error sending Pushover notification: %v", err)
			}
			
			// Rate limiting: sleep until ticker ticks to ensure at least 10s between notifications
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}

func (n *PushoverNotifier) send(alert Alert) error {
	if n.appToken == "" || n.userKey == "" {
		if n.logger != nil {
			n.logger("[MOCK PUSHOVER ALERT] Level: %s | Mag: %.1f | Location: %s | Dist: %.1fkm (Credentials missing, notification skipped)",
				alert.Level, alert.Sismo.Magnitude, alert.Sismo.Location, alert.Sismo.Distance)
		}
		return nil
	}

	apiURL := n.apiURL
	title := fmt.Sprintf("Seismic Alert Venezuelan Region: %s", alert.Level)
	message := fmt.Sprintf("Level: %s\nMagnitude: %.1f Mw\nDepth: %.1f km\nDistance: %.1f km\nLocation: %s\nTime: %s",
		alert.Level,
		alert.Sismo.Magnitude,
		alert.Sismo.Depth,
		alert.Sismo.Distance,
		alert.Sismo.Location,
		alert.Sismo.Time.Format("2006-01-02 15:04:05"),
	)

	data := url.Values{}
	data.Set("token", n.appToken)
	data.Set("user", n.userKey)
	data.Set("title", title)
	data.Set("message", message)

	priority := "0"
	if alert.Level == LevelCritical {
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
