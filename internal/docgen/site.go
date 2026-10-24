package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/russross/blackfriday/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var (
	//go:embed site.tmpl
	siteTemplate string
	//go:embed landing.tmpl
	landingTemplate string
	//go:embed search.js
	searchScript string
)

var (
	siteTmpl    = template.Must(template.New("site").Parse(siteTemplate))
	landingTmpl = template.Must(template.New("landing").Parse(landingTemplate))
)

// sitePage is one documentation page of the site.
type sitePage struct {
	Slug  string
	Group string
	Title string
	Body  template.HTML
	TOC   []heading // the page's sections, for "On this page"
	Text  string    // the page's plain text, for search
	// Edit is where the page's source is edited on GitHub; "" for a page
	// generated from the command tree.
	Edit string
}

// heading is a section of a page: a level 2 or 3 heading.
type heading struct {
	Level int    `json:"l"`
	ID    string `json:"i"`
	Text  string `json:"t"`
}

// siteData is what site.tmpl renders for one page.
type siteData struct {
	Page       sitePage
	Nav        []navGroup
	Prev, Next *sitePage
	Theme      template.CSS
	Version    string
	Repo       string
	// Base is the site's absolute URL, set only for the 404 page, which
	// GitHub Pages serves at whatever path was not found.
	Base string
}

// landingData is what landing.tmpl renders.
type landingData struct {
	Theme   template.CSS
	Version string
	Repo    string
}

type navGroup struct {
	Name  string
	Pages []sitePage
}

// searchEntry is one page in the search index (search.js).
type searchEntry struct {
	Title    string    `json:"t"`
	URL      string    `json:"u"`
	Group    string    `json:"g"`
	Headings []heading `json:"h"`
	Text     string    `json:"x"`
}

// writeSite writes the site into out/site: the landing page (index.html),
// a page for every document and command in navigation order, a 404 page,
// and search.js, the search box's script and index. Pages inline their CSS,
// like the review page, and work without JavaScript, which only adds search.
func (g generator) writeSite(docs map[string][]byte) error {
	theme, err := os.ReadFile(filepath.Join(g.Root, "internal", "reviewview", "theme.css"))
	if err != nil {
		return err
	}
	all, err := g.sitePages(docs)
	if err != nil {
		return err
	}
	dir := filepath.Join(g.Out, "site")
	nav := groupPages(all)
	for i, sp := range all {
		data := siteData{Page: sp, Nav: nav, Theme: template.CSS(theme), Version: g.Version, Repo: repoURL}
		if i > 0 {
			data.Prev = &all[i-1]
		}
		if i < len(all)-1 {
			data.Next = &all[i+1]
		}
		if err := render(siteTmpl, data, filepath.Join(dir, sp.Slug+".html")); err != nil {
			return err
		}
	}

	notFound := sitePage{Slug: "404", Title: "Page not found",
		Body: `<h1>Page not found</h1><p>No page is at this address. Try the search, or start from the <a href="overview.html">overview</a>.</p>`}
	data := siteData{Page: notFound, Nav: nav, Theme: template.CSS(theme), Version: g.Version, Repo: repoURL, Base: siteURL}
	if err := render(siteTmpl, data, filepath.Join(dir, "404.html")); err != nil {
		return err
	}
	if err := render(landingTmpl, landingData{Theme: template.CSS(theme), Version: g.Version, Repo: repoURL},
		filepath.Join(dir, "index.html")); err != nil {
		return err
	}
	return writeSearch(all, filepath.Join(dir, "search.js"))
}

// sitePages renders every page in navigation order: the documents of each
// group, with the CLI reference after the RULES reference.
func (g generator) sitePages(docs map[string][]byte) ([]sitePage, error) {
	var all []sitePage
	for _, group := range []string{groupGuide, groupRules, groupCLI, groupDev} {
		if group == groupCLI {
			for _, c := range commands(g.Cmd) {
				sp, err := renderCommand(c)
				if err != nil {
					return nil, err
				}
				all = append(all, sp)
			}
			continue
		}
		for _, p := range pages {
			if p.Group == group {
				all = append(all, renderDoc(p, docs[p.Source]))
			}
		}
	}
	return all, nil
}

func render(t *template.Template, data any, file string) error {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}
	return writeFile(file, buf.Bytes())
}

// writeSearch writes search.js: the search index as a constant, then the
// script that searches it. A script rather than JSON, so the search also
// works on a site opened from disk, where fetching a file is not allowed.
func writeSearch(all []sitePage, file string) error {
	index := make([]searchEntry, len(all))
	for i, sp := range all {
		index[i] = searchEntry{Title: sp.Title, URL: sp.Slug + ".html", Group: sp.Group, Headings: sp.TOC, Text: sp.Text}
	}
	// json.Marshal escapes <, >, and &, so the index cannot end the script.
	b, err := json.Marshal(index)
	if err != nil {
		return err
	}
	return writeFile(file, []byte("const canopySearchIndex = "+string(b)+";\n"+searchScript))
}

// groupPages groups pages by navigation group, keeping their order.
func groupPages(all []sitePage) []navGroup {
	var nav []navGroup
	for _, sp := range all {
		if len(nav) == 0 || nav[len(nav)-1].Name != sp.Group {
			nav = append(nav, navGroup{Name: sp.Group})
		}
		nav[len(nav)-1].Pages = append(nav[len(nav)-1].Pages, sp)
	}
	return nav
}

// renderDoc renders a hand-written document, pointing its relative links
// at the site's pages (or GitHub, for files the site does not publish).
func renderDoc(p page, md []byte) sitePage {
	sp := renderMarkdown(p.Slug, p.Group, md, func(dest string) string {
		if t, ok := resolveLink(p.Source, dest); ok {
			return t.href()
		}
		return dest
	})
	sp.Edit = repoURL + "/edit/main/" + p.Source
	return sp
}

// renderCommand renders a command's reference from cobra's Markdown, whose
// links name other commands' files ("canopy_rules.md").
func renderCommand(c *cobra.Command) (sitePage, error) {
	var md bytes.Buffer
	if err := doc.GenMarkdownCustom(c, &md, func(s string) string { return s }); err != nil {
		return sitePage{}, err
	}
	slug := strings.ReplaceAll(c.CommandPath(), " ", "_")
	return renderMarkdown(slug, groupCLI, md.Bytes(), func(dest string) string {
		if name, ok := strings.CutSuffix(dest, ".md"); ok && !strings.Contains(name, "/") {
			return name + ".html"
		}
		return dest
	}), nil
}

// renderMarkdown renders md to HTML, rewriting each link's destination
// with link. The page's title is its first heading, and its later level 2
// and 3 headings are its table of contents.
func renderMarkdown(slug, group string, md []byte, link func(string) string) sitePage {
	parser := blackfriday.New(blackfriday.WithExtensions(blackfriday.CommonExtensions | blackfriday.AutoHeadingIDs))
	ast := parser.Parse(md)
	sp := sitePage{Slug: slug, Group: group, Title: slug}
	titled := false
	seen := map[string]int{}
	var text strings.Builder
	ast.Walk(func(n *blackfriday.Node, entering bool) blackfriday.WalkStatus {
		if !entering {
			return blackfriday.GoToNext
		}
		switch n.Type {
		case blackfriday.Link:
			n.Destination = []byte(link(string(n.Destination)))
		case blackfriday.Heading:
			// Unique IDs here, so the renderer keeps them and the table
			// of contents links where the page's anchors are.
			if k := seen[n.HeadingID]; k > 0 {
				seen[n.HeadingID]++
				n.HeadingID = fmt.Sprintf("%s-%d", n.HeadingID, k)
			} else {
				seen[n.HeadingID] = 1
			}
			h := plainText(n)
			if !titled {
				sp.Title, titled = h, true
			} else if n.Level == 2 || n.Level == 3 {
				sp.TOC = append(sp.TOC, heading{Level: n.Level, ID: n.HeadingID, Text: h})
			}
		case blackfriday.Text, blackfriday.Code, blackfriday.CodeBlock:
			text.Write(n.Literal)
			text.WriteByte(' ')
		}
		return blackfriday.GoToNext
	})
	sp.Text = strings.Join(strings.Fields(text.String()), " ")
	r := blackfriday.NewHTMLRenderer(blackfriday.HTMLRendererParameters{})
	var buf bytes.Buffer
	ast.Walk(func(n *blackfriday.Node, entering bool) blackfriday.WalkStatus {
		return r.RenderNode(&buf, n, entering)
	})
	// The Markdown is the repository's own, so its HTML is trusted.
	sp.Body = template.HTML(buf.String())
	return sp
}

// plainText is the text of n and its descendants, without markup.
func plainText(n *blackfriday.Node) string {
	var b strings.Builder
	n.Walk(func(c *blackfriday.Node, entering bool) blackfriday.WalkStatus {
		if entering && (c.Type == blackfriday.Text || c.Type == blackfriday.Code) {
			b.Write(c.Literal)
		}
		return blackfriday.GoToNext
	})
	return b.String()
}
