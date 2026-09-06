package main

// Mouse handling.
//
// Bubble Tea v2 delivers MouseClickMsg/MouseWheelMsg/MouseMotionMsg with plain
// terminal cell coordinates — it knows nothing about panels. bubblezone closes
// that gap: View() wraps clickable strings in zero-width markers, zone.Scan()
// strips them at the root and records their rectangles, and here we ask
// zone.Get(id).InBounds(msg).
//
// The catch is that markers can only be injected where *we* build the string.
// Third-party bubbles (table, viewport) render themselves, so for those we mark
// the whole component and convert the click into a row index with
// ZoneInfo.Pos() plus a header offset.

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.mouseOn {
		return m, nil
	}

	// The huh form draws its own cursor and has no zones; ignore mouse there.
	if m.screen == screenForm {
		return m, nil
	}

	switch msg.(type) {
	case tea.MouseWheelMsg:
		return m.handleWheel(msg)
	case tea.MouseClickMsg:
		if msg.Mouse().Button != tea.MouseLeft {
			return m, nil
		}
		return m.handleClick(msg)
	}
	return m, nil
}

func (m model) handleWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	up := msg.Mouse().Button == tea.MouseWheelUp
	delta := 3
	if up {
		delta = -3
	}

	// Run view: the log viewport and the play tree.
	if m.screen == screenRun && m.run != nil {
		if inZone("log", msg) {
			if up {
				m.logvp.ScrollUp(3)
			} else {
				m.logvp.ScrollDown(3)
			}
			// Auto-scroll resumes as soon as the user lands back at the bottom.
			m.run.autoScroll = m.logvp.AtBottom()
			return m, nil
		}
		for i := range m.run.flatten() {
			if inZone(fmt.Sprintf("tree:%d", i), msg) {
				step := 1
				if up {
					step = -1
				}
				sel := m.run.treeSel + step
				if sel >= 0 && sel < len(m.run.flatten()) {
					m.run.treeSel = sel
				}
				return m, nil
			}
		}
		return m, nil
	}

	// Dashboard.
	for _, c := range m.customers {
		if inZone("cust:"+c.Name, msg) {
			n := len(m.customers)
			m.sel = (m.sel + sign(delta) + n) % n
			m.list.Select(m.sel)
			m.syncTables()
			return m, nil
		}
	}
	if inZone("tbl:nodes", msg) {
		if up {
			m.nodes.MoveUp(1)
		} else {
			m.nodes.MoveDown(1)
		}
		return m, nil
	}
	if inZone("tbl:checks", msg) {
		if up {
			m.checks.MoveUp(1)
		} else {
			m.checks.MoveDown(1)
		}
		return m, nil
	}
	return m, nil
}

func (m model) handleClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Overlays get first refusal.
	switch m.overlay {
	case overlaySecret:
		for i, k := range secretKeys {
			if inZone("secret:"+k, msg) {
				return m, m.secret.reveal(i)
			}
		}
		return m, nil
	case overlayConfirm:
		if inZone("btn:cancel", msg) {
			m.overlay = overlayNone
			m.confirm = nil
			return m, nil
		}
		if inZone("btn:confirm", msg) {
			cf := m.confirm
			if cf.needName && cf.typed != cf.cust.Name {
				cf.err = "name does not match"
				return m, nil
			}
			m.overlay = overlayNone
			m.confirm = nil
			return m.runConfirmed(cf.action)
		}
		return m, nil
	case overlayHelp:
		m.overlay = overlayNone
		return m, nil
	}

	// Top-bar hints and command-bar suggestions share the "hint:" namespace.
	if inZone("hint:help", msg) {
		m.overlay = overlayHelp
		return m, nil
	}
	for _, cmd := range commands {
		if inZone("hint:"+cmd, msg) {
			m.cmdOpen = false
			m.cmdBuf = ""
			return m.action(cmd)
		}
	}
	if inZone("btn:mouse", msg) {
		m.mouseOn = false
		m.flash("mouse off — press m to re-enable", "info")
		return m, m.expireToast()
	}

	if m.screen == screenRun && m.run != nil {
		rows := m.run.flatten()
		for i := range rows {
			if inZone(fmt.Sprintf("tree:%d", i), msg) {
				m.run.focusLog = false
				m.run.treeSel = i
				if rows[i].play != nil {
					rows[i].play.collapsed = !rows[i].play.collapsed
				}
				return m, nil
			}
		}
		if inZone("log", msg) {
			m.run.focusLog = true
		}
		return m, nil
	}

	// Customer rows: one zone per row, emitted by the list delegate.
	for i, c := range m.customers {
		if inZone("cust:"+c.Name, msg) {
			m.sel = i
			m.list.Select(i)
			m.focus = focusCustomers
			m.syncFocus()
			m.syncTables()
			return m, nil
		}
	}

	// Tables: bubbles renders them itself, so we mark the container and turn
	// the relative Y into a row index (header takes the first line).
	if z := zone.Get("tbl:nodes"); z != nil && z.InBounds(msg) {
		_, y := z.Pos(msg)
		m.focus = focusNodes
		m.syncFocus()
		if row := y - 1; row >= 0 && row < len(m.nodes.Rows()) {
			m.nodes.SetCursor(row)
		}
		return m, nil
	}
	if z := zone.Get("tbl:checks"); z != nil && z.InBounds(msg) {
		_, y := z.Pos(msg)
		m.focus = focusChecks
		m.syncFocus()
		if row := y - 1; row >= 0 && row < len(m.checks.Rows()) {
			m.checks.SetCursor(row)
		}
		return m, nil
	}
	return m, nil
}

func inZone(id string, msg tea.MouseMsg) bool {
	z := zone.Get(id)
	return z != nil && !z.IsZero() && z.InBounds(msg)
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	return 1
}
