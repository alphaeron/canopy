package rulesfile

import "testing"

func TestRuleKeyCompare(t *testing.T) {
	svc := Rule{RulesFile: PathInDir("svc"), Position: 1}.Key()
	if c := (RuleKey{RulesFile: FileName, Position: 5}); c.Compare(svc) >= 0 {
		t.Error("keys must order by rules file first, the root rules file first")
	}
	if c := (RuleKey{RulesFile: PathInDir("svc"), Position: 0}); c.Compare(svc) >= 0 {
		t.Error("keys in one rules file must order by position")
	}
}
