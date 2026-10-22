// Command kit is the Kit code review CLI. The commands themselves live in
// internal/cli; configuration is loaded in the root command's
// PersistentPreRunE, once cobra has parsed every flag.
package main

import "github.com/alphaeron/kit/internal/cli"

func main() {
	cli.Execute()
}
