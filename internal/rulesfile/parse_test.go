package rulesfile

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseRulesRoundTrip(t *testing.T) {
	original := Rules{rule1, rule2}
	kf := &File{Rules: original}
	dir := t.TempDir()
	path := dir + "/" + FileName
	if err := kf.Save(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	dat, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got, err := ParseRules(dat)
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	if !reflect.DeepEqual(got, original) {
		t.Errorf("round-trip mismatch: got %v, want %v", got, original)
	}
}

func TestParseRulesGarbage(t *testing.T) {
	_, err := ParseRules([]byte("not: [a rule"))
	if err == nil {
		t.Fatal("expected an error for invalid YAML")
	}
}

func TestParseApproversTable(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    ApproverTargets
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"single line", "team-a\t3",
			ApproverTargets{{Target: "team-a", RequiredCount: 3}}, false},
		{"multiple lines with blanks", "team-a 1\n\norg/b\t2\n",
			ApproverTargets{{Target: "team-a", RequiredCount: 1}, {Target: "org/b", RequiredCount: 2}}, false},
		{"missing count", "team-a\n", nil, true},
		{"non-integer count", "team-a abc\n", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseApprovers(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseApproversRoundTrip(t *testing.T) {
	targets := ApproverTargets{{Target: "a", RequiredCount: 1}, {Target: "b", RequiredCount: 2}}
	text := ApproversToText(targets)
	back, err := ParseApprovers(text)
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if !reflect.DeepEqual(back, targets) {
		t.Errorf("round-trip mismatch: %v", back)
	}
}

func TestApproverTargetRequiredCountValidation(t *testing.T) {
	r := Rule{Pattern: `.*`, Approvers: ApproverTargets{{Target: "a", RequiredCount: 0}}}
	kf := &File{Rules: Rules{r}}
	if len(kf.Violations()) == 0 {
		t.Fatal("expected a required_count validation error")
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
