// Package approval is the platform-independent review algorithm: it sifts
// rules into per-file rule chains and evaluates them against a pull
// request's approvals (fetch, sift, approve), recording every step in a
// ReviewState. Where rules come from and how approver targets resolve to
// people are supplied through the Fetcher and ApproverResolver interfaces;
// the platform packages (internal/platform/...) implement them. Package
// review builds on this one to run the algorithm over a change.
package approval

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alphaeron/kit/internal/logx"
)

// Evaluator orchestrates a code review: it fetches the repository's rules,
// sifts them into per-file rule chains, and approves them against the
// current state of the change (ReviewInput). All phases are traced as events
// in the resulting ReviewState and logged as structured JSON.
//
// An Evaluator is bound to one platform, which decides both where rules come
// from and how approver targets resolve to people. Each platform under
// internal/platform builds its own evaluator, so a review never mixes one
// platform's fetcher with another's resolver. Fetch and Resolver are both
// required.
type Evaluator struct {
	Fetch Fetcher
	// Resolver maps approver targets to their members.
	Resolver ApproverResolver
	// Log receives structured diagnostic output. The default logger is used
	// when nil.
	Log *slog.Logger
}

// Execute runs the full fetch -> sift -> approve workflow for files against
// the change described by in and returns the complete review state. Every
// error encountered along the way is accumulated in the returned state and
// forces the final approval status to StatusError, so the caller can block
// the review and surface the full failure history to the user.
func (s *Evaluator) Execute(ctx context.Context, in ReviewInput, files []string) *ReviewState {
	log := s.log()
	events := NewReviewStateBuilder()

	// Fetch phase: retrieve all rules.
	log.Info("review starting", "component", "approval", "phase", "fetch", "files", len(files))
	repoRules, err := s.Fetch.Execute(ctx)
	if err != nil {
		log.Error("review: fetching rules failed",
			"component", "approval", "phase", "fetch", "error", err)
		events.AddEvent("fetch", "", "", "", StatusError, err.Error())
		events.AddError(err)
		return events.WithFiles(map[string]FileState{}).
			WithOverall(StatusError).
			Build(err)
	}
	events.AddEvent("fetch", "", "", "", StatusDone,
		fmt.Sprintf("fetched rules from %d rules file(s)", len(repoRules)))
	log.Info("review: rules fetched",
		"component", "approval", "phase", "fetch", "rules_files", len(repoRules))

	// Sift phase: build the per-file rule chains from the fetched rules.
	log.Info("review: sifting rules for files",
		"component", "approval", "phase", "sift", "files", len(files))
	ruleChain := GetFileRuleChain(repoRules, files, events, log)

	// Approve phase: evaluate the rule chains and assemble the final state.
	reviewState := ruleChain.Execute(ctx, in, s.Resolver, events, log)
	log.Info("review complete",
		"component", "approval", "phase", "approve",
		"status", reviewState.Overall, "errors", len(reviewState.Errors))
	return reviewState
}

// Approve runs only the approve phase over an already sifted chain, with
// this platform's resolver: for callers that sifted the rules themselves
// and want who must approve each file under in.
func (s *Evaluator) Approve(ctx context.Context, in ReviewInput, chain RuleChain) *ReviewState {
	return chain.Execute(ctx, in, s.Resolver, NewReviewStateBuilder(), s.log())
}

// log returns the configured logger or the default one.
func (s *Evaluator) log() *slog.Logger {
	return logx.OrDefault(s.Log)
}
