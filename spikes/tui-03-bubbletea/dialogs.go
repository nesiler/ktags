package main

// Secret reveal dialog and the prod/non-prod confirmation prompt.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// ---------------------------------------------------------------------------
// Secret reveal
// ---------------------------------------------------------------------------

const secretTTL = 15

type secretState struct {
	customer  string
	sel       int
	revealed  string
	value     string
	remaining int
	gen       int
}

type secretTickMsg struct{ gen int }

func (s *secretState) tick() tea.Cmd {
	gen := s.gen
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return secretTickMsg{gen} })
}

func (s *secretState) reveal(i int) tea.Cmd {
	s.gen++
	s.sel = i
	s.revealed = secretKeys[i]
	s.value = secretValue(s.customer, s.revealed)
	s.remaining = secretTTL
	return s.tick()
}

func (m model) secretKey(k string) (tea.Model, tea.Cmd) {
	s := m.secret

	if s.revealed != "" {
		switch k {
		case "c":
			m.flash("copied "+s.revealed+" to clipboard (mock)", "ok")
			return m, m.expireToast()
		default:
			// Any key closes the reveal box early.
			s.revealed, s.value = "", ""
			s.gen++
			if k == "esc" {
				m.overlay = overlayNone
				m.secret = nil
			}
			return m, nil
		}
	}

	switch k {
	case "esc", "q":
		m.overlay = overlayNone
		m.secret = nil
		return m, nil
	case "up", "k":
		if s.sel > 0 {
			s.sel--
		}
		return m, nil
	case "down", "j":
		if s.sel < len(secretKeys)-1 {
			s.sel++
		}
		return m, nil
	case "enter", " ":
		return m, s.reveal(s.sel)
	}
	return m, nil
}

func (m model) renderSecret() string {
	s := m.secret
	w := 66
	if w > m.w-6 {
		w = m.w - 6
	}

	var b strings.Builder
	b.WriteString(m.th.faint.Render(" secrets for "+s.customer) + "\n\n")

	for i, k := range secretKeys {
		cursor := "  "
		label := m.th.dialog.Render(k)
		if i == s.sel {
			cursor = m.th.selected.Render("▸ ")
			label = m.th.selected.Render(k)
		}
		b.WriteString(zone.Mark("secret:"+k, padTo(" "+cursor+label, w-2)) + "\n")
	}

	b.WriteString("\n")
	if s.revealed != "" {
		val := lipgloss.NewStyle().Foreground(m.th.warn).Render(s.value)
		inner := m.th.box(
			fmt.Sprintf("%s — clears in %ds", s.revealed, s.remaining),
			" "+val,
			w-4, 3, true)
		b.WriteString(indent(inner, 1) + "\n")
		b.WriteString("\n " + m.th.keyHint.Render("c") + m.th.keyLabel.Render(" copy") +
			m.th.faint.Render("  ·  any key hides  ·  ") +
			m.th.keyHint.Render("esc") + m.th.keyLabel.Render(" close"))
	} else {
		b.WriteString(" " + m.th.faint.Render("enter to reveal · esc to close"))
	}

	body := b.String()
	return m.th.box("secret reveal", body, w, lipgloss.Height(body)+2, true)
}

// ---------------------------------------------------------------------------
// Confirmation
// ---------------------------------------------------------------------------

type confirmState struct {
	action   string // install | backup
	cust     Customer
	needName bool // prod: the operator must type the customer name
	typed    string
	err      string
}

func newConfirm(action string, c Customer) *confirmState {
	return &confirmState{action: action, cust: c, needName: c.Env == "prod"}
}

func (m model) confirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cf := m.confirm
	k := msg.String()

	if k == "esc" || k == "ctrl+c" {
		m.overlay = overlayNone
		m.confirm = nil
		return m, nil
	}

	if !cf.needName {
		switch k {
		case "y", "Y":
			m.overlay = overlayNone
			m.confirm = nil
			return m.runConfirmed(cf.action)
		default:
			m.overlay = overlayNone
			m.confirm = nil
			m.flash(cf.action+" cancelled", "info")
			return m, m.expireToast()
		}
	}

	switch k {
	case "backspace":
		if cf.typed != "" {
			cf.typed = cf.typed[:len(cf.typed)-1]
		}
		cf.err = ""
		return m, nil
	case "enter":
		if cf.typed == cf.cust.Name {
			m.overlay = overlayNone
			m.confirm = nil
			return m.runConfirmed(cf.action)
		}
		cf.err = "name does not match"
		return m, nil
	}
	if t := msg.Key().Text; t != "" {
		cf.typed += t
		cf.err = ""
	}
	return m, nil
}

func (m model) runConfirmed(action string) (tea.Model, tea.Cmd) {
	if action == "install" {
		return m.startRun("install")
	}
	m.flash("backup started for "+m.current().Name+" (mock)", "ok")
	m.status = "backup queued"
	return m, m.expireToast()
}

func (m model) renderConfirm() string {
	cf := m.confirm
	w := 62
	if w > m.w-6 {
		w = m.w - 6
	}

	var b strings.Builder
	b.WriteString("\n")
	head := fmt.Sprintf(" %s %s on %s",
		m.th.dialog.Render("Run"),
		m.th.selected.Render(cf.action),
		lipgloss.NewStyle().Foreground(m.th.statusColor(map[string]string{
			"prod": "fail", "staging": "warn", "test": "ok",
		}[cf.cust.Env])).Render(cf.cust.Name+" ("+cf.cust.Env+")"))
	b.WriteString(head + "\n\n")

	if cf.needName {
		b.WriteString(" " + m.th.toastErr.Render("This is a PRODUCTION cluster.") + "\n")
		b.WriteString(" " + m.th.faint.Render("Type the customer name to proceed:") + "\n\n")
		field := m.th.cmdBar.Render(padTo(" "+cf.typed+"█", w-6))
		b.WriteString(" " + field + "\n")
		if cf.err != "" {
			b.WriteString(" " + m.th.toastErr.Render(cf.err) + "\n")
		} else {
			b.WriteString("\n")
		}
		b.WriteString("\n " + zone.Mark("btn:confirm", m.th.keyHint.Render("enter")+m.th.keyLabel.Render(" confirm")) +
			m.th.faint.Render("  ·  ") +
			zone.Mark("btn:cancel", m.th.keyHint.Render("esc")+m.th.keyLabel.Render(" cancel")))
	} else {
		b.WriteString(" " + m.th.faint.Render("Proceed?") + "\n\n")
		b.WriteString(" " + zone.Mark("btn:confirm", m.th.keyHint.Render("y")+m.th.keyLabel.Render(" yes")) +
			m.th.faint.Render("  ·  ") +
			zone.Mark("btn:cancel", m.th.keyHint.Render("N")+m.th.keyLabel.Render(" no")))
	}

	body := b.String()
	return m.th.box("confirm", body, w, lipgloss.Height(body)+2, true)
}
