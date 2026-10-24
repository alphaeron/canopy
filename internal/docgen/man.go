package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cpuguy83/go-md2man/v2/md2man"
	"github.com/spf13/cobra/doc"
)

// manual is the man pages' manual name, shown in each page's header.
const manual = "Canopy Manual"

// writeMan writes a section 1 page per command and a page for each of
// pages with a manRef, gzipped as distributions install them.
func (g generator) writeMan(docs map[string][]byte) error {
	source := "Canopy " + g.Version
	for _, c := range commands(g.Cmd) {
		name := strings.ReplaceAll(c.CommandPath(), " ", "-")
		header := &doc.GenManHeader{Section: "1", Date: &g.Date, Source: source, Manual: manual}
		var buf bytes.Buffer
		if err := doc.GenMan(c, header, &buf); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := writeGzip(filepath.Join(g.Out, "man", "man1", name+".1.gz"), buf.Bytes()); err != nil {
			return err
		}
	}
	for _, p := range pages {
		if p.Man == nil {
			continue
		}
		roff := md2man.Render(g.manMarkdown(p, docs))
		dir := fmt.Sprintf("man%d", p.Man.Section)
		file := fmt.Sprintf("%s.%d.gz", p.Man.Name, p.Man.Section)
		if err := writeGzip(filepath.Join(g.Out, "man", dir, file), roff); err != nil {
			return err
		}
	}
	return nil
}

// manMarkdown frames a document as a man page for md2man: a title line,
// NAME, the document (without its own title) as DESCRIPTION and its
// sections, then for canopy-rules(5) a section per RULES key, and SEE ALSO
// listing canopy(1) and the other pages.
func (g generator) manMarkdown(p page, docs map[string][]byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s %d %q %q %q\n\n", strings.ToUpper(p.Man.Name), p.Man.Section,
		g.Date.Format("Jan 2006"), "Canopy "+g.Version, manual)
	fmt.Fprintf(&b, "# NAME\n%s - %s\n\n# DESCRIPTION\n", p.Man.Name, p.Man.Summary)
	b.Write(manLinks(p.Source, p.Man, stripTitle(docs[p.Source])))
	if p.Man == rulesMan {
		b.WriteString("\n\n# KEYS\n")
		for _, k := range pages {
			if k.Key != "" {
				// Each page's "# key" title becomes a subsection of KEYS.
				b.WriteString("\n")
				b.Write(manLinks(k.Source, p.Man, shiftHeadings(docs[k.Source], 2)))
			}
		}
	}
	see := []string{"canopy(1)"}
	for _, o := range pages {
		if o.Man != nil && o.Man != p.Man {
			see = append(see, o.Man.String())
		}
	}
	fmt.Fprintf(&b, "\n\n# SEE ALSO\n%s\n\nThe full documentation: %s\n", strings.Join(see, ", "), siteURL)
	return b.Bytes()
}

// stripTitle drops a document's leading "# Title" line, which the man page
// replaces with its own title and NAME section.
func stripTitle(md []byte) []byte {
	if bytes.HasPrefix(md, []byte("# ")) {
		if _, rest, ok := bytes.Cut(md, []byte("\n")); ok {
			return bytes.TrimLeft(rest, "\n")
		}
		return nil
	}
	return md
}

// shiftHeadings demotes every ATX heading ("# Title") in md by levels,
// leaving lines inside fenced code blocks, such as YAML comments, alone.
func shiftHeadings(md []byte, levels int) []byte {
	prefix := strings.Repeat("#", levels)
	lines := strings.SplitAfter(string(md), "\n")
	fenced := false
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "```"):
			fenced = !fenced
		case !fenced && strings.HasPrefix(l, "#"):
			lines[i] = prefix + l
		}
	}
	return []byte(strings.Join(lines, ""))
}

// writeGzip writes data gzipped to file, creating its directory. The gzip
// header carries no name or time, so the same input gives the same bytes.
func writeGzip(file string, data []byte) error {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return err
	}
	if _, err := zw.Write(data); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return writeFile(file, buf.Bytes())
}

// writeFile writes data to file, creating its directory.
func writeFile(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}
