package approval

import "github.com/alphaeron/canopy/internal/rulesfile"

// reviewJSON is the JSON form of a review (see ReviewState.MarshalJSON).
type reviewJSON struct {
	Overall  string           `json:"overall"`
	Outcome  *outcomeJSON     `json:"outcome,omitempty"`
	Files    []fileReviewJSON `json:"files"`
	Errors   []string         `json:"errors"`
	Warnings []string         `json:"warnings"`
	Events   []eventJSON      `json:"events"`
}

type outcomeJSON struct {
	Verdict  Verdict  `json:"verdict"`
	Summary  string   `json:"summary"`
	Items    []string `json:"items"`
	Warnings []string `json:"warnings"`
}

func outcomeJSONOf(o Outcome) outcomeJSON {
	return outcomeJSON{Verdict: o.Verdict, Summary: o.Summary, Items: orEmpty(o.Items), Warnings: orEmpty(o.Warnings)}
}

// orEmpty is s, or an empty list for nil, so it encodes as [] rather than
// null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
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
	Status     string   `json:"status"`
	Reason     string   `json:"reason,omitempty"`
	ApprovedBy []string `json:"approved_by"`
}

type layerStateJSON struct {
	RulesFile string                     `json:"rules_file"`
	Status    string                     `json:"status"`
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
	Phase     string `json:"phase"`
	Path      string `json:"path,omitempty"`
	RulesFile string `json:"rules_file,omitempty"`
	Rule      string `json:"rule,omitempty"`
	Status    string `json:"status"`
	Note      string `json:"note,omitempty"`
}

func reviewJSONOf(state *ReviewState) reviewJSON {
	out := reviewJSON{Overall: state.Overall.String(), Files: []fileReviewJSON{}, Errors: orEmpty(state.Errors), Warnings: orEmpty(state.Warnings), Events: []eventJSON{}}
	for _, fs := range state.SortedFiles() {
		f := fileReviewJSON{Path: fs.Path, Status: fs.Status.String(), UsedFallback: fs.UsedFallback, Required: targetsJSON(fs.Required), Layers: []layerStateJSON{}}
		for _, l := range fs.Layers {
			lj := layerStateJSON{RulesFile: rulesfile.PathInDir(l.DirectoryPath), Status: l.Status.String(), Rules: []ruleStateJSON{}, Requires: []rulesfile.ApproverTarget{}}
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
		ej := eventJSON{Phase: e.Phase, Path: e.Path, Rule: e.Rule, Status: e.Status.String(), Note: e.Note}
		if e.Layer != "" {
			ej.RulesFile = rulesfile.PathInDir(e.Layer)
		}
		out.Events = append(out.Events, ej)
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
			ApproverTarget: s.ApproverTarget,
			Status:         s.Status.String(), Reason: s.Reason, ApprovedBy: by,
		})
	}
	return out
}
