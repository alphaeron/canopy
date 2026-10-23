package tui

import (
	"context"
	"time"
)

// Options configures Run.
type Options struct {
	// Routes populate the home page and the command palette.
	Routes []Route
	// Start, when non-nil, is opened on top of the home page: this is how
	// a subcommand deep-links into its own page. Back returns home.
	Start *Route
	// StartPage is Start's already-loaded page. Subcommands load their
	// data before starting the TUI so load errors surface as ordinary CLI
	// errors; when nil, Start is loaded in the background instead.
	StartPage Page
	// Overview, when set, describes the repository on the home page's
	// root row. It runs off the UI loop.
	Overview func(ctx context.Context) (Detail, error)

	// previewDelay overrides how long the home page waits for the cursor
	// to rest before previewing; tests shorten it.
	previewDelay time.Duration
}
