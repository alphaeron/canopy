package main

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	repoURL = "https://github.com/alphaeron/canopy"
	// siteURL is where .github/workflows/pages.yml publishes the site; man
	// pages link to it for documents that have no man page.
	siteURL = "https://alphaeron.github.io/canopy/"
)

// manRef names the man page a document is also published as.
type manRef struct {
	Name    string // e.g. "canopy-rules"
	Section int
	Summary string // the NAME line's description
}

func (m manRef) String() string { return fmt.Sprintf("%s(%d)", m.Name, m.Section) }

// page is one hand-written Markdown document.
type page struct {
	Source string // repository-relative, slash-separated
	Slug   string // the site page is Slug + ".html"
	Group  string // the site navigation group
	// Man is the man page the document is published as; nil for none.
	Man *manRef
	// Key is the RULES key a RULES reference page documents (see
	// checkRuleKeys); its page is also a section of rulesMan.
	Key string
}

// The navigation groups, in order.
const (
	groupGuide = "Guide"
	groupRules = "RULES reference"
	groupCLI   = "CLI reference"
	groupDev   = "Developers"
)

// rulesMan is canopy-rules(5): docs/rules.md, followed by every RULES
// reference page.
var rulesMan = &manRef{"canopy-rules", 5, "format of Canopy RULES files"}

// pages lists every hand-written document, in navigation order. A document
// added under docs/ (or a developer guide) is published once listed here;
// the README is GitHub's front page, and the site has its own (landing.tmpl).
var pages = []page{
	{Source: "docs/index.md", Slug: "overview", Group: groupGuide},
	{Source: "docs/install.md", Slug: "install", Group: groupGuide},
	{Source: "docs/rules.md", Slug: "rules", Group: groupGuide, Man: rulesMan},
	{Source: "docs/review.md", Slug: "review", Group: groupGuide,
		Man: &manRef{"canopy-approval", 7, "how a Canopy review decides who must approve a change"}},
	{Source: "docs/configuration.md", Slug: "configuration", Group: groupGuide,
		Man: &manRef{"canopy-config", 5, "Canopy configuration file and environment variables"}},
	{Source: "docs/server.md", Slug: "server", Group: groupGuide},
	{Source: "docs/rules/pattern.md", Slug: "rules-pattern", Group: groupRules, Key: "pattern"},
	{Source: "docs/rules/name.md", Slug: "rules-name", Group: groupRules, Key: "name"},
	{Source: "docs/rules/approvers.md", Slug: "rules-approvers", Group: groupRules, Key: "approvers"},
	{Source: "docs/rules/combine_strategy.md", Slug: "rules-combine_strategy", Group: groupRules, Key: "combine_strategy"},
	{Source: "docs/rules/ignore_parent_dirs.md", Slug: "rules-ignore_parent_dirs", Group: groupRules, Key: "ignore_parent_dirs"},
	{Source: "docs/rules/current_directory_only.md", Slug: "rules-current_directory_only", Group: groupRules, Key: "current_directory_only"},
	{Source: "docs/rules/self_approve.md", Slug: "rules-self_approve", Group: groupRules, Key: "self_approve"},
	{Source: "CONTRIBUTING.md", Slug: "contributing", Group: groupDev},
	{Source: "internal/platform/README.md", Slug: "platforms", Group: groupDev},
}

// manOf is the man page p appears in: its own, rulesMan for a RULES
// reference page, or nil.
func manOf(p *page) *manRef {
	if p.Key != "" {
		return rulesMan
	}
	return p.Man
}

// generator holds one run's inputs.
type generator struct {
	Root    string // repository root
	Out     string // output directory
	Version string
	Date    time.Time
	Cmd     *cobra.Command // the CLI's root command
}

func (g generator) run() error {
	docs := make(map[string][]byte, len(pages))
	for _, p := range pages {
		b, err := os.ReadFile(filepath.Join(g.Root, filepath.FromSlash(p.Source)))
		if err != nil {
			return err
		}
		docs[p.Source] = b
	}
	if err := checkRuleKeys(docs); err != nil {
		return err
	}
	// Start from empty output directories, so a page for a command or
	// document since removed is not packaged. Only docgen's own
	// subdirectories are removed, whatever -out names.
	for _, dir := range []string{"man", "completions", "site"} {
		if err := os.RemoveAll(filepath.Join(g.Out, dir)); err != nil {
			return err
		}
	}
	// Cobra stamps every generated page with today's date unless told not
	// to, which would make each build differ.
	for _, c := range commands(g.Cmd) {
		c.DisableAutoGenTag = true
	}
	if err := g.writeMan(docs); err != nil {
		return fmt.Errorf("man pages: %w", err)
	}
	if err := g.writeCompletions(); err != nil {
		return fmt.Errorf("completions: %w", err)
	}
	if err := g.writeSite(docs); err != nil {
		return fmt.Errorf("site: %w", err)
	}
	return nil
}

// commands lists root and every command beneath it that a user can run, in
// the tree's order: those cobra's own doc generators document.
func commands(root *cobra.Command) []*cobra.Command {
	out := []*cobra.Command{root}
	for _, c := range root.Commands() {
		if c.IsAvailableCommand() && !c.IsAdditionalHelpTopicCommand() {
			out = append(out, commands(c)...)
		}
	}
	return out
}

// target is where a relative Markdown link in one of pages points.
type target struct {
	Page     *page  // the document it names; nil for any other repository file
	Path     string // repository-relative path of that file
	Fragment string // "" or the heading anchor, without "#"
}

// resolveLink resolves dest, a link in the document at source, to a
// target; ok is false for an absolute URL, which is left as written.
func resolveLink(source, dest string) (t target, ok bool) {
	if strings.Contains(dest, "://") || strings.HasPrefix(dest, "mailto:") {
		return target{}, false
	}
	p, frag, _ := strings.Cut(dest, "#")
	if p == "" {
		p = source
	} else {
		p = path.Clean(path.Join(path.Dir(source), p))
	}
	for i := range pages {
		if pages[i].Source == p {
			return target{Page: &pages[i], Path: p, Fragment: frag}, true
		}
	}
	return target{Path: p, Fragment: frag}, true
}

// blobURL is a repository file's address on GitHub, for links to files
// the documentation does not publish.
func blobURL(t target) string {
	u := repoURL + "/blob/main/" + t.Path
	if t.Fragment != "" {
		u += "#" + t.Fragment
	}
	return u
}

// href is t's address from a site page: relative for a published document,
// so the site works under any base path, and on GitHub otherwise.
func (t target) href() string {
	if t.Page == nil {
		return blobURL(t)
	}
	u := t.Page.Slug + ".html"
	if t.Fragment != "" {
		u += "#" + t.Fragment
	}
	return u
}

var markdownLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)

// manLinks rewrites the relative links in a document at source, published
// in the man page in, for a man page, where no link can be followed: one to
// a document in the same man page becomes just its text, one to a document
// in another man page "<text> in <page>(<section>)", and any other an
// absolute URL that md2man prints after the text.
func manLinks(source string, in *manRef, md []byte) []byte {
	return markdownLink.ReplaceAllFunc(md, func(m []byte) []byte {
		sub := markdownLink.FindSubmatch(m)
		text, dest := string(sub[1]), string(sub[2])
		t, ok := resolveLink(source, dest)
		switch {
		case !ok:
			return m
		case t.Path == source:
			return []byte(text)
		case t.Page != nil && in != nil && manOf(t.Page) == in:
			return []byte(text)
		case t.Page != nil && manOf(t.Page) != nil:
			return fmt.Appendf(nil, "%s in %s", text, manOf(t.Page))
		case t.Page != nil:
			return fmt.Appendf(nil, "[%s](%s%s)", text, siteURL, t.href())
		}
		return fmt.Appendf(nil, "[%s](%s)", text, blobURL(t))
	})
}
