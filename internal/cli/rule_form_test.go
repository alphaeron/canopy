package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/alphaeron/kit/internal/rulesfile"
)

func TestRuleFormOffersRulesFiles(t *testing.T) {
	f := ruleFields{RulesFile: rulesfile.PathInDir("svc")}
	// huh renders nothing until sized and initialized.
	form := newRuleForm(&f, []string{rulesfile.FileName, rulesfile.PathInDir("svc")}).WithWidth(100).WithHeight(40)
	_ = form.Init()
	if view := ansi.Strip(form.View()); !strings.Contains(view, "rules file") || !strings.Contains(view, "> "+rulesfile.PathInDir("svc")) {
		t.Errorf("the rule form must start with the rules file picker:\n%s", view)
	}
	var bare ruleFields
	form = newRuleForm(&bare, nil).WithWidth(100).WithHeight(40)
	_ = form.Init()
	if strings.Contains(form.View(), "rules file") {
		t.Error("without rules files the picker must be absent")
	}
}
