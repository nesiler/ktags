package main

// Mouse handling.
//
// Bubble Tea v2 delivers MouseClickMsg/MouseWheelMsg with plain terminal cell
// coordinates — it knows nothing about panels. bubblezone closes that gap:
// View() wraps clickable strings in zero-width markers, zone.Scan() strips them
// at the root and records their rectangles, and here we ask
// zone.Get(id).InBounds(msg).
//
// Every clickable action carries the zone id "act:<Action.ID>", so a hint click
// resolves back to a registry entry and goes through exec() exactly like the
// palette and the context menu.

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
		switch msg.Mouse().Button {
		case tea.MouseLeft:
			return m.handleClick(msg)
		case tea.MouseRight:
			return m.handleRightClick(msg)
		}
	}
	return m, nil
}

func (m model) handleWheel(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	up := msg.Mouse().Button == tea.MouseWheelUp

	// The palette match list scrolls with the wheel.
	if m.pal.open {
		q := m.parse()
		for i := range q.rows {
			if inZone("pal:"+fmt.Sprint(i), msg) {
				if up && m.pal.sel > 0 {
					m.pal.sel--
				} else if !up && m.pal.sel < len(q.rows)-1 {
					m.pal.sel++
				}
				return m, nil
			}
		}
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
		rows := m.run.flatten()
		for i := range rows {
			if inZone(fmt.Sprintf("tree:%d", i), msg) {
				step := 1
				if up {
					step = -1
				}
				if sel := m.run.treeSel + step; sel >= 0 && sel < len(rows) {
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
			d := 1
			if up {
				d = -1
			}
			n := len(m.customers)
			m.sel = (m.sel + d + n) % n
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
	case overlayMenu:
		for i := range m.menu.items {
			if inZone(fmt.Sprintf("menu:%d", i), msg) {
				act, name := m.menu.items[i], m.menu.name
				m.overlay, m.menu = overlayNone, nil
				return m.exec(act, name, Values{})
			}
		}
		m.overlay, m.menu = overlayNone, nil
		return m, nil
	case overlaySecret:
		for i, k := range secretKeys {
			if inZone("secret:"+k, msg) {
				return m, m.secret.reveal(i)
			}
		}
		return m, nil
	case overlayConfirm:
		if inZone("btn:cancel", msg) {
			m.overlay, m.confirm = overlayNone, nil
			return m, nil
		}
		if inZone("btn:confirm", msg) {
			return m.confirmAccept()
		}
		return m, nil
	case overlayHelp:
		m.overlay = overlayNone
		return m, nil
	}

	// Palette rows: clicking one is exactly Tab followed by Enter.
	if m.pal.open {
		q := m.parse()
		for i := range q.rows {
			if inZone("pal:"+fmt.Sprint(i), msg) {
				m.pal.buf = replaceLastToken(m.pal.buf, q.rows[i].label)
				m.pal.sel, m.pal.cycling = i, false
				return m.paletteEnter(m.parse())
			}
		}
	}

	// Hint bar.
	if inZone("btn:palette", msg) {
		m.openPalette()
		return m, nil
	}
	if inZone("btn:menu", msg) {
		m.openMenu()
		return m, nil
	}
	if inZone("btn:focus", msg) {
		m.focus = (m.focus + 1) % 3
		m.syncFocus()
		return m, nil
	}
	// Any zone named after a registry entry runs it through the pipeline.
	for _, a := range registry {
		if inZone("act:"+a.ID, msg) {
			return m.exec(a, "", Values{})
		}
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
	// the relative Y into a row index (the header takes the first line).
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

// handleRightClick selects whatever is under the pointer and opens its context
// menu — the third way into exec(), next to the palette and the hint bar.
func (m model) handleRightClick(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.screen != screenDashboard || m.overlay != overlayNone {
		return m, nil
	}

	for i, c := range m.customers {
		if inZone("cust:"+c.Name, msg) {
			m.sel = i
			m.list.Select(i)
			m.focus = focusCustomers
			m.syncFocus()
			m.syncTables()
			m.openMenu()
			return m, nil
		}
	}
	if z := zone.Get("tbl:nodes"); z != nil && z.InBounds(msg) {
		_, y := z.Pos(msg)
		m.focus = focusNodes
		m.syncFocus()
		if row := y - 1; row >= 0 && row < len(m.nodes.Rows()) {
			m.nodes.SetCursor(row)
		}
		m.openMenu()
		return m, nil
	}
	return m, nil
}

func inZone(id string, msg tea.MouseMsg) bool {
	z := zone.Get(id)
	return z != nil && !z.IsZero() && z.InBounds(msg)
}
