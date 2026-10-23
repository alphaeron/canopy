package cli

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v6"

	"github.com/alphaeron/kit/internal/approval"
	"github.com/alphaeron/kit/internal/review"
	"github.com/alphaeron/kit/internal/rulesfile"
	"github.com/alphaeron/kit/internal/tui"
)

// chdirBrokenRepo makes the current directory a new git repository with a
// valid root rules file and an unparseable one in svc, and returns the
// broken one's path.
func chdirBrokenRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := git.PlainInit(root, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	bad := rulesfile.PathInDir("svc")
	for p, content := range map[string]string{
		rulesfile.FileName: "- \".*\":\n  name: \"all\"\n",
		bad:                "{not: [valid",
	} {
		disk := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(disk, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	t.Chdir(root)
	return bad
}

// TestBrokenRulesFile pins how the CLI treats a rules file that cannot be
// parsed: the workspace keeps the other rules files and reports the broken
// one (a warning, a failed badge with the error in its detail, a validation
// violation), while matching still fails as before.
func TestBrokenRulesFile(t *testing.T) {
	bad := chdirBrokenRepo(t)

	repo, engine, err := openLocal(quietLog)
	if err != nil {
		t.Fatalf("openLocal: %v", err)
	}
	s, err := loadWorkspace(t.Context(), repo, engine)
	if err != nil {
		t.Fatalf("loadWorkspace must keep the rules files that loaded: %v", err)
	}
	if _, ok := s.Rules[rulesfile.FileName]; !ok || len(s.Rules) != 1 {
		t.Errorf("rules = %v, want only the root rules file", s.Rules)
	}
	if fe, ok := errors.AsType[*approval.RulesFileError](s.Broken[bad]); !ok || fe.Op != "parse" {
		t.Fatalf("broken = %v, want %s's parse error", s.Broken, bad)
	}

	var warning strings.Builder
	if err := warnBroken(&warning, s); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(warning.String(), "warning: skipping "+bad+",") || strings.Count(warning.String(), "\n") != 1 {
		t.Errorf("warning = %q", warning.String())
	}
	if got := brokenNote(s); got != "1 rules file skipped: cannot be read or parsed" {
		t.Errorf("brokenNote = %q", got)
	}

	desc, badge, d := rulesFileDetail(s, bad, "")
	if badge != tui.BadgeFail || desc != "rules file · parse failed, skipped" {
		t.Errorf("broken row = %q, %v; want a failed badge", desc, badge)
	}
	if f := d.Fields[len(d.Fields)-1]; f.Label != "Error" || f.Badge != tui.BadgeFail || !strings.Contains(f.Value, "parse rules file "+bad) {
		t.Errorf("broken detail must show the error: %+v", d.Fields)
	}
	if _, badge, _ := rulesFileDetail(s, rulesfile.FileName, ""); badge != tui.BadgeOK {
		t.Errorf("a loaded rules file's badge = %v, want OK", badge)
	}
	if got := s.Validate(""); len(got[bad]) != 1 {
		t.Errorf("validate = %v, want the broken file's error", got)
	}

	if _, err := matchWith(t.Context(), engine, review.Options{}); err == nil || !strings.Contains(err.Error(), "parse rules file "+bad) {
		t.Errorf("match error = %v, want it to fail on the broken rules file as before", err)
	}
}

// TestTUIDoesNotLog pins that the TUI's pages load the rules quietly, since
// a log line on stderr would tear the screen; the pages show the broken
// rules file themselves.
func TestTUIDoesNotLog(t *testing.T) {
	chdirBrokenRepo(t)
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, r := range []tui.Route{
		rulesFilesRoute(tui.Values{}),
		rulesListRoute(tui.Values{}),
		validateRoute(tui.Values{}),
		matchRoute(tui.Values{}),
		coverageRoute(tui.Values{}),
	} {
		_, _ = r.Load(t.Context(), r.Values) // match and coverage fail on the broken file
	}
	if _, err := rulesSummary(t.Context()); err != nil {
		t.Errorf("rulesSummary: %v", err)
	}
	if _, err := repoOverview(t.Context()); err != nil {
		t.Errorf("repoOverview: %v", err)
	}
	if logged.Len() > 0 {
		t.Errorf("the TUI logged:\n%s", logged.String())
	}
}
