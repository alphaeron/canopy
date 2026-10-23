package approval

import "github.com/alphaeron/kit/internal/rulesfile"

// reviewJSON is the JSON form of a review (see ReviewState.MarshalJSON).
type reviewJSON struct {
	Overall string           `json:"overall"`
	Files   []fileReviewJSON `json:"files"`
	Errors  []string         `json:"errors"`
	Events  []eventJSON      `json:"events"`
}

type fileReviewJSON struct {
	Path         string           `json:"path"`
	Status       string           `json:"status"`
	UsedFallback bool             `json:"used_fallback"`
	Required     []targetJSON     `json:"required"`
	Layers       []layerStateJSON `json:"layers"`
}

type targetJSON struct {
	rulesfile.ApproverTarget
	Approved   int      `json:"approved"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason,omitempty"`
	ApprovedBy []string `json:"approved_by"`
}

type layerStateJSON struct {
	RulesFile string                     `json:"rules_file"`
	Status    string                     `json:"status"`
	Active    bool                       `json:"active"`
	Reason    string                     `json:"reason,omitempty"`
	Rules     []ruleStateJSON            `json:"rules"`
	Requires  []rulesfile.ApproverTarget `json:"requires"`
}

type ruleStateJSON struct {
	Pattern   string       `json:"pattern"`
	Name      string       `json:"name,omitempty"`
	Strategy  string       `json:"combine_strategy"`
	Status    string       `json:"status"`
	Reason    string       `json:"reason,omitempty"`
	Approvers []targetJSON `json:"approvers"`
}

type eventJSON struct {
	Phase  string `json:"phase"`
	Path   string `json:"path,omitempty"`
	Layer  string `json:"layer,omitempty"`
	Rule   string `json:"rule,omitempty"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

func reviewJSONOf(state *ReviewState) reviewJSON {
	out := reviewJSON{Overall: state.Overall.String(), Files: []fileReviewJSON{}, Errors: state.Errors, Events: []eventJSON{}}
	if out.Errors == nil {
		out.Errors = []string{}
	}
	for _, fs := range state.SortedFiles() {
		f := fileReviewJSON{Path: fs.Path, Status: fs.Status.String(), UsedFallback: fs.UsedFallback, Required: targetsJSON(fs.Required), Layers: []layerStateJSON{}}
		for _, l := range fs.Layers {
			lj := layerStateJSON{RulesFile: rulesfile.PathInDir(l.DirectoryPath), Status: l.Status.String(), Active: l.Active, Reason: l.Reason, Rules: []ruleStateJSON{}, Requires: []rulesfile.ApproverTarget{}}
			for _, r := range l.Rules {
				lj.Rules = append(lj.Rules, ruleStateJSON{
					Pattern: r.Pattern, Name: r.Name, Strategy: r.CombineStrategy.String(),
					Status: r.Status.String(), Reason: r.Reason, Approvers: targetsJSON(r.Approvers),
				})
			}
			for _, t := range l.Required {
				lj.Requires = append(lj.Requires, t.ApproverTarget)
			}
			f.Layers = append(f.Layers, lj)
		}
		out.Files = append(out.Files, f)
	}
	for _, e := range state.Events {
		out.Events = append(out.Events, eventJSON{Phase: e.Phase, Path: e.Path, Layer: e.Layer, Rule: e.Rule, Status: e.Status.String(), Note: e.Note})
	}
	return out
}

func targetsJSON(states []ApproverTargetState) []targetJSON {
	out := make([]targetJSON, 0, len(states))
	for _, s := range states {
		by := s.ResolvedBy
		if by == nil {
			by = []string{}
		}
		out = append(out, targetJSON{
			ApproverTarget: s.ApproverTarget, Approved: s.SatisfiedCount,
			Status: s.Status.String(), Reason: s.Reason, ApprovedBy: by,
		})
	}
	return out
}
