package shonin

// RuleChain maps each changed file to its own localized, ordered rule pipeline.
type RuleChain struct {
	// Key: File path (e.g., "/src/api/v1/auth.go")
	// Value: Topologically ordered rules from child up to the allowed parent boundary
	FileChains map[string][]RuleLayer
}

// Execute evaluates the compiled rule chain on a per-file basis, applying
// rule-specific combination strategies sequentially from the root down to
// the target directory.
func (rc *RuleChain) Execute(ctx EvaluationContext) map[string]EvaluationResult {
	results := make(map[string]EvaluationResult)

	for file, layers := range rc.FileChains {
		// If sifting produced absolutely no valid matching rules, drop to the global
		// repo owner fallback
		if len(layers) == 0 {
			results[file] = rc.evaluateFallback(ctx)
			continue
		}

		// This slice tracks the cumulative list of required approvers as we
		// descend the tree
		var runningRequiredApprovers []string

		// Process layers chronologically: from Root (Index 0) down to
		//                                 Target Directory (Index N)
		for _, layer := range layers {
			layer.Execute(runningRequiredApprovers, ctx)
		}

		// Post-repatriation check: If all rules yielded empty sets or combined
		// into an empty set, trigger the absolute fallback chain to repository owners.
		if len(runningRequiredApprovers) == 0 {
			results[file] = rc.evaluateFallback(ctx)
			continue
		}

		// Calculate if current PR approvals fulfill the remaining targeted
		// approver lists
		results[file] = calculateApproval(runningRequiredApprovers, ctx)
	}

	return results
}

func (rc *RuleChain) evaluateFallback(ctx EvaluationContext) EvaluationResult {
	var activeOwners []string
	for _, owner := range ctx.RepositoryOwners {
		if ctx.IsOwnerActive(owner) {
			activeOwners = append(activeOwners, owner)
		}
	}
	return calculateApproval(activeOwners, ctx)
}

func calculateApproval(required []string, ctx EvaluationContext) EvaluationResult {
	var missing []string
	for _, req := range required {
		if !ctx.CurrentApprovers[req] {
			missing = append(missing, req)
		}
	}
	return EvaluationResult{
		IsApproved:        len(missing) == 0,
		RequiredApprovers: missing,
	}
}
