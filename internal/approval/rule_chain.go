package approval

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/alphaeron/kit/internal/parallel"
	"github.com/alphaeron/kit/internal/rulesfile"
)

// RuleChain maps each changed file to its own localized, ordered rule
// pipeline. Files that match the same rules may share one []RuleLayer, so a
// chain's layers must not be modified.
type RuleChain struct {
	// FileChains maps each repository-relative file path (e.g.
	// "src/api/v1/auth.go") to its rule layers, from the root down.
	FileChains map[string][]RuleLayer
}

// Execute evaluates the compiled rule chain on a per-file basis, applying
// rule-specific combination strategies sequentially from the root down to
// the target directory. It records "approve" phase events and every error
// condition into the supplied builder and assembles the complete review
// state for the input files. resolver is the platform's (each platform under
// internal/platform builds its evaluator with one) and is required. Every name
// in the review is compared in the resolver's canonical form, and each
// target is resolved at most once.
//
// Evaluating a chain depends on its layers, not on the file's path, so
// files that share a []RuleLayer (as GetFileRuleChain's do) are evaluated
// once: they share the resulting layer and approver states, which must not
// be modified, and each file gets its own copy of the chain's trace. A
// failure inside a shared chain is logged once, for the first file.
func (rc *RuleChain) Execute(
	ctx context.Context,
	in ReviewInput,
	resolver ApproverResolver,
	events *ReviewStateBuilder,
	log *slog.Logger,
) *ReviewState {
	resolver = newCachedReviewResolver(resolver)
	p := newParticipants(in, resolver.Canonical)
	rc.prefetch(ctx, p, resolver)
	log.Info("approve: evaluating rule chain",
		"component", "rule_chain", "phase", "approve", "files", len(rc.FileChains))

	files := make(map[string]FileState, len(rc.FileChains))
	evaluated := map[chainKey]*chainResult{}
	uncovered := 0

	for file, layers := range rc.FileChains {
		if err := ctx.Err(); err != nil {
			events.AddError(fmt.Errorf("approve: %w", err))
			break
		}
		r, ok := evaluated[keyOf(layers)]
		if !ok {
			r = rc.evaluate(ctx, layers, p, resolver, file, log)
			evaluated[keyOf(layers)] = r
		}
		fs := r.fileState(file, events)
		if len(layers) == 0 {
			uncovered++
		}
		log.Debug("approve: file evaluated",
			"component", "rule_chain", "phase", "approve",
			"file", file, "status", fs.Status, "required", len(fs.Required))
		files[file] = fs
	}

	// Determine overall status across all files.
	overallStatus := StatusSatisfied
	for _, fs := range files {
		if fs.Status == StatusError {
			overallStatus = StatusError
			break
		}
		if fs.Status != StatusSatisfied {
			overallStatus = StatusMissingApprovers
		}
	}

	// A file no rule covers is reviewed by the repository owners; that
	// does not change the verdict, but a reader should know.
	if uncovered > 0 {
		events.AddWarning(UncoveredWarning(uncovered))
	}

	// The builder forces Overall to StatusError when errors accumulated.
	return events.WithFiles(files).
		WithOverall(overallStatus).
		Build()
}

// UncoveredWarning is the review warning for n files that no rule covers,
// which fall back to the repository owners.
func UncoveredWarning(n int) string {
	return plural(n, "file") + " covered by no rule; the repository owners review them"
}

// chainKey identifies a chain by its layers' backing array: files whose
// chains share one hold the same layers. Distinct arrays with equal
// contents are merely evaluated twice.
type chainKey struct {
	first *RuleLayer
	n     int
}

func keyOf(layers []RuleLayer) chainKey {
	if len(layers) == 0 {
		return chainKey{}
	}
	return chainKey{first: &layers[0], n: len(layers)}
}

// chainResult is a chain's evaluation, shared by every file with that
// chain: the file state without its path, and the trace evaluating it
// recorded, to repeat for each file.
type chainResult struct {
	layers   []LayerState
	required []ApproverTargetState
	status   Status
	fallback bool
	trace    *ReviewStateBuilder
}

// evaluate evaluates layers, recording its trace under file's path.
func (rc *RuleChain) evaluate(
	ctx context.Context,
	layers []RuleLayer,
	p participants,
	resolver ApproverResolver,
	file string,
	log *slog.Logger,
) *chainResult {
	trace := NewReviewStateBuilder()
	// This slice tracks the cumulative list of required approvers as we
	// descend the tree.
	var runningRequiredApprovers ExecutableTargets

	// Process layers chronologically: from Root (Index 0) down to
	//                                 Target Directory (Index N).
	// Each layer consumes the downstream target state and returns only
	// its own LayerState; the chain reads Required from that state to
	// feed the next layer, so it never reaches into the rule or target
	// levels itself.
	fileLayers := make([]LayerState, 0, len(layers))
	for _, layer := range layers {
		layerState := layer.Execute(
			ctx, runningRequiredApprovers, p, trace, resolver, file, log,
		)
		runningRequiredApprovers = layerState.Required

		trace.AddEvent("approve", file, layer.DirectoryPath, "", layerState.Status,
			fmt.Sprintf("Processed %d rule(s) in layer %s", len(layerState.Rules), layer.DirectoryPath))
		log.Debug("approve: processed rule layer",
			"component", "rule_chain", "phase", "approve",
			"file", file, "layer", layer.DirectoryPath,
			"rules", len(layerState.Rules), "status", layerState.Status)

		fileLayers = append(fileLayers, layerState)
	}

	// If sifting produced no matching rules, or every rule yielded or
	// combined into an empty set, fall back to the repository owners.
	if len(runningRequiredApprovers) == 0 {
		note := "No approvers found after processing all layers; falling back to repository owners"
		if len(layers) == 0 {
			note = "No matching rules found for file; falling back to repository owners"
		}
		trace.AddEvent("approve", file, "", "", StatusUnresolved, note)
		return rc.fallbackResult(ctx, p, resolver, file, fileLayers, trace, log)
	}

	// Build required approver states with proper per-target satisfaction
	// tracking.
	var requiredApprovers []ApproverTargetState
	for _, rt := range runningRequiredApprovers {
		// The rule target carries its own rule-specific state;
		// resolution, counting, and the author / self-approval rule all
		// live on the rule-target type.
		state := rt.Execute(ctx, p, resolver)
		if state.Status == StatusUnresolved {
			log.Error("approve: could not resolve approver target",
				"component", "rule_chain", "phase", "approve",
				"file", file, "target", rt.Target, "reason", state.Reason)
		}
		requiredApprovers = append(requiredApprovers, *state)
	}

	// Determine file-level status from the per-target states the chain
	// just assembled. An unresolvable target escalates the file to
	// StatusUnresolved; any unsatisfied target yields
	// StatusMissingApprovers.
	return &chainResult{
		layers:   fileLayers,
		required: requiredApprovers,
		status:   statusFromApproverStates(requiredApprovers),
		trace:    trace,
	}
}

// fileState is file's state under the chain r evaluated: it repeats r's
// trace under file's path into events, and records the errors that name
// file.
func (r *chainResult) fileState(file string, events *ReviewStateBuilder) FileState {
	events.replay(r.trace, file)
	fs := FileState{
		Path:         file,
		Layers:       r.layers,
		Required:     r.required,
		Status:       r.status,
		UsedFallback: r.fallback,
	}
	if r.fallback && r.status == StatusError {
		events.AddEvent("approve", file, "", "", StatusError,
			"No active repository owners available; rule is unsatisfiable")
		events.AddError(fmt.Errorf("unsatisfiable rules for %s: no active repository owners", file))
		return fs
	}
	if r.fallback {
		return fs
	}
	for _, state := range r.required {
		if state.Status == StatusUnresolved {
			events.AddError(fmt.Errorf("approver target %q for file %q: %w", state.Target, file, state.err))
		}
	}
	return fs
}

// prefetch makes, concurrently, the platform calls that evaluating the
// chain makes first, one after another: it resolves every rule target, then
// looks up whether the first member each resolved target could count is
// active. Evaluation then finds both in the review's caches (resolver, the
// review's cachedReviewResolver, and the platform's IsActive). Evaluation
// prunes every rule's targets, which resolves each one and checks its
// members up to the first eligible one, so prefetch makes no call
// evaluation would not.
func (rc *RuleChain) prefetch(ctx context.Context, p participants, resolver ApproverResolver) {
	targets := map[ExecutableTarget]bool{}
	named := map[string]bool{}
	seen := map[chainKey]bool{}
	for _, layers := range rc.FileChains {
		if seen[keyOf(layers)] {
			continue
		}
		seen[keyOf(layers)] = true
		for _, layer := range layers {
			for _, rule := range layer.Rules {
				for _, t := range rule.Targets() {
					targets[t] = true
					named[t.Target] = true
				}
			}
		}
	}
	names := slices.Collect(maps.Keys(named))
	parallel.For(len(names), func(i int) {
		_, _ = resolver.Resolve(ctx, names[i]) // cached for evaluation, which reports any error
	})

	first := map[string]bool{}
	for t := range targets {
		members, err := resolver.Resolve(ctx, t.Target)
		if err != nil {
			continue
		}
		if i := slices.IndexFunc(members, func(m string) bool { return t.contributes(m, p) }); i >= 0 {
			first[members[i]] = true
		}
	}
	users := slices.Collect(maps.Keys(first))
	parallel.For(len(users), func(i int) { p.isActive(users[i]) })
}

// statusFromApproverStates derives a layer/file status from a slice of
// ApproverTargetState. A target that could not be resolved at all
// (StatusUnresolved) escalates the status to StatusUnresolved; otherwise any
// unsatisfied target yields StatusMissingApprovers; otherwise
// StatusSatisfied.
func statusFromApproverStates(states []ApproverTargetState) Status {
	fileStatus := StatusSatisfied
	for _, s := range states {
		if s.Status != StatusSatisfied {
			fileStatus = StatusMissingApprovers
		}
	}
	// An unresolved target is worse than a missing approval.
	if _, ok := firstMatching(states, func(s ApproverTargetState) bool { return s.Status == StatusUnresolved }); ok {
		return StatusUnresolved
	}
	return fileStatus
}

// fallbackResult is a chain's result when the repository owners decide
// it: their per-owner approval states, or StatusError when no repository
// owner is active (which fileState records, per file, as an
// unsatisfiable-rules error).
func (rc *RuleChain) fallbackResult(
	ctx context.Context,
	p participants,
	resolver ApproverResolver,
	file string,
	layers []LayerState,
	trace *ReviewStateBuilder,
	log *slog.Logger,
) *chainResult {
	r := &chainResult{layers: layers, required: []ApproverTargetState{}, fallback: true, trace: trace}
	states, unsatisfiable := rc.evaluateFallback(ctx, p, resolver, file, trace, log)
	if unsatisfiable {
		log.Error("approve: fallback unsatisfiable",
			"component", "rule_chain", "phase", "approve",
			"file", file, "error", "no active repository owners")
		r.status = StatusError
		return r
	}
	r.required = states
	r.status = statusFromApproverStates(states)
	return r
}

// evaluateFallback attempts to satisfy a file's requirements by falling back
// to the repository owners. It returns the per-owner ApproverTargetState
// slice and a boolean indicating whether the fallback itself is unsatisfiable
// (i.e. no active owners exist at all).
func (rc *RuleChain) evaluateFallback(
	ctx context.Context,
	p participants,
	resolver ApproverResolver,
	file string,
	events *ReviewStateBuilder,
	log *slog.Logger,
) ([]ApproverTargetState, bool) {
	var activeOwners []string
	for _, owner := range p.owners {
		if p.isActive(owner) {
			activeOwners = append(activeOwners, owner)
		}
	}

	// If no active owners exist the rule is unsatisfiable.
	if len(activeOwners) == 0 {
		log.Warn("fallback: no active repository owners available",
			"component", "rule_chain", "phase", "approve",
			"file", file, "repository_owners", len(p.owners))
		return nil, true
	}

	var states []ApproverTargetState
	for _, owner := range activeOwners {
		members, err := resolver.Resolve(ctx, owner)
		if err != nil {
			resolveErr := fmt.Errorf(
				"could not resolve repository owner %q to members: %w", owner, err)
			events.AddError(resolveErr)
			log.Error("fallback: could not resolve repository owner",
				"component", "rule_chain", "phase", "approve",
				"file", file, "owner", owner, "error", resolveErr)
			states = append(states, ApproverTargetState{
				ApproverTarget: rulesfile.ApproverTarget{Target: owner, RequiredCount: 1},
				ResolvedBy:     []string{},
				Status:         StatusUnresolved,
				Reason:         resolveFailure(err),
				err:            err,
			})
			continue
		}

		// No rule covers the file, so no rule allows self-approval: the
		// author's authoring never counts as an owner's review.
		var resolved []string
		for _, member := range members {
			if member != p.author && p.approvers[member] {
				resolved = append(resolved, member)
			}
		}
		st := StatusSatisfied
		if len(resolved) == 0 {
			st = StatusMissingApprovers
		}
		states = append(states, ApproverTargetState{
			ApproverTarget: rulesfile.ApproverTarget{Target: owner, RequiredCount: 1},
			SatisfiedCount: len(resolved),
			ResolvedBy:     resolved,
			Status:         st,
		})
	}

	return states, false
}

// RulesByFile is every rule applying to each file, in chain order, each
// with its location set; a file no rule applies to has none.
func (rc *RuleChain) RulesByFile() map[string]rulesfile.Rules {
	out := make(map[string]rulesfile.Rules, len(rc.FileChains))
	for file, layers := range rc.FileChains {
		var rules rulesfile.Rules
		for _, layer := range layers {
			for _, rule := range layer.Rules {
				r := rule.Rule
				r.RulesFile = layer.RulesFile()
				rules = append(rules, r)
			}
		}
		out[file] = rules
	}
	return out
}

// ByRule inverts the chain from "which rules apply to this file" to "which
// files does this rule apply to": one entry per rule (see GroupByRule).
func (rc *RuleChain) ByRule() []RuleFiles {
	return GroupByRule(rc.RulesByFile())
}
