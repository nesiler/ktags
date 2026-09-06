package main

// The `:` palette and the context menu. Both are pure views over the registry
// in actions.go: neither one knows a single command name.
//
// The palette is a one-line input plus a match list. Its state machine is
// derived from the buffer rather than stored, so there is nothing to keep in
// sync: parse() re-reads the buffer on every keystroke and every frame and
// decides which of three stages the caret is in.
//
//	stageVerb    ":inst"                    match actions
//	stageTarget  ":install glo"             match customers or nodes
//	stageArgs    ":install acme --ch"       match the action's own flags
//
// The only mutable state is the buffer, the selected row, the Tab-cycling stem
// and the history.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

type palStage int

const (
	stageVerb palStage = iota
	stageTarget
	stageArgs
)

// palette is the whole state of the command bar.
type palette struct {
	open bool
	buf  string
	sel  int

	// Tab cycling: once the user starts completing, the match list keeps being
	// computed from `stem` (what they had typed) instead of from the completed
	// buffer, otherwise the second Tab would have nothing left to cycle over.
	cycling bool
	stem    string

	history []string
	histIx  int
}

// palRow is one line of the match list.
type palRow struct {
	group string // kind header this row belongs to
	label string // what Tab completes to
	help  string
	right string // danger level, or the target's status
	zone  string // bubblezone id suffix
}

// palQuery is the parsed buffer: everything the palette needs for one frame.
type palQuery struct {
	stage palStage
	act   *Action // resolved verb, nil while the verb is still being typed
	cur   string  // the token under the caret
	rows  []palRow
	group bool // insert kind headers into the list

	target string // an explicit target token the user already typed
	inline string // the target inherited from the dashboard, e.g. "acme (prod)"
	args   []string

	err  string   // "no such command: instal1"
	near []string // the three closest verbs
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// parse turns the buffer into everything the palette shows and does.
func (m model) parse() palQuery {
	p := m.pal
	toks := strings.Split(p.buf, " ")
	cur := toks[len(toks)-1]

	// While cycling with Tab the match list is computed from the original stem.
	q := cur
	if p.cycling {
		q = p.stem
	}

	if len(toks) == 1 {
		out := palQuery{stage: stageVerb, cur: cur, group: strings.TrimSpace(q) == ""}
		for _, a := range rankActions(q) {
			out.rows = append(out.rows, palRow{
				group: a.Target.Label(),
				label: a.Verb,
				help:  a.Title,
				right: a.Danger.Label(),
				zone:  a.ID,
			})
		}
		if len(out.rows) == 0 && cur != "" {
			out.err = "no such command: " + cur
			out.near = closestVerbs(cur, 3)
		}
		return out
	}

	act := actionByVerb(toks[0])
	if act == nil {
		return palQuery{
			stage: stageVerb,
			cur:   cur,
			err:   "no such command: " + toks[0],
			near:  closestVerbs(toks[0], 3),
		}
	}

	out := palQuery{act: act, cur: cur}
	for _, t := range toks[1 : len(toks)-1] {
		switch {
		case t == "":
		case strings.HasPrefix(t, "-"):
			out.args = append(out.args, t)
		default:
			out.target = t
		}
	}
	if strings.HasPrefix(cur, "-") {
		out.args = append(out.args, cur)
	}

	// A target inherited from the dashboard is shown inline and can always be
	// overridden by typing over it, which is what switches the palette into
	// target search.
	inherited := ""
	if act.Target != KindGlobal && out.target == "" {
		inherited = m.inheritedTarget(act.Target)
	}

	switch {
	case strings.HasPrefix(cur, "-") || act.Target == KindGlobal:
		out.stage = stageArgs
	case cur != "":
		out.stage = stageTarget
	case out.target == "" && inherited == "":
		out.stage = stageTarget
	default:
		out.stage = stageArgs
	}

	// While the user is searching, the list is the answer; otherwise show the
	// target that would be used on Enter.
	if out.stage != stageTarget {
		if out.inline = out.target; out.inline == "" {
			out.inline = inherited
		}
	}

	switch out.stage {
	case stageTarget:
		for _, n := range rankNames(q, m.targetNames(act.Target)) {
			label, right := m.targetHelp(act.Target, n)
			out.rows = append(out.rows, palRow{label: n, help: label, right: right, zone: "t/" + n})
		}
		if len(out.rows) == 0 {
			out.err = "no " + act.Target.Label() + " matching " + cur
		}
	case stageArgs:
		want := strings.ToLower(strings.TrimLeft(q, "-"))
		for _, g := range act.Args {
			if want != "" && !strings.HasPrefix(g.Name, want) {
				continue
			}
			out.rows = append(out.rows, palRow{label: g.Spec(), help: g.Help, zone: "a/" + g.Name})
		}
	}
	return out
}

// inheritedTarget is the dashboard selection, when it is of the right kind.
// A node only counts while the node panel has focus, so `:drain ` opens a node
// search unless the operator really has a node under the cursor.
func (m model) inheritedTarget(k Kind) string {
	switch k {
	case KindCustomer:
		return m.current().Name
	case KindNode:
		if m.focus == focusNodes {
			if n := m.focusedNode(); n != nil {
				return n.Name
			}
		}
	}
	return ""
}

// targetNames lists every possible target of a kind, across all customers.
func (m model) targetNames(k Kind) []string {
	var out []string
	for _, c := range m.customers {
		if k == KindCustomer {
			out = append(out, c.Name)
			continue
		}
		for _, n := range c.Nodes {
			out = append(out, n.Name)
		}
	}
	return out
}

// targetHelp describes one target in the match list.
func (m model) targetHelp(k Kind, name string) (help, right string) {
	for i := range m.customers {
		c := &m.customers[i]
		if k == KindCustomer && c.Name == name {
			lock := ""
			if c.Locked() {
				lock = " · locked"
			}
			unit := " nodes"
			if len(c.Nodes) == 1 {
				unit = " node"
			}
			return fmt.Sprintf("%s · %d%s%s", c.Env, len(c.Nodes), unit, lock), c.Health
		}
		for _, n := range c.Nodes {
			if k == KindNode && n.Name == name {
				return fmt.Sprintf("%s · %s · %s", c.Name, n.Role, n.NodeIP), n.Status
			}
		}
	}
	return "", ""
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

func (m *model) openPalette() {
	m.pal.open = true
	m.pal.buf = ""
	m.pal.sel = 0
	m.pal.cycling = false
	m.pal.histIx = len(m.pal.history)
	m.layout()
}

func (m *model) closePalette() {
	m.pal.open = false
	m.pal.buf = ""
	m.pal.sel = 0
	m.pal.cycling = false
	m.layout()
}

func (m model) paletteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	q := m.parse()

	// Everything except Tab breaks a completion cycle.
	if k != "tab" && k != "shift+tab" {
		m.pal.cycling = false
	}

	switch k {
	case "esc", "ctrl+c":
		m.closePalette()
		return m, nil

	case "backspace":
		if m.pal.buf != "" {
			r := []rune(m.pal.buf)
			m.pal.buf = string(r[:len(r)-1])
			m.pal.sel = 0
		}
		return m, nil

	case "ctrl+u":
		m.pal.buf = ""
		m.pal.sel = 0
		return m, nil

	case "tab", "shift+tab":
		return m.paletteTab(k == "shift+tab"), nil

	case "up", "ctrl+p":
		if m.pal.buf == "" {
			m.recallHistory(-1)
			return m, nil
		}
		if m.pal.sel > 0 {
			m.pal.sel--
		}
		return m, nil

	case "down", "ctrl+n":
		if m.pal.buf == "" && m.pal.histIx < len(m.pal.history) {
			m.recallHistory(1)
			return m, nil
		}
		if m.pal.sel < len(q.rows)-1 {
			m.pal.sel++
		}
		return m, nil

	case "enter":
		return m.paletteEnter(q)
	}

	if t := msg.Key().Text; t != "" {
		m.pal.buf += t
		m.pal.sel = 0
	}
	return m, nil
}

// paletteTab completes the token under the caret, cycling on repeat.
func (m model) paletteTab(back bool) model {
	q := m.parse()
	if len(q.rows) == 0 {
		return m
	}
	if !m.pal.cycling {
		m.pal.stem = q.cur
		m.pal.cycling = true
	} else {
		n := len(q.rows)
		if back {
			m.pal.sel = (m.pal.sel - 1 + n) % n
		} else {
			m.pal.sel = (m.pal.sel + 1) % n
		}
	}
	if m.pal.sel >= len(q.rows) {
		m.pal.sel = 0
	}
	m.pal.buf = replaceLastToken(m.pal.buf, q.rows[m.pal.sel].label)
	return m
}

// replaceLastToken swaps the token under the caret for a completion.
func replaceLastToken(buf, tok string) string {
	i := strings.LastIndex(buf, " ")
	if i < 0 {
		return tok
	}
	return buf[:i+1] + tok
}

func (m *model) recallHistory(d int) {
	if len(m.pal.history) == 0 {
		return
	}
	m.pal.histIx += d
	if m.pal.histIx < 0 {
		m.pal.histIx = 0
	}
	if m.pal.histIx >= len(m.pal.history) {
		m.pal.histIx = len(m.pal.history)
		m.pal.buf = ""
		return
	}
	m.pal.buf = m.pal.history[m.pal.histIx]
	m.pal.sel = 0
}

// paletteEnter resolves what the caret is pointing at and hands it to the one
// execution pipeline. It never runs anything itself.
func (m model) paletteEnter(q palQuery) (tea.Model, tea.Cmd) {
	if q.err != "" {
		return m, m.toastCmd(q.err, "err")
	}

	act := q.act
	target := q.target

	switch q.stage {
	case stageVerb:
		// An exactly typed verb wins over the highlighted row, so that a user
		// who typed the whole word is never surprised.
		if act = actionByVerb(q.cur); act == nil {
			if len(q.rows) == 0 {
				return m, m.toastCmd("no such command: "+q.cur, "err")
			}
			act = actionByVerb(q.rows[m.pal.sel].label)
		}
	case stageTarget:
		if len(q.rows) > 0 && m.pal.sel < len(q.rows) {
			target = q.rows[m.pal.sel].label
		} else if q.cur != "" {
			target = q.cur
		}
	}
	if act == nil {
		return m, m.toastCmd("no such command", "err")
	}

	args, err := parseArgs(act, q.args)
	if err != nil {
		return m, m.toastCmd(err.Error(), "err")
	}

	if s := strings.TrimSpace(m.pal.buf); s != "" {
		m.pal.history = append(m.pal.history, s)
	}
	m.closePalette()
	return m.exec(act, target, args)
}

// ---------------------------------------------------------------------------
// Palette rendering
// ---------------------------------------------------------------------------

const palMaxRows = 8

// palLines renders the pop-up that sits above the command bar. It returns the
// finished lines so both the layout maths and View() see the same height.
func (m model) palLines() []string {
	if !m.pal.open || m.w < 20 {
		return nil
	}
	q := m.parse()

	var body []string
	if q.err != "" {
		body = append(body, " "+m.th.toastErr.Render(q.err))
		if len(q.near) > 0 {
			body = append(body, " "+m.th.faint.Render("did you mean: "+strings.Join(q.near, ", ")+"?"))
		}
	}

	// Window the match list around the selection.
	rows, first := q.rows, 0
	if len(rows) > palMaxRows {
		first = m.pal.sel - palMaxRows/2
		if first < 0 {
			first = 0
		}
		if first+palMaxRows > len(rows) {
			first = len(rows) - palMaxRows
		}
		rows = rows[first : first+palMaxRows]
	}

	inner := m.w - 2
	lastGroup := ""
	for i, r := range rows {
		idx := first + i
		if q.group && r.group != lastGroup {
			lastGroup = r.group
			body = append(body, " "+m.th.faint.Render(r.group))
		}
		body = append(body, zone.Mark("pal:"+fmt.Sprint(idx), padTo(m.palRow(r, idx == m.pal.sel, inner-1), inner-1)))
	}

	if len(body) == 0 {
		return nil
	}
	if n := len(body); n > palMaxRows+3 {
		body = body[:palMaxRows+3]
	}

	title := map[palStage]string{
		stageVerb:   "commands",
		stageTarget: "targets",
		stageArgs:   "flags",
	}[q.stage]
	if q.act != nil && q.stage != stageVerb {
		title = q.act.Verb + " · " + title
	}
	box := m.th.box(title, strings.Join(body, "\n"), m.w, len(body)+2, true)
	return strings.Split(box, "\n")
}

// palRow renders one match: label, help, and a right-hand annotation.
func (m model) palRow(r palRow, selected bool, w int) string {
	cursor := "  "
	label := m.th.dialog.Render(padTo(r.label, 14))
	if selected {
		cursor = m.th.selected.Render("▸ ")
		label = m.th.selected.Render(padTo(r.label, 14))
	}

	right := ""
	if r.right != "" {
		right = lipgloss.NewStyle().Foreground(m.th.statusColor(r.right)).Render(r.right)
	}
	help := m.th.faint.Render(r.help)

	used := 2 + 14 + 1 + lipgloss.Width(right)
	gap := w - used - lipgloss.Width(help) - 1
	if gap < 1 {
		gap = 1
		help = ansiTrim(help, w-used-2)
	}
	return " " + cursor + label + " " + help + strings.Repeat(" ", gap) + right
}

// renderCommandBar is the input line itself: `:` + what was typed + a dim
// ghost completion (spike-01's fish-style prompt) + the inherited target.
func (m model) renderCommandBar() string {
	q := m.parse()

	ghost := ""
	if len(q.rows) > 0 && m.pal.sel < len(q.rows) {
		lab := q.rows[m.pal.sel].label
		if len(lab) > len(q.cur) && strings.EqualFold(lab[:len(q.cur)], q.cur) {
			ghost = lab[len(q.cur):]
		}
	}

	line := m.th.keyHint.Render(":") + m.th.dialog.Render(m.pal.buf)
	if ghost != "" {
		g := []rune(ghost)
		line += lipgloss.NewStyle().Foreground(m.th.dim).Reverse(true).Render(string(g[0]))
		line += m.th.faint.Render(string(g[1:]))
	} else {
		line += lipgloss.NewStyle().Reverse(true).Render(" ")
	}

	right := ""
	if q.inline != "" {
		right = m.th.faint.Render("→ ") + m.th.selected.Render(q.inline)
	} else if q.act != nil && q.stage == stageArgs && len(q.act.Args) == 0 {
		right = m.th.faint.Render("no flags · enter runs")
	} else if q.stage == stageVerb && m.pal.buf == "" {
		right = m.th.faint.Render("tab completes · space picks a target · esc closes")
	}

	gap := m.w - lipgloss.Width(line) - lipgloss.Width(right) - 3
	if gap < 1 {
		gap = 1
	}
	return m.th.cmdBar.Render(padTo(" "+line+strings.Repeat(" ", gap)+right+" ", m.w))
}

// ---------------------------------------------------------------------------
// Context menu
// ---------------------------------------------------------------------------

// menuState is the little pop-up opened with Enter, Space or a right-click.
// It is nothing but actionsFor(kind).
type menuState struct {
	kind  Kind
	name  string // the resolved target
	label string // "acme (prod)"
	items []*Action
	sel   int
	x, y  int
	w     int
}

// openMenu builds the context menu for whatever the dashboard has focused.
func (m *model) openMenu() {
	k := m.focusKind()
	items := actionsFor(k)
	if len(items) == 0 {
		return
	}

	st := &menuState{kind: k, items: items, w: 54}
	if st.w > m.w-6 {
		st.w = m.w - 6
	}
	switch k {
	case KindNode:
		n := m.focusedNode()
		if n == nil {
			return
		}
		st.name, st.label = n.Name, n.Name+" ("+n.Role+")"
		st.x, st.y = m.leftW()+4, 3
	default:
		c := m.current()
		st.name, st.label = c.Name, c.Name+" ("+c.Env+")"
		st.x, st.y = 4, 2+m.sel
	}
	// Keep the pop-up inside the frame on a narrow terminal.
	if st.x+st.w > m.w-1 {
		st.x = m.w - st.w - 1
	}
	if st.x < 0 {
		st.x = 0
	}
	m.menu = st
	m.overlay = overlayMenu
}

func (m model) menuKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	mu := m.menu
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.overlay, m.menu = overlayNone, nil
		return m, nil
	case "up", "k":
		if mu.sel > 0 {
			mu.sel--
		}
		return m, nil
	case "down", "j":
		if mu.sel < len(mu.items)-1 {
			mu.sel++
		}
		return m, nil
	case "enter", "space":
		act := mu.items[mu.sel]
		name := mu.name
		m.overlay, m.menu = overlayNone, nil
		return m.exec(act, name, Values{})
	}
	return m, nil
}

func (m model) renderMenu() string {
	mu := m.menu
	w := mu.w

	var b strings.Builder
	for i, a := range mu.items {
		cursor := "  "
		verb := m.th.dialog.Render(padTo(a.Verb, 10))
		if i == mu.sel {
			cursor = m.th.selected.Render("▸ ")
			verb = m.th.selected.Render(padTo(a.Verb, 10))
		}
		right := ""
		if a.Danger != DangerNone {
			right = m.th.faint.Render("!")
		}
		if a.Long {
			right = m.th.faint.Render("↻")
		}
		line := " " + cursor + verb + " " + m.th.faint.Render(a.Title)
		line = padTo(line, w-4) + right
		b.WriteString(zone.Mark(fmt.Sprintf("menu:%d", i), padTo(line, w-2)) + "\n")
	}
	b.WriteString(m.th.faint.Render(" enter runs · esc closes"))

	body := b.String()
	return m.th.box(mu.label, body, w, lipgloss.Height(body)+2, true)
}
