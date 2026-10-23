package rulesfile

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseRulesRoundTrip(t *testing.T) {
	original := Rules{rule1, rule2}
	dat, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := ParseRules(dat)
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	// ParseRules numbers the rules by their position in the file.
	want := Rules{rule1, rule2}
	want[1].Position = 1
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestParseRulesGarbage(t *testing.T) {
	_, err := ParseRules([]byte("not: [a rule"))
	if err == nil {
		t.Fatal("expected an error for invalid YAML")
	}
}

// TestRulesFileSizeLimit: a rules file over MaxSize is ErrTooLarge, from
// the parser and from a bounded read, and one at the limit is read.
func TestRulesFileSizeLimit(t *testing.T) {
	atLimit, over := strings.Repeat("#", MaxSize), strings.Repeat("#", MaxSize+1)
	if _, err := ParseRules([]byte(over)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("ParseRules over the limit: err = %v, want ErrTooLarge", err)
	}
	if _, err := ParseRules([]byte(atLimit)); err != nil {
		t.Errorf("ParseRules at the limit: %v", err)
	}
	if _, err := ReadLimited(strings.NewReader(over)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("ReadLimited over the limit: err = %v, want ErrTooLarge", err)
	}
	if got, err := ReadLimited(strings.NewReader(atLimit)); err != nil || len(got) != MaxSize {
		t.Errorf("ReadLimited at the limit = %d bytes, %v; want all %d", len(got), err, MaxSize)
	}
}
