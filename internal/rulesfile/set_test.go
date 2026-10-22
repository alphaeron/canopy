package rulesfile

import (
	"reflect"
	"testing"
)

func TestSetPathsRootFirst(t *testing.T) {
	s := Set{PathInDir("b"): nil, PathInDir("A"): nil, FileName: nil, PathInDir("a"): nil}
	if got, want := s.Paths(), []string{FileName, PathInDir("A"), PathInDir("a"), PathInDir("b")}; !reflect.DeepEqual(got, want) {
		t.Errorf("Paths() = %v, want %v", got, want)
	}
}

func TestSetNamesDistinctNonEmpty(t *testing.T) {
	s := Set{
		FileName:         {{Pattern: "a", Name: "docs"}, {Pattern: "b"}},
		PathInDir("svc"): {{Pattern: "c", Name: "core"}, {Pattern: "d", Name: "docs"}},
	}
	if got, want := s.Names(), []string{"core", "docs"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

func TestSetValidateOmitsValidFiles(t *testing.T) {
	ok := Rules{{Pattern: "a", Approvers: ApproverTargets{{Target: "x", RequiredCount: 1}}}}
	s := Set{
		FileName:         ok,
		PathInDir("svc"): {{Pattern: "("}, {Pattern: "a"}, {Pattern: "a"}},
	}
	got := s.Validate()
	if _, found := got[FileName]; found {
		t.Errorf("valid rules file reported: %v", got[FileName])
	}
	if n := len(got[PathInDir("svc")]); n != 2 {
		t.Errorf("svc violations = %d (%v), want 2 (invalid and duplicate pattern)", n, got[PathInDir("svc")])
	}
}

func TestDir(t *testing.T) {
	for p, want := range map[string]string{FileName: "", PathInDir("svc"): "svc", PathInDir("a/b"): "a/b"} {
		if got := Dir(p); got != want {
			t.Errorf("Dir(%q) = %q, want %q", p, got, want)
		}
		if want != "" && PathInDir(want) != p {
			t.Errorf("PathInDir(Dir(%q)) = %q", p, PathInDir(want))
		}
	}
}

func TestDeclaredRuleKeyIgnoresName(t *testing.T) {
	a := DeclaredRule{RulesFile: PathInDir("svc"), Rule: Rule{Pattern: "x", Name: "one"}}
	b := DeclaredRule{RulesFile: PathInDir("svc"), Rule: Rule{Pattern: "x", Name: "two"}}
	if a.Key() != b.Key() {
		t.Errorf("Key() differs by name: %v vs %v", a.Key(), b.Key())
	}
	if c := (RuleKey{RulesFile: FileName, Pattern: "x"}); c.Compare(a.Key()) >= 0 {
		t.Error("root rules file must sort before svc's for the same pattern")
	}
}
