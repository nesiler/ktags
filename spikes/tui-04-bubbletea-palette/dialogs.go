package main

// Secret reveal dialog and the danger prompt.
//
// The prompt is no longer written per command: exec() builds it from the
// action's Danger level, so a new registry entry gets the right ceremony for
// free (y/N, or "type the customer name" once the target is prod).

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
	case "enter", "space":
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
// Danger prompt
// ---------------------------------------------------------------------------

// confirmState is a pending invocation waiting for the operator's blessing. It
// keeps the target by name, not by pointer, and re-resolves on confirm.
type confirmState struct {
	act    *Action
	target string
	args   Values

	cust     Customer
	what     string // "install acme" / "drain acme-srv-1"
	needName bool   // DangerTypeName on a prod cluster
	typed    string
	err      string
}

// newConfirm builds the prompt for one invocation straight from the action.
func newConfirm(a *Action, target string, args Values, ctx Context) *confirmState {
	cf := &confirmState{act: a, target: target, args: args, what: a.Verb}
	if ctx.Customer != nil {
		cf.cust = *ctx.Customer
		cf.what = a.Verb + " " + ctx.Customer.Name
	}
	if ctx.Node != nil {
		cf.what = a.Verb + " " + ctx.Node.Name
	}
	cf.needName = a.Danger == DangerTypeName && cf.cust.Env == "prod"
	return cf
}

func (m model) confirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	cf := m.confirm
	k := msg.String()

	if k == "esc" || k == "ctrl+c" {
		m.overlay, m.confirm = overlayNone, nil
		return m, nil
	}

	if !cf.needName {
		switch k {
		case "y", "Y", "enter":
			return m.confirmAccept()
		default:
			m.overlay, m.confirm = overlayNone, nil
			m.flash(cf.act.Verb+" cancelled", "info")
			return m, m.expireToast()
		}
	}

	switch k {
	case "backspace":
		if cf.typed != "" {
			r := []rune(cf.typed)
			cf.typed = string(r[:len(r)-1])
		}
		cf.err = ""
		return m, nil
	case "enter":
		return m.confirmAccept()
	}
	if t := msg.Key().Text; t != "" {
		cf.typed += t
		cf.err = ""
	}
	return m, nil
}

// confirmAccept is the last gate of the pipeline: it re-resolves the target and
// hands the invocation to fire().
func (m model) confirmAccept() (tea.Model, tea.Cmd) {
	cf := m.confirm
	if cf.needName && cf.typed != cf.cust.Name {
		cf.err = "name does not match"
		return m, nil
	}
	m.overlay, m.confirm = overlayNone, nil

	ctx, err := m.resolve(cf.act, cf.target)
	if err != nil {
		m.flash(err.Error(), "err")
		return m, m.expireToast()
	}
	return m.fire(cf.act, ctx, cf.args)
}

func (m model) renderConfirm() string {
	cf := m.confirm
	w := 62
	if w > m.w-6 {
		w = m.w - 6
	}

	var b strings.Builder
	b.WriteString("\n")

	envColour := map[string]string{"prod": "fail", "staging": "warn", "test": "ok"}[cf.cust.Env]
	head := " " + m.th.dialog.Render("Run") + " " + m.th.selected.Render(cf.what) +
		" " + lipgloss.NewStyle().Foreground(m.th.statusColor(envColour)).Render("("+cf.cust.Env+")")
	if s := cf.args.String(); s != "" {
		head += " " + m.th.faint.Render(s)
	}
	b.WriteString(head + "\n")
	b.WriteString(" " + m.th.faint.Render(cf.act.Help) + "\n\n")

	if cf.needName {
		b.WriteString(" " + m.th.toastErr.Render("This is a PRODUCTION cluster.") + "\n")
		b.WriteString(" " + m.th.faint.Render("Type the customer name to proceed:") + "\n\n")
		b.WriteString(" " + m.th.cmdBar.Render(padTo(" "+cf.typed+"█", w-6)) + "\n")
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
