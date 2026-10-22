package shonin

import (
	"fmt"
	"log/slog"
	"slices"

	"github.com/alphaeron/kit/internal/kitfile"
)

// RuleChain maps each changed file to its own localized, ordered rule pipeline.
type RuleChain struct {
	// Key: File path (e.g., "/src/api/v1/auth.go")
	// Value: Topologically ordered rules from child up to the allowed parent boundary
	FileChains map[string][]RuleLayer
}

// Execute evaluates the compiled rule chain on a per-file basis, applying
// rule-specific combination strategies sequentially from the root down to
// the target directory. It records "approve" phase events and every error
// condition into the supplied builder and assembles the complete review
// state, including the repository root, for the input files. resolver is
// the platform's (see NewLocal and NewRemote) and is required.
func (rc *RuleChain) Execute(
	ctx EvaluationContext,
	resolver ApproverResolver,
	events *ReviewStateBuilder,
	log *slog.Logger,
	repoRoot string,
) *ReviewState {
	log.Info("approve: evaluating rule chain",
		"component", "rule_chain", "phase", "approve", "files", len(rc.FileChains))

	// Create initial files map for review state
	files := make(map[string]FileState)
	var unsatisfiableFiles []string

	for file, layers := range rc.FileChains {
		// This slice tracks the cumulative list of required approvers as we
		// descend the tree.
		var runningRequiredApprovers RuleTargets

		// Process layers chronologically: from Root (Index 0) down to
		//                                 Target Directory (Index N).
		// Each layer consumes the downstream target state and returns only
		// its own LayerState; the chain reads Required from that state to
		// feed the next layer, so it never reaches into the rule or target
		// levels itself.
		fileLayers := make([]LayerState, 0, len(layers))
		for _, layer := range layers {
			layerState := layer.Execute(
				runningRequiredApprovers, ctx, events, resolver, file, log,
			)
			runningRequiredApprovers = layerState.Required

			events.AddEvent("approve", file, layer.DirectoryPath, "", layerState.Status,
				fmt.Sprintf("Processed %d rule(s) in layer %s", len(layerState.Rules), layer.DirectoryPath))
			log.Info("approve: processed rule layer",
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
			events.AddEvent("approve", file, "", "", StatusUnresolved, note)
			fs := rc.fallbackFileState(ctx, resolver, file, fileLayers, events, log)
			if fs.Status == StatusError {
				unsatisfiableFiles = append(unsatisfiableFiles, file)
			}
			files[file] = fs
			continue
		}

		// Build required approver states with proper per-target satisfaction
		// tracking.
		var requiredApprovers []ApproverTargetState
		for _, rt := range runningRequiredApprovers {
			// The rule target carries its own rule-specific state;
			// resolution, counting, and the author / self-approval rule all
			// live on the rule-target type.
			state := rt.Execute(ctx, resolver)
			if state.Status == StatusUnresolved {
				resolveErr := fmt.Errorf(
					"could not resolve approver target %q for file %q to members",
					rt.Target, file)
				events.AddError(resolveErr)
				log.Error("approve: could not resolve approver target",
					"component", "rule_chain", "phase", "approve",
					"file", file, "target", rt.Target, "error", resolveErr)
			}
			requiredApprovers = append(requiredApprovers, *state)
		}

		// Determine file-level status from the per-target states the chain
		// just assembled. An unresolvable target escalates the file to
		// StatusUnresolved; any unsatisfied target yields
		// StatusMissingApprovers.
		fileStatus := statusFromApproverStates(requiredApprovers)

		log.Info("approve: file evaluated",
			"component", "rule_chain", "phase", "approve",
			"file", file, "status", fileStatus, "required", len(requiredApprovers))

		files[file] = FileState{
			Path:         file,
			Layers:       fileLayers,
			Required:     requiredApprovers,
			Status:       fileStatus,
			UsedFallback: false,
		}
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

	// Surface an error when any file is unsatisfiable so that callers can
	// block the PR and post a warning comment.
	var overallErr error
	if len(unsatisfiableFiles) > 0 {
		overallErr = fmt.Errorf("unsatisfiable rules for %d file(s): %v",
			len(unsatisfiableFiles), unsatisfiableFiles)
	}

	// The builder forces Overall to StatusError and combines accumulated
	// errors into Err when anything went wrong.
	return events.WithRepoRoot(repoRoot).
		WithFiles(files).
		WithOverall(overallStatus).
		Build(overallErr)
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
	// An unresolved target is worse than a missing approval: the first one
	// present escalates the state to StatusUnresolved so the cause stays
	// obvious in the state tree, mirroring the non-fallback branch.
	if _, ok := firstMatching(states, func(s ApproverTargetState) bool { return s.Status == StatusUnresolved }); ok {
		return StatusUnresolved
	}
	return fileStatus
}

// fallbackFileState is a file's state when the repository owners decide
// it: their per-owner approval states, or StatusError (recorded as an
// unsatisfiable-rules error) when no repository owner is active.
func (rc *RuleChain) fallbackFileState(
	ctx EvaluationContext,
	resolver ApproverResolver,
	file string,
	layers []LayerState,
	events *ReviewStateBuilder,
	log *slog.Logger,
) FileState {
	fs := FileState{Path: file, Layers: layers, Required: []ApproverTargetState{}, UsedFallback: true}
	states, unsatisfiable := rc.evaluateFallback(ctx, resolver, file, events, log)
	if unsatisfiable {
		events.AddEvent("approve", file, "", "", StatusError,
			"No active repository owners available; rule is unsatisfiable")
		fallbackErr := fmt.Errorf(
			"unsatisfiable rules for %s: no active repository owners", file)
		events.AddError(fallbackErr)
		log.Error("approve: fallback unsatisfiable",
			"component", "rule_chain", "phase", "approve",
			"file", file, "error", fallbackErr)
		fs.Status = StatusError
		return fs
	}
	fs.Required = states
	fs.Status = statusFromApproverStates(states)
	return fs
}

// evaluateFallback attempts to satisfy a file's requirements by falling back
// to the repository owners. It returns the per-owner ApproverTargetState
// slice and a boolean indicating whether the fallback itself is unsatisfiable
// (i.e. no active owners exist at all).
func (rc *RuleChain) evaluateFallback(
	ctx EvaluationContext,
	resolver ApproverResolver,
	file string,
	events *ReviewStateBuilder,
	log *slog.Logger,
) ([]ApproverTargetState, bool) {
	var activeOwners []string
	for _, owner := range ctx.RepositoryOwners {
		if ctx.IsOwnerActive(owner) {
			activeOwners = append(activeOwners, owner)
		}
	}

	// If no active owners exist the rule is unsatisfiable.
	if len(activeOwners) == 0 {
		log.Warn("fallback: no active repository owners available",
			"component", "rule_chain", "phase", "approve",
			"file", file, "repository_owners", len(ctx.RepositoryOwners))
		return nil, true
	}

	var states []ApproverTargetState
	for _, owner := range activeOwners {
		members, ok := resolver.Resolve(owner, ctx)
		if !ok {
			resolveErr := fmt.Errorf(
				"could not resolve repository owner %q to members", owner)
			events.AddError(resolveErr)
			log.Error("fallback: could not resolve repository owner",
				"component", "rule_chain", "phase", "approve",
				"file", file, "owner", owner, "error", resolveErr)
			states = append(states, ApproverTargetState{
				ApproverTarget: kitfile.ApproverTarget{Target: owner, RequiredCount: 1},
				SatisfiedCount: 0,
				Satisfied:      false,
				ResolvedBy:     []string{},
				Status:         StatusUnresolved,
				Reason:         "Could not resolve target to members",
			})
			continue
		}

		satisfiedCount := 0
		var resolved []string
		for _, member := range members {
			if ctx.CurrentApprovers[member] {
				satisfiedCount++
				resolved = append(resolved, member)
			}
		}

		satisfied := satisfiedCount >= 1
		st := StatusSatisfied
		if !satisfied {
			st = StatusMissingApprovers
		}

		states = append(states, ApproverTargetState{
			ApproverTarget: kitfile.ApproverTarget{Target: owner, RequiredCount: 1},
			SatisfiedCount: satisfiedCount,
			Satisfied:      satisfied,
			ResolvedBy:     resolved,
			Status:         st,
			Reason:         "",
		})
	}

	return states, false
}

// RuleFiles is one rule of a RuleChain with every file whose chain includes
// it: the chain inverted from "which rules apply to this file" to "which
// files does this rule apply to".
type RuleFiles struct {
	kitfile.DeclaredRule
	// Files is sorted and holds each file once.
	Files []string
}

// ByRule inverts the chain into one entry per distinct rule, by
// kitfile.RuleKey, sorted by pattern, then KIT file. A pattern declared
// twice in one KIT file (a Validate violation) is reported once, under the
// first declaration.
func (rc *RuleChain) ByRule() []RuleFiles {
	byKey := map[kitfile.RuleKey]*RuleFiles{}
	for file, layers := range rc.FileChains {
		for _, layer := range layers {
			for _, rule := range layer.Rules {
				d := kitfile.DeclaredRule{KitFile: layer.KitFile(), Rule: rule.Rule}
				rf, ok := byKey[d.Key()]
				if !ok {
					rf = &RuleFiles{DeclaredRule: d}
					byKey[d.Key()] = rf
				}
				// Files are visited one at a time, so a repeat (a pattern
				// declared twice in one KIT file) is always the last entry.
				if n := len(rf.Files); n == 0 || rf.Files[n-1] != file {
					rf.Files = append(rf.Files, file)
				}
			}
		}
	}
	out := make([]RuleFiles, 0, len(byKey))
	for _, rf := range byKey {
		slices.Sort(rf.Files)
		out = append(out, *rf)
	}
	slices.SortFunc(out, func(a, b RuleFiles) int { return a.Key().Compare(b.Key()) })
	return out
}
