// Package log provides a thin shared logging helper used across the sismo-monitor
// internal packages. It removes the boilerplate nil-check that was copy-pasted in
// every ingest and API client.
package log

// Logger is the callback signature used throughout the project for logging.
type Logger func(string, ...interface{})

// Log invokes logger with the given format and args if logger is non-nil.
// Use this instead of repeating the nil-guard pattern in every struct method.
func Log(logger Logger, format string, args ...interface{}) {
	if logger != nil {
		logger(format, args...)
	}
}
