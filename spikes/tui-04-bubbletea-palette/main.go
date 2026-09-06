package main

// ktags TUI spike 04 — Bubble Tea v2 / Lip Gloss v2 / Bubbles v2 / huh v2 /
// bubblezone v2, with the registry-driven command system from
// docs/design/command-system.md.
//
// The dashboard, run view, secret dialog and add-customer form are spike 03's.
// What is new is that no screen holds a list of commands any more: the palette,
// the context menu, the hint bar and the help overlay are all generated from
// `registry` in actions.go, and all three entry points funnel into exec().
//
// Everything is fake: no SSH, no Ansible, no disk writes. See SPEC.md.

import (
	"fmt"
	"os"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"
)

// prog is the running program. The fake Ansible goroutine uses prog.Send to
// push log lines back into the Elm loop; there is no io.Writer plumbing.
var prog *tea.Program

type screen int

const (
	screenDashboard screen = iota
	screenRun
	screenForm
)

type overlayKind int

const (
	overlayNone overlayKind = iota
	overlayHelp
	overlaySecret
	overlayConfirm
	overlayMenu
)

type focusArea int

const (
	focusCustomers focusArea = iota
	focusNodes
	focusChecks
)

type toast struct {
	text string
	kind string // err | ok | info
	id   int
}

type model struct {
	w, h int
	th   *theme

	customers []Customer
	sel       int

	list   list.Model
	nodes  table.Model
	checks table.Model
	logvp  viewport.Model
	spin   spinner.Model

	screen  screen
	overlay overlayKind
	focus   focusArea

	mouseOn bool

	pal  palette
	menu *menuState

	toast   toast
	toastID int

	run     *runState
	runGen  int
	form    *addForm
	secret  *secretState
	confirm *confirmState

	status string
}

func newModel() model {
	th := newTheme(true)
	cs := seedCustomers()

	l := list.New(nil, custDelegate{th: &th}, 28, 10)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	nodes := table.New(table.WithColumns(nodeColumns(60)), table.WithHeight(6))
	checks := table.New(table.WithColumns(checkColumns(60)), table.WithHeight(6))

	vp := viewport.New(viewport.WithWidth(60), viewport.WithHeight(10))
	vp.MouseWheelEnabled = true
	vp.SoftWrap = false

	m := model{
		th:        &th,
		customers: cs,
		list:      l,
		nodes:     nodes,
		checks:    checks,
		logvp:     vp,
		spin:      spinner.New(spinner.WithSpinner(spinner.Dot)),
		mouseOn:   true,
		status:    "ready",
	}
	m.applyTheme()
	m.syncCustomers()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.spin.Tick)
}

// current returns the selected customer.
func (m model) current() Customer { return m.customers[m.sel] }

// focusKind maps the focused panel to the kind of object the registry should
// be filtered by. The health table belongs to the customer, so it counts as one.
func (m model) focusKind() Kind {
	if m.focus == focusNodes {
		return KindNode
	}
	return KindCustomer
}

// focusedNode is the node row under the cursor, if any.
func (m model) focusedNode() *Node {
	c := &m.customers[m.sel]
	i := m.nodes.Cursor()
	if i < 0 || i >= len(c.Nodes) {
		return nil
	}
	return &c.Nodes[i]
}

// ---------------------------------------------------------------------------
// Execution pipeline
// ---------------------------------------------------------------------------
//
// resolve target → Guard → danger prompt → Long ? run view : inline toast.
// The palette, the context menu and the hint bar all call exec() and nothing
// else, which is the only reason the three cannot drift apart.

// exec runs one action against one target.
func (m model) exec(a *Action, target string, args Values) (tea.Model, tea.Cmd) {
	ctx, err := m.resolve(a, target)
	if err != nil {
		m.flash(err.Error(), "err")
		return m, m.expireToast()
	}

	if a.Guard != nil {
		if err := a.Guard(ctx); err != nil {
			m.flash(err.Error(), "err")
			return m, m.expireToast()
		}
	}

	if a.Danger != DangerNone {
		m.confirm = newConfirm(a, target, args, ctx)
		m.overlay = overlayConfirm
		return m, nil
	}
	return m.fire(a, ctx, args)
}

// resolve turns a target name (or the dashboard selection) into a Context.
// The pointers aim into m.customers, so an action can mutate a cluster.
func (m model) resolve(a *Action, target string) (Context, error) {
	var ctx Context

	switch a.Target {
	case KindGlobal:
		return ctx, nil

	case KindCustomer:
		name := target
		if name == "" {
			name = m.current().Name
		}
		for i := range m.customers {
			if m.customers[i].Name == name {
				ctx.Customer = &m.customers[i]
				return ctx, nil
			}
		}
		return ctx, fmt.Errorf("no such customer: %s", name)

	case KindNode:
		name := target
		if name == "" {
			if n := m.focusedNode(); n != nil {
				name = n.Name
			}
		}
		if name == "" {
			return ctx, fmt.Errorf("%s needs a node: select one or type its name", a.Verb)
		}
		for i := range m.customers {
			for j := range m.customers[i].Nodes {
				if m.customers[i].Nodes[j].Name == name {
					ctx.Customer = &m.customers[i]
					ctx.Node = &m.customers[i].Nodes[j]
					return ctx, nil
				}
			}
		}
		return ctx, fmt.Errorf("no such node: %s", name)
	}
	return ctx, nil
}

// fire is the last step. `Long` is what decides between the streaming run view
// and an inline toast; the action itself only supplies the script.
func (m model) fire(a *Action, ctx Context, args Values) (tea.Model, tea.Cmd) {
	ctx.M = &m
	cmd := a.Run(ctx, args)
	if a.Long {
		return m, tea.Batch(cmd, m.spin.Tick)
	}
	return m, cmd
}

// execVerb is the entry point for the few global keys that are themselves
// registry actions (`?`, `m`, `q`).
func (m model) execVerb(verb string) (tea.Model, tea.Cmd) {
	a := actionByVerb(verb)
	if a == nil {
		return m, nil
	}
	return m.exec(a, "", Values{})
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil

	case tea.BackgroundColorMsg:
		*m.th = newTheme(msg.IsDark())
		m.applyTheme()
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case runLineMsg:
		if m.run == nil || msg.gen != m.run.gen {
			return m, nil
		}
		m.run.push(m.th, msg.ev, m.run.cust)
		m.refreshLog()
		return m, nil

	case runDoneMsg:
		if m.run == nil || msg.gen != m.run.gen {
			return m, nil
		}
		m.run.done = true
		m.status = m.run.title + " finished"
		m.refreshLog()
		return m, nil

	case toastExpireMsg:
		if msg.id == m.toast.id {
			m.toast = toast{}
		}
		return m, nil

	case secretTickMsg:
		if m.secret == nil || msg.gen != m.secret.gen {
			return m, nil
		}
		m.secret.remaining--
		if m.secret.remaining <= 0 {
			m.secret.revealed = ""
			m.secret.value = ""
			return m, nil
		}
		return m, m.secret.tick()

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	// Anything else (huh internals, viewport, list) goes to the active screen.
	if m.screen == screenForm && m.form != nil {
		return m.updateForm(msg)
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// ---------------------------------------------------------------------------
// Keyboard
// ---------------------------------------------------------------------------

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	// The huh form owns the keyboard while it is up.
	if m.screen == screenForm && m.form != nil {
		return m.updateForm(msg)
	}

	// Overlays are modal.
	switch m.overlay {
	case overlayHelp:
		if k == "?" || k == "esc" || k == "q" || k == "enter" {
			m.overlay = overlayNone
		}
		return m, nil
	case overlaySecret:
		return m.secretKey(k)
	case overlayConfirm:
		return m.confirmKey(msg)
	case overlayMenu:
		return m.menuKey(msg)
	}

	if m.pal.open {
		return m.paletteKey(msg)
	}

	// The only global keys left. There are no per-action letters any more:
	// `?`, `m` and `q` are themselves registry entries and go through exec().
	switch k {
	case "ctrl+c":
		if m.screen == screenRun && m.run != nil && !m.run.done {
			m.run.askAbort = true
			return m, nil
		}
		return m, tea.Quit
	case ":":
		m.openPalette()
		return m, nil
	case "?":
		return m.execVerb("help")
	case "m":
		return m.execVerb("mouse")
	}

	if m.screen == screenRun {
		return m.runKey(msg)
	}
	return m.dashboardKey(msg)
}

func (m model) dashboardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m.execVerb("quit")
	case "enter", "space":
		m.openMenu()
		return m, nil
	case "tab":
		m.focus = (m.focus + 1) % 3
		m.syncFocus()
		return m, nil
	case "shift+tab":
		m.focus = (m.focus + 2) % 3
		m.syncFocus()
		return m, nil
	case "up", "k":
		m.moveCursor(-1)
		return m, nil
	case "down", "j":
		m.moveCursor(1)
		return m, nil
	case "home", "g":
		m.jumpCursor(true)
		return m, nil
	case "end", "G":
		m.jumpCursor(false)
		return m, nil
	}
	return m, nil
}

func (m *model) moveCursor(d int) {
	switch m.focus {
	case focusCustomers:
		n := len(m.customers)
		m.sel = (m.sel + d + n) % n
		m.list.Select(m.sel)
		m.syncTables()
	case focusNodes:
		if d < 0 {
			m.nodes.MoveUp(1)
		} else {
			m.nodes.MoveDown(1)
		}
	case focusChecks:
		if d < 0 {
			m.checks.MoveUp(1)
		} else {
			m.checks.MoveDown(1)
		}
	}
}

func (m *model) jumpCursor(top bool) {
	switch m.focus {
	case focusCustomers:
		if top {
			m.sel = 0
		} else {
			m.sel = len(m.customers) - 1
		}
		m.list.Select(m.sel)
		m.syncTables()
	case focusNodes:
		if top {
			m.nodes.GotoTop()
		} else {
			m.nodes.GotoBottom()
		}
	case focusChecks:
		if top {
			m.checks.GotoTop()
		} else {
			m.checks.GotoBottom()
		}
	}
}

// ---------------------------------------------------------------------------
// Toast
// ---------------------------------------------------------------------------

func (m *model) flash(text, kind string) {
	m.toastID++
	m.toast = toast{text: text, kind: kind, id: m.toastID}
}

func (m model) expireToast() tea.Cmd {
	id := m.toast.id
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return toastExpireMsg{id} })
}

// toastCmd is flash + expire in one call, for registry Run functions.
func (m *model) toastCmd(text, kind string) tea.Cmd {
	m.flash(text, kind)
	return m.expireToast()
}

type toastExpireMsg struct{ id int }

// ---------------------------------------------------------------------------
// Layout plumbing
// ---------------------------------------------------------------------------

func (m model) leftW() int {
	w := m.w / 4
	if w < 24 {
		w = 24
	}
	if w > 34 {
		w = 34
	}
	if w > m.w-20 {
		w = m.w - 20
	}
	if w < 10 {
		w = 10
	}
	return w
}

// contentH is the height available between the top bar and the bottom bars.
func (m model) contentH() int {
	h := m.h - 1 - m.bottomH()
	if h < 5 {
		h = 5
	}
	return h
}

func (m model) contentW() int {
	if m.w < 20 {
		return 20
	}
	return m.w
}

// bottomH counts the hint bar, the command bar, an optional toast and the
// palette pop-up. palLines() is the same function View() uses, so the two
// cannot disagree about the height.
func (m model) bottomH() int {
	h := 2 // command bar + hint bar
	if m.toast.text != "" {
		h++
	}
	return h + len(m.palLines())
}

func (m *model) layout() {
	lw := m.leftW()
	ch := m.contentH()
	rw := m.w - lw

	m.list.SetSize(lw-2, ch-2)

	topH := ch / 2
	botH := ch - topH

	m.nodes.SetWidth(rw - 2)
	m.nodes.SetColumns(nodeColumns(rw - 2))
	m.nodes.SetHeight(topH - 3)

	m.checks.SetWidth(rw - 2)
	m.checks.SetColumns(checkColumns(rw - 2))
	m.checks.SetHeight(botH - 3)

	m.logvp.SetWidth(rw - 2)
	m.logvp.SetHeight(ch - 2)

	if m.form != nil {
		m.form.resize(m.contentW(), ch)
	}
	m.syncTables()
}

func (m *model) applyTheme() {
	ts := table.DefaultStyles()
	ts.Header = ts.Header.Bold(true).Foreground(m.th.accent)
	ts.Cell = ts.Cell.Foreground(m.th.fg)
	ts.Selected = ts.Selected.Bold(true).Foreground(m.th.focus)
	m.nodes.SetStyles(ts)
	m.checks.SetStyles(ts)
	m.spin.Style = m.spin.Style.Foreground(m.th.accent)
	m.list.SetDelegate(custDelegate{th: m.th})
	m.syncFocus()
}

func (m *model) syncFocus() {
	if m.focus == focusNodes {
		m.nodes.Focus()
	} else {
		m.nodes.Blur()
	}
	if m.focus == focusChecks {
		m.checks.Focus()
	} else {
		m.checks.Blur()
	}
}

// syncCustomers pushes the customer slice back into the list widget. Actions
// mutate customers through Context pointers (lock/unlock) or append to the
// slice (add), so this runs after any of them.
func (m *model) syncCustomers() {
	items := make([]list.Item, len(m.customers))
	for i, c := range m.customers {
		items[i] = custItem{c}
	}
	_ = m.list.SetItems(items)
	if m.sel >= len(m.customers) {
		m.sel = len(m.customers) - 1
	}
	m.list.Select(m.sel)
	m.syncTables()
}

// syncTables refills the node and health tables from the selected customer.
func (m *model) syncTables() {
	c := m.current()

	rows := make([]table.Row, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		lh := "no"
		if n.Longhorn {
			lh = "yes"
		}
		rows = append(rows, table.Row{n.Name, n.Role, n.NodeIP, n.AccessIP, n.Version, n.Status, lh})
	}
	cur := m.nodes.Cursor()
	m.nodes.SetRows(rows)
	if cur < 0 || cur >= len(rows) {
		cur = 0
	}
	m.nodes.SetCursor(cur)

	crows := make([]table.Row, 0, len(c.Checks))
	for _, ck := range c.Checks {
		crows = append(crows, table.Row{ck.Name, upper(ck.Status), ck.Detail})
	}
	m.checks.SetRows(crows)
}

// bubbles/table pads every cell with Padding(0, 1), so a column occupies
// Width+2 cells on screen.
const cellPad = 2

// fitColumns sizes table columns to the panel. `flex` absorbs spare space;
// when space is short, columns are shrunk toward their minimum in `order`.
func fitColumns(titles []string, minw, want []int, flex int, order []int, w int) []table.Column {
	n := len(titles)
	budget := w - n*cellPad

	cols := make([]int, n)
	copy(cols, want)

	total := 0
	for _, v := range cols {
		total += v
	}

	if budget >= total {
		cols[flex] += budget - total
	} else {
		need := total - budget
		for _, i := range order {
			if need <= 0 {
				break
			}
			give := cols[i] - minw[i]
			if give > need {
				give = need
			}
			cols[i] -= give
			need -= give
		}
	}

	out := make([]table.Column, n)
	for i := range out {
		width := cols[i]
		if width < 3 {
			width = 3
		}
		out[i] = table.Column{Title: titles[i], Width: width}
	}
	return out
}

func nodeColumns(w int) []table.Column {
	return fitColumns(
		[]string{"NAME", "ROLE", "NODE IP", "ACCESS IP", "RKE2 VERSION", "STATUS", "LONGHORN"},
		[]int{10, 5, 11, 11, 8, 8, 3},
		[]int{18, 6, 11, 12, 15, 8, 8},
		0,
		[]int{0, 4, 3, 2, 6, 5, 1},
		w,
	)
}

func checkColumns(w int) []table.Column {
	return fitColumns(
		[]string{"CHECK", "STATUS", "DETAIL"},
		[]int{10, 4, 10},
		[]int{18, 6, 24},
		2,
		[]int{2, 0, 1},
		w,
	)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	zone.NewGlobal()
	prog = tea.NewProgram(newModel())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
