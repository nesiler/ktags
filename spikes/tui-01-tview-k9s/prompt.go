package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Prompt is the ":" command bar. The inline (fish-style) suggestion behaviour is
// modelled on the prompt of k9s (derailed/k9s, ui/prompt.go, Apache-2.0); the code
// below is an independent reimplementation, not a copy.
//
// tview has no primitive with a real text cursor plus ghost text, so Prompt is a
// TextView with its own InputHandler: the buffer is kept here and the "cursor" is
// drawn as a reverse-video cell.
type Prompt struct {
	*tview.TextView

	commands []string
	help     map[string]string

	buf     []rune
	matches []string
	matchIx int

	// OnExecute receives the final command ("" when the line was empty).
	OnExecute func(cmd string)
	// OnCancel is called on Esc.
	OnCancel func()
}

// NewPrompt builds a command prompt over the given command vocabulary.
func NewPrompt(commands []string, help map[string]string) *Prompt {
	cmds := append([]string(nil), commands...)
	sort.Strings(cmds)

	p := &Prompt{
		TextView: tview.NewTextView().SetDynamicColors(true).SetScrollable(false),
		commands: cmds,
		help:     help,
	}
	p.SetBorder(true).
		SetTitle(" command ").
		SetTitleAlign(tview.AlignLeft).
		SetBorderColor(borderFocused).
		SetTitleColor(borderFocused)
	p.render()
	return p
}

// Reset clears the buffer, ready for a new command.
func (p *Prompt) Reset() {
	p.buf = p.buf[:0]
	p.recompute()
}

func (p *Prompt) text() string { return string(p.buf) }

// recompute refreshes the prefix matches and redraws the line.
func (p *Prompt) recompute() {
	prefix := p.text()
	p.matches = p.matches[:0]
	for _, c := range p.commands {
		if strings.HasPrefix(c, prefix) {
			p.matches = append(p.matches, c)
		}
	}
	if p.matchIx >= len(p.matches) {
		p.matchIx = 0
	}
	p.render()
}

// suggestion returns the currently highlighted completion, if any.
func (p *Prompt) suggestion() string {
	if len(p.matches) == 0 {
		return ""
	}
	return p.matches[p.matchIx]
}

func (p *Prompt) render() {
	typed := p.text()
	sug := p.suggestion()
	rest := ""
	if sug != "" && len(sug) > len(typed) {
		rest = sug[len(typed):]
	}

	// The cursor is the first ghost character in reverse video, or a reverse
	// space when the suggestion has been fully typed.
	var line strings.Builder
	fmt.Fprintf(&line, "[%s::b]:[-::-]%s", colAccent, tview.Escape(typed))
	if rest != "" {
		r := []rune(rest)
		fmt.Fprintf(&line, "[%s::r]%c[-::-][%s]%s[-]", colDim, r[0], colDim, tview.Escape(string(r[1:])))
	} else {
		line.WriteString("[::r] [::-]")
	}

	if desc, ok := p.help[sug]; ok {
		fmt.Fprintf(&line, "   [%s]— %s[-]", colDim, desc)
	}
	if len(p.matches) > 1 {
		alts := make([]string, 0, len(p.matches))
		for i, m := range p.matches {
			if i == p.matchIx {
				alts = append(alts, "["+colAccent+"::b]"+m+"[-::-]")
				continue
			}
			alts = append(alts, "["+colDim+"]"+m+"[-]")
		}
		line.WriteString("   [" + colDim + "]{[-]" + strings.Join(alts, "["+colDim+"]|[-]") + "[" + colDim + "]}[-]")
	}
	if typed == "" && len(p.matches) <= 1 {
		fmt.Fprintf(&line, "   [%s]Tab completes · Ctrl-N/Ctrl-P cycle · Enter runs · Esc cancels[-]", colDim)
	}
	p.SetText(line.String())
}

// InputHandler implements tview.Primitive; the prompt owns every key while focused.
func (p *Prompt) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return p.WrapInputHandler(func(ev *tcell.EventKey, _ func(tview.Primitive)) {
		switch ev.Key() {
		case tcell.KeyEsc:
			if p.OnCancel != nil {
				p.OnCancel()
			}
			return
		case tcell.KeyEnter:
			cmd := p.text()
			if !p.isCommand(cmd) && p.suggestion() != "" {
				cmd = p.suggestion()
			}
			if p.OnExecute != nil {
				p.OnExecute(strings.TrimSpace(cmd))
			}
			return
		case tcell.KeyTab, tcell.KeyRight:
			if s := p.suggestion(); s != "" {
				p.buf = []rune(s)
			}
		case tcell.KeyBackspace, tcell.KeyBackspace2, tcell.KeyLeft:
			if len(p.buf) > 0 {
				p.buf = p.buf[:len(p.buf)-1]
			}
		case tcell.KeyCtrlU:
			p.buf = p.buf[:0]
		case tcell.KeyCtrlW:
			p.buf = p.buf[:0]
		case tcell.KeyCtrlN, tcell.KeyDown:
			if len(p.matches) > 0 {
				p.matchIx = (p.matchIx + 1) % len(p.matches)
			}
		case tcell.KeyCtrlP, tcell.KeyUp:
			if len(p.matches) > 0 {
				p.matchIx = (p.matchIx - 1 + len(p.matches)) % len(p.matches)
			}
		case tcell.KeyRune:
			p.buf = append(p.buf, ev.Rune())
			p.matchIx = 0
		default:
			return
		}
		p.recompute()
	})
}

func (p *Prompt) isCommand(s string) bool {
	for _, c := range p.commands {
		if c == s {
			return true
		}
	}
	return false
}
