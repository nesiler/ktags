package tui

import (
	tea "charm.land/bubbletea/v2"
)

// handleKey dispatches one key: the topmost overlay first, then the connection states, then
// the screen. k is the key name, text the printable characters it carries.
func (m Model) handleKey(k, text string) (tea.Model, tea.Cmd) {
	// Space is matched by name (docs/guides/ui.md §12); a terminal may send it without text.
	if k == "space" && text == "" {
		text = " "
	}
	if m.width > 0 && (m.width < minWidth || m.height < minHeight) {
		if k == "q" || k == "ctrl+c" {
			return m.quit()
		}
		return m, nil
	}
	switch m.overlay {
	case overlayHelp:
		m.overlay = overlayNone
		return m, nil
	case overlayDialog:
		return m.dialogKey(k, text)
	case overlayPalette:
		return m.paletteKey(k, text)
	case overlayMenu:
		return m.menuKey(k)
	}
	if m.conn == connUnavailable || m.conn == connMismatch {
		return m.offlineKey(k)
	}
	switch k {
	case ":":
		return m.openPalette(), nil
	case "?":
		m.overlay = overlayHelp
		return m, nil
	case "m":
		m.mouse = !m.mouse
		return m, nil
	}
	if m.screen == screenRun {
		return m.runKey(k)
	}
	return m.fleetKey(k)
}

func (m Model) offlineKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "enter":
		return m.retry()
	case "q", "ctrl+c":
		return m.quit()
	case "?":
		m.overlay = overlayHelp
	case "m":
		m.mouse = !m.mouse
	}
	return m, nil
}

func (m Model) fleetKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "q", "ctrl+c":
		return m.quit()
	case "tab", "shift+tab":
		if m.focus == focusList {
			m.focus = focusDetail
		} else {
			m.focus = focusList
		}
	case "left":
		if m.focus == focusDetail {
			m.tab = (m.tab + tab(len(tabNames)) - 1) % tab(len(tabNames))
		}
	case "right":
		if m.focus == focusDetail {
			m.tab = (m.tab + 1) % tab(len(tabNames))
		}
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-max(1, m.panelRows()))
	case "pgdown":
		m.move(max(1, m.panelRows()))
	case "home":
		m.move(-1 << 30)
	case "end":
		m.move(1 << 30)
	case "enter", "space":
		return m.activate()
	}
	return m, nil
}

// move moves within the focused panel: the customer list, or the Runs tab rows.
func (m *Model) move(delta int) {
	if m.focus == focusList {
		m.selectIndex(max(0, m.selectedIndex()) + delta)
		return
	}
	if m.tab == tabRuns {
		n := len(m.customerRuns(m.selected))
		m.runCursor = max(0, min(n-1, m.runCursor+delta))
	}
}

// activate is Enter or Space: the context menu of the selected item. On the Runs tab the
// selected item is a run, and Enter opens it.
func (m Model) activate() (tea.Model, tea.Cmd) {
	if m.focus == focusDetail && m.tab == tabRuns {
		runs := m.customerRuns(m.selected)
		if m.runCursor < len(runs) {
			return m.openRun(runs[m.runCursor])
		}
		return m, nil
	}
	if m.current() == nil {
		return m, nil
	}
	m.menu = menu{customer: m.selected, items: m.customerActions()}
	m.overlay = overlayMenu
	return m, nil
}

func (m Model) menuKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "esc":
		m.overlay = overlayNone
	case "up", "k":
		m.menu.sel = max(0, m.menu.sel-1)
	case "down", "j":
		m.menu.sel = max(0, min(len(m.menu.items)-1, m.menu.sel+1))
	case "enter", "space":
		m.overlay = overlayNone
		if m.menu.sel < len(m.menu.items) {
			return m.begin(request{action: m.menu.items[m.menu.sel], customer: m.menu.customer})
		}
	}
	return m, nil
}

func (m Model) runKey(k string) (tea.Model, tea.Cmd) {
	r := m.run
	switch k {
	case "esc":
		return m.leaveRun(), nil
	case "ctrl+c":
		if r.info.Status != "running" {
			m.status = "run " + r.id + " has ended; there is nothing to cancel"
			return m, nil
		}
		return m.beginCancel(r.info), nil
	case "up", "k":
		r.scroll(1, m.logRows())
	case "down", "j":
		r.scroll(-1, m.logRows())
	case "pgup":
		r.scroll(m.logRows(), m.logRows())
	case "pgdown":
		r.scroll(-m.logRows(), m.logRows())
	case "home":
		r.scroll(1<<30, m.logRows())
	case "end":
		r.offset = 0
	}
	return m, nil
}
