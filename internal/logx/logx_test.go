package logx

import (
	"io"
	"log/slog"
	"testing"
)

func TestOrDefault(t *testing.T) {
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name string
		in   *slog.Logger
		want *slog.Logger
	}{
		{name: "nil falls back to default", in: nil, want: slog.Default()},
		{name: "non-nil is returned as is", in: custom, want: custom},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := OrDefault(tt.in); got != tt.want {
				t.Errorf("OrDefault(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
