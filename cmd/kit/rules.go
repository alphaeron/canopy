package main

import (
	"github.com/spf13/cobra"
)

// rulesCmd represents the rules command
var rulesCmd = &cobra.Command{
	Use:   "rules",
	Short: "Perform operation on KIT file rules",
	Long: `The rules command allows for various operations on local KIT file
rules, such as checking applicable rules for a file/pattern matching a set of
files.`,
}

func init() {
	rootCmd.AddCommand(rulesCmd)
}
