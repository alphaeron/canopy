package main

// The kit CLI entry point. All commands, the root command, and the config
// load live in this package (cmd/kit/), so the only import in main() is
// the package-local one: Execute(). Configuration is loaded inside the root
// command's PreRun, which runs after cobra has finished parsing every flag
// for the command tree; --config is available there, and so are all the
// internal packages' slog calls, which already pick up the resolved level.
func main() {
	Execute()
}
