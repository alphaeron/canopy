package approval

// RulesFileError reports a rules file the fetch could not read or parse
// (see TreeFetcher), so callers can tell which rules files failed (see
// RulesFileErrors) while still using the ones that loaded.
type RulesFileError struct {
	// Path is the repo-relative path of the rules file.
	Path string
	// Op is what failed: "read" or "parse".
	Op  string
	Err error
}

func (e *RulesFileError) Error() string {
	return e.Op + " rules file " + e.Path + ": " + e.Err.Error()
}

func (e *RulesFileError) Unwrap() error { return e.Err }

// RulesFileErrors collects every *RulesFileError in err, including those
// joined (errors.Join) or wrapped inside it, keyed by Path.
func RulesFileErrors(err error) map[string]*RulesFileError {
	out := map[string]*RulesFileError{}
	var walk func(error)
	walk = func(err error) {
		switch e := err.(type) {
		case *RulesFileError:
			out[e.Path] = e
		case interface{ Unwrap() []error }:
			for _, inner := range e.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(e.Unwrap())
		}
	}
	if err != nil {
		walk(err)
	}
	return out
}
