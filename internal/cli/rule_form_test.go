package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/alphaeron/kit/internal/rulesedit"
	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestRuleFormOffersRulesFiles(t *testing.T) {
	f := rulesfile.Rule{RulesFile: rulesfile.PathInDir("svc")}
	// huh renders nothing until sized and initialized.
	form := newRuleForm(&f, []string{rulesfile.FileName, rulesfile.PathInDir("svc")}).WithWidth(100).WithHeight(40)
	_ = form.Init()
	if view := ansi.Strip(form.View()); !strings.Contains(view, "rules file") || !strings.Contains(view, "> "+rulesfile.PathInDir("svc")) {
		t.Errorf("the rule form must start with the rules file picker:\n%s", view)
	}
	var bare rulesfile.Rule
	form = newRuleForm(&bare, nil).WithWidth(100).WithHeight(40)
	_ = form.Init()
	if strings.Contains(form.View(), "rules file") {
		t.Error("without rules files the picker must be absent")
	}
}

// TestApproversText pins that the approvers field keeps the text as typed
// and only replaces the rule's approvers with text that parses.
func TestApproversText(t *testing.T) {
	targets := rulesfile.ApproverTargets{{Target: "org/sec", RequiredCount: 1}}
	a := &approversText{text: rulesedit.ApproversToText(targets), targets: &targets}
	a.Set("lead")
	if a.Get() != "lead" || targets[0].Target != "org/sec" {
		t.Errorf("unparseable text must be kept without touching the approvers: %q, %+v", a.Get(), targets)
	}
	a.Set("lead\t2")
	if len(targets) != 1 || targets[0] != (rulesfile.ApproverTarget{Target: "lead", RequiredCount: 2}) {
		t.Errorf("parsed approvers = %+v", targets)
	}
}
