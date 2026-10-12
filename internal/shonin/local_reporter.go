package shonin

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alphaeron/kit/internal/kitfile"
)

func PrintRuleChain(ruleChain RuleChain) {
	for file, layers := range ruleChain.FileChains {
		fmt.Printf("%s\n", file)
		for _, layer := range layers {
			directoryPathParts := strings.Split(
				filepath.ToSlash(layer.DirectoryPath),
				"/",
			)
			indentCount := len(directoryPathParts) - 1
			directoryLevelIndent := strings.Repeat("  ", indentCount)
			fmt.Printf("%s%s/\n", directoryLevelIndent, layer.DirectoryPath)
			patternLevelIndent := strings.Repeat("  ", indentCount + 1)
			ruleLevelIndent := strings.Repeat("  ", indentCount + 2)
			for _, rule := range layer.Rules {
				fmt.Printf("%s%s:\n", patternLevelIndent, rule.Pattern)
				printRule(rule, ruleLevelIndent)
			}
		}
	}
}

func printRule(rule kitfile.Rule, indent string) {
	// Verify the results
	fmt.Printf("%sName:                 %s\n", indent, rule.Name)
	fmt.Printf("%sIgnoreParentDirs:     %t\n", indent, rule.IgnoreParentDirs)
	fmt.Printf("%sCombineStrategy:      %s\n", indent, rule.CombineStrategy)
	fmt.Printf("%sCurrentDirectoryOnly: %t\n", indent, rule.CurrentDirectoryOnly)
	fmt.Printf("%sSelfApprove:          %t\n", indent, rule.SelfApprove)
	fmt.Printf("%sApprovers:\n", indent)
	for _, approver := range rule.Approvers {
		fmt.Printf("%s  %s\n", indent, approver)
	}
	fmt.Println("")
}
