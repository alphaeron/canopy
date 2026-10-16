package shonin

import (
	"fmt"

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
// the target directory.
func (rc *RuleChain) Execute(ctx EvaluationContext) *ReviewState {
	resolver := &DefaultApproverResolver{}
	builder := NewReviewStateBuilder(resolver)

	// Create initial files map for review state
	files := make(map[string]FileState)
	var unsatisfiableFiles []string

	for file, layers := range rc.FileChains {
		// If sifting produced absolutely no valid matching rules, drop to the
		// global repo owner fallback.
		if len(layers) == 0 {
			builder.AddEvent("approve", file, "", "", StatusUnresolved, "No matching rules found for file")
			fallbackStates, unsatisfiable := rc.evaluateFallback(ctx, resolver)
			if unsatisfiable {
				builder.AddEvent("approve", file, "", "", StatusError,
					"No active repository owners available; rule is unsatisfiable")
				unsatisfiableFiles = append(unsatisfiableFiles, file)
				files[file] = FileState{
					Path:         file,
					Layers:       []LayerState{},
					Required:     []ApproverTargetState{},
					Status:       StatusError,
					UsedFallback: true,
				}
			} else {
				fileStatus := statusFromApproverStates(fallbackStates)
				files[file] = FileState{
					Path:         file,
					Layers:       []LayerState{},
					Required:     fallbackStates,
					Status:       fileStatus,
					UsedFallback: true,
				}
			}
			continue
		}

		// This slice tracks the cumulative list of required approvers as we
		// descend the tree.
		var runningRequiredApprovers []kitfile.ApproverTarget

		// Process layers chronologically: from Root (Index 0) down to
		//                                 Target Directory (Index N).
		var fileLayers []LayerState
		for _, layer := range layers {
			// Execute the layer: it returns the updated running approvers and
			// the per-rule state tracking for this layer.
			newApprovers, ruleStates := layer.Execute(
				runningRequiredApprovers, ctx, builder, resolver, file, layer.DirectoryPath,
			)
			runningRequiredApprovers = newApprovers

			// Collect resolvedBy from all rule states in this layer.
			var layerResolvedBy []string
			for _, rs := range ruleStates {
				layerResolvedBy = append(layerResolvedBy, rs.ResolvedBy...)
			}

			// Determine layer-level status from its rule states.
			layerStatus := StatusSatisfied
			for _, rs := range ruleStates {
				if rs.Status != StatusSatisfied {
					layerStatus = rs.Status
					break
				}
			}

			layerState := LayerState{
				DirectoryPath: layer.DirectoryPath,
				Rules:         ruleStates,
				ResolvedBy:    layerResolvedBy,
				Active:        true,
				Status:        layerStatus,
				Reason:        "",
			}

			builder.AddEvent("approve", file, layer.DirectoryPath, "", layerStatus,
				fmt.Sprintf("Processed %d rule(s) in layer %s", len(ruleStates), layer.DirectoryPath))

			fileLayers = append(fileLayers, layerState)
		}

		// Post-repatriation check: if all rules yielded empty sets or combined
		// into an empty set, trigger the absolute fallback chain to repository
		// owners.
		if len(runningRequiredApprovers) == 0 {
			builder.AddEvent("approve", file, "", "", StatusUnresolved,
				"No approvers found after processing all layers")
			fallbackStates, unsatisfiable := rc.evaluateFallback(ctx, resolver)
			if unsatisfiable {
				builder.AddEvent("approve", file, "", "", StatusError,
					"No active repository owners available; rule is unsatisfiable")
				unsatisfiableFiles = append(unsatisfiableFiles, file)
				files[file] = FileState{
					Path:         file,
					Layers:       fileLayers,
					Required:     []ApproverTargetState{},
					Status:       StatusError,
					UsedFallback: true,
				}
			} else {
				fileStatus := statusFromApproverStates(fallbackStates)
				files[file] = FileState{
					Path:         file,
					Layers:       fileLayers,
					Required:     fallbackStates,
					Status:       fileStatus,
					UsedFallback: true,
				}
			}
			continue
		}

		// Calculate whether current PR approvals fulfil the remaining targeted
		// approver lists. Each target is resolved to its member set and the
		// count of matching current approvers is compared against RequiredCount.
		evalResult := calculateApprovalFromTargets(runningRequiredApprovers, ctx, resolver)

		// Build required approver states with proper per-target satisfaction
		// tracking.
		var requiredApprovers []ApproverTargetState
		for _, target := range runningRequiredApprovers {
			members, ok := resolver.Resolve(target.Target, ctx)
			if !ok {
				requiredApprovers = append(requiredApprovers, ApproverTargetState{
					Target:         target.Target,
					RequiredCount:  target.RequiredCount,
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

			satisfied := satisfiedCount >= target.RequiredCount
			st := StatusSatisfied
			if !satisfied {
				st = StatusMissingApprovers
			}

			requiredApprovers = append(requiredApprovers, ApproverTargetState{
				Target:         target.Target,
				RequiredCount:  target.RequiredCount,
				SatisfiedCount: satisfiedCount,
				Satisfied:      satisfied,
				ResolvedBy:     resolved,
				Status:         st,
				Reason:         "",
			})
		}

		// Determine file-level status.
		fileStatus := StatusSatisfied
		if !evalResult.IsApproved {
			fileStatus = StatusMissingApprovers
		}

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

	return builder.BuildReviewState("", "", files, overallStatus, overallErr)
}

// statusFromApproverStates derives a layer/file status from a slice of
// ApproverTargetState. If any target is unsatisfied the status is
// StatusMissingApprovers; otherwise StatusSatisfied.
func statusFromApproverStates(states []ApproverTargetState) Status {
	for _, s := range states {
		if !s.Satisfied {
			return StatusMissingApprovers
		}
	}
	return StatusSatisfied
}

// evaluateFallback attempts to satisfy a file's requirements by falling back
// to the repository owners. It returns the per-owner ApproverTargetState
// slice and a boolean indicating whether the fallback itself is unsatisfiable
// (i.e. no active owners exist at all).
func (rc *RuleChain) evaluateFallback(ctx EvaluationContext, resolver ApproverResolver) ([]ApproverTargetState, bool) {
	var activeOwners []string
	for _, owner := range ctx.RepositoryOwners {
		if ctx.IsOwnerActive(owner) {
			activeOwners = append(activeOwners, owner)
		}
	}

	// If no active owners exist the rule is unsatisfiable.
	if len(activeOwners) == 0 {
		return nil, true
	}

	var states []ApproverTargetState
	for _, owner := range activeOwners {
		members, ok := resolver.Resolve(owner, ctx)
		if !ok {
			states = append(states, ApproverTargetState{
				Target:         owner,
				RequiredCount:  1,
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
			Target:         owner,
			RequiredCount:  1,
			SatisfiedCount: satisfiedCount,
			Satisfied:      satisfied,
			ResolvedBy:     resolved,
			Status:         st,
			Reason:         "",
		})
	}

	return states, false
}

// calculateApprovalFromTargets resolves each approver target to its member set
// via the supplied ApproverResolver, counts how many resolved members appear
// in the current approver list, and compares against RequiredCount. Each
// target is evaluated independently so that a recurring target across layers
// is counted once per target occurrence.
func calculateApprovalFromTargets(required []kitfile.ApproverTarget, ctx EvaluationContext, resolver ApproverResolver) EvaluationResult {
	var missing []string
	for _, req := range required {
		members, ok := resolver.Resolve(req.Target, ctx)
		if !ok {
			missing = append(missing, req.Target)
			continue
		}

		satisfiedCount := 0
		for _, member := range members {
			if ctx.CurrentApprovers[member] {
				satisfiedCount++
			}
		}

		if satisfiedCount < req.RequiredCount {
			missing = append(missing, req.Target)
		}
	}
	return EvaluationResult{
		IsApproved:        len(missing) == 0,
		RequiredApprovers: missing,
	}
}
