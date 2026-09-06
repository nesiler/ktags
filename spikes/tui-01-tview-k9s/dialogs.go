package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// centered wraps a primitive in two Flexes so it floats in the middle of the
// screen; tview.Pages draws it over the dashboard.
func centered(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 0, true).
			AddItem(nil, 0, 1, false), width, 0, true).
		AddItem(nil, 0, 1, false)
}

func (a *App) showOverlay(name string, p tview.Primitive, width, height int) {
	a.pages.AddPage(name, centered(p, width, height), true, true)
	a.app.SetFocus(p)
}

func (a *App) showFullPage(name string, p tview.Primitive) {
	a.pages.AddPage(name, p, true, true)
	a.app.SetFocus(p)
}

func (a *App) closeOverlay(name string) {
	a.pages.RemovePage(name)
	a.focusCurrent()
	a.renderStatus()
}

// ------------------------------------------------------------------ help ----

func (a *App) showHelp() {
	var b strings.Builder
	b.WriteString("[" + colAccent + "::b]ktags — keys[-::-]\n\n")
	rows := [][2]string{
		{"Tab / Shift-Tab", "cycle panel focus (focused panel is highlighted)"},
		{"↑ ↓ / j k / wheel", "move inside a panel"},
		{"click", "select a customer, a node or a health row"},
		{":", "open the command prompt (inline suggestions)"},
		{"h i b a s", "health, install, backup, add, secret"},
		{"m", "toggle mouse capture (off = terminal text selection)"},
		{"?", "this help"},
		{"Esc", "close an overlay / leave the run view"},
		{"q, Ctrl-C", "quit (Ctrl-C on a live run asks to abort)"},
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "  [%s::b]%-18s[-::-] %s\n", colHeading, r[0], r[1])
	}
	b.WriteString("\n[" + colAccent + "::b]commands[-::-]\n\n")
	for _, c := range CommandNames {
		fmt.Fprintf(&b, "  [%s::b]:%-9s[-::-] [%s]%s[-]\n", colHeading, c, colDim, CommandHelp[c])
	}
	b.WriteString("\n[" + colDim + "]any key closes[-]")

	tv := tview.NewTextView().SetDynamicColors(true).SetText(b.String())
	tv.SetBorder(true).SetTitle(" Help ").SetBorderColor(borderFocused).SetBorderPadding(0, 0, 2, 2)
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		a.closeOverlay("help")
		return nil
	})
	a.showOverlay("help", tv, 74, 26)
}

// ---------------------------------------------------------------- secrets ---

func (a *App) showSecrets() {
	c := a.current()
	secrets := Secrets(c)

	inner := tview.NewPages()
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(inner, 0, 1, true)
	box.SetBorder(true).
		SetTitle(fmt.Sprintf(" Secrets · %s ", c.Name)).
		SetBorderColor(borderFocused).
		SetBorderPadding(0, 0, 1, 1)

	stop := make(chan struct{})
	closed := false
	closeAll := func() {
		if closed {
			return
		}
		closed = true
		close(stop)
		a.closeOverlay("secret")
	}

	list := tview.NewList().ShowSecondaryText(false).SetUseStyleTags(true, false)
	list.SetSelectedStyle(tcell.StyleDefault.Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite))
	reveal := tview.NewTextView().SetDynamicColors(true)

	for _, s := range secrets {
		sec := s
		list.AddItem("[::b]"+sec.Key+"[::-]", "", 0, func() {
			inner.SwitchToPage("reveal")
			a.app.SetFocus(reveal)
			a.revealSecret(reveal, sec, stop, closeAll)
		})
	}
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEsc {
			closeAll()
			return nil
		}
		return ev
	})
	reveal.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Rune() == 'c' {
			a.flash("[%s]copied to clipboard (mock)[-]", colOK)
			return nil
		}
		closeAll()
		return nil
	})

	inner.AddPage("list", list, true, true).AddPage("reveal", reveal, true, false)
	a.showOverlay("secret", box, 72, 11)
	a.app.SetFocus(list)
}

// revealSecret prints the value and clears it after a 15 second countdown.
func (a *App) revealSecret(tv *tview.TextView, s Secret, stop <-chan struct{}, done func()) {
	const total = 15
	draw := func(left int) {
		tv.SetText(fmt.Sprintf(
			"\n  [%s::b]%s[-::-]\n\n  [%s::b]%s[-::-]\n\n  [%s]clears in %2ds · c copies (mock) · any key closes[-]",
			colHeading, s.Key, colWarn, s.Value, colDim, left))
	}
	draw(total)

	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for left := total - 1; left >= 0; left-- {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			l := left
			a.app.QueueUpdateDraw(func() {
				if l == 0 {
					tv.SetText(fmt.Sprintf("\n  [%s]secret cleared.[-]\n", colDim))
					return
				}
				draw(l)
			})
		}
		time.Sleep(700 * time.Millisecond)
		select {
		case <-stop:
		default:
			a.app.QueueUpdateDraw(done)
		}
	}()
}

// ----------------------------------------------------------- confirmations --

// confirmAction asks for confirmation before a destructive action: prod needs the
// customer name typed out, everything else is a y/N modal.
func (a *App) confirmAction(cmd string, c *Customer, onOK func()) {
	if c.Environment != "prod" {
		a.yesNo(fmt.Sprintf("Run [%s::b]%s[-::-] on [%s::b]%s[-::-] (%s)?", colAccent, cmd, colAccent, c.Name, c.Environment), onOK)
		return
	}

	msg := tview.NewTextView().SetDynamicColors(true)
	setMsg := func(extra string) {
		msg.SetText(fmt.Sprintf(
			"[%s::b]PRODUCTION[-::-] cluster.\n\nTo run [%s::b]%s[-::-] on [%s::b]%s[-::-], type the customer name below.%s",
			colFail, colAccent, cmd, colAccent, c.Name, extra))
	}
	setMsg("")

	form := tview.NewForm()
	form.AddInputField("name", "", 34, nil, nil)
	form.AddButton("Proceed", func() {
		typed := form.GetFormItem(0).(*tview.InputField).GetText()
		if strings.TrimSpace(typed) != c.Name {
			setMsg(fmt.Sprintf("\n\n[%s::b]%q does not match %q[-::-]", colFail, typed, c.Name))
			return
		}
		a.closeOverlay("confirm")
		onOK()
	})
	form.AddButton("Cancel", func() {
		a.closeOverlay("confirm")
		a.flash("[%s]%s cancelled[-]", colDim, cmd)
	})
	form.SetCancelFunc(func() {
		a.closeOverlay("confirm")
		a.flash("[%s]%s cancelled[-]", colDim, cmd)
	})

	box := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(msg, 0, 1, false).
		AddItem(form, 5, 0, true)
	box.SetBorder(true).SetTitle(" Confirm production action ").
		SetBorderColor(tcell.ColorRed).SetBorderPadding(1, 0, 2, 2)

	a.showOverlay("confirm", box, 64, 14)
	a.app.SetFocus(form)
}

func (a *App) yesNo(text string, onYes func()) {
	m := tview.NewModal().
		SetText(text).
		AddButtons([]string{"Yes", "No"}).
		SetDoneFunc(func(_ int, label string) {
			a.closeOverlay("confirm")
			if label == "Yes" {
				onYes()
			}
		})
	m.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'y', 'Y':
			a.closeOverlay("confirm")
			onYes()
			return nil
		case 'n', 'N':
			a.closeOverlay("confirm")
			return nil
		}
		if ev.Key() == tcell.KeyEsc {
			a.closeOverlay("confirm")
			return nil
		}
		return ev
	})
	a.pages.AddPage("confirm", m, true, true)
	a.app.SetFocus(m)
}

// confirmAbort is the Ctrl-C dialog of the run view.
func (a *App) confirmAbort() {
	rv := a.runView
	m := tview.NewModal().
		SetText(fmt.Sprintf("Abort run?  [%s]%s %s[-]   [y/N]", colAccent, rv.kind, rv.customer)).
		AddButtons([]string{"Abort", "Keep running"}).
		SetDoneFunc(func(_ int, label string) {
			a.closeOverlay("abort")
			if label == "Abort" {
				rv.requestAbort()
			}
		})
	m.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'y', 'Y':
			a.closeOverlay("abort")
			rv.requestAbort()
			return nil
		case 'n', 'N':
			a.closeOverlay("abort")
			return nil
		}
		if ev.Key() == tcell.KeyEsc {
			a.closeOverlay("abort")
			return nil
		}
		return ev
	})
	a.pages.AddPage("abort", m, true, true)
	a.app.SetFocus(m)
}

func (rv *runView) requestAbort() {
	if rv.running && rv.abort != nil {
		select {
		case <-rv.abort:
		default:
			close(rv.abort)
		}
	}
}
