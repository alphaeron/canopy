package tui

// Values are a route's positional arguments and flag values, exactly as the
// CLI receives them. Boolean flags are "true" or "false"; unset flags are
// absent.
type Values struct {
	Args  []string
	Flags map[string]string
}

// Flag returns a flag's value, or "" when unset.
func (v Values) Flag(name string) string { return v.Flags[name] }

// Bool reports whether a boolean flag is set to true.
func (v Values) Bool(name string) bool { return v.Flags[name] == "true" }
