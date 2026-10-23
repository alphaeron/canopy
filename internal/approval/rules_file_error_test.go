package approval

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

// TestRulesFileErrors pins that every rules file failure is found by path, however
// it is joined or wrapped, and that the error keeps its cause.
func TestRulesFileErrors(t *testing.T) {
	read := &RulesFileError{Path: "a/" + rulesfile.FileName, Op: "read", Err: fs.ErrNotExist}
	parse := &RulesFileError{Path: rulesfile.FileName, Op: "parse", Err: errors.New("yaml: bad")}
	err := fmt.Errorf("load: %w", errors.Join(read, errors.New("unrelated"), parse))

	got := RulesFileErrors(err)
	if len(got) != 2 || got[read.Path] != read || got[parse.Path] != parse {
		t.Errorf("RulesFileErrors = %v, want both rules files", got)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("a RulesFileError must unwrap to its cause")
	}
	if want := "parse rules file " + rulesfile.FileName + ": yaml: bad"; parse.Error() != want {
		t.Errorf("Error() = %q, want %q", parse.Error(), want)
	}
	if got := RulesFileErrors(nil); len(got) != 0 {
		t.Errorf("RulesFileErrors(nil) = %v, want none", got)
	}
}
