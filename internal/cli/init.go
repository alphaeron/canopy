package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/alphaeron/kit/internal/kitfile"
)

// initCmd scaffolds a new KIT file. It accepts an optional [path], where an
// empty or missing argument writes the repository-root KIT and a directory
// argument writes the KIT file inside that directory. Without --pattern it
// runs the interactive rule form; with --pattern it scaffolds the file and
// the rule together.
var initCmd = &cobra.Command{
	Use:         "init [path]",
	Short:       "Initialize a KIT file in the current repository",
	Long:        "Initialize a KIT file in the current repository, with an optional rule pre-filled by the form or by flags.",
	Annotations: map[string]string{"arg": "the KIT file to write, or a directory to write it in (default: KIT in the current directory)"},
	RunE: func(cmd *cobra.Command, args []string) error {
		var arg string
		if len(args) > 0 {
			arg = args[0]
		}
		path := initPath(arg)
		force, _ := cmd.Flags().GetBool("force")
		if err := checkInitTarget(path, force); err != nil {
			return err
		}
		pattern, _ := cmd.Flags().GetString("pattern")
		mode, _ := resolveOutputMode(cmd)

		var fields ruleFields
		var err error
		if pattern == "" && mode == modeTUI {
			fields, err = runRuleForm(ruleFields{Strategy: "union"}, nil)
			if err != nil {
				return fmt.Errorf("run form: %w", err)
			}
		} else if fields, err = ruleFieldsFrom(ruleFields{Strategy: "union"}, valuesFrom(cmd, args)); err != nil {
			return err
		}

		k, err := writeKitFile(path, force, fields)
		if err != nil {
			return err
		}
		if mode == modeJSON {
			return writeJSON(os.Stdout, k.Rules)
		}
		fmt.Println(initMessage(path, k))
		return nil
	},
}

// initPath is the KIT file `kit init [path]` writes: KIT in the current
// directory when arg is empty, KIT inside arg when it is a directory, and
// arg itself otherwise.
func initPath(arg string) string {
	switch {
	case arg == "":
		return "KIT"
	case isDir(arg):
		return filepath.Join(arg, "KIT")
	}
	return arg
}

// checkInitTarget refuses to overwrite an existing KIT file without force.
func checkInitTarget(path string, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("KIT file %q already exists; pass --force to overwrite", path)
	}
	return nil
}

// writeKitFile writes a new KIT file at path holding the rule fields
// describe, or no rule when they have no pattern. The CLI and the TUI both
// create KIT files through it.
func writeKitFile(path string, force bool, fields ruleFields) (*kitfile.File, error) {
	if err := checkInitTarget(path, force); err != nil {
		return nil, err
	}
	k := &kitfile.File{Path: path}
	if fields.Pattern != "" {
		rule, err := buildRuleFromFields(fields)
		if err != nil {
			return nil, err
		}
		if err := k.Add(rule); err != nil {
			return nil, err
		}
	}
	if err := k.Save(path); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return k, nil
}

// initMessage confirms what writeKitFile wrote.
func initMessage(path string, k *kitfile.File) string {
	if len(k.Rules) > 0 {
		return fmt.Sprintf("initialized %s with rule %q", path, k.Rules[0].Pattern)
	}
	return "initialized empty KIT file at " + path
}

// isDir reports whether the path is a directory; os.Stat errors are treated
// as "not-a-directory" so the caller falls through to the plain-file path.
func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func init() {
	initCmd.Flags().Bool("force", false, "overwrite an existing KIT file")
	initCmd.Flags().String("pattern", "", "optional rule pattern (regex) to register at init time")
	initCmd.Flags().String("name", "", "optional rule name")
	initCmd.Flags().String("strategy", "union", "combine strategy: union, override, or intersect")
	initCmd.Flags().Bool("ignore-parent", false, "ignore_parent_dirs: discard parent directories' rules")
	initCmd.Flags().Bool("current-dir-only", false, "current_directory_only: scope this rule to the file's own directory")
	initCmd.Flags().Bool("self-approve", false, "self_approve: allow the author to satisfy this rule")
	initCmd.Flags().StringSlice("approver", nil, `optional rule approver pairs 'target required_count', repeatable`)
	rootCmd.AddCommand(initCmd)
}
