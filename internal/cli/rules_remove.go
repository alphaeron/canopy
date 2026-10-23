package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/alphaeron/canopy/internal/rulesedit"
	"github.com/alphaeron/canopy/internal/rulesfile"
	"github.com/alphaeron/canopy/internal/tui"
)

// rulesRemoveCmd deletes one rule, found by its pattern as for rules edit
// (see ruleTarget).
var rulesRemoveCmd = &cobra.Command{
	Use:         "remove <pattern>",
	Short:       "Remove a rule from a rules file",
	Args:        cobra.ExactArgs(1),
	Annotations: map[string]string{"arg": "the pattern of the rule to remove"},
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := args[0]
		force, _ := cmd.Flags().GetBool("force")
		mode, _ := resolveOutputMode(cmd)
		interactive := mode == modeTUI
		s, err := openWorkspace(cmd.Context(), workspaceLog(interactive))
		if err != nil {
			return err
		}
		if !interactive {
			if err := warnBroken(os.Stderr, s); err != nil {
				return err
			}
		}
		target, err := ruleTarget(s, valuesFrom(cmd, args), pattern, interactive)
		if errors.Is(err, tui.ErrAborted) {
			fmt.Fprintln(os.Stderr, "remove aborted")
			return nil
		}
		// The rule picker has closed, so the warning no longer lands above
		// it; it comes before the confirmation, or explains a missing rule.
		if interactive {
			if werr := warnBroken(os.Stderr, s); werr != nil {
				return werr
			}
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
		msg, err := removeRule(s, target)
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	},
}

// removeRule removes target, for the CLI and the TUI alike, and returns the
// confirmation message.
func removeRule(s *rulesedit.Workspace, target rulesfile.Rule) (string, error) {
	if err := s.Remove(target); err != nil {
		return "", err
	}
	return fmt.Sprintf("removed rule %q at %s", target.Pattern, target.Location()), nil
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

// rulesRemoveRoute is `rules remove <pattern>`, which asks before
// removing unless --force is set.
func rulesRemoveRoute(v tui.Values) tui.Route {
	r := ruleRoute(rulesRemoveCmd, v, rulesRemoveRoute, func(ctx context.Context, v tui.Values) (string, error) {
		s, err := openWorkspace(ctx, quietLog)
		if err != nil {
			return "", err
		}
		return removeFromValues(s, v)
	})
	r.Confirm = func(v tui.Values) string {
		if v.Bool("force") {
			return ""
		}
		return fmt.Sprintf("Remove rule %q?", strings.Join(v.Args, " "))
	}
	return r
}

func removeFromValues(s *rulesedit.Workspace, v tui.Values) (string, error) {
	pattern, err := patternArg(v)
	if err != nil {
		return "", err
	}
	target, err := ruleTarget(s, v, pattern, false)
	if err != nil {
		return "", err
	}
	return removeRule(s, target)
}

func init() {
	rulesFileFlag(rulesRemoveCmd, "the rules file holding the rule (default: the one rules file with that pattern)")
	positionFlagFor(rulesRemoveCmd)
	rulesRemoveCmd.Flags().Bool("force", false, "skip the interactive confirmation prompt")
	tuiUnset(rulesRemoveCmd, "file", "the rules file declaring the pattern")
	rulesCmd.AddCommand(rulesRemoveCmd)
}
