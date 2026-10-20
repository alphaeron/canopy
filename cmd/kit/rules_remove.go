package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/tui"
)

// rulesRemoveCmd deletes one rule by its pattern from a KIT file. A rule is
// identified by its pattern, which shonin uses as the scope-matching key.
var rulesRemoveCmd = &cobra.Command{
	Use:   "remove <pattern>",
	Short: "Remove a rule from a KIT file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		targetPath, err := targetKitFile(cmd)
		if err != nil {
			return err
		}
		force, _ := cmd.Flags().GetBool("force")

		k, err := loadKitFileContent(targetPath)
		if err != nil {
			return err
		}

		if !force {
			if !confirm(os.Stdin, os.Stderr) {
				fmt.Fprintln(os.Stderr, "remove aborted")
				return nil
			}
		}
		if err := k.Remove(pattern); err != nil {
			return err
		}
		if err := k.Save(targetPath); err != nil {
			return fmt.Errorf("save %s: %w", targetPath, err)
		}
		fmt.Printf("removed rule %q from %s\n", pattern, targetPath)
		return nil
	},
}

// confirm prints a prompt to out when out is attached to a terminal, reads
// one line from in, and returns true only on an explicit affirmative. Any
// non-affirmative input, or a read that yields no input (piped stdin with no
// --force), declines the removal: the caller never mutates without an
// explicit yes.
func confirm(in io.Reader, out io.Writer) bool {
	if tui.Interactive(out) {
		if _, err := fmt.Fprintf(out, "remove rule? [y/N] "); err != nil {
			return false
		}
	}
	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return answer == "y" || answer == "yes"
}

func init() {
	kitFileFlag(rulesRemoveCmd)
	rulesRemoveCmd.Flags().Bool("force", false, "skip the interactive confirmation prompt")
	rulesCmd.AddCommand(rulesRemoveCmd)
}
