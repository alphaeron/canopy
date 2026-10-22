package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
	"github.com/alphaeron/kit/internal/tui"
)

// rulesRemoveCmd deletes one rule by its pattern. A rule is identified by
// its pattern within its KIT file; without --file the KIT file is found as
// for rules edit (see kitSet.ruleTarget).
var rulesRemoveCmd = &cobra.Command{
	Use:         "remove <pattern>",
	Short:       "Remove a rule from a KIT file",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"arg": "the pattern of the rule to remove"},
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		force, _ := cmd.Flags().GetBool("force")
		mode, _ := resolveOutputMode(cmd)
		s, err := openKitSet(cmd.Context())
		if err != nil {
			return err
		}
		fileFlag, _ := cmd.Flags().GetString("file")
		target, err := s.ruleTarget(fileFlag, pattern, mode == modeTUI)
		if errors.Is(err, tui.ErrAborted) {
			fmt.Fprintln(os.Stderr, "remove aborted")
			return nil
		}
		if err != nil {
			return err
		}

		if !force {
			if !confirm(os.Stdin, os.Stderr) {
				fmt.Fprintln(os.Stderr, "remove aborted")
				return nil
			}
		}
		msg, err := removeRule(s, target, pattern)
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	},
}

// removeRule removes the rule keyed by pattern from kitFile, for the CLI and
// the TUI alike, and returns the confirmation message.
func removeRule(s *kitSet, kitFile, pattern string) (string, error) {
	if _, err := s.mutate(kitFile, func(k *kitfile.File) error { return k.Remove(pattern) }); err != nil {
		return "", err
	}
	return fmt.Sprintf("removed rule %q from %s", pattern, kitFile), nil
}

// confirm prints a prompt to out when out is attached to a terminal, reads
// one line from in, and returns true only on an explicit affirmative. Any
// non-affirmative input, or a read that yields no input (piped stdin with no
// --force), declines the removal: the caller never mutates without an
// explicit yes.
func confirm(in io.Reader, out io.Writer) bool {
	if isTerminal(out) {
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
	kitFileFlag(rulesRemoveCmd, "the KIT file holding the rule (default: the one KIT file with that pattern)")
	rulesRemoveCmd.Flags().Bool("force", false, "skip the interactive confirmation prompt")
	tuiUnset(rulesRemoveCmd, "file", "the KIT file declaring the pattern")
	rulesCmd.AddCommand(rulesRemoveCmd)
}
