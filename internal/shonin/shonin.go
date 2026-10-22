package shonin

import (
	"fmt"
	"log/slog"

	"github.com/google/go-github/v71/github"

	"github.com/alphaeron/kit/internal/gitutil"
)

// Shonin orchestrates a code review: it fetches the repository's KIT rules,
// sifts them into per-file rule chains, and approves them against the current
// pull request state. All phases are traced as events in the resulting
// ReviewState and logged as structured JSON.
//
// A Shonin is bound to one platform, which decides both where rules come
// from and how approver targets resolve to people. NewLocal and NewRemote
// build the two platforms, so a review never mixes a local fetcher with a
// remote resolver or the reverse. Fetch and Resolver are both required.
type Shonin struct {
	Fetch Fetcher
	// Resolver maps approver targets to their members.
	Resolver ApproverResolver
	// Log receives structured diagnostic output. The default logger is used
	// when nil.
	Log *slog.Logger
}

// NewLocal is the local platform: rules from repo's working tree, and
// approver targets that stand for themselves (DefaultApproverResolver),
// since a local review has no platform to expand groups against.
func NewLocal(repo *gitutil.KitGitRepository, log *slog.Logger) *Shonin {
	return &Shonin{
		Fetch:    &LocalFetcher{Repo: repo, Log: log},
		Resolver: &DefaultApproverResolver{},
		Log:      log,
	}
}

// NewRemote is the GitHub platform for one pull request: rules from the
// owner/repo tree at baseSHA, and approver targets resolved through the
// GitHub API (RemoteApproverResolver). Build one per review: the resolver
// caches team membership for its lifetime.
func NewRemote(client *github.Client, owner, repo, baseSHA string, log *slog.Logger) *Shonin {
	return &Shonin{
		Fetch:    &RemoteFetcher{Client: client, Owner: owner, Repo: repo, BaseSHA: baseSHA, Log: log},
		Resolver: NewRemoteApproverResolver(client, log),
		Log:      log,
	}
}

// Execute runs the full fetch -> sift -> approve workflow for files against
// the pull request state in ctx and returns the complete review state. Every
// error encountered along the way is accumulated in the returned state and
// forces the final approval status to StatusError, so the caller can block
// the review and surface the full failure history to the user.
func (s *Shonin) Execute(ctx EvaluationContext, files []string) *ReviewState {
	log := s.log()
	events := NewReviewStateBuilder()

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
	ruleChain := GetFileRuleChain(repoRules, files, events, log)

	// Approve phase: evaluate the rule chains and assemble the final state.
	reviewState := ruleChain.Execute(ctx, s.Resolver, events, log, repoRoot)
	log.Info("kit review complete",
		"component", "shonin", "phase", "approve",
		"status", reviewState.Overall, "errors", len(reviewState.Errors))
	return reviewState
}

// Approve runs only the approve phase over an already sifted chain, with
// this platform's resolver: for callers that sifted the rules themselves
// and want who must approve each file under ctx.
func (s *Shonin) Approve(ctx EvaluationContext, chain RuleChain) *ReviewState {
	return chain.Execute(ctx, s.Resolver, NewReviewStateBuilder(), s.log(), "")
}

// log returns the configured logger or the default one.
func (s *Shonin) log() *slog.Logger {
	return logOrDefault(s.Log)
}

// logOrDefault returns l, or slog.Default() when l is nil.
func logOrDefault(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
