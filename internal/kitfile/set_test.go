package kitfile

import (
	"reflect"
	"testing"
)

func TestSetPathsRootFirst(t *testing.T) {
	s := Set{"b/KIT": nil, "A/KIT": nil, "KIT": nil, "a/KIT": nil}
	if got, want := s.Paths(), []string{"KIT", "A/KIT", "a/KIT", "b/KIT"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Paths() = %v, want %v", got, want)
	}
}

func TestSetNamesDistinctNonEmpty(t *testing.T) {
	s := Set{
		"KIT":     {{Pattern: "a", Name: "docs"}, {Pattern: "b"}},
		"svc/KIT": {{Pattern: "c", Name: "core"}, {Pattern: "d", Name: "docs"}},
	}
	if got, want := s.Names(), []string{"core", "docs"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

func TestSetValidateOmitsValidFiles(t *testing.T) {
	ok := Rules{{Pattern: "a", Approvers: ApproverTargets{{Target: "x", RequiredCount: 1}}}}
	s := Set{
		"KIT":     ok,
		"svc/KIT": {{Pattern: "("}, {Pattern: "a"}, {Pattern: "a"}},
	}
	got := s.Validate()
	if _, found := got["KIT"]; found {
		t.Errorf("valid KIT file reported: %v", got["KIT"])
	}
	if n := len(got["svc/KIT"]); n != 2 {
		t.Errorf("svc/KIT violations = %d (%v), want 2 (invalid and duplicate pattern)", n, got["svc/KIT"])
	}
	if err := (&File{Rules: s["svc/KIT"]}).Validate(); err == nil {
		t.Error("Validate() = nil, want the joined violations")
	}
}

func TestDir(t *testing.T) {
	for p, want := range map[string]string{"KIT": "", "svc/KIT": "svc", "a/b/KIT": "a/b"} {
		if got := Dir(p); got != want {
			t.Errorf("Dir(%q) = %q, want %q", p, got, want)
		}
		if want != "" && PathInDir(want) != p {
			t.Errorf("PathInDir(Dir(%q)) = %q", p, PathInDir(want))
		}
	}
}

func TestDeclaredRuleKeyIgnoresName(t *testing.T) {
	a := DeclaredRule{KitFile: "svc/KIT", Rule: Rule{Pattern: "x", Name: "one"}}
	b := DeclaredRule{KitFile: "svc/KIT", Rule: Rule{Pattern: "x", Name: "two"}}
	if a.Key() != b.Key() {
		t.Errorf("Key() differs by name: %v vs %v", a.Key(), b.Key())
	}
	if c := (RuleKey{KitFile: "KIT", Pattern: "x"}); c.Compare(a.Key()) >= 0 {
		t.Error("root KIT file must sort before svc/KIT for the same pattern")
	}
}
