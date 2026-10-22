package tui

// newHomePage lists every route, the TUI equivalent of `kit --help`: each
// row opens its command's page with default values, and the detail pane
// shows the CLI invocation and the arguments and flags the options form (o)
// edits.
func newHomePage(routes []Route) Page {
	items := make([]Item, len(routes))
	for i := range routes {
		r := &routes[i]
		var opts []string
		for _, p := range r.Params {
			opts = append(opts, paramUsage(p)+"  "+p.Help)
		}
		d := Detail{Fields: []Field{
			{Label: "Command", Value: r.CommandLine()},
			{Label: "About", Value: r.Description},
		}}
		if len(opts) > 0 {
			d.Sections = []Section{{Title: "Options (o on the page)", Lines: opts}}
		}
		items[i] = Item{Title: r.Command, Description: r.Description, Route: r, Detail: d}
	}
	return NewListPage("Commands", items).WithSummary(countLabel(len(routes), "command"))
}

// paramUsage is how a parameter is written on the command line:
// "<pattern>", "[pattern...]", or "--scope".
func paramUsage(p Param) string {
	if !p.Arg {
		return "--" + p.Name
	}
	name := p.Name
	if p.Variadic {
		name += "..."
	}
	if p.Required {
		return "<" + name + ">"
	}
	return "[" + name + "]"
}
