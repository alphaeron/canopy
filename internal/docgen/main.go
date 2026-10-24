// Command docgen builds Canopy's documentation from its two sources, the
// Markdown under docs/ (with the README, CONTRIBUTING.md, and the developer
// guides beside the code) and the CLI's cobra command tree:
//
//   - man/man1, man5, man7: gzipped man pages, one per command, plus
//     canopy-rules(5), canopy-config(5), and canopy-approval(7) from docs/.
//   - completions/: bash, zsh, fish, and PowerShell completion scripts.
//   - site/: the static HTML documentation site published to GitHub Pages,
//     styled with the review page's theme (internal/reviewview/theme.css).
//
// The release (.goreleaser.yaml) packages the man pages and completions for
// each platform, and .github/workflows/pages.yml publishes the site. Run it
// from the repository root:
//
//	go run ./internal/docgen [-root .] [-out build/docs] [-version dev] [-date 2026-01-02T15:04:05Z]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/alphaeron/canopy/internal/cli"
)

func main() {
	root := flag.String("root", ".", "repository root holding the Markdown sources")
	out := flag.String("out", filepath.Join("build", "docs"), "output directory")
	version := flag.String("version", "dev", "version printed in the man pages")
	date := flag.String("date", "", "man page date, RFC 3339 (default $SOURCE_DATE_EPOCH, else now)")
	flag.Parse()

	when, err := manDate(*date, os.Getenv("SOURCE_DATE_EPOCH"), time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(2)
	}
	g := generator{Root: *root, Out: *out, Version: *version, Date: when, Cmd: cli.Command()}
	if err := g.run(); err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

// manDate picks the man pages' date: the -date flag, else
// SOURCE_DATE_EPOCH (the reproducible-builds convention), else now.
func manDate(flagValue, epoch string, now time.Time) (time.Time, error) {
	switch {
	case flagValue != "":
		t, err := time.Parse(time.RFC3339, flagValue)
		if err != nil {
			return time.Time{}, fmt.Errorf("-date: %w", err)
		}
		return t.UTC(), nil
	case epoch != "":
		sec, err := strconv.ParseInt(epoch, 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("SOURCE_DATE_EPOCH: %w", err)
		}
		return time.Unix(sec, 0).UTC(), nil
	}
	return now.UTC(), nil
}
