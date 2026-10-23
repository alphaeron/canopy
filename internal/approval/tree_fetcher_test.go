package approval

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// mapTree is an in-memory Tree that records which paths were read, in
// sorted order (TreeFetcher reads concurrently). listedOnly paths are listed
// but cannot be read.
type mapTree struct {
	files      map[string]string
	listedOnly []string
	listErr    error

	mu   sync.Mutex
	read []string
}

func (m *mapTree) Files(context.Context) ([]string, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	paths := slices.Clone(m.listedOnly)
	for p := range m.files {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	return paths, nil
}

func (m *mapTree) ReadFile(_ context.Context, path string) ([]byte, error) {
	m.mu.Lock()
	m.read = append(m.read, path)
	slices.Sort(m.read)
	m.mu.Unlock()
	data, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
	}
	return []byte(data), nil
}

// TestTreeFetcherReadsOnlyRulesFiles pins whole-name rules file matching:
// look-alike names are neither read nor parsed.
func TestTreeFetcherReadsOnlyRulesFiles(t *testing.T) {
	tree := &mapTree{files: map[string]string{
		rulesfile.FileName:                   "- \".*\":\n  name: \"Root\"\n",
		rulesfile.PathInDir("svc"):           "- \".*\\\\.go\":\n  name: \"Svc\"\n",
		"docs/" + rulesfile.FileName + ".md": "not rules",
		"main.go":                            "package main",
	}}
	rules, err := (&TreeFetcher{Tree: tree, Log: testLogger()}).Execute(t.Context())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{rulesfile.FileName, rulesfile.PathInDir("svc")}
	if got := rules.Paths(); !reflect.DeepEqual(got, want) {
		t.Errorf("rules files = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(tree.read, want) {
		t.Errorf("read = %v, want only the rules files %v", tree.read, want)
	}
	if got := rules[rulesfile.PathInDir("svc")]; len(got) != 1 || got[0].Name != "Svc" {
		t.Errorf("svc rules = %+v, want the parsed Svc rule", got)
	}
}

// TestTreeFetcherKeepsGoodFilesWhenOneFails: a rules file that cannot be
// parsed is reported, and the others are still returned.
func TestTreeFetcherKeepsGoodFilesWhenOneFails(t *testing.T) {
	tree := &mapTree{files: map[string]string{
		rulesfile.FileName:         "- \".*\":\n  name: \"Root\"\n",
		rulesfile.PathInDir("bad"): "{not: [valid",
	}}
	rules, err := (&TreeFetcher{Tree: tree, Log: testLogger()}).Execute(t.Context())
	if err == nil {
		t.Fatal("Execute succeeded, want the parse error reported")
	}
	if got := rules.Paths(); !reflect.DeepEqual(got, []string{rulesfile.FileName}) {
		t.Errorf("rules files = %v, want the root rules file kept", got)
	}
	fe, ok := RulesFileErrors(err)[rulesfile.PathInDir("bad")]
	if !ok || fe.Op != "parse" {
		t.Fatalf("FileErrors = %v, want the bad rules file's parse error", RulesFileErrors(err))
	}
	if !strings.HasPrefix(err.Error(), "parse rules file "+rulesfile.PathInDir("bad")+": ") {
		t.Errorf("err = %q, want the review's message unchanged", err)
	}
}

func TestTreeFetcherErrorsSurface(t *testing.T) {
	listErr := errors.New("tree unavailable")
	tests := []struct {
		name string
		tree *mapTree
		want error
	}{
		{name: "listing fails", tree: &mapTree{listErr: listErr}, want: listErr},
		{name: "listed file cannot be read", tree: &mapTree{listedOnly: []string{rulesfile.FileName}}, want: fs.ErrNotExist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := (&TreeFetcher{Tree: tt.tree, Log: testLogger()}).Execute(t.Context())
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want it to wrap %v", err, tt.want)
			}
		})
	}
}
