package shonin

// func Execute() (string, map[string]kitfile.Rules) {
// 	// TODO Get rule files from cache?
// 	isAbsolute := false
// 	paths, err := repo.FindFiles(pattern, ref, isAbsolute)
// 	if err == nil {
// 		for _, path := range paths {
// 			fmt.Println(path)
// 			rules := rulesForFile(repoRoot, path)
// 			for _, rule := range rules {
// 				printRule(rule)
// 			}
// 		}
// 	}
// }

// func rulesForFile(repoRoot string, fileName string) kitfile.Rules {
// 	var rules kitfile.Rules

// 	subdir, _ := filepath.Split(fileName)
// 	subdirParts := strings.Split(subdir, string(os.PathSeparator))

// 	for i := range subdirParts {
// 		f := path.Join(repoRoot, path.Join(subdirParts[:i]...), "KIT")
// 		exists, err := fileExistsCaseSensitive(f)
// 		if err != nil || !exists {
// 			continue
// 		}
// 		dat, err := os.ReadFile(f)
// 		if err != nil {
// 			fmt.Printf("Unable to read file '%s'.\nCannot get rules for '%s'.\n", f, fileName)
// 			return nil
// 		}
// 		var tmpRules kitfile.Rules
// 		parseerr := yaml.Unmarshal(dat, &tmpRules)
// 		if parseerr != nil {
// 			fmt.Printf("error while reading '%s': %v\nCannot get rules.\n", fileName, parseerr)
// 			return nil
// 		}
// 		for _, rule := range tmpRules {
// 			isMatch, err := regexp.MatchString(rule.Pattern, fileName)
// 			if err == nil {
// 				if isMatch {
// 					rules = append(rules, rule)
// 				}
// 			} else {
// 				fmt.Printf("error while checking rule match for '%s': %v\nCannot get rules.\n", fileName, parseerr)
// 				return nil
// 			}
// 		}
// 	}
// 	return rules
// }

// // Checks if path exists, is a regular file, and matches exact character casing on disk.
// func fileExistsCaseSensitive(path string) (bool, error) {
// 	info, err := os.Stat(path)
// 	if err != nil {
// 		if os.IsNotExist(err) {
// 			return false, nil
// 		}
// 		return false, err
// 	}

// 	if info.IsDir() {
// 		return false, nil
// 	}

// 	// Verify exact casing by reading parent directory
// 	dir, expectedFile := filepath.Split(path)
// 	if dir == "" {
// 		dir = "."
// 	} else {
// 		dir = filepath.Clean(dir)
// 	}

// 	entries, err := os.ReadDir(dir)
// 	if err != nil {
// 		return false, err
// 	}

// 	for _, entry := range entries {
// 		if !entry.IsDir() && entry.Name() == expectedFile {
// 			return true, nil
// 		}
// 	}

// 	return false, nil
// }

// func printRule(rule kitfile.Rule) {
// 	// Verify the results
// 	fmt.Printf("Pattern:              %s\n", rule.Pattern)
// 	fmt.Printf("Name:                 %s\n", rule.Name)
// 	fmt.Printf("IgnoreParentDirs:     %t\n", rule.IgnoreParentDirs)
// 	fmt.Printf("CombineStrategy:      %s\n", rule.CombineStrategy)
// 	fmt.Printf("CurrentDirectoryOnly: %t\n", rule.CurrentDirectoryOnly)
// 	fmt.Printf("SelfApprove:          %t\n", rule.SelfApprove)
// 	fmt.Printf("Approvers:\n")
// 	for _, approver := range rule.Approvers {
// 		fmt.Printf("\t%s\n", approver)
// 	}
// 	fmt.Println("")
// }
