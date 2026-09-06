package main

// Dashboard rendering, the hint bar and the help overlay.
//
// The hint bar and the help overlay contain no command names: both walk
// `registry` from actions.go. Adding an entry there makes it appear here.

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// ---------------------------------------------------------------------------
// Customer list item + delegate
// ---------------------------------------------------------------------------

type custItem struct{ c Customer }

func (i custItem) FilterValue() string { return i.c.Name }

type custDelegate struct{ th *theme }

func (custDelegate) Height() int                         { return 1 }
func (custDelegate) Spacing() int                        { return 0 }
func (custDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (d custDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	it, ok := item.(custItem)
	if !ok {
		return
	}
	c := it.c

	lock := " "
	if c.Locked() {
		lock = lipgloss.NewStyle().Foreground(d.th.warn).Render("⚿")
	}

	name := c.Name
	cursor := "  "
	if index == m.Index() {
		name = d.th.selected.Render(name)
		cursor = d.th.selected.Render("▎ ")
	}

	left := fmt.Sprintf("%s %s", d.th.dot(c.Health), name)
	right := fmt.Sprintf("%s %s", d.th.envBadge(c.Env), lock)

	gap := m.Width() - lipgloss.Width(cursor) - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := cursor + left + strings.Repeat(" ", gap) + right

	// One zone per row: the delegate writes the row itself, which is the only
	// place a bubbles component lets us inject per-item markers.
	fmt.Fprint(w, zone.Mark("cust:"+c.Name, padTo(line, m.Width())))
}

// markBlock marks a multi-line block for bubblezone.
//
// Every line is padded to the full panel width first. ZoneInfo.InBounds is a
// plain box test that bails out when StartX > EndX, and that is exactly what
// happens for a block whose last line is shorter than its first: the closing
// marker lands to the left of the opening one and the whole panel becomes
// unclickable. Padding the block square is the fix.
func markBlock(id, s string, w, h int) string {
	lines := strings.Split(fitLines(s, h), "\n")
	for i := range lines {
		lines[i] = padTo(lines[i], w)
	}
	return zone.Mark(id, strings.Join(lines, "\n"))
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m model) View() tea.View {
	if m.w == 0 || m.h == 0 {
		return tea.NewView("")
	}

	var body string
	switch m.screen {
	case screenForm:
		body = m.renderFormScreen()
	case screenRun:
		body = m.renderRunScreen()
	default:
		body = m.renderDashboard()
	}

	full := lipgloss.JoinVertical(lipgloss.Left,
		m.renderTopBar(),
		body,
		m.renderBottom(),
	)

	switch m.overlay {
	case overlayHelp:
		full = overlay(full, m.renderHelp(), m.w, m.h)
	case overlaySecret:
		full = overlay(full, m.renderSecret(), m.w, m.h)
	case overlayConfirm:
		full = overlay(full, m.renderConfirm(), m.w, m.h)
	case overlayMenu:
		// The context menu pops up next to the row it belongs to.
		full = overlayAt(full, m.renderMenu(), m.menu.x, m.menu.y)
	}

	v := tea.NewView(zone.Scan(full))
	v.AltScreen = true
	v.WindowTitle = "ktags"
	// Mouse is a per-view property in Bubble Tea v2: toggling `m` simply
	// changes which mode the next frame declares.
	if m.mouseOn {
		v.MouseMode = tea.MouseModeCellMotion
	} else {
		v.MouseMode = tea.MouseModeNone
	}
	return v
}

func (m model) renderTopBar() string {
	c := m.current()

	who := fmt.Sprintf("%s (%s)", c.Name, c.Env)
	if c.Locked() {
		who += lipgloss.NewStyle().Foreground(m.th.warn).Render(" ⚿")
	}

	left := m.th.title.Render("ktags") +
		m.th.faint.Render(" · ") + m.th.topBar.Render(who) +
		m.th.faint.Render(" · ") +
		lipgloss.NewStyle().Foreground(m.th.statusColor(c.Health)).Render(c.HealthSummary())

	right := m.th.faint.Render("no letter shortcuts — press ") +
		m.th.keyHint.Render(":") + m.th.faint.Render(" or ") + m.th.keyHint.Render("↵")
	if m.run != nil && !m.run.done {
		right = m.spin.View() + " " + m.th.title.Render(m.run.title+" "+m.run.customer) +
			m.th.faint.Render(fmt.Sprintf(" %d/%d", m.run.seen, m.run.total))
	}

	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	// padTo also truncates: on a narrow terminal the bar must never be wider
	// than the frame or every following line is pushed out of alignment.
	return padTo(" "+left+strings.Repeat(" ", gap)+right+" ", m.w)
}

func (m model) renderDashboard() string {
	lw := m.leftW()
	rw := m.w - lw
	ch := m.contentH()
	topH := ch / 2
	botH := ch - topH

	c := m.current()

	left := m.th.box("customers", fitLines(m.list.View(), ch-2), lw, ch, m.focus == focusCustomers)

	nodesBody := markBlock("tbl:nodes", m.nodes.View(), rw-2, topH-2)
	nodesBox := m.th.box("nodes · "+c.Name, nodesBody, rw, topH, m.focus == focusNodes)

	checksBody := markBlock("tbl:checks", m.checks.View(), rw-2, botH-2)
	checksBox := m.th.box("health checks", checksBody, rw, botH, m.focus == focusChecks)

	right := lipgloss.JoinVertical(lipgloss.Left, nodesBox, checksBox)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// renderBottom stacks the palette pop-up, an optional toast, the command bar
// and the hint bar. bottomH() counts exactly these lines.
func (m model) renderBottom() string {
	lines := m.palLines()

	if m.toast.text != "" {
		st, prefix := m.th.toastOK, "✓ "
		switch m.toast.kind {
		case "err":
			st, prefix = m.th.toastErr, "✗ "
		case "info":
			st, prefix = m.th.title, "› "
		}
		lines = append(lines, padTo(" "+st.Render(prefix+m.toast.text), m.w))
	}

	if m.pal.open {
		lines = append(lines, m.renderCommandBar())
	} else {
		lines = append(lines, m.renderStatus())
	}
	lines = append(lines, m.renderHints())
	return strings.Join(lines, "\n")
}

func (m model) renderStatus() string {
	c := m.current()

	left := []string{m.th.statBar.Render(m.status), m.th.statBar.Render(c.BackupSummary())}
	if c.Locked() {
		left = append(left, lipgloss.NewStyle().Foreground(m.th.warn).Render("⚿ "+c.LockMessage()))
	}

	mouseLbl := m.th.statBar.Render("mouse:") + map[bool]string{
		true:  m.th.toastOK.Render("on"),
		false: m.th.toastErr.Render("off"),
	}[m.mouseOn]
	right := zone.Mark("act:app.mouse", mouseLbl)

	l := " " + strings.Join(left, m.th.faint.Render(" · "))
	gap := m.w - lipgloss.Width(l) - lipgloss.Width(right) - 1
	if gap < 1 {
		gap = 1
	}
	return padTo(l+strings.Repeat(" ", gap)+right+" ", m.w)
}

// ---------------------------------------------------------------------------
// Hint bar (spike-01 style, one line, clickable)
// ---------------------------------------------------------------------------

// globalHints are the only keys left. The last three are registry actions and
// carry their action zone id so a click runs them through exec() as well.
var globalHints = []struct{ key, label, zone string }{
	{":", "command", "btn:palette"},
	{"↵", "actions", "btn:menu"},
	{"⇥", "focus", "btn:focus"},
	{"?", "help", "act:app.help"},
	{"m", "mouse", "act:app.mouse"},
	{"q", "quit", "act:app.quit"},
}

// hintActions is the up-to-three most relevant actions for the focused item:
// registry order, skipping the ones whose Guard would refuse right now. On the
// locked cluster the hint bar therefore offers unlock instead of install.
func (m model) hintActions() []*Action {
	var out []*Action
	for _, a := range actionsFor(m.focusKind()) {
		ctx, err := m.resolve(a, "")
		if err != nil {
			continue
		}
		if a.Guard != nil && a.Guard(ctx) != nil {
			continue
		}
		if out = append(out, a); len(out) == 3 {
			break
		}
	}
	return out
}

func (m model) renderHints() string {
	parts := make([]string, 0, len(globalHints))
	for _, h := range globalHints {
		parts = append(parts, zone.Mark(h.zone,
			m.th.keyHint.Render(h.key)+m.th.keyLabel.Render(" "+h.label)))
	}
	line := " " + strings.Join(parts, m.th.faint.Render("  "))

	target := m.current().Name
	if m.focusKind() == KindNode {
		if n := m.focusedNode(); n != nil {
			target = n.Name
		}
	}

	acts := make([]string, 0, 3)
	for _, a := range m.hintActions() {
		acts = append(acts, zone.Mark("act:"+a.ID, m.th.selected.Render(a.Verb)))
	}
	right := m.th.faint.Render(target+": ") + strings.Join(acts, m.th.faint.Render("  "))

	gap := m.w - lipgloss.Width(line) - lipgloss.Width(right) - 2
	if gap < 1 {
		// Never wrap: the rest is behind Enter anyway.
		return padTo(ansiTrim(line, m.w-1), m.w)
	}
	return padTo(line+strings.Repeat(" ", gap)+right+" ", m.w)
}

// ---------------------------------------------------------------------------
// Help overlay, generated from the registry
// ---------------------------------------------------------------------------

func (m model) renderHelp() string {
	w := m.w - 6
	if w > 100 {
		w = 100
	}
	if w < 40 {
		w = 40
	}
	inner := w - 2

	lines := []string{m.th.faint.Render(" keys")}
	for _, h := range globalHints {
		lines = append(lines, "  "+m.th.keyHint.Render(padTo(h.key, 4))+m.th.dialog.Render(h.label))
	}
	lines = append(lines,
		"  "+m.th.keyHint.Render(padTo("↑↓", 4))+m.th.dialog.Render("move · the wheel and clicks work everywhere"),
		"  "+m.th.keyHint.Render(padTo("esc", 4))+m.th.dialog.Render("close · leave a running job in the background"))

	// One section per target kind, one line per registry entry.
	const gutter = 2 + 10 + 20
	for _, k := range kindOrder {
		acts := actionsFor(k)
		if len(acts) == 0 {
			continue
		}
		lines = append(lines, "", m.th.faint.Render(" "+k.Label()+" actions"))
		for _, a := range acts {
			right := strings.TrimSpace(a.ArgSpec() + "  " + a.Danger.Label())
			if a.Long {
				right = strings.TrimSpace(right + "  streams")
			}
			leftW := inner - lipgloss.Width(right) - 2
			left := "  " + m.th.selected.Render(padTo(a.Verb, 10)) +
				m.th.faint.Render(padTo(a.AliasSpec(), 20)) +
				m.th.dialog.Render(ansiTrim(a.Help, leftW-gutter))
			lines = append(lines, padTo(left, leftW)+" "+m.th.faint.Render(right))
		}
	}

	return m.th.box("help — generated from the action registry — esc closes",
		strings.Join(lines, "\n"), w, len(lines)+2, true)
}
