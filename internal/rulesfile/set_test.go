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
	if n := len(got[PathInDir("svc")]); n != 1 {
		t.Errorf("svc violations = %d (%v), want 1 (the invalid pattern; a repeated one is valid)", n, got[PathInDir("svc")])
	}
}
