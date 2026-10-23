package rulesedit

import (
	"reflect"
	"testing"

	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestParseApproversTable(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    rulesfile.ApproverTargets
		wantErr bool
	}{
		{"empty", "", nil, false},
		{"single line", "team-a\t3",
			rulesfile.ApproverTargets{{Target: "team-a", RequiredCount: 3}}, false},
		{"multiple lines with blanks", "team-a 1\n\norg/b\t2\n",
			rulesfile.ApproverTargets{{Target: "team-a", RequiredCount: 1}, {Target: "org/b", RequiredCount: 2}}, false},
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
	targets := rulesfile.ApproverTargets{{Target: "a", RequiredCount: 1}, {Target: "b", RequiredCount: 2}}
	text := ApproversToText(targets)
	back, err := ParseApprovers(text)
	if err != nil {
		t.Fatalf("round-trip: %v", err)
	}
	if !reflect.DeepEqual(back, targets) {
		t.Errorf("round-trip mismatch: %v", back)
	}
}

func TestParseApproverPairs(t *testing.T) {
	cases := []struct {
		name    string
		pairs   []string
		want    rulesfile.ApproverTargets
		wantErr bool
	}{
		{"none", nil, rulesfile.ApproverTargets{}, false},
		{"one pair", []string{"team-a", "2"}, rulesfile.ApproverTargets{{Target: "team-a", RequiredCount: 2}}, false},
		{"two pairs", []string{"a", "1", "b", "3"}, rulesfile.ApproverTargets{{Target: "a", RequiredCount: 1}, {Target: "b", RequiredCount: 3}}, false},
		{"odd count", []string{"a"}, nil, true},
		{"bad count", []string{"a", "abc"}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseApproverPairs(c.pairs)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, %v; want %+v", got, err, c.want)
			}
		})
	}
}
