package ingest

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/gorilla/websocket"
	"sismo-monitor/internal/alert"
	"sismo-monitor/internal/geo"
	"sismo-monitor/internal/log"
)


const (
	writeWait  = 10 * time.Second
	readWait   = 60 * time.Second
	pingPeriod = (readWait * 9) / 10
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
//
// Every in-bounds event is sent unconditionally to `out` (the main
// coordinator pipeline). When `fastOut` is non-nil, the same event is also
// sent to the fast-path channel using a non-blocking `select` with a default
// branch — if the fast-path consumer is slow, the event is dropped from the
// fast path with a [FASTPATH] log line, but still reaches the main pipeline.
// Passing `fastOut = nil` disables the fast-path dispatch (used when
// EMSC_FASTPATH_ENABLED=false).
func (c *EMSCClient) Start(ctx context.Context, out chan<- alert.Sismo, fastOut chan<- alert.Sismo) {
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

		// Configure deadlines and pong handler
		conn.SetReadDeadline(time.Now().Add(readWait))
		conn.SetPongHandler(func(string) error {
			conn.SetReadDeadline(time.Now().Add(readWait))
			return nil
		})

		// Connection-specific context to stop the ping goroutine when the connection exits
		connCtx, connCancel := context.WithCancel(ctx)

		// Run ping worker goroutine
		go func() {
			ticker := time.NewTicker(pingPeriod)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					// Send ping control message
					if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
						return
					}
				case <-connCtx.Done():
					return
				}
			}
		}()

		// Run message reading loop
		errChan := make(chan error, 1)
		go func() {
			defer connCancel()
			defer conn.Close()
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					errChan <- err
					return
				}

				// Reset read deadline on successful message
				conn.SetReadDeadline(time.Now().Add(readWait))

				var msg alertMessage
				if err := json.Unmarshal(message, &msg); err != nil {
					c.log("EMSC JSON parsing error: %v", err)
					continue
				}

				if msg.Action != "create" && msg.Action != "update" {
					continue
				}

				sismo := c.mapMessageToSismo(msg)
				if sismo.GridCell == "OUT_OF_BOUNDS" {
					continue
				}
				select {
				case out <- sismo:
				default:
					c.log("Output queue full, dropping EMSC event %s", sismo.ID)
				}
				if fastOut != nil {
					select {
					case fastOut <- sismo:
					default:
						c.log("[FASTPATH] fastOut channel full, dropping fast-path dispatch for %s", sismo.ID)
					}
				}
			}
		}()

		select {
		case <-ctx.Done():
			c.log("Context cancelled. Closing EMSC connection.")
			conn.Close()
			connCancel()
			return
		case err := <-errChan:
			c.log("EMSC connection closed or failed: %v. Retrying...", err)
			connCancel()
		}
	}
}

func (c *EMSCClient) log(format string, args ...interface{}) {
	log.Log(c.logger, format, args...)
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

	gridCell := geo.GetGridCell(lat, lon)

	return alert.Sismo{
		ID:        "emsc-" + msg.Data.Properties.Unid,
		Source:    "EMSC",
		Magnitude: msg.Data.Properties.Mag,
		Depth:     msg.Data.Properties.Depth,
		Latitude:  lat,
		Longitude: lon,
		Location:  location,
		Time:      t,
		Distance:  dist,
		GridCell:  gridCell,
	}
}
