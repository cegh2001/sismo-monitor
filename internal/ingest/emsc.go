package ingest

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/gorilla/websocket"
	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
)

// EMSCClient handles real-time ingestion from the EMSC WebSocket feed.
type EMSCClient struct {
	url            string
	logger         func(string, ...interface{})
	reconnectDelay time.Duration // Custom initial reconnect delay for testing
}

// NewEMSCClient creates a new EMSCClient.
func NewEMSCClient(logger func(string, ...interface{})) *EMSCClient {
	return &EMSCClient{
		url:            "wss://www.seismicportal.eu/standing_order/websocket",
		logger:         logger,
		reconnectDelay: 0,
	}
}

// Start initiates the WebSocket client and listens for incoming seismic events.
// It automatically reconnects using an exponential backoff (1s up to 60s).
func (c *EMSCClient) Start(ctx context.Context, out chan<- alert.Sismo) {
	backoff := 1 * time.Second
	if c.reconnectDelay > 0 {
		backoff = c.reconnectDelay
	}
	maxBackoff := 60 * time.Second

	for {
		select {
		case <-ctx.Done():
			c.log("EMSC client loop exiting.")
			return
		default:
		}

		c.log("Connecting to EMSC WebSocket: %s", c.url)
		
		dialer := websocket.DefaultDialer
		dialer.HandshakeTimeout = 10 * time.Second
		
		conn, _, err := dialer.DialContext(ctx, c.url, nil)
		if err != nil {
			c.log("EMSC dial error: %v. Reconnecting in %v...", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}

		c.log("EMSC WebSocket connected successfully.")
		backoff = 1 * time.Second // Reset backoff on success

		// Run message reading loop
		errChan := make(chan error, 1)
		go func() {
			defer conn.Close()
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					errChan <- err
					return
				}

				var msg alertMessage
				if err := json.Unmarshal(message, &msg); err != nil {
					c.log("EMSC JSON parsing error: %v", err)
					continue
				}

				if msg.Action != "create" && msg.Action != "update" {
					continue
				}

				sismo := c.mapMessageToSismo(msg)
				select {
				case out <- sismo:
				default:
					c.log("Output queue full, dropping EMSC event %s", sismo.ID)
				}
			}
		}()

		select {
		case <-ctx.Done():
			c.log("Context cancelled. Closing EMSC connection.")
			conn.Close()
			return
		case err := <-errChan:
			c.log("EMSC connection closed or failed: %v. Retrying...", err)
		}
	}
}

func (c *EMSCClient) log(format string, args ...interface{}) {
	if c.logger != nil {
		c.logger(format, args...)
	}
}

type alertMessage struct {
	Action string `json:"action"`
	Data   struct {
		Geometry struct {
			Coordinates []float64 `json:"coordinates"` // [lon, lat]
		} `json:"geometry"`
		Properties struct {
			Unid        string  `json:"unid"`
			Time        string  `json:"time"`
			Mag         float64 `json:"mag"`
			Depth       float64 `json:"depth"`
			FlynnRegion string  `json:"flynn_region"`
			Lat         float64 `json:"lat"`
			Lon         float64 `json:"lon"`
		} `json:"properties"`
	} `json:"data"`
}

func (c *EMSCClient) mapMessageToSismo(msg alertMessage) alert.Sismo {
	lat := msg.Data.Properties.Lat
	lon := msg.Data.Properties.Lon

	// Fallback to geometry coordinates if properties don't specify lat/lon
	if len(msg.Data.Geometry.Coordinates) >= 2 {
		lon = msg.Data.Geometry.Coordinates[0]
		lat = msg.Data.Geometry.Coordinates[1]
	}

	t, err := time.Parse(time.RFC3339, msg.Data.Properties.Time)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05Z", msg.Data.Properties.Time)
		if err != nil {
			t = time.Now()
		}
	}

	dist := geo.DistanceToLaGuaira(lat, lon)
	location := msg.Data.Properties.FlynnRegion
	if location == "" {
		location = "Unknown Region (EMSC)"
	}

	return alert.Sismo{
		ID:        msg.Data.Properties.Unid,
		Source:    "EMSC",
		Magnitude: msg.Data.Properties.Mag,
		Depth:     msg.Data.Properties.Depth,
		Latitude:  lat,
		Longitude: lon,
		Location:  location,
		Time:      t,
		Distance:  dist,
	}
}
