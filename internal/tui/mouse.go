package tui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
)

// doubleClick is how close two clicks on one row must be to open its menu.
const doubleClickMS = 400

// onMouse hit-tests against the zones marked in the last frame. With the mouse off nothing
// reacts, so the terminal's own selection works.
func (m Model) onMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.mouse {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		return m.wheel(msg)
	case tea.MouseClickMsg:
		switch msg.Button {
		case tea.MouseLeft:
			return m.click(msg, false)
		case tea.MouseRight:
			return m.click(msg, true)
		}
	}
	return m, nil
}

func (m Model) in(id string, msg tea.MouseMsg) bool {
	return m.zones.Get(id).InBounds(msg)
}

func (m Model) wheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	delta := 1
	if msg.Button == tea.MouseWheelUp {
		delta = -1
	}
	switch {
	case m.overlay != overlayNone:
		return m, nil
	case m.screen == screenRun && m.run != nil && m.in("panel:log", msg):
		m.run.scroll(-delta*3, m.logRows())
	case m.in("panel:list", msg):
		m.focus = focusList
		m.move(delta)
	case m.in("panel:detail", msg):
		m.focus = focusDetail
		m.move(delta)
	}
	return m, nil
}

func (m Model) click(msg tea.MouseClickMsg, right bool) (tea.Model, tea.Cmd) {
	// Only two consecutive clicks on one row make a double click; any other click ends it.
	prev := m.lastClick
	m.lastClick = click{}
	switch m.overlay {
	case overlayHelp:
		m.overlay = overlayNone
		return m, nil
	case overlayDialog:
		switch {
		case m.in("btn:yes", msg):
			return m.dialogKey("y", "y")
		case m.in("btn:no", msg):
			return m.dialogKey("esc", "")
		case m.in("btn:close", msg):
			return m.dialogKey("enter", "")
		}
		return m, nil
	case overlayMenu:
		for i := range m.menu.items {
			if m.in("menu:"+strconv.Itoa(i), msg) {
				m.menu.sel = i
				return m.menuKey("enter")
			}
		}
		m.overlay = overlayNone
		return m, nil
	case overlayPalette:
		for i := range m.parse(m.pal.input).rows {
			if m.in("pal:"+strconv.Itoa(i), msg) {
				m.pal.sel = i
				return m.paletteRun()
			}
		}
		if !m.hintClicked(msg) {
			m.overlay = overlayNone
			return m, nil
		}
	}
	for i, h := range m.hints() {
		if !m.in("hint:"+strconv.Itoa(i), msg) || h.off {
			continue
		}
		if h.action != nil {
			return m.begin(request{action: *h.action, customer: m.selected})
		}
		if h.press != "" {
			return m.handleKey(h.press, "")
		}
		return m, nil
	}
	if m.in("badge", msg) {
		return m.openBadge()
	}
	if m.screen != screenFleet || m.conn == connUnavailable || m.conn == connMismatch {
		return m, nil
	}
	for i := range tabNames {
		if m.in("tab:"+strconv.Itoa(i), msg) {
			m.focus, m.tab = focusDetail, tab(i)
			return m, nil
		}
	}
	for i, c := range m.customers() {
		if !m.in("row:"+c.ID, msg) {
			continue
		}
		m.focus = focusList
		m.selectIndex(i)
		if right || m.doubleClick(prev, "row:"+c.ID) {
			return m.activate()
		}
		return m, nil
	}
	for i, r := range m.customerRuns(m.selected) {
		if !m.in("run:"+r.ID, msg) {
			continue
		}
		m.focus, m.runCursor = focusDetail, i
		if right || m.doubleClick(prev, "run:"+r.ID) {
			return m.activate()
		}
		return m, nil
	}
	return m, nil
}

func (m Model) hintClicked(msg tea.MouseMsg) bool {
	for i := range m.hints() {
		if m.in("hint:"+strconv.Itoa(i), msg) {
			return true
		}
	}
	return false
}

// doubleClick reports whether a click on id follows prev, the click just before it, on the
// same id closely enough. A single click is remembered for the next one.
func (m *Model) doubleClick(prev click, id string) bool {
	now := m.now()
	if prev.id == id && now.Sub(prev.at).Milliseconds() < doubleClickMS {
		return true
	}
	m.lastClick = click{id: id, at: now}
	return false
}

// openBadge reopens the run view from the top bar's run badge: the run last viewed while it
// is active, otherwise the newest active run.
func (m Model) openBadge() (tea.Model, tea.Cmd) {
	active := m.activeRuns()
	if len(active) == 0 {
		return m, nil
	}
	target := active[0]
	if m.run != nil {
		for _, r := range active {
			if r.ID == m.run.id {
				target = r
			}
		}
	}
	return m.openRun(target)
}
