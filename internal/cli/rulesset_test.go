package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alphaeron/canopy/internal/rulesedit"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
)

// newTestWorkspace writes rules as rules files under a temp root and returns
// the workspace over them.
func newTestWorkspace(t *testing.T, rules map[string]rulesfile.Rules) *rulesedit.Workspace {
	t.Helper()
	s := &rulesedit.Workspace{Root: t.TempDir(), Rules: rules}
	for p, rs := range rules {
		disk := s.DiskPath(p)
		if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := rulesedit.Save(disk, rs); err != nil {
			t.Fatalf("save %s: %v", p, err)
		}
	}
	return s
}

// reloadRules rereads every rules file of s from disk, as each command
// and each TUI reload does, so the next change starts from what was saved.
func reloadRules(t *testing.T, s *rulesedit.Workspace) {
	t.Helper()
	for p := range s.Rules {
		s.Rules[p] = loadRules(t, s, p)
	}
}

func loadRules(t *testing.T, s *rulesedit.Workspace, p string) rulesfile.Rules {
	t.Helper()
	k, err := rulesedit.Load(s.DiskPath(p))
	if err != nil {
		t.Fatalf("load %s: %v", p, err)
	}
	return k
}

var twoRulesFiles = map[string]rulesfile.Rules{
	rulesfile.PathInDir("cmd/app"): {{Pattern: `.*\.go`, Name: "Core Backend Review"}},
	rulesfile.FileName:             {{Pattern: `.*\.go`, Name: "Security Review"}, {Pattern: `docs/.*`, Name: "Docs", Position: 1}},
	rulesfile.PathInDir("a"):       {},
}

// TestRuleTarget pins how edit and remove find their rule: by pattern,
// narrowed by --file and --position, and never by silently picking one of
// several in a script.
func TestRuleTarget(t *testing.T) {
	rules := map[string]rulesfile.Rules{
		rulesfile.PathInDir("cmd/app"): {{Pattern: `.*\.go`, Name: "Core Backend Review"}},
		rulesfile.FileName:             {{Pattern: `.*\.go`, Name: "Security Review"}, {Pattern: `docs/.*`, Name: "Docs", Position: 1}, {Pattern: `.*\.go`, Name: "Override", Position: 2}},
	}
	s := &rulesedit.Workspace{Root: t.TempDir(), Rules: rules}
	tests := []struct {
		name, file, position, pattern, want, wantErr string
		wantIs                                       error
	}{
		{name: "unique pattern", pattern: `docs/.*`, want: rulesfile.FileName + "[1]"},
		{name: "--file narrows to one", file: "cmd/app", pattern: `.*\.go`, want: rulesfile.PathInDir("cmd/app") + "[0]"},
		{name: "--file and --position", file: ".", position: "2", pattern: `.*\.go`, want: rulesfile.FileName + "[2]"},
		{name: "--file outside", file: "../elsewhere", pattern: `.*\.go`, wantErr: `--file "../elsewhere" is outside the repository`},
		{name: "bad --position", position: "-1", pattern: `.*\.go`, wantErr: `--position "-1"`},
		{name: "ambiguous in one file", file: ".", pattern: `.*\.go`, wantErr: fmt.Sprintf(`pattern ".*\\.go" matches 2 rules: %s[0] (Security Review), %s[2] (Override); pass --file and --position`, rulesfile.FileName, rulesfile.FileName)},
		{name: "ambiguous", pattern: `.*\.go`, wantErr: "matches 3 rules"},
		{name: "missing", pattern: `nope`, wantIs: rulesedit.ErrRuleNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := tui.Values{Flags: map[string]string{}}
			if tc.file != "" {
				v.Flags["file"] = tc.file
			}
			if tc.position != "" {
				v.Flags["position"] = tc.position
			}
			got, err := ruleTarget(s, v, tc.pattern, false)
			switch {
			case tc.wantIs != nil:
				if !errors.Is(err, tc.wantIs) {
					t.Errorf("error = %v, want %v", err, tc.wantIs)
				}
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}
			case err != nil || got.Location() != tc.want:
				t.Errorf("ruleTarget = %v, %v; want %s", got, err, tc.want)
			}
		})
	}
}

// TestRuleChoicesPage pins the TUI's answer to an ambiguous pattern: a tree
// of the rules files declaring it, whose rows open the command for one rule.
func TestRuleChoicesPage(t *testing.T) {
	ambiguous := &rulesedit.AmbiguousRuleError{Pattern: "x", Rules: rulesfile.Rules{
		{RulesFile: rulesfile.FileName, Position: 0, Pattern: "x", Name: "base"},
		{RulesFile: rulesfile.FileName, Position: 2, Pattern: "x", Name: "override"},
		{RulesFile: rulesfile.PathInDir("svc"), Position: 1, Pattern: "x"},
	}}
	var opened []tui.Values
	route := func(v tui.Values) tui.Route { opened = append(opened, v); return tui.Route{Values: v} }
	ruleChoicesPage(ambiguous, tui.Values{Args: []string{"x"}, Flags: map[string]string{"name": "n"}}, route)
	if len(opened) != 3 {
		t.Fatalf("routes = %+v, want one per rule", opened)
	}
	if v := opened[1]; v.Flag("file") != rulesfile.FileName || v.Flag("position") != "2" || v.Flag("name") != "n" || v.Args[0] != "x" {
		t.Errorf("the second rule's route values = %+v, want its file and position with the other values kept", v)
	}
	if got := ruleChoiceLabel(ambiguous.Rules[1]); !strings.HasPrefix(got, "[2] override: x") {
		t.Errorf("label = %q", got)
	}
}

// TestNearestRulesFile checks the add form's default rules file: the one in
// the current directory or its nearest ancestor.
func TestNearestRulesFile(t *testing.T) {
	s := &rulesedit.Workspace{Root: t.TempDir(), Rules: twoRulesFiles}
	if err := os.MkdirAll(filepath.Join(s.Root, "cmd", "app", "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	t.Chdir(filepath.Join(s.Root, "cmd", "app", "sub"))
	if got, want := nearestRulesFile(s), rulesfile.PathInDir("cmd/app"); got != want {
		t.Errorf("nearestRulesFile = %q, want %q", got, want)
	}
}
