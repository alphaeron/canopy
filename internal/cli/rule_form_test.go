package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRuleFormOffersKitFiles(t *testing.T) {
	f := ruleFields{KitFile: "svc/KIT"}
	// huh renders nothing until sized and initialized.
	form := newRuleForm(&f, []string{"KIT", "svc/KIT"}).WithWidth(100).WithHeight(40)
	_ = form.Init()
	if view := ansi.Strip(form.View()); !strings.Contains(view, "KIT file") || !strings.Contains(view, "> svc/KIT") {
		t.Errorf("the rule form must start with the KIT file picker:\n%s", view)
	}
	var bare ruleFields
	form = newRuleForm(&bare, nil).WithWidth(100).WithHeight(40)
	_ = form.Init()
	if strings.Contains(form.View(), "KIT file") {
		t.Error("without KIT files the picker must be absent")
	}
}
