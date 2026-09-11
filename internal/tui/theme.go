package tui

import (
	"image/color" //nolint:misspell // standard library package name

	"charm.land/lipgloss/v2"
)

// tone is the semantic colour of a value (docs/guides/ui.md §11). Colour never carries a state
// alone: every toned value also has a symbol or a word.
type tone int

const (
	toneNone tone = iota
	toneOK
	toneWarn
	toneFail
	toneMuted
	toneAccent
)

// ink is one terminal colour.
type ink = color.Color //nolint:misspell // standard library type name

// hex is a colour from its hex code.
func hex(code string) ink {
	return lipgloss.Color(code) //nolint:misspell // lipgloss API name
}

// theme is the adaptive palette: one set for a light and one for a dark terminal background.
type theme struct {
	inks map[tone]ink
}

func newTheme(dark bool) theme {
	ld := lipgloss.LightDark(dark)
	return theme{inks: map[tone]ink{
		toneOK:     ld(hex("#1a7f37"), hex("#3fb950")),
		toneWarn:   ld(hex("#9a6700"), hex("#d29922")),
		toneFail:   ld(hex("#cf222e"), hex("#f85149")),
		toneMuted:  ld(hex("#6e7781"), hex("#8b949e")),
		toneAccent: ld(hex("#0969da"), hex("#58a6ff")),
	}}
}

// greyed is the theme for data kept on screen while reconnecting: every value muted.
func (t theme) greyed() theme {
	muted := t.inks[toneMuted]
	g := theme{inks: map[tone]ink{toneNone: muted}}
	for k := range t.inks {
		g.inks[k] = muted
	}
	return g
}

func (t theme) paint(to tone, s string) string {
	c, ok := t.inks[to]
	if !ok || s == "" {
		return s
	}
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

func bold(s string) string {
	return lipgloss.NewStyle().Bold(true).Render(s)
}

func reverse(s string) string {
	return lipgloss.NewStyle().Reverse(true).Render(s)
}
