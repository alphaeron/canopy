package shonin

// TODO Local Shonin and GitHub/remote shonin, since the remote shonin
// will need to be able to fetch from the cache for rules, ...?
// Or, will that be handled by the interface implementations?
// Also, files will be implementation specific, they'll either be in the
// webhook body for the remote version, or we'll need to fetch based on
// a pattern as an initial step for the local version.
// For now, we'll leave that as an implementation requirement
// in the corresponding clients above this...
type Shonin struct {
	Fetch Fetcher
}

func (s *Shonin) Execute(
	author string,
	currentApprovers map[string]bool,
	isOwnerActive func(string) bool,
	repositoryOwners []string,
	files []string,
) map[string]EvaluationResult {
	// TODO Rename GetRules Execute?
	repoRoot, repoRules := s.Fetch.GetRules()
	ruleChain := GetFileRuleChain(repoRoot, repoRules, files)
	evaluationContext := EvaluationContext{
		Author:           author,
		CurrentApprovers: currentApprovers,
		IsOwnerActive:    isOwnerActive,
		RepositoryOwners: repositoryOwners,
	}
	filesToEvaluationResult := ruleChain.Execute(evaluationContext)
	return filesToEvaluationResult
}

func (s *Shonin) GetRuleChainForFiles(files []string) RuleChain {
	// TODO Rename GetRules Execute?
	repoRoot, repoRules := s.Fetch.GetRules()
	ruleChain := GetFileRuleChain(repoRoot, repoRules, files)
	return ruleChain
}
