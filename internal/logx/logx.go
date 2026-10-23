// Package logx holds the logging helpers shared across canopy's packages. It
// imports only the standard library, so any package may depend on it.
package logx

import "log/slog"

// OrDefault returns l, or slog.Default() when l is nil. Types whose logger is
// optional call it at the point of use, so a nil logger is never dereferenced.
func OrDefault(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
