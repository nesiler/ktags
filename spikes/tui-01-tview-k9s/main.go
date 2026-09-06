// Command ktags-spike-tview-k9s is a throwaway TUI mock-up of ktags built with
// rivo/tview, laid out the way k9s lays out its screens: a header with context
// and key hints, a body of framed panels, a status line at the bottom and a ":"
// command prompt with inline suggestions.
//
// No SSH, no Ansible, no file I/O: every value comes from data.go.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Colours are chosen to work on both dark and light terminals: the background
// stays the terminal default and only foreground colours are set.
const (
	colOK      = "green"
	colWarn    = "yellow"
	colFail    = "red"
	colAccent  = "aqua"
	colDim     = "gray"
	colHeading = "white"
)

var (
	borderIdle    = tcell.ColorGray
	borderFocused = tcell.ColorDodgerBlue
)

// App holds every primitive of the spike plus the (in-memory) domain state.
type App struct {
	app   *tview.Application
	pages *tview.Pages // "main" + every modal overlay
	root  *tview.Flex

	ctxInfo *tview.TextView
	hints   *tview.TextView

	bodyPages   *tview.Pages // "dashboard" / "run"
	custList    *tview.List
	nodeTable   *tview.Table
	healthTable *tview.Table

	runView *runView

	bottomPages *tview.Pages // "status" / "prompt"
	status      *tview.TextView
	prompt      *Prompt

	customers []Customer
	sel       int

	mouseOn    bool
	promptOpen bool

	focusRing []tview.Primitive
	focusName []string
	focusIdx  int

	flashText string
	flashGen  int

	// redraw coalesces "the log grew" notifications coming from the run
	// goroutine into QueueUpdateDraw calls on a separate goroutine, so that a
	// SetChangedFunc firing on the main goroutine can never deadlock.
	redraw chan struct{}
}

func main() {
	a := newApp()
	if err := a.app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ktags spike:", err)
		os.Exit(1)
	}
}

func newApp() *App {
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.PrimaryTextColor = tcell.ColorDefault
	tview.Styles.BorderColor = borderIdle
	tview.Styles.TitleColor = tcell.ColorDefault
	tview.Styles.GraphicsColor = tcell.ColorGray
	tview.Styles.ContrastBackgroundColor = tcell.ColorDarkBlue
	tview.Styles.MoreContrastBackgroundColor = tcell.ColorTeal

	a := &App{
		app:       tview.NewApplication(),
		customers: Customers(),
		mouseOn:   true,
		redraw:    make(chan struct{}, 1),
	}

	a.buildHeader()
	a.buildDashboard()
	a.buildRunView()
	a.buildBottom()

	a.bodyPages = tview.NewPages().
		AddPage("dashboard", a.dashboardBody(), true, true).
		AddPage("run", a.runView.flex, true, false)

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.headerFlex(), 3, 0, false).
		AddItem(a.bodyPages, 0, 1, true).
		AddItem(a.bottomPages, 1, 0, false)

	a.pages = tview.NewPages().AddPage("main", a.root, true, true)

	a.setFocusRing("dashboard")
	a.selectCustomer(0)
	a.renderStatus()

	a.app.SetRoot(a.pages, true).EnableMouse(a.mouseOn).SetInputCapture(a.onKey)

	// Coalescing redraw pump for the streaming log (see App.redraw).
	go func() {
		for range a.redraw {
			a.app.QueueUpdateDraw(func() {})
		}
	}()

	return a
}

// ---------------------------------------------------------------- header ----

func (a *App) buildHeader() {
	a.ctxInfo = tview.NewTextView().SetDynamicColors(true)
	a.ctxInfo.SetBorderPadding(0, 0, 1, 1)

	a.hints = tview.NewTextView().SetDynamicColors(true).SetRegions(true)
	a.hints.SetBorderPadding(0, 0, 1, 1)
	a.hints.SetHighlightedFunc(func(added, removed, remaining []string) {
		if len(added) == 0 {
			return
		}
		cmd := added[0]
		// Clear the highlight on the next draw so the same hint can be
		// clicked twice in a row.
		go a.app.QueueUpdateDraw(func() { a.hints.Highlight() })
		// tview's TextView grabs the focus on MouseLeftDown; give it back to
		// the panel the user was working in before running the command.
		a.focusCurrent()
		a.clickHint(cmd)
	})
	a.renderHints()
}

func (a *App) headerFlex() *tview.Flex {
	return tview.NewFlex().
		AddItem(a.ctxInfo, 0, 1, false).
		AddItem(a.hints, 46, 0, false)
}

func (a *App) renderHints() {
	mouse := "on "
	if !a.mouseOn {
		mouse = "off"
	}
	hint := func(id, key, label string) string {
		return fmt.Sprintf(`["%s"][%s::b]<%s>[-::-] %-9s[""]`, id, colAccent, key, label)
	}
	a.hints.SetText(
		hint("health", "h", "health") + hint("install", "i", "install") + hint("backup", "b", "backup") + "\n" +
			hint("add", "a", "add") + hint("secret", "s", "secret") + hint("help", "?", "help") + "\n" +
			hint("command", ":", "command") + hint("mouse", "m", "mouse:"+mouse) + hint("quit", "q", "quit"),
	)
}

func (a *App) renderContext() {
	c := a.current()
	lock := ""
	if c.Locked() {
		lock = fmt.Sprintf("   [%s::b]LOCKED[-::-] [%s]%s[-]", colFail, colDim, c.LockMessage())
	}
	a.ctxInfo.SetText(fmt.Sprintf(
		"[%s::b]ktags[-::-] [%s]spike tui-01 (tview + tcell, k9s layout)[-]\n"+
			"Customer: [%s::b]%s[-::-]   Env: %s   Access: [%s]%s[-]%s\n"+
			"Health: %s   Backup: [%s]%s[-]",
		colHeading, colDim,
		colAccent, c.Name, envTag(c.Environment), colHeading, accessLabel(c), lock,
		stateTag(c.HealthState, c.LastHealth), colHeading, c.LastBackup,
	))
}

func accessLabel(c *Customer) string {
	if c.Access == "jump" {
		return "jump via " + c.JumpHost
	}
	return "direct"
}

func envTag(env string) string {
	switch env {
	case "prod":
		return "[" + colFail + "::b]prod[-::-]"
	case "staging":
		return "[" + colWarn + "]staging[-]"
	default:
		return "[" + colOK + "]" + env + "[-]"
	}
}

func stateTag(state, text string) string {
	return "[" + statusColor(state) + "]" + text + "[-]"
}

func statusColor(state string) string {
	switch state {
	case StatusOK:
		return colOK
	case StatusWarn:
		return colWarn
	case StatusFail:
		return colFail
	}
	return colDim
}

// ---------------------------------------------------------------- bottom ----

func (a *App) buildBottom() {
	a.status = tview.NewTextView().SetDynamicColors(true)
	a.status.SetBorderPadding(0, 0, 1, 1)

	a.prompt = NewPrompt(CommandNames, CommandHelp)
	a.prompt.SetBorderPadding(0, 0, 1, 1)
	a.prompt.OnExecute = func(cmd string) {
		a.closePrompt()
		if cmd != "" {
			a.execCommand(cmd)
		}
	}
	a.prompt.OnCancel = a.closePrompt

	a.bottomPages = tview.NewPages().
		AddPage("status", a.status, true, true).
		AddPage("prompt", a.prompt, true, false)
}

func (a *App) openPrompt() {
	a.promptOpen = true
	a.prompt.Reset()
	a.bottomPages.SwitchToPage("prompt")
	a.root.ResizeItem(a.bottomPages, 3, 0)
	a.app.SetFocus(a.prompt)
}

func (a *App) closePrompt() {
	a.promptOpen = false
	a.bottomPages.SwitchToPage("status")
	a.root.ResizeItem(a.bottomPages, 1, 0)
	a.focusCurrent()
}

func (a *App) renderStatus() {
	mouse := "[black:green] MOUSE ON [-:-]"
	if !a.mouseOn {
		mouse = "[black:orange] MOUSE OFF [-:-]"
	}
	focus := ""
	if a.focusIdx < len(a.focusName) {
		focus = a.focusName[a.focusIdx]
	}
	msg := a.flashText
	if msg == "" {
		msg = fmt.Sprintf("[%s]%d customers  ·  Tab switches panel  ·  \":\" for commands  ·  \"?\" for help[-]",
			colDim, len(a.customers))
	}
	run := ""
	if s := a.runStatus(); s != "" {
		run = "  " + s
	}
	a.status.SetText(fmt.Sprintf("%s [%s]focus:%s[-]%s  %s", mouse, colDim, focus, run, msg))
}

// flash shows a transient message in the status bar for 3 seconds.
func (a *App) flash(format string, args ...any) {
	a.flashGen++
	gen := a.flashGen
	a.flashText = fmt.Sprintf(format, args...)
	a.renderStatus()
	time.AfterFunc(3*time.Second, func() {
		a.app.QueueUpdateDraw(func() {
			if a.flashGen == gen {
				a.flashText = ""
				a.renderStatus()
			}
		})
	})
}

func (a *App) flashErr(format string, args ...any) {
	a.flash("[black:red] ERROR [-:-] [red]"+format+"[-]", args...)
}

// ----------------------------------------------------------------- focus ----

func (a *App) setFocusRing(page string) {
	switch page {
	case "run":
		a.focusRing = []tview.Primitive{a.runView.tree, a.runView.log, a.runView.summary}
		a.focusName = []string{"plays", "log", "summary"}
	default:
		a.focusRing = []tview.Primitive{a.custList, a.nodeTable, a.healthTable}
		a.focusName = []string{"customers", "nodes", "health"}
	}
	a.focusIdx = 0
	a.focusCurrent()
}

func (a *App) focusCurrent() {
	if a.focusIdx >= len(a.focusRing) {
		a.focusIdx = 0
	}
	a.app.SetFocus(a.focusRing[a.focusIdx])
	a.renderStatus()
}

func (a *App) cycleFocus(delta int) {
	n := len(a.focusRing)
	a.focusIdx = (a.focusIdx + delta + n) % n
	a.focusCurrent()
}

// frame gives a primitive a title and a border that lights up when focused.
func frame(b *tview.Box, title string) *tview.Box {
	b.SetBorder(true).SetTitle(" " + title + " ").SetTitleAlign(tview.AlignLeft)
	b.SetBorderColor(borderIdle)
	b.SetFocusFunc(func() {
		b.SetBorderColor(borderFocused)
		b.SetTitleColor(borderFocused)
	})
	b.SetBlurFunc(func() {
		b.SetBorderColor(borderIdle)
		b.SetTitleColor(tcell.ColorDefault)
	})
	return b
}

// -------------------------------------------------------------- key input ---

func (a *App) onKey(ev *tcell.EventKey) *tcell.EventKey {
	if ev.Key() == tcell.KeyCtrlC {
		return a.onCtrlC()
	}
	// Overlays (prompt, forms, modals) own the keyboard while they are up.
	if a.promptOpen || a.overlayOpen() {
		return ev
	}

	body, _ := a.bodyPages.GetFrontPage()
	switch ev.Key() {
	case tcell.KeyTab:
		a.cycleFocus(1)
		return nil
	case tcell.KeyBacktab:
		a.cycleFocus(-1)
		return nil
	case tcell.KeyEsc:
		if body == "run" {
			a.showDashboard()
		}
		return nil
	case tcell.KeyRune:
	default:
		return ev
	}

	switch ev.Rune() {
	case ':':
		a.openPrompt()
	case '?':
		a.showHelp()
	case 'm':
		a.toggleMouse()
	case 'h':
		a.execCommand("health")
	case 'i':
		a.execCommand("install")
	case 'b':
		a.execCommand("backup")
	case 'a':
		a.execCommand("add")
	case 's':
		a.execCommand("secret")
	case 'q':
		if body == "run" {
			a.showDashboard()
		} else {
			a.app.Stop()
		}
	default:
		return ev
	}
	return nil
}

func (a *App) onCtrlC() *tcell.EventKey {
	body, _ := a.bodyPages.GetFrontPage()
	if body == "run" && a.runView.active() {
		a.confirmAbort()
		return nil
	}
	if a.overlayOpen() || a.promptOpen {
		return nil
	}
	a.app.Stop()
	return nil
}

func (a *App) overlayOpen() bool {
	name, _ := a.pages.GetFrontPage()
	return name != "main"
}

func (a *App) toggleMouse() {
	a.mouseOn = !a.mouseOn
	a.app.EnableMouse(a.mouseOn)
	a.renderHints()
	if a.mouseOn {
		a.flash("[green]mouse enabled[-]")
	} else {
		a.flash("[orange]mouse disabled – the terminal can select text again (m to re-enable)[-]")
	}
}

func (a *App) clickHint(id string) {
	switch id {
	case "help":
		a.showHelp()
	case "mouse":
		a.toggleMouse()
	case "command":
		a.openPrompt()
	default:
		a.execCommand(id)
	}
}

// -------------------------------------------------------------- commands ----

func (a *App) current() *Customer { return &a.customers[a.sel] }

func (a *App) execCommand(cmd string) {
	c := a.current()
	switch cmd {
	case "quit", "q":
		a.app.Stop()
	case "help":
		a.showHelp()
	case "add":
		a.showAddForm()
	case "secret":
		a.showSecrets()
	case "health":
		a.startRun("health")
	case "install", "backup":
		if c.Locked() {
			a.flashErr("%s", c.LockMessage())
			return
		}
		a.confirmAction(cmd, c, func() { a.startRun(cmd) })
	default:
		a.flashErr("unknown command %q – try :health :install :backup :add :secret :quit", cmd)
	}
}
