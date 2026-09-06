package main

// Run view: fake streaming Ansible output.
//
// Streaming is wired with a plain goroutine + prog.Send(). Bubble Tea v2 has no
// io.Writer sink, so the producer converts each fake event into a typed message
// and pushes it into the Elm loop; Update appends it to a []string and refreshes
// the viewport. A generation counter (gen) makes stale runs harmless.

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

type runLineMsg struct {
	gen int
	ev  runEvent
}

type runDoneMsg struct{ gen int }

// taskNode is one TASK inside a play, with live per-result counters.
type taskNode struct {
	name                     string
	ok, changed, skip, faild int
}

// playNode is one PLAY in the tree.
type playNode struct {
	name      string
	tasks     []*taskNode
	collapsed bool
}

type runState struct {
	gen      int
	title    string // health | install
	customer string

	cancel context.CancelFunc

	lines []string // pre-styled log lines
	plays []*playNode
	hosts map[string]*[4]int // host -> ok, changed, skipped, failed

	total int
	seen  int

	done     bool
	askAbort bool
	aborted  bool

	treeSel    int // index into flattened tree
	autoScroll bool
	focusLog   bool
}

// startRun kicks off a fake run for the selected customer.
func (m model) startRun(kind string) (tea.Model, tea.Cmd) {
	c := m.current()
	if m.run != nil && m.run.cancel != nil {
		m.run.cancel()
	}

	var events []runEvent
	if kind == "install" {
		events = installScript(c)
	} else {
		events = healthScript(c)
	}

	m.runGen++
	gen := m.runGen
	ctx, cancel := context.WithCancel(context.Background())

	m.run = &runState{
		gen:        gen,
		title:      kind,
		customer:   c.Name,
		cancel:     cancel,
		total:      len(events),
		autoScroll: true,
	}
	m.screen = screenRun
	m.status = kind + " running"
	m.logvp.SetYOffset(0)
	m.refreshLog()

	go streamRun(ctx, gen, events)
	return m, m.spin.Tick
}

// send pushes a message into the running program. The producer goroutine lives
// outside the Elm loop, so prog.Send is the only way back in; the nil guard
// keeps the goroutine harmless if it outlives the program.
func send(msg tea.Msg) {
	if prog != nil {
		prog.Send(msg)
	}
}

// streamRun is the producer goroutine. It sends each event through
// prog.Send(), spacing them so the whole run takes roughly eight seconds.
func streamRun(ctx context.Context, gen int, events []runEvent) {
	if len(events) == 0 {
		send(runDoneMsg{gen})
		return
	}
	step := (8 * time.Second) / time.Duration(len(events))
	if step < 50*time.Millisecond {
		step = 50 * time.Millisecond
	}
	if step > 150*time.Millisecond {
		step = 150 * time.Millisecond
	}

	for _, ev := range events {
		jitter := time.Duration(rand.Int63n(int64(step/2) + 1))
		select {
		case <-ctx.Done():
			return
		case <-time.After(step/2 + jitter):
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		send(runLineMsg{gen: gen, ev: ev})
	}
	send(runDoneMsg{gen})
}

// push folds one event into the log buffer and the play/task tree.
func (r *runState) push(th *theme, ev runEvent, c Customer) {
	r.seen++

	switch ev.Kind {
	case "play":
		r.plays = append(r.plays, &playNode{name: ev.Play})
		r.lines = append(r.lines,
			lipgloss.NewStyle().Bold(true).Foreground(th.accent).Render("PLAY ["+ev.Play+"]"),
			"")

	case "task":
		if p := r.lastPlay(); p != nil {
			p.tasks = append(p.tasks, &taskNode{name: ev.Task})
		}
		r.lines = append(r.lines,
			lipgloss.NewStyle().Bold(true).Foreground(th.fg).Render("TASK ["+ev.Task+"]"))

	case "result":
		if r.hosts == nil {
			r.hosts = map[string]*[4]int{}
		}
		hc, found := r.hosts[ev.Host]
		if !found {
			hc = &[4]int{}
			r.hosts[ev.Host] = hc
		}
		t := r.lastTask()
		switch ev.Result {
		case "ok":
			hc[0]++
			if t != nil {
				t.ok++
			}
		case "changed":
			hc[1]++
			if t != nil {
				t.changed++
			}
		case "skipping":
			hc[2]++
			if t != nil {
				t.skip++
			}
		case "failed":
			hc[3]++
			if t != nil {
				t.faild++
			}
		}
		st := lipgloss.NewStyle().Foreground(th.statusColor(ev.Result))
		r.lines = append(r.lines, st.Render(ev.Result+": ")+
			lipgloss.NewStyle().Foreground(th.dim).Render("["+ev.Host+"]"))

	case "recap":
		r.lines = append(r.lines, "",
			lipgloss.NewStyle().Bold(true).Foreground(th.accent).Render("PLAY RECAP"),
			strings.Repeat("─", 40))
		for _, n := range c.Nodes {
			ok, ch, sk, fa := r.hostCounts(n.Name)
			r.lines = append(r.lines, fmt.Sprintf("%-18s : %s %s %s %s",
				n.Name,
				lipgloss.NewStyle().Foreground(th.ok).Render(fmt.Sprintf("ok=%-3d", ok)),
				lipgloss.NewStyle().Foreground(th.warn).Render(fmt.Sprintf("changed=%-3d", ch)),
				lipgloss.NewStyle().Foreground(th.dim).Render(fmt.Sprintf("skipped=%-3d", sk)),
				lipgloss.NewStyle().Foreground(th.fail).Render(fmt.Sprintf("failed=%-3d", fa)),
			))
		}

	case "summary":
		r.lines = append(r.lines, "",
			lipgloss.NewStyle().Bold(true).Foreground(th.accent).Render("SUMMARY"),
			strings.Repeat("─", 40))
		for _, ck := range c.Checks {
			r.lines = append(r.lines, fmt.Sprintf("%-18s %s  %s",
				ck.Name,
				lipgloss.NewStyle().Foreground(th.statusColor(ck.Status)).Render(padTo(upper(ck.Status), 5)),
				lipgloss.NewStyle().Foreground(th.dim).Render(ck.Detail)))
		}
	}
}

// hostCounts returns the ok/changed/skipped/failed tally for one host.
func (r *runState) hostCounts(host string) (ok, ch, sk, fa int) {
	if c, found := r.hosts[host]; found {
		return c[0], c[1], c[2], c[3]
	}
	return 0, 0, 0, 0
}

func (r *runState) lastPlay() *playNode {
	if len(r.plays) == 0 {
		return nil
	}
	return r.plays[len(r.plays)-1]
}

func (r *runState) lastTask() *taskNode {
	p := r.lastPlay()
	if p == nil || len(p.tasks) == 0 {
		return nil
	}
	return p.tasks[len(p.tasks)-1]
}

// refreshLog pushes the log buffer into the viewport, preserving the
// scroll-back position unless the user is parked at the bottom.
func (m *model) refreshLog() {
	if m.run == nil {
		return
	}
	atBottom := m.logvp.AtBottom()
	m.logvp.SetContentLines(m.run.lines)
	if m.run.autoScroll && atBottom {
		m.logvp.GotoBottom()
	}
}

// ---------------------------------------------------------------------------
// Run view keys
// ---------------------------------------------------------------------------

func (m model) runKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.run
	if r == nil {
		m.screen = screenDashboard
		return m, nil
	}

	if r.askAbort {
		switch msg.String() {
		case "y", "Y":
			if r.cancel != nil {
				r.cancel()
			}
			r.done, r.aborted, r.askAbort = true, true, false
			m.status = r.title + " aborted"
			m.flash("run aborted", "err")
			return m, m.expireToast()
		default:
			r.askAbort = false
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		m.screen = screenDashboard
		if !r.done {
			m.status = r.title + " running in background"
		}
		return m, nil
	case "q":
		return m, tea.Quit
	case "tab":
		r.focusLog = !r.focusLog
		return m, nil
	case "up", "k":
		if r.focusLog {
			m.logvp.ScrollUp(1)
			r.autoScroll = m.logvp.AtBottom()
		} else if r.treeSel > 0 {
			r.treeSel--
		}
		return m, nil
	case "down", "j":
		if r.focusLog {
			m.logvp.ScrollDown(1)
			r.autoScroll = m.logvp.AtBottom()
		} else if r.treeSel < len(r.flatten())-1 {
			r.treeSel++
		}
		return m, nil
	case "pgup":
		m.logvp.PageUp()
		r.autoScroll = m.logvp.AtBottom()
		return m, nil
	case "pgdown":
		m.logvp.PageDown()
		r.autoScroll = m.logvp.AtBottom()
		return m, nil
	case "end", "G":
		m.logvp.GotoBottom()
		r.autoScroll = true
		return m, nil
	case " ", "enter":
		rows := r.flatten()
		if r.treeSel < len(rows) && rows[r.treeSel].play != nil {
			rows[r.treeSel].play.collapsed = !rows[r.treeSel].play.collapsed
			if r.treeSel >= len(r.flatten()) {
				r.treeSel = len(r.flatten()) - 1
			}
		}
		return m, nil
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Run view rendering
// ---------------------------------------------------------------------------

type treeRow struct {
	play *playNode
	task *taskNode
}

func (r *runState) flatten() []treeRow {
	var out []treeRow
	for _, p := range r.plays {
		out = append(out, treeRow{play: p})
		if p.collapsed {
			continue
		}
		for _, t := range p.tasks {
			out = append(out, treeRow{task: t})
		}
	}
	return out
}

func (m model) renderRunScreen() string {
	r := m.run
	lw := m.leftW() + 14
	if lw > m.w/2 {
		lw = m.w / 2
	}
	rw := m.w - lw
	ch := m.contentH()

	tree := m.renderTree(lw-2, ch-2)
	treeBox := m.th.box("plays / tasks", tree, lw, ch, !r.focusLog)

	logTitle := fmt.Sprintf("%s · %s", r.title, r.customer)
	if !r.autoScroll {
		logTitle += m.th.faint.Render("  (scroll paused — end to resume)")
	}
	m.logvp.SetWidth(rw - 2)
	m.logvp.SetHeight(ch - 2)
	logBody := markBlock("log", m.logvp.View(), rw-2, ch-2)
	logBox := m.th.box(logTitle, logBody, rw, ch, r.focusLog)

	out := lipgloss.JoinHorizontal(lipgloss.Top, treeBox, logBox)

	if r.askAbort {
		dlg := m.th.box("abort", "\n  "+m.th.toastErr.Render("Abort run? [y/N]")+"\n", 30, 5, true)
		out = overlay(out, dlg, m.w, ch)
	}
	return out
}

func (m model) renderTree(w, h int) string {
	r := m.run
	rows := r.flatten()
	out := make([]string, 0, h)

	for i, row := range rows {
		prefix := " "
		if i == r.treeSel && !r.focusLog {
			prefix = lipgloss.NewStyle().Foreground(m.th.focus).Render("▎")
		}
		inner := w - 1

		var line string
		if row.play != nil {
			marker := "▾"
			if row.play.collapsed {
				marker = "▸"
			}
			line = padTo(m.th.title.Render(marker+" PLAY ")+m.th.topBar.Render(row.play.name), inner)
		} else {
			t := row.task
			var counts []string
			if t.ok > 0 {
				counts = append(counts, lipgloss.NewStyle().Foreground(m.th.ok).Render(fmt.Sprintf("ok %d", t.ok)))
			}
			if t.changed > 0 {
				counts = append(counts, lipgloss.NewStyle().Foreground(m.th.warn).Render(fmt.Sprintf("ch %d", t.changed)))
			}
			if t.skip > 0 {
				counts = append(counts, m.th.faint.Render(fmt.Sprintf("sk %d", t.skip)))
			}
			if t.faild > 0 {
				counts = append(counts, lipgloss.NewStyle().Foreground(m.th.fail).Render(fmt.Sprintf("fail %d", t.faild)))
			}
			cstr := strings.Join(counts, " ")
			name := "  ├ " + t.name
			gap := inner - lipgloss.Width(name) - lipgloss.Width(cstr)
			if gap < 1 {
				gap = 1
				name = ansiTrim(name, inner-lipgloss.Width(cstr)-1)
			}
			line = padTo(m.th.faint.Render(name)+strings.Repeat(" ", gap)+cstr, inner)
		}

		out = append(out, zone.Mark(fmt.Sprintf("tree:%d", i), prefix+line))
	}

	// Keep the selected row visible in a simple sliding window.
	if len(out) > h {
		start := r.treeSel - h/2
		if start < 0 {
			start = 0
		}
		if start+h > len(out) {
			start = len(out) - h
		}
		out = out[start : start+h]
	}
	return strings.Join(out, "\n")
}
