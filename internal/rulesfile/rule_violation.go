package rulesfile

// RuleViolation is an integrity violation of one rule, identified by its
// position among its rules file's rules (its pattern alone may be
// repeated). Callers recover the rule with errors.As.
type RuleViolation struct {
	Position int
	Pattern  string
	Err      error
}

func (v *RuleViolation) Error() string { return v.Err.Error() }

func (v *RuleViolation) Unwrap() error { return v.Err }
