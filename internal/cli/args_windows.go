package cli

// platformArgs rewrites the Windows help switches -? and /? to --help, which
// cobra recognizes, so `canopy /?` (as cmd's `help canopy` advises) and
// `canopy rules add -?` print help. Arguments after a "--" terminator are
// left as given.
func platformArgs(args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	for i, arg := range out {
		if arg == "--" {
			break
		}
		if arg == "-?" || arg == "/?" {
			out[i] = "--help"
		}
	}
	return out
}
