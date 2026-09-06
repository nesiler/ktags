package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// taskNode keeps the live counters of one task in the play/task tree.
type taskNode struct {
	node                        *tview.TreeNode
	label                       string
	ok, changed, skipped, faild int
}

// playNode is a play in the tree; it aggregates the counters of its tasks.
type playNode struct {
	node                        *tview.TreeNode
	name                        string
	ok, changed, skipped, faild int
}

// runView is the second body page: an ansible-navigator style play/task tree on
// the left and a streaming log (plus a summary table) on the right.
type runView struct {
	app *App

	flex    *tview.Flex
	tree    *tview.TreeView
	log     *tview.TextView
	summary *tview.Table
	right   *tview.Flex

	kind     string
	customer string

	running bool
	aborted bool
	abort   chan struct{}
	spin    int

	steps, done int
	// scrollDepth mirrors how far the user scrolled up in the log. tview keeps
	// its own auto-scroll flag (TextView.trackEnd) but exposes no getter, and
	// GetWrappedLineCount/GetScrollOffset are not safe to call while another
	// goroutine writes into the view, so the pause state is tracked here.
	scrollDepth int
	plays       []*playNode
	curPlay     *playNode
	curTask     *taskNode
	hostStats   map[string]*hostStat
	hostOrder   []string
}

type hostStat struct{ ok, changed, skipped, faild int }

func (r *runView) active() bool { return r != nil && r.running }

func (a *App) buildRunView() {
	rv := &runView{app: a}
	a.runView = rv

	rv.tree = tview.NewTreeView()
	rv.tree.SetGraphics(true).SetTopLevel(0)
	rv.tree.SetRoot(tview.NewTreeNode("no run yet").SetColor(tcell.ColorGray))
	frame(rv.tree.Box, "Plays / Tasks")

	rv.log = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetMaxLines(5000)
	// io.Writer + SetChangedFunc is the tview way to stream into a TextView. The
	// callback may fire on either goroutine, so it only nudges a channel; a pump
	// goroutine turns that into QueueUpdateDraw (see App.redraw).
	rv.log.SetChangedFunc(func() {
		select {
		case a.redraw <- struct{}{}:
		default:
		}
	})
	rv.log.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyUp:
			rv.scroll(1)
		case tcell.KeyDown:
			rv.scroll(-1)
		case tcell.KeyPgUp:
			rv.scroll(10)
		case tcell.KeyPgDn:
			rv.scroll(-10)
		case tcell.KeyHome:
			rv.scroll(1 << 20)
		case tcell.KeyEnd:
			rv.scroll(-(1 << 20))
		case tcell.KeyRune:
			switch ev.Rune() {
			case 'k':
				rv.scroll(1)
			case 'j':
				rv.scroll(-1)
			case 'g':
				rv.scroll(1 << 20)
			case 'G':
				rv.scroll(-(1 << 20))
			}
		}
		return ev
	})
	rv.log.SetMouseCapture(func(action tview.MouseAction, ev *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		switch action {
		case tview.MouseScrollUp:
			rv.scroll(1)
		case tview.MouseScrollDown:
			rv.scroll(-1)
		}
		return action, ev
	})
	frame(rv.log.Box, "Output")

	rv.summary = newTable()
	frame(rv.summary.Box, "Summary")

	rv.right = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(rv.log, 0, 1, true).
		AddItem(rv.summary, 0, 0, false)

	rv.flex = tview.NewFlex().
		AddItem(rv.tree, 44, 0, false).
		AddItem(rv.right, 0, 1, true)
}

// startRun switches to the run page and launches the fake Ansible run.
func (a *App) startRun(kind string) {
	rv := a.runView
	if rv.active() {
		a.flashErr("a run is already in progress (%s %s)", rv.kind, rv.customer)
		return
	}
	c := a.current()

	rv.kind, rv.customer = kind, c.Name
	rv.running, rv.aborted = true, false
	rv.abort = make(chan struct{})
	rv.done, rv.spin = 0, 0
	rv.plays, rv.curPlay, rv.curTask = nil, nil, nil
	rv.hostStats, rv.hostOrder = map[string]*hostStat{}, nil

	rv.log.Clear()
	rv.log.ScrollToEnd()
	rv.scrollDepth = 0
	rv.summary.Clear()
	rv.right.ResizeItem(rv.summary, 0, 0)

	root := tview.NewTreeNode(fmt.Sprintf("%s %s", kind, c.Name)).
		SetColor(tcell.ColorDodgerBlue).SetSelectable(false)
	rv.tree.SetRoot(root).SetCurrentNode(root)
	rv.tree.SetSelectedFunc(func(n *tview.TreeNode) { n.SetExpanded(!n.IsExpanded()) })
	rv.tree.SetTitle(fmt.Sprintf(" Plays / Tasks · %s ", kind))
	rv.log.SetTitle(fmt.Sprintf(" Output · %s %s ", kind, c.Name))

	steps := RunScript(kind, c)
	rv.steps = len(steps)

	a.bodyPages.SwitchToPage("run")
	a.setFocusRing("run")
	a.flash("[%s]%s started for %s[-] — Esc returns to the dashboard, the run keeps going", colAccent, kind, c.Name)

	go rv.execute(steps, kind, c)
	go rv.spinner()
}

// spinner keeps the status bar alive while the run is in the background.
func (rv *runView) spinner() {
	t := time.NewTicker(120 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		stop := false
		rv.app.app.QueueUpdateDraw(func() {
			rv.spin++
			rv.app.renderStatus()
			stop = !rv.running
		})
		if stop {
			return
		}
	}
}

// runStatus renders the run progress fragment of the status bar.
func (a *App) runStatus() string {
	rv := a.runView
	if rv == nil || (!rv.running && rv.done == 0) {
		return ""
	}
	ok, changed, faild := 0, 0, 0
	for _, p := range rv.plays {
		ok, changed, faild = ok+p.ok, changed+p.changed, faild+p.faild
	}
	head := fmt.Sprintf("[%s]done[-]", colOK)
	if rv.aborted {
		head = "[" + colFail + "]aborted[-]"
	} else if rv.running {
		head = fmt.Sprintf("[%s]%c[-]", colAccent, spinnerFrames[rv.spin%len(spinnerFrames)])
	}
	scroll := ""
	if body, _ := a.bodyPages.GetFrontPage(); body == "run" && !rv.atBottom() {
		scroll = " [" + colWarn + "]scroll paused (End resumes)[-]"
	}
	return fmt.Sprintf("%s %s %s %d/%d [%s]ok=%d changed=%d failed=%d[-]%s",
		head, rv.kind, rv.customer, rv.done, rv.steps, colDim, ok, changed, faild, scroll)
}

// scroll records a scroll gesture. tview does the actual scrolling (and turns its
// own auto-scroll back on once the view reaches the bottom again); this only keeps
// the status-bar indicator in sync.
func (rv *runView) scroll(delta int) {
	rv.scrollDepth += delta
	if rv.scrollDepth < 0 {
		rv.scrollDepth = 0
	}
}

// atBottom reports whether auto-scroll is still active.
func (rv *runView) atBottom() bool { return rv.scrollDepth == 0 }

// execute is the fake Ansible runner. It writes to the log through io.Writer and
// pushes every tree/counter mutation onto the main goroutine.
func (rv *runView) execute(steps []RunStep, kind string, c *Customer) {
	a := rv.app
	base := 8000 / max(len(steps), 1)
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))

	for _, s := range steps {
		select {
		case <-rv.abort:
			fmt.Fprintf(rv.log, "\n[%s::b]*** run aborted by the operator ***[-::-]\n", colFail)
			a.app.QueueUpdateDraw(func() { rv.finish(kind, c, true) })
			return
		default:
		}

		d := clampInt(base, 50, 150)
		time.Sleep(time.Duration(d)*time.Millisecond + time.Duration(rnd.Intn(40))*time.Millisecond)

		switch {
		case s.Play != "":
			fmt.Fprintf(rv.log, "\n[%s::b]PLAY %s[-::-] %s\n",
				colAccent, tview.Escape("["+s.Play+"]"), strings.Repeat("*", 20))
			name := s.Play
			a.app.QueueUpdateDraw(func() { rv.addPlay(name) })
		case s.Task != "":
			fmt.Fprintf(rv.log, "\n[%s::b]TASK %s[-::-] %s\n",
				colHeading, tview.Escape("["+s.Task+"]"), strings.Repeat("*", 20))
			name := s.Task
			a.app.QueueUpdateDraw(func() { rv.addTask(name) })
		default:
			fmt.Fprint(rv.log, resultLine(s))
			host, res := s.Host, s.Result
			a.app.QueueUpdateDraw(func() { rv.count(host, res) })
		}
		a.app.QueueUpdateDraw(func() { rv.done++ })
	}

	rv.writeRecap()
	a.app.QueueUpdateDraw(func() { rv.finish(kind, c, false) })
}

func resultLine(s RunStep) string {
	switch s.Result {
	case "changed":
		return fmt.Sprintf("[%s]changed: %s[-]\n", colWarn, tview.Escape("["+s.Host+"]"))
	case "skipping":
		return fmt.Sprintf("[%s]skipping: %s[-]\n", colDim, tview.Escape("["+s.Host+"]"))
	case "failed":
		return fmt.Sprintf("[%s::b]fatal: %s: FAILED! =>[-::-] [%s]%s[-]\n",
			colFail, tview.Escape("["+s.Host+"]"), colDim,
			tview.Escape(`{"changed": false, "msg": "systemd unit not active"}`))
	default:
		return fmt.Sprintf("[%s]ok: %s[-]\n", colOK, tview.Escape("["+s.Host+"]"))
	}
}

func (rv *runView) writeRecap() {
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s::b]PLAY RECAP[-::-] %s\n", colAccent, strings.Repeat("*", 20))
	for _, h := range rv.hostOrder {
		st := rv.hostStats[h]
		fmt.Fprintf(&b, "%-18s : [%s]ok=%-4d[-] [%s]changed=%-4d[-] unreachable=0 [%s]failed=%-3d[-] [%s]skipped=%d[-]\n",
			h, colOK, st.ok, colWarn, st.changed, colFail, st.faild, colDim, st.skipped)
	}
	fmt.Fprint(rv.log, b.String())
}

// --- main-goroutine mutations -------------------------------------------------

func (rv *runView) addPlay(name string) {
	n := tview.NewTreeNode("").SetColor(tcell.ColorDodgerBlue).SetSelectable(true)
	p := &playNode{node: n, name: name}
	rv.plays = append(rv.plays, p)
	rv.curPlay = p
	rv.curTask = nil
	rv.refreshPlay(p)
	rv.tree.GetRoot().AddChild(n)
}

func (rv *runView) addTask(label string) {
	if rv.curPlay == nil {
		rv.addPlay("default")
	}
	n := tview.NewTreeNode("").SetSelectable(true)
	t := &taskNode{node: n, label: label}
	rv.curTask = t
	rv.refreshTask(t)
	rv.curPlay.node.AddChild(n)
}

func (rv *runView) count(host, result string) {
	st := rv.hostStats[host]
	if st == nil {
		st = &hostStat{}
		rv.hostStats[host] = st
		rv.hostOrder = append(rv.hostOrder, host)
	}
	t, p := rv.curTask, rv.curPlay
	switch result {
	case "changed":
		st.changed++
		if t != nil {
			t.changed++
		}
		if p != nil {
			p.changed++
		}
	case "skipping":
		st.skipped++
		if t != nil {
			t.skipped++
		}
		if p != nil {
			p.skipped++
		}
	case "failed":
		st.faild++
		if t != nil {
			t.faild++
		}
		if p != nil {
			p.faild++
		}
	default:
		st.ok++
		if t != nil {
			t.ok++
		}
		if p != nil {
			p.ok++
		}
	}
	if t != nil {
		rv.refreshTask(t)
	}
	if p != nil {
		rv.refreshPlay(p)
	}
}

func counterText(ok, changed, skipped, faild int) string {
	parts := []string{fmt.Sprintf("[%s]ok=%d[-]", colOK, ok)}
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("[%s]changed=%d[-]", colWarn, changed))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("[%s]skip=%d[-]", colDim, skipped))
	}
	if faild > 0 {
		parts = append(parts, fmt.Sprintf("[%s::b]failed=%d[-::-]", colFail, faild))
	}
	return strings.Join(parts, " ")
}

func (rv *runView) refreshPlay(p *playNode) {
	p.node.SetText(fmt.Sprintf("PLAY %s  %s", p.name, counterText(p.ok, p.changed, p.skipped, p.faild)))
	if p.faild > 0 {
		p.node.SetColor(tcell.ColorRed)
	}
}

func (rv *runView) refreshTask(t *taskNode) {
	t.node.SetText(fmt.Sprintf("%s  %s", t.label, counterText(t.ok, t.changed, t.skipped, t.faild)))
	if t.faild > 0 {
		t.node.SetColor(tcell.ColorRed)
	}
}

func (rv *runView) finish(kind string, c *Customer, aborted bool) {
	a := rv.app
	rv.running = false
	rv.aborted = aborted
	if aborted {
		a.flashErr("%s run for %s aborted", kind, c.Name)
		a.renderStatus()
		return
	}
	checks := RunSummary(kind, c)
	fillHealthTable(rv.summary, checks)
	rv.summary.SetTitle(fmt.Sprintf(" Summary · %s %s ", kind, c.Name))
	rv.right.ResizeItem(rv.summary, min(len(checks)+3, 14), 0)
	a.flash("[%s]%s finished for %s[-] — Esc returns to the dashboard", colOK, kind, c.Name)
	a.renderStatus()
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
