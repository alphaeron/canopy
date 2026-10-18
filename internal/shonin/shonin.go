package shonin

import (
	"fmt"
	"log/slog"
)

// TODO Local Shonin and GitHub/remote shonin, since the remote shonin
// will need to be able to fetch from the cache for rules, ...?
// Or, will that be handled by the interface implementations?
// Also, files will be implementation specific, they'll either be in the
// webhook body for the remote version, or we'll need to fetch based on
// a pattern as an initial step for the local version.
// For now, we'll leave that as an implementation requirement
// in the corresponding clients above this...
//
// Shonin orchestrates a code review: it fetches the repository's KIT rules,
// sifts them into per-file rule chains, and approves them against the current
// pull request state. All phases are traced as events in the resulting
// ReviewState and logged as structured JSON.
type Shonin struct {
	Fetch Fetcher
	// Resolver maps approver targets to active members. The default stub
	// resolver is used when nil, preserving the historical behavior.
	Resolver ApproverResolver
	// Log receives structured diagnostic output. The default logger is used
	// when nil.
	Log *slog.Logger
}

// Execute runs the full fetch -> sift -> approve workflow and returns the
// complete review state for the given files. Every error encountered along
// the way is accumulated in the returned state and forces the final approval
// status to StatusError, so the caller can block the review and surface the
// full failure history to the user.
func (s *Shonin) Execute(
	author string,
	currentApprovers map[string]bool,
	isOwnerActive func(string) bool,
	repositoryOwners []string,
	files []string,
) *ReviewState {
	log := s.log()
	events := NewReviewStateBuilder(s.resolver())

	// Fetch phase: retrieve the repository root and all KIT rules.
	log.Info("kit review starting", "component", "shonin", "phase", "fetch", "files", len(files))
	repoRoot, repoRules, err := s.Fetch.Execute()
	if err != nil {
		log.Error("kit review: fetching rules failed",
			"component", "shonin", "phase", "fetch", "repo_root", repoRoot, "error", err)
		events.AddEvent("fetch", repoRoot, "", "", StatusError, err.Error())
		events.AddError(err)
		return events.WithRepoRoot(repoRoot).
			WithFiles(map[string]FileState{}).
			WithOverall(StatusError).
			Build(err)
	}
	events.AddEvent("fetch", repoRoot, "", "", StatusDone,
		fmt.Sprintf("fetched rules from %d KIT file(s)", len(repoRules)))
	log.Info("kit review: rules fetched",
		"component", "shonin", "phase", "fetch", "repo_root", repoRoot, "kit_files", len(repoRules))

	// Sift phase: build the per-file rule chains from the fetched rules.
	log.Info("kit review: sifting rules for files",
		"component", "shonin", "phase", "sift", "files", len(files))
	ruleChain := GetFileRuleChain(repoRoot, repoRules, files, events, log)

	// Approve phase: evaluate the rule chains and assemble the final state.
	reviewState := ruleChain.Execute(
		EvaluationContext{
			Author:           author,
			CurrentApprovers: currentApprovers,
			IsOwnerActive:    isOwnerActive,
			RepositoryOwners: repositoryOwners,
		},
		events, log, repoRoot,
	)
	log.Info("kit review complete",
		"component", "shonin", "phase", "approve",
		"status", reviewState.Overall, "errors", len(reviewState.Errors))
	return reviewState
}

// GetRuleChainForFiles fetches the repository rules and sifts them into a
// rule chain for the given files. Any fetch or parse failure is returned as
// an error rather than silently producing an empty result.
func (s *Shonin) GetRuleChainForFiles(files []string) (RuleChain, error) {
	log := s.log()
	repoRoot, repoRules, err := s.Fetch.Execute()
	if err != nil {
		log.Error("kit: fetching rules failed",
			"component", "shonin", "phase", "fetch", "repo_root", repoRoot, "error", err)
		return RuleChain{FileChains: map[string][]RuleLayer{}}, err
	}
	return GetFileRuleChain(repoRoot, repoRules, files, nil, log), nil
}

// resolver returns the configured ApproverResolver or the default stub.
func (s *Shonin) resolver() ApproverResolver {
	if s.Resolver != nil {
		return s.Resolver
	}
	return &DefaultApproverResolver{}
}

// log returns the configured logger or the default one.
func (s *Shonin) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}
