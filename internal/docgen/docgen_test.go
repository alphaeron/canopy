package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestManDate(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name, flag, epoch string
		want              time.Time
		wantErr           bool
	}{
		{"flag wins", "2025-01-02T03:04:05Z", "0", time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), false},
		{"epoch", "", "86400", time.Unix(86400, 0).UTC(), false},
		{"now", "", "", now, false},
		{"bad flag", "yesterday", "", time.Time{}, true},
		{"bad epoch", "", "soon", time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := manDate(tt.flag, tt.epoch, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("manDate(%q, %q) error = %v, want error %v", tt.flag, tt.epoch, err, tt.wantErr)
			}
			if !got.Equal(tt.want) {
				t.Errorf("manDate(%q, %q) = %v, want %v", tt.flag, tt.epoch, got, tt.want)
			}
		})
	}
}

func TestResolveLinkHref(t *testing.T) {
	tests := []struct {
		source, dest, want string
	}{
		{"docs/index.md", "rules.md", "rules.html"},
		{"docs/rules.md", "rules/approvers.md#how-targets-are-resolved", "rules-approvers.html#how-targets-are-resolved"},
		{"docs/rules/approvers.md", "../review.md", "review.html"},
		{"docs/configuration.md", "server.md#database", "server.html#database"},
		{"docs/review.md", "#blocking-labels", "review.html#blocking-labels"},
		{"CONTRIBUTING.md", "internal/platform/README.md", "platforms.html"},
		{"internal/platform/README.md", "../../docs/server.md", "server.html"},
		{"CONTRIBUTING.md", "Makefile", repoURL + "/blob/main/Makefile"},
	}
	for _, tt := range tests {
		tgt, ok := resolveLink(tt.source, tt.dest)
		if !ok {
			t.Errorf("resolveLink(%q, %q) not resolved", tt.source, tt.dest)
			continue
		}
		if got := tgt.href(); got != tt.want {
			t.Errorf("resolveLink(%q, %q).href() = %q, want %q", tt.source, tt.dest, got, tt.want)
		}
	}
	if _, ok := resolveLink("README.md", "https://example.com/x.md"); ok {
		t.Error("an absolute URL resolved; want it left as written")
	}
}

func TestManLinks(t *testing.T) {
	in := "see [Blocking Labels](review.md#blocking-labels), [Database](server.md#database), " +
		"[here](#configuration), [Go](https://go.dev), and [make](../Makefile)."
	want := "see Blocking Labels in canopy-approval(7), [Database](" + siteURL + "server.html#database), " +
		"here, [Go](https://go.dev), and [make](" + repoURL + "/blob/main/Makefile)."
	if got := string(manLinks("docs/configuration.md", nil, []byte(in))); got != want {
		t.Errorf("manLinks =\n%s\nwant\n%s", got, want)
	}
	// Within canopy-rules(5), a link to a RULES key is to a section of the
	// same page.
	in = "see [approvers](rules/approvers.md) and [combining](review.md)"
	want = "see approvers and combining in canopy-approval(7)"
	if got := string(manLinks("docs/rules.md", rulesMan, []byte(in))); got != want {
		t.Errorf("manLinks in canopy-rules(5) = %q, want %q", got, want)
	}
}

func TestShiftHeadings(t *testing.T) {
	in := "# key\n\ntext\n\n## More\n\n```yaml\n# a YAML comment\n```\n"
	want := "### key\n\ntext\n\n#### More\n\n```yaml\n# a YAML comment\n```\n"
	if got := string(shiftHeadings([]byte(in), 2)); got != want {
		t.Errorf("shiftHeadings =\n%s\nwant\n%s", got, want)
	}
}

func TestCheckRuleKeys(t *testing.T) {
	docs := map[string][]byte{"docs/rules/approvers.md": []byte("`target` and `required_count`")}
	if err := checkRuleKeys(docs); err != nil {
		t.Fatalf("checkRuleKeys with every key documented: %v", err)
	}

	saved := pages
	t.Cleanup(func() { pages = saved })
	pages = nil
	for _, p := range saved {
		if p.Key != "self_approve" {
			pages = append(pages, p)
		}
	}
	pages = append(pages, page{Source: "docs/rules/owners.md", Key: "owners"})
	err := checkRuleKeys(map[string][]byte{"docs/rules/approvers.md": []byte("`target`")})
	if err == nil {
		t.Fatal("checkRuleKeys passed with a key undocumented")
	}
	for _, want := range []string{`"self_approve" has no page`, `documents "owners"`, `"required_count" is not documented`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkRuleKeys error does not mention %s:\n%v", want, err)
		}
	}
}

// testCommand is a small command tree standing in for the CLI's.
func testCommand() *cobra.Command {
	root := &cobra.Command{Use: "canopy", Short: "Canopy code review CLI", Run: func(*cobra.Command, []string) {}}
	rules := &cobra.Command{Use: "rules", Short: "Manage rules"}
	rules.AddCommand(&cobra.Command{Use: "add", Short: "Add a rule", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(rules)
	return root
}

func TestRun(t *testing.T) {
	gen := func(out string) {
		t.Helper()
		g := generator{Root: filepath.Join("..", ".."), Out: out, Version: "1.2.3",
			Date: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC), Cmd: testCommand()}
		if err := g.run(); err != nil {
			t.Fatal(err)
		}
	}
	out := t.TempDir()
	gen(out)

	for _, want := range []struct{ file, contains string }{
		{"man/man1/canopy.1.gz", `.TH "CANOPY" "1" "Oct 2026" "Canopy 1.2.3" "Canopy Manual"`},
		{"man/man1/canopy-rules-add.1.gz", "canopy-rules-add"},
		{"man/man5/canopy-rules.5.gz", ".TH CANOPY-RULES 5"},
		{"man/man5/canopy-rules.5.gz", ".SH KEYS"},
		{"man/man5/canopy-rules.5.gz", ".SS self_approve"},
		{"man/man5/canopy-config.5.gz", "canopy-approval(7)"},
		{"man/man7/canopy-approval.7.gz", ".SH SEE ALSO"},
	} {
		if got := gunzip(t, filepath.Join(out, want.file)); !strings.Contains(got, want.contains) {
			t.Errorf("%s does not contain %q:\n%s", want.file, want.contains, head(got))
		}
	}
	for _, f := range []string{"canopy.bash", "_canopy", "canopy.fish", "canopy.ps1"} {
		if b, err := os.ReadFile(filepath.Join(out, "completions", f)); err != nil || len(b) == 0 {
			t.Errorf("completions/%s: %v, %d bytes", f, err, len(b))
		}
	}

	site := func(file string, wants ...string) string {
		t.Helper()
		got := read(t, filepath.Join(out, "site", file))
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s does not contain %q", file, want)
			}
		}
		return got
	}
	site("index.html", `href="overview.html"`, `href="install.html"`, "--acid", "Not AI.")
	overview := site("overview.html",
		`href="rules.html"`, `href="canopy_rules_add.html"`, `href="rules-self_approve.html"`, `href="platforms.html"`,
		`<a href="#comparison-of-code-review-tooling">`, `href="install.html"><span>Next`,
		repoURL+"/edit/main/docs/index.md", `src="search.js"`)
	if m := regexp.MustCompile(`href="[^":]*\.md[#"]`).FindString(overview); m != "" {
		t.Errorf("overview.html has a relative link to a Markdown file: %s", m)
	}
	site("canopy_rules.html", `href="canopy_rules_add.html"`, "internal/cli")
	site("404.html", `<base href="`+siteURL+`">`)
	search := site("search.js", "const canopySearchIndex = [", `"u":"rules-approvers.html"`, `"i":"how-targets-are-resolved"`)
	if strings.Contains(search, "</script") {
		t.Error("search.js contains a closing script tag")
	}

	// The same inputs give the same bytes, so release artifacts reproduce.
	again := t.TempDir()
	gen(again)
	for _, f := range []string{"man/man5/canopy-rules.5.gz", "man/man1/canopy.1.gz", "site/index.html"} {
		if !bytes.Equal([]byte(read(t, filepath.Join(out, f))), []byte(read(t, filepath.Join(again, f)))) {
			t.Errorf("%s differs between identical runs", f)
		}
	}
}

func read(t *testing.T, file string) string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func gunzip(t *testing.T, file string) string {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func head(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
