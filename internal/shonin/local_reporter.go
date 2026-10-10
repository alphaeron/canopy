package shonin

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alphaeron/kit/internal/kitfile"
)

func PrintRuleChain(ruleChain RuleChain) {
	for file, layers := range ruleChain.FileChains {
		_, fileName := filepath.Split(file)
		fmt.Printf("%s\n", file)
		for _, layer := range layers {
			directoryPathParts := strings.Split(
				filepath.ToSlash(layer.DirectoryPath),
				"/",
			)
			indentCount := len(directoryPathParts)
			lastDirectory := layer.DirectoryPath[indentCount - 1]
			directoryLevelIndent := strings.Repeat("\t", indentCount)
			fmt.Printf("%s%s\n", directoryLevelIndent, lastDirectory)
			fileLevelIndent := strings.Repeat("\t", indentCount + 1)
			ruleLevelIndent := strings.Repeat("\t", indentCount + 2)
			fmt.Printf("%s%s\n", fileName, fileLevelIndent)
			for _, rule := range layer.Rules {
				printRule(rule, ruleLevelIndent)
			}
		}
	}
}

func printRule(rule kitfile.Rule, indent string) {
	// Verify the results
	fmt.Printf("%sPattern:              %s\n", indent, rule.Pattern)
	fmt.Printf("%sName:                 %s\n", indent, rule.Name)
	fmt.Printf("%sIgnoreParentDirs:     %t\n", indent, rule.IgnoreParentDirs)
	fmt.Printf("%sCombineStrategy:      %s\n", indent, rule.CombineStrategy)
	fmt.Printf("%sCurrentDirectoryOnly: %t\n", indent, rule.CurrentDirectoryOnly)
	fmt.Printf("%sSelfApprove:          %t\n", indent, rule.SelfApprove)
	fmt.Printf("%sApprovers:\n")
	for _, approver := range rule.Approvers {
		fmt.Printf("%s\t%s\n", indent, approver)
	}
	fmt.Println("")
}
