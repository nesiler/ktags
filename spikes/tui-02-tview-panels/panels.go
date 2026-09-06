package main

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// colorNames maps the theme colours back to tview colour-tag names so the same
// palette can be used for both SetXxxColor() calls and "[green]" style tags.
var colorNames = map[tcell.Color]string{
	tcell.ColorGray:   "gray",
	tcell.ColorAqua:   "aqua",
	tcell.ColorSilver: "silver",
	tcell.ColorGreen:  "green",
	tcell.ColorYellow: "yellow",
	tcell.ColorRed:    "red",
	tcell.ColorTeal:   "teal",
	tcell.ColorWhite:  "white",
	tcell.ColorBlack:  "black",
}

func colName(c tcell.Color) string {
	if n, ok := colorNames[c]; ok {
		return n
	}
	return "-"
}

func statusColor(status string) tcell.Color {
	switch status {
	case "ok", "Ready", "yes":
		return colOK
	case "warn", "none":
		return colWarn
	case "fail", "NotReady":
		return colFail
	}
	return colMuted
}

func statusColorName(status string) string { return colName(statusColor(status)) }

func dot(status string) string {
	switch status {
	case "ok":
		return "●"
	case "warn":
		return "◐"
	case "fail":
		return "✖"
	}
	return "○"
}

func envBadge(env string) string {
	switch env {
	case "prod":
		return fmt.Sprintf("[%s]prod[-]", colName(colFail))
	case "staging":
		return fmt.Sprintf("[%s]staging[-]", colName(colWarn))
	default:
		return fmt.Sprintf("[%s]test[-]", colName(colMuted))
	}
}

// ------------------------------------------------------------------- top bar

func (u *ui) renderTopBar() {
	c := u.current()
	var sb strings.Builder
	width := 0
	u.hints = u.hints[:0]

	write := func(visible, tagged string) {
		sb.WriteString(tagged)
		width += utf8.RuneCountInString(visible)
	}
	hint := func(key, label string, action func()) {
		vis := key + ":" + label
		x0 := width
		write(vis, fmt.Sprintf("[%s::b]%s[-:-:-]:%s", colName(colAccent), key, label))
		u.hints = append(u.hints, topHint{label: label, x0: x0, x1: width, action: action})
		write("  ", "  ")
	}

	write(" ktags ", fmt.Sprintf("[%s::b] ktags [-:-:-]", colName(colAccent)))
	write("  ", "  ")
	write(c.Name, c.Name)
	write(" (", " (")
	write(c.Env, envBadge(c.Env))
	write(") ", ") ")
	write(c.Access, fmt.Sprintf("[%s]%s[-]", colName(colMuted), c.Access))
	if c.Lock != "" {
		write("  lock ", fmt.Sprintf("  [%s]lock[-] ", colName(colWarn)))
	}
	write("   ", "   ")

	hint("h", "health", func() { u.runCommand("health") })
	hint("i", "install", func() { u.runCommand("install") })
	hint("b", "backup", func() { u.runCommand("backup") })
	hint("a", "add", func() { u.runCommand("add") })
	hint("s", "secret", func() { u.runCommand("secret") })
	hint("m", "mouse", u.toggleMouse)
	hint("?", "help", u.showHelp)
	hint("q", "quit", func() { u.app.Stop() })

	u.topBar.SetText(sb.String())
}

// ------------------------------------------------------------ customer panel

func (u *ui) renderCustomers() {
	keep := u.customers.GetCurrentItem()
	u.customers.Clear()
	for _, c := range u.data {
		lock := ""
		if c.Lock != "" {
			lock = fmt.Sprintf("  [%s]🔒[-]", colName(colWarn))
		}
		main := fmt.Sprintf("[%s]%s[-] %s%s", statusColorName(c.HealthStatus), dot(c.HealthStatus), c.Name, lock)
		sec := fmt.Sprintf("   %s  [%s]%s  backup %s[-]", envBadge(c.Env), colName(colMuted), c.Access, c.BackupAge)
		u.customers.AddItem(main, sec, 0, nil)
	}
	if keep < u.customers.GetItemCount() {
		u.customers.SetCurrentItem(keep)
	}
}

// ---------------------------------------------------------------- node panel

func (u *ui) renderNodes() {
	c := u.current()
	t := u.nodes
	t.Clear()
	headers := []string{"NAME", "ROLE", "NODE IP", "ACCESS IP", "RKE2 VERSION", "STATUS", "LONGHORN"}
	for i, h := range headers {
		t.SetCell(0, i, headerCell(h))
	}
	for r, n := range c.Nodes {
		lh := "no"
		if n.Longhorn {
			lh = "yes"
		}
		t.SetCell(r+1, 0, cell(n.Name, tcell.ColorDefault))
		t.SetCell(r+1, 1, cell(n.Role, colMuted))
		t.SetCell(r+1, 2, cell(n.NodeIP, tcell.ColorDefault))
		t.SetCell(r+1, 3, cell(n.AccessIP, colMuted))
		t.SetCell(r+1, 4, cell(n.RKE2, colMuted))
		t.SetCell(r+1, 5, cell(n.Status, statusColor(n.Status)))
		t.SetCell(r+1, 6, cell(lh, statusColor(lh)))
	}
	if t.GetRowCount() > 1 {
		t.Select(1, 0)
	}
	t.ScrollToBeginning()
}

// -------------------------------------------------------------- health panel

func (u *ui) renderHealth() {
	c := u.current()
	t := u.health
	t.Clear()
	for i, h := range []string{"CHECK", "STATUS", "DETAIL"} {
		t.SetCell(0, i, headerCell(h))
	}
	for r, h := range c.Health {
		t.SetCell(r+1, 0, cell(h.Name, tcell.ColorDefault))
		t.SetCell(r+1, 1, cell(fmt.Sprintf("%s %s", dot(h.Status), strings.ToUpper(h.Status)), statusColor(h.Status)))
		t.SetCell(r+1, 2, cell(h.Detail, colMuted))
	}
	if t.GetRowCount() > 1 {
		t.Select(1, 0)
	}
	t.ScrollToBeginning()
}

func headerCell(s string) *tview.TableCell {
	return tview.NewTableCell(s).
		SetTextColor(colHeader).
		SetAttributes(tcell.AttrBold).
		SetSelectable(false).
		SetExpansion(1)
}

func cell(s string, color tcell.Color) *tview.TableCell {
	return tview.NewTableCell(s + "  ").SetTextColor(color).SetExpansion(1)
}
