package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestJSONEncoderEmitsIndentedJSON(t *testing.T) {
	var buf bytes.Buffer
	err := JSONEncoder(&buf, map[string]int{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("JSONEncoder: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "\"a\": 1") || !strings.Contains(out, "\"b\": 2") {
		t.Errorf("missing expected key/value pairs in:\n%s", out)
	}
	if !strings.Contains(out, "\n  ") {
		t.Errorf("expected indented output, got:\n%s", out)
	}
}

func TestTablePrinterPadsShortRows(t *testing.T) {
	var buf bytes.Buffer
	if err := TablePrinter(&buf, []string{"NAME", "PAT"}, [][]string{{"r1", "x.go"}, {"r2"}}); err != nil {
		t.Fatalf("TablePrinter: %v", err)
	}
	out := buf.String()
	// A short row is padded to the header width, so "r2" sits on its own
	// row and the padding column width is derived from the header + the
	// longest cell.
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "PAT") {
		t.Errorf("header missing from:\n%s", out)
	}
	if !strings.Contains(out, "r1") || !strings.Contains(out, "x.go") || !strings.Contains(out, "r2") {
		t.Errorf("row content missing from:\n%s", out)
	}
	// Every row has a trailing newline.
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output must end with a newline:\n%s", out)
	}
}

func TestTablePrinterDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	if err := TablePrinter(&a, []string{"K"}, [][]string{{"x"}, {"y"}}); err != nil {
		t.Fatalf("TablePrinter a: %v", err)
	}
	if err := TablePrinter(&b, []string{"K"}, [][]string{{"x"}, {"y"}}); err != nil {
		t.Fatalf("TablePrinter b: %v", err)
	}
	if a.String() != b.String() {
		t.Errorf("identical inputs must produce identical bytes:\n%s\nvs\n%s", a.String(), b.String())
	}
}
