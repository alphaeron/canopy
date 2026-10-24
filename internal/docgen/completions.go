package main

import (
	"bytes"
	"io"
	"path/filepath"
)

// writeCompletions writes each shell's completion script, named as the
// packages install them (see .goreleaser.yaml and scripts/package-*).
func (g generator) writeCompletions() error {
	scripts := []struct {
		file string
		gen  func(io.Writer) error
	}{
		{"canopy.bash", func(w io.Writer) error { return g.Cmd.GenBashCompletionV2(w, true) }},
		{"_canopy", g.Cmd.GenZshCompletion},
		{"canopy.fish", func(w io.Writer) error { return g.Cmd.GenFishCompletion(w, true) }},
		{"canopy.ps1", g.Cmd.GenPowerShellCompletionWithDesc},
	}
	for _, s := range scripts {
		var buf bytes.Buffer
		if err := s.gen(&buf); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(g.Out, "completions", s.file), buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}
