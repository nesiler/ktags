package main

// Theme and small layout helpers built on lipgloss v2.
//
// lipgloss v2 dropped AdaptiveColor: colours are plain image/color values now,
// and the app is expected to ask the terminal for its background colour
// (tea.RequestBackgroundColor) and rebuild its styles. newTheme(isDark) does
// exactly that, and main.go rebuilds on tea.BackgroundColorMsg.

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type theme struct {
	isDark bool

	// base
	fg     color.Color
	dim    color.Color
	accent color.Color
	border color.Color
	focus  color.Color

	// semantic
	ok     color.Color
	warn   color.Color
	fail   color.Color
	subtle color.Color

	title    lipgloss.Style
	topBar   lipgloss.Style
	statBar  lipgloss.Style
	cmdBar   lipgloss.Style
	keyHint  lipgloss.Style
	keyLabel lipgloss.Style
	toastErr lipgloss.Style
	toastOK  lipgloss.Style
	dialog   lipgloss.Style
	selected lipgloss.Style
	faint    lipgloss.Style
}

func newTheme(isDark bool) theme {
	ld := lipgloss.LightDark(isDark)

	t := theme{
		isDark: isDark,
		fg:     ld(lipgloss.Color("#1c1c1c"), lipgloss.Color("#e6e6e6")),
		dim:    ld(lipgloss.Color("#6c6c6c"), lipgloss.Color("#8a8a8a")),
		accent: ld(lipgloss.Color("#0060c0"), lipgloss.Color("#7dcfff")),
		border: ld(lipgloss.Color("#b4b4b4"), lipgloss.Color("#4a4a4a")),
		focus:  ld(lipgloss.Color("#0060c0"), lipgloss.Color("#7dcfff")),
		ok:     ld(lipgloss.Color("#137a3a"), lipgloss.Color("#9ece6a")),
		warn:   ld(lipgloss.Color("#8a6100"), lipgloss.Color("#e0af68")),
		fail:   ld(lipgloss.Color("#b02020"), lipgloss.Color("#f7768e")),
		subtle: ld(lipgloss.Color("#dcdcdc"), lipgloss.Color("#2a2a2a")),
	}

	t.title = lipgloss.NewStyle().Bold(true).Foreground(t.accent)
	t.topBar = lipgloss.NewStyle().Foreground(t.fg)
	t.statBar = lipgloss.NewStyle().Foreground(t.dim)
	t.cmdBar = lipgloss.NewStyle().Foreground(t.fg).Background(t.subtle)
	t.keyHint = lipgloss.NewStyle().Bold(true).Foreground(t.accent)
	t.keyLabel = lipgloss.NewStyle().Foreground(t.dim)
	t.toastErr = lipgloss.NewStyle().Bold(true).Foreground(t.fail)
	t.toastOK = lipgloss.NewStyle().Bold(true).Foreground(t.ok)
	t.dialog = lipgloss.NewStyle().Foreground(t.fg)
	t.selected = lipgloss.NewStyle().Bold(true).Foreground(t.accent)
	t.faint = lipgloss.NewStyle().Foreground(t.dim)

	return t
}

// statusColor maps ok/warn/fail to a colour.
func (t theme) statusColor(status string) color.Color {
	switch status {
	case "ok", "Ready", "changed":
		if status == "changed" {
			return t.warn
		}
		return t.ok
	case "warn", "skipping":
		return t.warn
	case "fail", "failed", "NotReady":
		return t.fail
	}
	return t.dim
}

// dot renders the coloured health indicator used in the customer list.
func (t theme) dot(status string) string {
	return lipgloss.NewStyle().Foreground(t.statusColor(status)).Render("●")
}

// envBadge renders the environment label.
func (t theme) envBadge(env string) string {
	c := t.dim
	switch env {
	case "prod":
		c = t.fail
	case "staging":
		c = t.warn
	case "test":
		c = t.accent
	}
	return lipgloss.NewStyle().Foreground(c).Render(env)
}

// ---------------------------------------------------------------------------
// Box drawing
// ---------------------------------------------------------------------------

// box draws a titled, fixed-size panel. It is hand-rolled instead of using
// lipgloss borders because bubblezone markers live inside the body and we want
// full control over padding/truncation so the marker bytes survive.
func (t theme) box(title, body string, w, h int, focused bool) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	innerW := w - 2
	innerH := h - 2

	bc := t.border
	if focused {
		bc = t.focus
	}
	bs := lipgloss.NewStyle().Foreground(bc)
	ts := lipgloss.NewStyle().Foreground(bc)
	if focused {
		ts = ts.Bold(true)
	}

	label := " " + title + " "
	if lipgloss.Width(label) > innerW {
		label = ansi.Truncate(label, innerW, "")
	}
	fill := innerW - lipgloss.Width(label) - 1
	if fill < 0 {
		fill = 0
	}
	top := bs.Render("╭─") + ts.Render(label) + bs.Render(strings.Repeat("─", fill)+"╮")
	bottom := bs.Render("╰" + strings.Repeat("─", innerW) + "╯")

	lines := strings.Split(body, "\n")
	out := make([]string, 0, h)
	out = append(out, top)
	for i := 0; i < innerH; i++ {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		out = append(out, bs.Render("│")+padTo(l, innerW)+bs.Render("│"))
	}
	out = append(out, bottom)
	return strings.Join(out, "\n")
}

// padTo pads or truncates a possibly-styled string to an exact cell width.
func padTo(s string, w int) string {
	if w <= 0 {
		return ""
	}
	cur := lipgloss.Width(s)
	switch {
	case cur == w:
		return s
	case cur < w:
		return s + strings.Repeat(" ", w-cur)
	default:
		return ansi.Truncate(s, w, "")
	}
}

// ansiTrim shortens a styled string to at most w cells, adding an ellipsis.
func ansiTrim(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// indent shifts every line of a block right by n cells. Prefixing a multi-line
// string with spaces only indents its first line, which silently breaks nested
// boxes.
func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// fitLines forces a block to exactly n lines.
func fitLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines[:n], "\n")
}

// overlay places a rendered dialog in the centre of the background, replacing
// the cells it covers. lipgloss v2 has a Canvas/Layer compositor, but this
// hand-rolled version keeps bubblezone markers intact in both layers.
func overlay(bg, fg string, w, h int) string {
	fgLines := strings.Split(fg, "\n")

	fw := 0
	for _, l := range fgLines {
		if x := lipgloss.Width(l); x > fw {
			fw = x
		}
	}
	return overlayAt(bg, fg, (w-fw)/2, (h-len(fgLines))/2)
}

// overlayAt places a rendered block at an exact cell position, so the context
// menu can pop up next to the row it belongs to instead of in the middle.
func overlayAt(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	fw := 0
	for _, l := range fgLines {
		if n := lipgloss.Width(l); n > fw {
			fw = n
		}
	}
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if y+len(fgLines) > len(bgLines) {
		y = len(bgLines) - len(fgLines)
		if y < 0 {
			y = 0
		}
	}

	for i, fl := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		left := padTo(ansi.Truncate(bgLines[row], x, ""), x)
		right := ansi.TruncateLeft(bgLines[row], x+fw, "")
		bgLines[row] = left + padTo(fl, fw) + right
	}
	return strings.Join(bgLines, "\n")
}
