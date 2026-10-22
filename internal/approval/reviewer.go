// Package approval is the platform-independent review algorithm: it sifts
// KIT rules into per-file rule chains and evaluates them against a pull
// request's approvals (fetch, sift, approve), recording every step in a
// ReviewState. Where rules come from and how approver targets resolve to
// people are supplied through the Fetcher and ApproverResolver interfaces;
// the local and remote packages implement them.
package approval

import (
	"context"
	"fmt"
	"log/slog"
)

// Reviewer orchestrates a code review: it fetches the repository's KIT rules,
// sifts them into per-file rule chains, and approves them against the current
// pull request state. All phases are traced as events in the resulting
// ReviewState and logged as structured JSON.
//
// A Reviewer is bound to one platform, which decides both where rules come
// from and how approver targets resolve to people. local.NewReviewer and
// remote.NewReviewer build the two platforms, so a review never mixes a local fetcher with a
// remote resolver or the reverse. Fetch and Resolver are both required.
type Reviewer struct {
	Fetch Fetcher
	// Resolver maps approver targets to their members.
	Resolver ApproverResolver
	// Log receives structured diagnostic output. The default logger is used
	// when nil.
	Log *slog.Logger
}

// Execute runs the full fetch -> sift -> approve workflow for files against
// the pull request state in pr and returns the complete review state. Every
// error encountered along the way is accumulated in the returned state and
// forces the final approval status to StatusError, so the caller can block
// the review and surface the full failure history to the user.
func (s *Reviewer) Execute(ctx context.Context, pr PullRequest, files []string) *ReviewState {
	log := s.log()
	events := NewReviewStateBuilder()

	// Fetch phase: retrieve the repository root and all KIT rules.
	log.Info("kit review starting", "component", "approval", "phase", "fetch", "files", len(files))
	repoRoot, repoRules, err := s.Fetch.Execute(ctx)
	if err != nil {
		log.Error("kit review: fetching rules failed",
			"component", "approval", "phase", "fetch", "repo_root", repoRoot, "error", err)
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
		"component", "approval", "phase", "fetch", "repo_root", repoRoot, "kit_files", len(repoRules))

	// Sift phase: build the per-file rule chains from the fetched rules.
	log.Info("kit review: sifting rules for files",
		"component", "approval", "phase", "sift", "files", len(files))
	ruleChain := GetFileRuleChain(repoRules, files, events, log)

	// Approve phase: evaluate the rule chains and assemble the final state.
	reviewState := ruleChain.Execute(ctx, pr, s.Resolver, events, log, repoRoot)
	log.Info("kit review complete",
		"component", "approval", "phase", "approve",
		"status", reviewState.Overall, "errors", len(reviewState.Errors))
	return reviewState
}

// Approve runs only the approve phase over an already sifted chain, with
// this platform's resolver: for callers that sifted the rules themselves
// and want who must approve each file under pr.
func (s *Reviewer) Approve(ctx context.Context, pr PullRequest, chain RuleChain) *ReviewState {
	return chain.Execute(ctx, pr, s.Resolver, NewReviewStateBuilder(), s.log(), "")
}

// log returns the configured logger or the default one.
func (s *Reviewer) log() *slog.Logger {
	return logOrDefault(s.Log)
}

// logOrDefault returns l, or slog.Default() when l is nil.
func logOrDefault(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
