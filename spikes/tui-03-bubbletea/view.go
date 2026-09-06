package main

// Dashboard rendering. Layout is pure lipgloss JoinHorizontal/JoinVertical over
// hand-drawn boxes; every clickable region is wrapped in a bubblezone marker
// and the whole tree is zone.Scan'ed once in View().

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
	if index == m.Index() {
		name = d.th.selected.Render(name)
	}

	cursor := "  "
	if index == m.Index() {
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

	title := m.th.title.Render("ktags")
	who := fmt.Sprintf("%s (%s)", c.Name, c.Env)
	if c.Locked() {
		who += lipgloss.NewStyle().Foreground(m.th.warn).Render(" ⚿")
	}

	hints := make([]string, 0, len(hintDefs))
	for _, hd := range hintDefs {
		s := m.th.keyHint.Render(hd.key) + m.th.keyLabel.Render(":"+hd.label)
		hints = append(hints, zone.Mark("hint:"+hd.cmd, s))
	}

	left := title + m.th.faint.Render(" · ") + m.th.topBar.Render(who) +
		m.th.faint.Render(" · ") + lipgloss.NewStyle().Foreground(m.th.statusColor(c.Health)).Render(c.HealthSummary())
	right := strings.Join(hints, m.th.faint.Render(" "))

	gap := m.w - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 1 {
		gap = 1
	}
	// padTo also truncates: on a narrow terminal the bar must never be wider
	// than the frame or every following line is pushed out of alignment.
	return padTo(" "+left+strings.Repeat(" ", gap)+right+" ", m.w)
}

type hintDef struct{ key, label, cmd string }

var hintDefs = []hintDef{
	{"h", "health", "health"},
	{"i", "install", "install"},
	{"b", "backup", "backup"},
	{"a", "add", "add"},
	{"s", "secret", "secret"},
	{"q", "quit", "quit"},
}

func (m model) renderDashboard() string {
	lw := m.leftW()
	rw := m.w - lw
	ch := m.contentH()
	topH := ch / 2
	botH := ch - topH

	c := m.current()

	left := m.th.box("customers", fitLines(m.list.View(), ch-2), lw, ch, m.focus == focusCustomers)

	nodesTitle := fmt.Sprintf("nodes · %s", c.Name)
	nodesBody := markBlock("tbl:nodes", m.nodes.View(), rw-2, topH-2)
	nodesBox := m.th.box(nodesTitle, nodesBody, rw, topH, m.focus == focusNodes)

	checksBody := markBlock("tbl:checks", m.checks.View(), rw-2, botH-2)
	checksBox := m.th.box("health checks", checksBody, rw, botH, m.focus == focusChecks)

	right := lipgloss.JoinVertical(lipgloss.Left, nodesBox, checksBox)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m model) renderBottom() string {
	var lines []string

	if m.toast.text != "" {
		st := m.th.toastOK
		prefix := "✓ "
		switch m.toast.kind {
		case "err":
			st = m.th.toastErr
			prefix = "✗ "
		case "info":
			st = m.th.title
			prefix = "› "
		}
		lines = append(lines, padTo(" "+st.Render(prefix+m.toast.text), m.w))
	}

	if m.cmdOpen {
		buf := m.th.cmdBar.Render(" :" + m.cmdBuf + "█")
		lines = append(lines, padTo(buf, m.w))
		sug := m.suggestions()
		var parts []string
		for i, s := range sug {
			st := m.th.faint
			if i == 0 {
				st = m.th.keyHint
			}
			parts = append(parts, zone.Mark("hint:"+s, st.Render(s)))
		}
		hint := m.th.faint.Render("  no match")
		if len(parts) > 0 {
			hint = "  " + strings.Join(parts, m.th.faint.Render(" · "))
		}
		lines = append(lines, padTo(hint, m.w))
	}

	lines = append(lines, m.renderStatus())
	return strings.Join(lines, "\n")
}

func (m model) renderStatus() string {
	c := m.current()

	var left []string
	if m.run != nil && !m.run.done {
		left = append(left, m.spin.View()+" "+m.th.title.Render(m.run.title+" "+m.run.customer)+
			m.th.faint.Render(fmt.Sprintf(" %d/%d", m.run.seen, m.run.total)))
	} else {
		left = append(left, m.th.statBar.Render(m.status))
	}
	left = append(left, m.th.statBar.Render(c.BackupSummary()))
	if c.Locked() {
		left = append(left, lipgloss.NewStyle().Foreground(m.th.warn).Render("⚿ "+c.LockMessage()))
	}

	mouseLbl := m.th.statBar.Render("mouse:") + map[bool]string{
		true:  m.th.toastOK.Render("on"),
		false: m.th.toastErr.Render("off"),
	}[m.mouseOn]

	right := strings.Join([]string{
		zone.Mark("btn:mouse", mouseLbl),
		zone.Mark("hint:help", m.th.keyHint.Render("?")+m.th.keyLabel.Render(":help")),
	}, m.th.faint.Render(" · "))

	l := " " + strings.Join(left, m.th.faint.Render(" · "))
	gap := m.w - lipgloss.Width(l) - lipgloss.Width(right) - 1
	if gap < 1 {
		gap = 1
	}
	return padTo(l+strings.Repeat(" ", gap)+right+" ", m.w)
}

func (m model) renderHelp() string {
	body := m.helpM.FullHelpView(keyMap{}.FullHelp())
	body += "\n\n" + m.th.faint.Render("mouse: click customers, node rows, top-bar hints and the mouse toggle;\nwheel scrolls the panel under the cursor. `m` disables mouse reporting so\nthe terminal's own text selection works again.")
	w := lipgloss.Width(body) + 4
	if w > m.w-4 {
		w = m.w - 4
	}
	h := lipgloss.Height(body) + 2
	return m.th.box("help — ? or esc to close", body, w, h, true)
}
