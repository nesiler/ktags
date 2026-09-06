package main

// ktags TUI spike — Bubble Tea v2 / Lip Gloss v2 / Bubbles v2 / huh v2 / bubblezone v2.
//
// Everything is fake: no SSH, no Ansible, no disk writes. See SPEC.md.

import (
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
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
	helpM  help.Model

	screen  screen
	overlay overlayKind
	focus   focusArea

	mouseOn bool

	cmdOpen bool
	cmdBuf  string

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

	items := make([]list.Item, len(cs))
	for i, c := range cs {
		items[i] = custItem{c}
	}

	l := list.New(items, custDelegate{th: &th}, 28, 10)
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

	sp := spinner.New(spinner.WithSpinner(spinner.Dot))

	m := model{
		th:        &th,
		customers: cs,
		list:      l,
		nodes:     nodes,
		checks:    checks,
		logvp:     vp,
		spin:      sp,
		helpM:     help.New(),
		mouseOn:   true,
		status:    "ready",
	}
	m.applyTheme()
	m.syncTables()
	return m
}

func (m model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.spin.Tick)
}

// current returns the selected customer.
func (m model) current() Customer { return m.customers[m.sel] }

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
		m.run.push(m.th, msg.ev, m.current())
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
	}

	// Command bar.
	if m.cmdOpen {
		return m.cmdKey(msg)
	}

	switch k {
	case "ctrl+c":
		if m.screen == screenRun && m.run != nil && !m.run.done {
			m.run.askAbort = true
			return m, nil
		}
		return m, tea.Quit
	case ":":
		m.cmdOpen = true
		m.cmdBuf = ""
		return m, nil
	case "?":
		m.overlay = overlayHelp
		return m, nil
	case "m":
		m.mouseOn = !m.mouseOn
		m.flash("mouse "+onOff(m.mouseOn), "info")
		return m, m.expireToast()
	}

	if m.screen == screenRun {
		return m.runKey(msg)
	}
	return m.dashboardKey(msg)
}

func (m model) dashboardKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
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
	case "h":
		return m.action("health")
	case "i":
		return m.action("install")
	case "b":
		return m.action("backup")
	case "a":
		return m.action("add")
	case "s":
		return m.action("secret")
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
// Command bar
// ---------------------------------------------------------------------------

var commands = []string{"health", "install", "backup", "add", "secret", "quit"}

func (m model) suggestions() []string {
	var out []string
	for _, c := range commands {
		if strings.HasPrefix(c, m.cmdBuf) {
			out = append(out, c)
		}
	}
	return out
}

func (m model) cmdKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.cmdOpen = false
		m.cmdBuf = ""
		return m, nil
	case "backspace":
		if m.cmdBuf != "" {
			m.cmdBuf = m.cmdBuf[:len(m.cmdBuf)-1]
		}
		return m, nil
	case "tab":
		if s := m.suggestions(); len(s) > 0 {
			m.cmdBuf = s[0]
		}
		return m, nil
	case "enter":
		cmdName := m.cmdBuf
		if s := m.suggestions(); len(s) == 1 {
			cmdName = s[0]
		}
		m.cmdOpen = false
		m.cmdBuf = ""
		return m.action(cmdName)
	}
	if t := msg.Key().Text; t != "" {
		m.cmdBuf += t
	}
	return m, nil
}

// action runs a command by name, from the command bar, a hotkey or a click.
func (m model) action(name string) (tea.Model, tea.Cmd) {
	c := m.current()
	switch name {
	case "quit":
		return m, tea.Quit
	case "add":
		m.form = newAddForm(m.th, m.contentW(), m.contentH())
		m.screen = screenForm
		return m, m.form.form.Init()
	case "secret":
		m.secret = &secretState{customer: c.Name}
		m.overlay = overlaySecret
		return m, nil
	case "health":
		return m.startRun("health")
	case "install", "backup":
		if c.Locked() {
			m.flash(c.LockMessage(), "err")
			return m, m.expireToast()
		}
		m.confirm = newConfirm(name, c)
		m.overlay = overlayConfirm
		return m, nil
	case "":
		return m, nil
	}
	m.flash("unknown command: "+name, "err")
	return m, m.expireToast()
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

// contentH is the height available between the top bar and the status bars.
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

func (m model) bottomH() int {
	h := 1 // status bar
	if m.cmdOpen {
		h += 2 // command line + suggestions
	}
	if m.toast.text != "" {
		h++
	}
	return h
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
	m.helpM.SetWidth(m.w)
	m.syncTables()
}

func (m *model) applyTheme() {
	ts := table.DefaultStyles()
	ts.Header = ts.Header.Bold(true).Foreground(m.th.accent)
	ts.Cell = ts.Cell.Foreground(m.th.fg)
	ts.Selected = ts.Selected.Bold(true).Foreground(m.th.focus)
	m.nodes.SetStyles(ts)
	m.checks.SetStyles(ts)
	m.helpM.Styles = help.DefaultStyles(m.th.isDark)
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

// rebuildList refreshes the list items after a customer is added.
func (m *model) rebuildList() tea.Cmd {
	items := make([]list.Item, len(m.customers))
	for i, c := range m.customers {
		items[i] = custItem{c}
	}
	return m.list.SetItems(items)
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
	m.nodes.SetRows(rows)
	m.nodes.SetCursor(0)

	crows := make([]table.Row, 0, len(c.Checks))
	for _, ck := range c.Checks {
		crows = append(crows, table.Row{ck.Name, upper(ck.Status), ck.Detail})
	}
	m.checks.SetRows(crows)
	m.checks.SetCursor(0)
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
// Help keymap
// ---------------------------------------------------------------------------

type keyMap struct{}

func (keyMap) ShortHelp() []key.Binding { return shortKeys }
func (keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{shortKeys, actionKeys, panelKeys}
}

var (
	shortKeys = []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "focus panel")),
		key.NewBinding(key.WithKeys("up", "down"), key.WithHelp("↑/↓", "move")),
		key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "command")),
		key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mouse on/off")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	}
	actionKeys = []key.Binding{
		key.NewBinding(key.WithKeys("h"), key.WithHelp("h", ":health")),
		key.NewBinding(key.WithKeys("i"), key.WithHelp("i", ":install")),
		key.NewBinding(key.WithKeys("b"), key.WithHelp("b", ":backup")),
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", ":add customer")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", ":secret")),
	}
	panelKeys = []key.Binding{
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back to dashboard")),
		key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "collapse play")),
		key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdn", "scroll log")),
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "abort run / quit")),
	}
)

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
