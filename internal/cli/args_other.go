//go:build !windows

package cli

// platformArgs returns the arguments as given: only Windows has help switches
// (-? and /?) beyond cobra's own; see args_windows.go.
func platformArgs(args []string) []string { return args }
