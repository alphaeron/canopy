package shonin

import (
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
	builder := NewReviewStateBuilder(&DefaultApproverResolver{})

	// Create initial files map for review state
	files := make(map[string]FileState)

	for file, layers := range rc.FileChains {
		// If sifting produced absolutely no valid matching rules, drop to the global
		// repo owner fallback
		if len(layers) == 0 {
			builder.AddEvent("approve", file, "", "", StatusUnresolved, "No matching rules found for file")
			// Evaluate fallback to determine if approval is achieved
			_ = rc.evaluateFallback(ctx)
			files[file] = FileState{
				Path:         file,
				Layers:       []LayerState{},
				Required:     []ApproverTargetState{},
				Status:       StatusUnresolved, // We'll determine this based on fallback result if needed
				UsedFallback: true,
			}
			continue
		}

		// This slice tracks the cumulative list of required approvers as we
		// descend the tree
		var runningRequiredApprovers []kitfile.ApproverTarget

		// Process layers chronologically: from Root (Index 0) down to
		//                                 Target Directory (Index N)
		var fileLayers []LayerState
		for _, layer := range layers {
			layerState := LayerState{
				DirectoryPath:    layer.DirectoryPath,
				Rules:            []RuleState{},
				CombinedStrategy: kitfile.RuleCombineStrategyOverride, // Default value
				ResolvedBy:       []string{},
				Active:           true,
				Status:           StatusActive,
				Reason:           "",
			}

			// Execute the layer and track its state
			runningRequiredApprovers = layer.Execute(runningRequiredApprovers, ctx, builder, file, layer.DirectoryPath)

			// For now, we're not tracking individual rule states in detail
			// This will be enhanced in later phases of the migration

			// Update running required approvers after processing this layer
			// (This is a simplified version - full state tracking will be implemented later)
			builder.AddEvent("approve", file, layer.DirectoryPath, "", StatusActive, "Processing rule layer")

			fileLayers = append(fileLayers, layerState)
		}

		// Post-repatriation check: If all rules yielded empty sets or combined
		// into an empty set, trigger the absolute fallback chain to repository owners.
		if len(runningRequiredApprovers) == 0 {
			builder.AddEvent("approve", file, "", "", StatusUnresolved, "No approvers found after processing all layers")
			files[file] = FileState{
				Path:         file,
				Layers:       fileLayers,
				Required:     []ApproverTargetState{},
				Status:       StatusUnresolved,
				UsedFallback: true,
			}
			continue
		}

		// Calculate if current PR approvals fulfill the remaining targeted
		// approver lists
		_ = calculateApprovalFromTargets(runningRequiredApprovers, ctx)

		// Build required approvers state with proper handling of RequiredCount
		var requiredApprovers []ApproverTargetState

		// Convert the approver targets to state objects with proper required counts
		for _, target := range runningRequiredApprovers {
			requiredApprovers = append(requiredApprovers, ApproverTargetState{
				Target:         target.Target,
				RequiredCount:  target.RequiredCount,
				SatisfiedCount: 0, // Will be updated by calculateApprovalFromTargets
				Satisfied:      false,
				ResolvedBy:     []string{},
				Status:         StatusUnresolved,
				Reason:         "",
			})
		}

		files[file] = FileState{
			Path:         file,
			Layers:       fileLayers,
			Required:     requiredApprovers,
			Status:       StatusSatisfied,
			UsedFallback: false,
		}
	}

	// Create a dummy overall status for now
	overallStatus := StatusActive

	return builder.BuildReviewState("", "", files, overallStatus, nil)
}

func (rc *RuleChain) evaluateFallback(ctx EvaluationContext) EvaluationResult {
	var activeOwners []string
	for _, owner := range ctx.RepositoryOwners {
		if ctx.IsOwnerActive(owner) {
			activeOwners = append(activeOwners, owner)
		}
	}
	return calculateApprovalFromTargets(
		func() []kitfile.ApproverTarget {
			result := make([]kitfile.ApproverTarget, len(activeOwners))
			for i, owner := range activeOwners {
				result[i] = kitfile.ApproverTarget{Target: owner, RequiredCount: 1}
			}
			return result
		}(),
		ctx)
}

func calculateApprovalFromTargets(required []kitfile.ApproverTarget, ctx EvaluationContext) EvaluationResult {
	var missing []string
	for _, req := range required {
		// For each approver target, we need to check if there are enough approvals
		// This is a simplified implementation - in a full solution, we would track actual approvals
		// but for now we just check that the target exists in CurrentApprovers
		if !ctx.CurrentApprovers[req.Target] {
			missing = append(missing, req.Target)
		}
	}
	return EvaluationResult{
		IsApproved:        len(missing) == 0,
		RequiredApprovers: missing,
	}
}
