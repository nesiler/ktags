package tui

// Surface is one place where the TUI offers an operation. Action is the registry ID of an
// action, empty for a client operation on runs; CLI is the equivalent command line.
type Surface struct {
	Where  string
	Action string
	CLI    string
}

// Surfaces lists what the fleet screen offers for the current selection: palette, context
// menu, hint bar and help come from the registry; the run operations map to `ktags run`.
func (m Model) Surfaces() []Surface {
	var out []Surface
	add := func(where, id string) {
		for _, a := range m.actions {
			if a.ID == id {
				out = append(out, Surface{Where: where, Action: id, CLI: "ktags action run " + usage(a)})
				return
			}
		}
		// A visible entry the registry does not hold still gets a row; the caller checks it.
		out = append(out, Surface{Where: where, Action: id})
	}
	for _, r := range m.parse("").rows {
		if r.action != nil {
			add("palette", r.action.ID)
		}
	}
	if m.current() != nil {
		for _, a := range m.customerActions() {
			add("menu", a.ID)
		}
	}
	for _, h := range m.hints() {
		if h.action != nil {
			add("hint bar", h.action.ID)
		}
	}
	for _, a := range m.actions {
		add("help", a.ID)
	}
	return append(out,
		Surface{Where: "Runs tab", CLI: "ktags run watch <run>"},
		Surface{Where: "run view", CLI: "ktags run cancel <run>"},
	)
}
