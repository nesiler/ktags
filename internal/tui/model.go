package tui

import (
	"context"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/nesiler/ktags/internal/service"
)

// Minimum terminal size (docs/guides/ui.md §11).
const (
	minWidth  = 80
	minHeight = 24
)

// connState is the service connection state of docs/guides/ui.md §3.
type connState int

const (
	connConnecting connState = iota
	connConnected
	connReconnecting
	connUnavailable
	connMismatch
)

type focusArea int

const (
	focusList focusArea = iota
	focusDetail
)

type tab int

const (
	tabNodes tab = iota
	tabHealth
	tabRuns
)

var tabNames = []string{"Nodes", "Health", "Runs"}

type overlay int

const (
	overlayNone overlay = iota
	overlayPalette
	overlayMenu
	overlayDialog
	overlayHelp
)

type screen int

const (
	screenFleet screen = iota
	screenRun
)

// Model is the TUI state. It is a Bubble Tea model; every service call runs in a tea.Cmd.
type Model struct {
	client  Client
	version string
	now     func() time.Time
	poll    time.Duration
	zones   *zone.Manager
	th      theme

	width, height int

	conn        connState
	connErr     problem
	startFailed bool
	refreshing  bool
	hello       service.Hello
	fleet       service.FleetInfo
	nodes       map[string]int
	actions     []service.ActionInfo
	runs        []service.RunInfo
	haveData    bool
	// dataAt is when the data on screen was last refreshed, on the client clock.
	dataAt time.Time

	selected  string
	listTop   int
	focus     focusArea
	tab       tab
	runCursor int

	overlay overlay
	pal     palette
	menu    menu
	dialog  dialog
	screen  screen
	run     *runView
	runGen  int

	mouse     bool
	status    string
	frame     int
	lastClick click
	quitting  bool
	exitLine  string
}

type click struct {
	id string
	at time.Time
}

// Messages.
type (
	tickMsg    struct{}
	refreshMsg struct {
		hello     service.Hello
		fleet     service.FleetInfo
		customers []service.CustomerInfo
		actions   []service.ActionInfo
		runs      []service.RunInfo
		err       error
	}
	startedMsg struct {
		action string
		info   service.RunInfo
		err    error
	}
	cancelledMsg struct {
		id  string
		err error
	}
)

// New builds the model. Nothing is drawn as current until the service has answered.
func New(opts Options) Model {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	zones := opts.Zones
	if zones == nil {
		zones = zone.New()
	}
	m := Model{
		client:  opts.Client,
		version: opts.Version,
		now:     now,
		poll:    opts.Poll,
		zones:   zones,
		th:      newTheme(true),
		mouse:   true,
		conn:    connConnecting,
		pal:     palette{hist: -1},
	}
	if opts.StartErr != nil {
		m.conn, m.connErr, m.startFailed = connUnavailable, describe(opts.StartErr), true
	} else {
		m.refreshing = true
	}
	return m
}

// Init asks for the terminal background, starts polling and loads the first data.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor, m.tick()}
	if !m.startFailed {
		cmds = append(cmds, m.refresh())
	}
	return tea.Batch(cmds...)
}

// ExitLine is what the client prints after quitting: whether runs continue in the service.
func (m Model) ExitLine() string {
	return m.exitLine
}

func (m Model) tick() tea.Cmd {
	if m.poll <= 0 {
		return nil
	}
	return tea.Tick(m.poll, func(time.Time) tea.Msg { return tickMsg{} })
}

// refresh loads everything the screens show, in one pass over the service.
func (m Model) refresh() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		var r refreshMsg
		if r.hello, r.err = client.Hello(ctx); r.err != nil {
			return r
		}
		if r.fleet, r.err = client.Fleet(ctx); r.err != nil {
			return r
		}
		if r.customers, r.err = client.Customers(ctx); r.err != nil {
			return r
		}
		if r.actions, r.err = client.Actions(ctx); r.err != nil {
			return r
		}
		r.runs, r.err = client.Runs(ctx)
		return r
	}
}

// Update splits messages: global messages, then mouse, then keys by screen and overlay.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampList()
		return m, nil
	case tea.BackgroundColorMsg:
		m.th = newTheme(msg.IsDark())
		return m, nil
	case tickMsg:
		return m.onTick()
	case refreshMsg:
		return m.onRefresh(msg)
	case startedMsg:
		return m.onStarted(msg)
	case cancelledMsg:
		return m.onCancelled(msg)
	case streamMsg:
		return m.onStream(msg)
	case tea.MouseMsg:
		return m.onMouse(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg.String(), msg.Text)
	}
	return m, nil
}

func (m Model) onTick() (tea.Model, tea.Cmd) {
	m.frame++
	cmds := []tea.Cmd{m.tick()}
	// Polling stops in the unavailable and mismatch states, where Enter retries. A lost run
	// stream resumes when this refresh succeeds (onRefresh).
	if !m.refreshing && (m.conn == connConnected || m.conn == connReconnecting || m.conn == connConnecting) {
		m.refreshing = true
		cmds = append(cmds, m.refresh())
	}
	return m, tea.Batch(cmds...)
}

func (m Model) onRefresh(msg refreshMsg) (tea.Model, tea.Cmd) {
	m.refreshing = false
	if msg.err != nil {
		m.lose(msg.err)
		return m, nil
	}
	m.conn, m.connErr, m.startFailed = connConnected, problem{}, false
	m.hello, m.fleet, m.actions, m.runs = msg.hello, msg.fleet, msg.actions, msg.runs
	m.nodes = make(map[string]int, len(msg.customers))
	for _, c := range msg.customers {
		m.nodes[c.ID] = c.Nodes
	}
	m.haveData, m.dataAt = true, m.now()
	m.keepSelection()
	if m.run != nil {
		if i := slices.IndexFunc(m.runs, func(r service.RunInfo) bool { return r.ID == m.run.id }); i >= 0 && m.run.info.Result == nil {
			m.run.info = m.runs[i]
		}
		if m.run.lost && m.screen == screenRun {
			return m, m.resume()
		}
	}
	return m, nil
}

// lose records a failed refresh. With data on screen it is a reconnect: the data stays, marked
// with its age. Without data the service is unavailable. A protocol mismatch drops the data:
// nothing from a service of another version is rendered.
func (m *Model) lose(err error) {
	p := describe(err)
	switch {
	case p.code == service.CodeProtocolMismatch:
		m.conn = connMismatch
		m.haveData, m.fleet, m.actions, m.runs, m.nodes = false, service.FleetInfo{}, nil, nil, nil
	case m.haveData:
		m.conn = connReconnecting
	default:
		m.conn = connUnavailable
	}
	m.connErr = p
}

// retry is Enter in the unavailable and mismatch states.
func (m Model) retry() (Model, tea.Cmd) {
	if m.refreshing {
		return m, nil
	}
	m.refreshing = true
	m.status = "retrying the connection to the ktags service…"
	return m, m.refresh()
}

func (m Model) customers() []service.FleetEntry {
	return m.fleet.Customers
}

func (m Model) selectedIndex() int {
	return slices.IndexFunc(m.fleet.Customers, func(c service.FleetEntry) bool { return c.ID == m.selected })
}

func (m Model) current() *service.FleetEntry {
	if i := m.selectedIndex(); i >= 0 {
		return &m.fleet.Customers[i]
	}
	return nil
}

func (m Model) entry(id string) *service.FleetEntry {
	if i := slices.IndexFunc(m.fleet.Customers, func(c service.FleetEntry) bool { return c.ID == id }); i >= 0 {
		return &m.fleet.Customers[i]
	}
	return nil
}

// keepSelection keeps the selected customer across refreshes; a customer that left the
// inventory hands the selection to the first one.
func (m *Model) keepSelection() {
	if m.selectedIndex() < 0 {
		m.selected = ""
		if len(m.fleet.Customers) > 0 {
			m.selected = m.fleet.Customers[0].ID
		}
		m.runCursor = 0
	}
	m.clampList()
}

func (m *Model) selectIndex(i int) {
	cs := m.customers()
	if len(cs) == 0 {
		return
	}
	i = max(0, min(i, len(cs)-1))
	if cs[i].ID != m.selected {
		m.runCursor = 0
	}
	m.selected = cs[i].ID
	m.clampList()
}

// clampList scrolls the customer list so the selection is visible.
func (m *Model) clampList() {
	h := m.panelRows()
	i := m.selectedIndex()
	if h <= 0 || i < 0 {
		m.listTop = 0
		return
	}
	if i < m.listTop {
		m.listTop = i
	}
	if i >= m.listTop+h {
		m.listTop = i - h + 1
	}
}

// customerRuns lists the runs of a customer, newest first.
func (m Model) customerRuns(id string) []service.RunInfo {
	var out []service.RunInfo
	for i := len(m.runs) - 1; i >= 0; i-- {
		if m.runs[i].Target.Customer == id {
			out = append(out, m.runs[i])
		}
	}
	return out
}

// activeRuns lists the runs the service reports as running, newest first.
func (m Model) activeRuns() []service.RunInfo {
	var out []service.RunInfo
	for i := len(m.runs) - 1; i >= 0; i-- {
		if m.runs[i].Status == "running" {
			out = append(out, m.runs[i])
		}
	}
	return out
}

// customerActions are the registry actions that operate on one customer: the context menu
// and the hint bar offer them for the selected customer.
func (m Model) customerActions() []service.ActionInfo {
	var out []service.ActionInfo
	for _, a := range m.actions {
		if a.Target == "customer" {
			out = append(out, a)
		}
	}
	return out
}

func (m Model) quit() (Model, tea.Cmd) {
	m.quitting = true
	if m.run != nil {
		m.run.halt()
	}
	if n := len(m.activeRuns()); n > 0 {
		m.exitLine = pluralRuns(n) + " continue in the ktags service; reopen ktags or run: ktags run list"
	}
	return m, tea.Quit
}
