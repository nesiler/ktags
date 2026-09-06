package main

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/gdamore/tcell/v2"
	"strings"
	"sync"
	"time"

	"github.com/rivo/tview"
)

const (
	// logBufferLines is the lazygit-style view buffer size: only the last N
	// lines are kept in memory and in the TextView.
	logBufferLines = 2000
	// redrawInterval batches log appends so a chatty run cannot force one
	// redraw per line.
	redrawInterval = 30 * time.Millisecond
	// runDuration is the rough wall time of a fake run.
	runDuration = 8 * time.Second
)

// ---------------------------------------------------------------- viewBuffer

// viewBuffer is a bounded, append-only line buffer with a pending queue. The UI
// thread drains the pending queue every redrawInterval; when the ring wrapped
// it asks for a full rewrite instead of an incremental append.
type viewBuffer struct {
	mu      sync.Mutex
	max     int
	lines   []string
	pending []string
	trimmed bool
	total   int
}

func newViewBuffer(max int) *viewBuffer {
	return &viewBuffer{max: max, lines: make([]string, 0, max)}
}

func (b *viewBuffer) Append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, line)
	b.pending = append(b.pending, line)
	b.total++
	if len(b.lines) > b.max {
		drop := len(b.lines) - b.max
		b.lines = append(b.lines[:0], b.lines[drop:]...)
		b.trimmed = true
	}
}

func (b *viewBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = b.lines[:0]
	b.pending = b.pending[:0]
	b.trimmed = true
	b.total = 0
}

func (b *viewBuffer) hasPending() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending) > 0 || b.trimmed
}

// flush returns the lines to append, or the whole buffer when a rewrite is
// needed. Only the UI thread calls it.
func (b *viewBuffer) flush() (add []string, all string, rewrite bool, count int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rewrite = b.trimmed
	b.trimmed = false
	count = len(b.lines)
	if rewrite {
		all = strings.Join(b.lines, "\n")
	} else {
		add = append(add, b.pending...)
	}
	b.pending = b.pending[:0]
	return
}

func (b *viewBuffer) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// ------------------------------------------------------------------ run state

type taskModel struct {
	name                       string
	ok, changed, skipped, fail int
	node                       *tview.TreeNode
}

type playModel struct {
	name  string
	tasks []*taskModel
	node  *tview.TreeNode
}

type runState struct {
	mu        sync.Mutex
	label     string
	running   bool
	aborted   bool
	finished  bool
	cancel    context.CancelFunc
	plays     []*playModel
	dirty     bool
	frame     int
	follow    bool
	lastOff   int
	startedAt time.Time
	ok, chg   int
	skip, bad int
}

func newRunState() *runState { return &runState{follow: true} }

func (r *runState) active() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (r *runState) following() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.follow
}

func (r *runState) setFollow(v bool) {
	r.mu.Lock()
	r.follow = v
	r.mu.Unlock()
}

func (r *runState) toggleFollow() {
	r.mu.Lock()
	r.follow = !r.follow
	r.mu.Unlock()
}

func (r *runState) treeDirty() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dirty
}

func (r *runState) start(label string, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.label = label
	r.running = true
	r.finished = false
	r.aborted = false
	r.cancel = cancel
	r.plays = nil
	r.dirty = true
	r.follow = true
	r.startedAt = time.Now()
	r.ok, r.chg, r.skip, r.bad = 0, 0, 0, 0
}

func (r *runState) stop(aborted bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = false
	r.finished = true
	r.aborted = aborted
	r.dirty = true
}

func (r *runState) abort() {
	r.mu.Lock()
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (r *runState) addPlay(name string) *playModel {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := &playModel{name: name}
	r.plays = append(r.plays, p)
	r.dirty = true
	return p
}

func (r *runState) addTask(p *playModel, name string) *taskModel {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := &taskModel{name: name}
	p.tasks = append(p.tasks, t)
	r.dirty = true
	return t
}

func (r *runState) count(t *taskModel, result string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch result {
	case "ok":
		t.ok++
		r.ok++
	case "changed":
		t.changed++
		r.chg++
	case "skipping":
		t.skipped++
		r.skip++
	case "failed":
		t.fail++
		r.bad++
	}
	r.dirty = true
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// statusText is the run progress shown in the status bar while the dashboard
// stays fully usable.
func (r *runState) statusText() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running && !r.finished {
		return ""
	}
	tasks := 0
	for _, p := range r.plays {
		tasks += len(p.tasks)
	}
	counts := fmt.Sprintf("[%s]ok %d[-] [%s]changed %d[-] [%s]failed %d[-]",
		colName(colOK), r.ok, colName(colWarn), r.chg, colName(colFail), r.bad)
	if r.running {
		r.frame++
		return fmt.Sprintf("[%s]%s %s[-]  %d tasks  %s  %s",
			colName(colAccent), spinner[(r.frame/3)%len(spinner)], r.label, tasks, counts,
			time.Since(r.startedAt).Truncate(time.Second))
	}
	verdict := fmt.Sprintf("[%s]done[-]", colName(colOK))
	if r.aborted {
		verdict = fmt.Sprintf("[%s]aborted[-]", colName(colFail))
	}
	return fmt.Sprintf("%s %s  %d tasks  %s", verdict, r.label, tasks, counts)
}

// ------------------------------------------------------------------- ticker

// startTicker drives the throttled redraw loop. It only wakes the UI when
// there is something new, so an idle dashboard costs no redraws.
func (u *ui) startTicker() {
	go func() {
		t := time.NewTicker(redrawInterval)
		defer t.Stop()
		for range t.C {
			if !u.logBuf.hasPending() && !u.run.active() && !u.run.treeDirty() {
				continue
			}
			u.app.QueueUpdateDraw(func() {
				u.syncLogScroll()
				u.renderLog(false)
				u.renderTree()
				u.statusBar.SetText(u.statusLine("", colMuted))
			})
		}
	}()
}

// syncLogScroll implements "auto-scroll only when at bottom": a scroll the user
// performed since the last tick pauses or resumes following.
func (u *ui) syncLogScroll() {
	row, _ := u.logView.GetScrollOffset()
	u.run.mu.Lock()
	changed := row != u.run.lastOff
	u.run.lastOff = row
	u.run.mu.Unlock()
	if !changed {
		return
	}
	u.run.setFollow(u.logAtBottom())
}

func (u *ui) logAtBottom() bool {
	row, _ := u.logView.GetScrollOffset()
	_, _, _, h := u.logView.GetInnerRect()
	n := u.logBuf.len()
	if n <= h {
		return true
	}
	return row >= n-h
}

// renderLog drains the view buffer into the TextView. Appends are incremental;
// only a wrapped ring buffer forces a full SetText.
func (u *ui) renderLog(force bool) {
	if !force && !u.logBuf.hasPending() {
		return
	}
	add, all, rewrite, _ := u.logBuf.flush()
	if rewrite {
		u.logView.SetText(all)
	} else {
		for _, l := range add {
			fmt.Fprintln(u.logView, l)
		}
	}
	if u.run.following() {
		u.logView.ScrollToEnd()
		row, _ := u.logView.GetScrollOffset()
		u.run.mu.Lock()
		u.run.lastOff = row
		u.run.mu.Unlock()
	}
	u.logView.SetTitle(u.panelTitle(4))
}

// renderTree mirrors the run model into the task tree side panel, keeping the
// expand/collapse state of nodes that already exist.
func (u *ui) renderTree() {
	if !u.run.treeDirty() {
		return
	}
	u.run.mu.Lock()
	defer u.run.mu.Unlock()
	u.run.dirty = false

	root := u.tree.GetRoot()
	if root == nil {
		root = tview.NewTreeNode("run").SetSelectable(false)
		u.tree.SetRoot(root).SetTopLevel(1)
	}
	if len(u.run.plays) == 0 {
		root.ClearChildren()
	}
	for _, p := range u.run.plays {
		if p.node == nil {
			p.node = tview.NewTreeNode("").SetColor(colAccent).SetExpanded(true)
			root.AddChild(p.node)
			if u.tree.GetCurrentNode() == nil {
				u.tree.SetCurrentNode(p.node)
			}
		}
		var ok, chg, skip, bad int
		for _, t := range p.tasks {
			ok, chg, skip, bad = ok+t.ok, chg+t.changed, skip+t.skipped, bad+t.fail
			if t.node == nil {
				t.node = tview.NewTreeNode("")
				p.node.AddChild(t.node)
			}
			t.node.SetText(taskLabel(t))
			t.node.SetColor(taskColorFor(t))
		}
		p.node.SetText(fmt.Sprintf("PLAY %s  [%s]%d/%d ok[-] [%s]%d chg[-] [%s]%d fail[-]",
			p.name, colName(colOK), ok, ok+chg+skip+bad, colName(colWarn), chg, colName(colFail), bad))
	}
}

func taskLabel(t *taskModel) string {
	return fmt.Sprintf("%s  [%s]%d[-]/[%s]%d[-]/[%s]%d[-]",
		t.name, colName(colOK), t.ok, colName(colWarn), t.changed, colName(colFail), t.fail)
}

func taskColorFor(t *taskModel) tcell.Color {
	switch {
	case t.fail > 0:
		return colFail
	case t.changed > 0:
		return colWarn
	default:
		return colOK
	}
}

// ------------------------------------------------------------ the fake run

type scriptPlay struct {
	name  string
	hosts []string
	tasks []string
}

func installScript(c Customer) []scriptPlay {
	var servers, agents []string
	for _, n := range c.Nodes {
		if n.Role == "server" {
			servers = append(servers, n.Name)
		} else {
			agents = append(agents, n.Name)
		}
	}
	all := append(append([]string{}, servers...), agents...)
	plays := []scriptPlay{
		{"bootstrap", all, []string{
			"common : install base packages", "common : set hostname", "common : disable swap",
			"common : configure sysctl", "common : open firewall ports",
		}},
		{"rke2_server", servers, []string{
			"rke2 : create config directory", "rke2 : render config.yaml", "rke2 : install rke2 binaries",
			"rke2 : start rke2-server", "rke2 : wait for node-token", "rke2 : write kubeconfig",
		}},
	}
	if len(agents) > 0 {
		plays = append(plays, scriptPlay{"rke2_agent", agents, []string{
			"rke2 : render agent config", "rke2 : install rke2 binaries", "rke2 : start rke2-agent",
			"rke2 : wait for node Ready",
		}})
	}
	first := all[:1]
	if len(servers) > 0 {
		first = servers[:1]
	}
	plays = append(plays, scriptPlay{"platform", first, []string{
		"longhorn : add helm repo", "longhorn : deploy chart", "rancher : deploy chart",
		"rancher : wait for ingress", "rancher : verify login",
	}})
	return plays
}

func healthScript(c Customer) []scriptPlay {
	all := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		all = append(all, n.Name)
	}
	checks := make([]string, 0, len(c.Health))
	for _, h := range c.Health {
		checks = append(checks, "check : "+h.Name)
	}
	return []scriptPlay{
		{"gather", all, []string{"facts : gather node facts", "facts : read rke2 version", "facts : read kubelet status"}},
		{"health", all, checks},
		{"report", all[:1], []string{"report : render summary", "report : store result"}},
	}
}

func backupScript(c Customer) []scriptPlay {
	all := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		all = append(all, n.Name)
	}
	return []scriptPlay{
		{"etcd_snapshot", all[:1], []string{"etcd : trigger snapshot", "etcd : wait for snapshot", "etcd : copy to vault"}},
		{"volumes", all, []string{"longhorn : freeze volumes", "longhorn : snapshot volumes", "longhorn : thaw volumes"}},
	}
}

// startRun launches the fake Ansible run in a goroutine. Nothing here touches
// tview directly: it writes into the view buffer and the run model, and the
// throttled ticker moves that into the UI.
func (u *ui) startRun(kind string, c Customer) {
	if u.run.active() {
		u.showToast("a run is already in progress")
		return
	}
	var script []scriptPlay
	switch kind {
	case "install":
		script = installScript(c)
	case "backup":
		script = backupScript(c)
	default:
		script = healthScript(c)
	}

	ctx, cancel := context.WithCancel(context.Background())
	u.run.start(fmt.Sprintf("%s %s", kind, c.Name), cancel)
	u.logBuf.Reset()
	u.tree.SetRoot(nil)
	u.logBuf.Append(fmt.Sprintf("[%s::b]$ ansible-playbook %s.yml -l %s[-:-:-]", colName(colAccent), kind, c.Name))
	u.logBuf.Append("")

	total := 0
	for _, p := range script {
		total += 1 + len(p.tasks)*(1+len(p.hosts))
	}
	delay := runDuration / time.Duration(total+1)
	if delay < 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	if delay > 150*time.Millisecond {
		delay = 150 * time.Millisecond
	}

	go func() {
		defer cancel()
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		stats := map[string]*[4]int{}
		sleep := func() bool {
			jitter := time.Duration(float64(delay) * (0.7 + 0.6*rng.Float64()))
			select {
			case <-ctx.Done():
				return false
			case <-time.After(jitter):
				return true
			}
		}

		for _, sp := range script {
			if !sleep() {
				u.finishRun(true, c)
				return
			}
			pm := u.run.addPlay(sp.name)
			// tview reads "[word]" as a colour tag, so bracketed Ansible
			// identifiers have to go through tview.Escape.
			u.logBuf.Append(fmt.Sprintf("[%s::b]PLAY %s %s[-:-:-]",
				colName(colAccent), tview.Escape("["+sp.name+"]"), strings.Repeat("*", 30)))

			for _, task := range sp.tasks {
				if !sleep() {
					u.finishRun(true, c)
					return
				}
				tm := u.run.addTask(pm, task)
				u.logBuf.Append(fmt.Sprintf("[%s]TASK %s %s[-]",
					colName(colHeader), tview.Escape("["+task+"]"), strings.Repeat("*", 20)))

				for _, host := range sp.hosts {
					if !sleep() {
						u.finishRun(true, c)
						return
					}
					result := pickResult(rng, c, task)
					u.run.count(tm, result)
					if _, ok := stats[host]; !ok {
						stats[host] = &[4]int{}
					}
					s := stats[host]
					switch result {
					case "ok":
						s[0]++
					case "changed":
						s[1]++
					case "skipping":
						s[2]++
					case "failed":
						s[3]++
					}
					u.logBuf.Append(fmt.Sprintf("[%s]%s: %s[-]", resultColorName(result), result, tview.Escape("["+host+"]")))
				}
			}
			u.logBuf.Append("")
		}

		u.logBuf.Append(fmt.Sprintf("[%s::b]PLAY RECAP %s[-:-:-]", colName(colAccent), strings.Repeat("*", 30)))
		for _, n := range c.Nodes {
			s, ok := stats[n.Name]
			if !ok {
				continue
			}
			u.logBuf.Append(fmt.Sprintf("%-18s : [%s]ok=%-3d[-] [%s]changed=%-3d[-] unreachable=0   [%s]failed=%-3d[-] skipped=%d",
				tview.Escape(n.Name), colName(colOK), s[0], colName(colWarn), s[1], colName(colFail), s[3], s[2]))
		}
		u.finishRun(false, c)
	}()
}

func (u *ui) finishRun(aborted bool, c Customer) {
	if aborted {
		u.logBuf.Append("")
		u.logBuf.Append(fmt.Sprintf("[%s::b]run aborted by user[-:-:-]", colName(colFail)))
		u.run.stop(true)
		return
	}
	u.logBuf.Append("")
	u.logBuf.Append(fmt.Sprintf("[%s::b]SUMMARY %s[-:-:-]", colName(colAccent), strings.Repeat("*", 33)))
	for _, h := range c.Health {
		u.logBuf.Append(fmt.Sprintf("  [%s]%s %-5s[-] %-16s %s",
			statusColorName(h.Status), dot(h.Status), strings.ToUpper(h.Status), h.Name, tview.Escape(h.Detail)))
	}
	u.run.stop(false)
}

func pickResult(rng *rand.Rand, c Customer, task string) string {
	// Make the failing customer actually fail a few tasks.
	if c.HealthStatus == "fail" && rng.Float64() < 0.12 {
		return "failed"
	}
	switch n := rng.Float64(); {
	case n < 0.62:
		return "ok"
	case n < 0.86:
		return "changed"
	default:
		return "skipping"
	}
}

func resultColorName(result string) string {
	switch result {
	case "ok":
		return colName(colOK)
	case "changed":
		return colName(colWarn)
	case "failed":
		return colName(colFail)
	default:
		return colName(colMuted)
	}
}
