package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"golang.org/x/term"
)

// JSONEncoder writes a value as JSON to w. The CLI uses this for its
// --json scripted mode; the TUI mode prints the same data as a table or
// form instead. Encoding is indented for readability by humans reading the
// output in a terminal.
func JSONEncoder(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}

// TablePrinter writes a left-anchored, column-aligned table: the header row
// followed by every row, flushed together so column widths span all rows.
// Rows with fewer cells than the header are padded with empty cells; rows
// with more are truncated. The output is deterministic for a given input,
// so scripted CLI output (and its tests) can assert on exact bytes. The
// error return is the write/flush error, so a caller in an error-handling
// context can report a broken pipe or closed stdout instead of silently
// dropping the table.
func TablePrinter(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	cells := func(row []string) []string {
		out := make([]string, len(header))
		for i := range header {
			if i < len(row) {
				out[i] = row[i]
			}
		}
		return out
	}
	if err := printRow(tw, header); err != nil {
		return err
	}
	for _, r := range rows {
		if err := printRow(tw, cells(r)); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	return nil
}

func printRow(tw *tabwriter.Writer, cells []string) error {
	for i, c := range cells {
		var err error
		if i < len(cells)-1 {
			_, err = fmt.Fprintf(tw, "%s\t", c)
		} else {
			_, err = fmt.Fprintf(tw, "%s\n", c)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Interactive reports whether w is attached to a real terminal. The CLI
// uses this to pick between the TUI (bubbletea) and a scripted table when
// neither --json nor --table is specified: no TTY means no TUI. The check
// is against the true TTY bit of the descriptor (x/term.IsTerminal), not
// merely "does it have a file descriptor", since a piped stdout has an fd
// but is not a terminal and must not receive a bubbletea program. Since the
// check needs an integer file descriptor, only a writer with an exported
// Fd() method is inspectable; every other value is not a terminal.
func Interactive(w io.Writer) bool {
	type fdWriter interface{ Fd() uintptr }
	fw, ok := w.(fdWriter)
	if !ok {
		return false
	}
	return term.IsTerminal(int(fw.Fd()))
}
