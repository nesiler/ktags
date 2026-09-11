package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/nesiler/ktags/internal/service"
)

var testNow = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

func past(d time.Duration) *time.Time {
	t := testNow.Add(-d)
	return &t
}

type startCall struct {
	action string
	target service.Target
	args   map[string]any
}

type eventsCall struct {
	id    string
	after uint64
}

// fakeClient is a synthetic service. Every field is guarded by mu.
type fakeClient struct {
	mu        sync.Mutex
	hello     service.Hello
	fleet     service.FleetInfo
	customers []service.CustomerInfo
	actions   []service.ActionInfo
	runs      []service.RunInfo
	// err fails every refresh call; block holds Hello until closed.
	err       error
	block     chan struct{}
	startErr  error
	cancelErr error
	started   []startCall
	cancelled []string
	refreshes int
	// events and final are what run.events replays; dropAt ends the stream with a lost
	// connection right after that event (once); invalid refuses the next cursor above 0 (once);
	// hold keeps a stream open until the client detaches; ignoreCursor resends every event.
	events       map[string][]service.Event
	final        map[string]service.RunInfo
	dropAt       map[string]uint64
	invalid      map[string]bool
	hold         map[string]bool
	ignoreCursor bool
	// refuse fails every stream of a run with this error.
	refuse   map[string]*service.Error
	calls    []eventsCall
	detached chan string
}

func (f *fakeClient) Hello(ctx context.Context) (service.Hello, error) {
	f.mu.Lock()
	f.refreshes++
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return service.Hello{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hello, f.err
}

func (f *fakeClient) Fleet(context.Context) (service.FleetInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fleet, f.err
}

func (f *fakeClient) Customers(context.Context) ([]service.CustomerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.customers), f.err
}

func (f *fakeClient) Actions(context.Context) ([]service.ActionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.actions), f.err
}

func (f *fakeClient) Runs(context.Context) ([]service.RunInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.runs), f.err
}

func (f *fakeClient) Start(_ context.Context, action string, target service.Target, args map[string]any) (service.RunInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return service.RunInfo{}, f.startErr
	}
	f.started = append(f.started, startCall{action: action, target: target, args: args})
	info := service.RunInfo{ID: fmt.Sprintf("20260911T120000Z-%08d", len(f.started)), Action: action, Target: target, Status: "running", StartedAt: testNow}
	f.runs = append(f.runs, info)
	return info, nil
}

func (f *fakeClient) Cancel(_ context.Context, id string) (service.RunInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	return service.RunInfo{ID: id}, f.cancelErr
}

func (f *fakeClient) Events(ctx context.Context, id string, after uint64, _ bool, fn func(service.Event) error) (service.RunInfo, error) {
	f.mu.Lock()
	f.calls = append(f.calls, eventsCall{id: id, after: after})
	events := slices.Clone(f.events[id])
	drop := f.dropAt[id]
	delete(f.dropAt, id)
	invalid := f.invalid[id] && after > 0
	if invalid {
		delete(f.invalid, id)
	}
	final, hold, ignore, detached, refuse := f.final[id], f.hold[id], f.ignoreCursor, f.detached, f.refuse[id]
	f.mu.Unlock()
	if refuse != nil {
		return service.RunInfo{}, refuse
	}
	if invalid {
		return service.RunInfo{}, &service.Error{Code: service.CodeInvalid, Message: fmt.Sprintf("cursor %d is beyond the last event 0 of run %s", after, id), Hint: "resume from the last event ID the client received"}
	}
	for _, e := range events {
		if e.ID <= after && !ignore {
			continue
		}
		if err := fn(e); err != nil {
			return service.RunInfo{}, err
		}
		if drop > 0 && e.ID == drop {
			return service.RunInfo{}, &service.Error{Code: service.CodeUnavailable, Message: "the connection to the ktags service ended before its reply", Hint: "check that the ktags service is running, then retry"}
		}
	}
	if hold {
		<-ctx.Done()
		if detached != nil {
			detached <- id
		}
		return service.RunInfo{}, ctx.Err()
	}
	return final, nil
}

func (f *fakeClient) set(fn func(*fakeClient)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeClient) snapshot() fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeClient{started: slices.Clone(f.started), cancelled: slices.Clone(f.cancelled), calls: slices.Clone(f.calls), refreshes: f.refreshes}
}

func testActions() []service.ActionInfo {
	return []service.ActionInfo{
		{ID: "fake check", Title: "Fake check", Help: "Reads a customer.", Target: "customer", Effect: "read-only"},
		{ID: "fake quick", Title: "Fake quick", Help: "Reports one event.", Target: "global", Effect: "read-only", Args: []service.ArgInfo{
			{Name: "count", Kind: "int", Help: "how many"},
			{Name: "note", Kind: "string", Help: "free text"},
			{Name: "verbose", Kind: "bool", Help: "more output"},
		}},
		{ID: "fake touch", Title: "Fake touch", Help: "Changes a customer.", Target: "customer", Effect: "mutating"},
		{ID: "fake wipe", Title: "Fake wipe", Help: "Removes something for good.", Target: "customer", Effect: "destructive"},
		{ID: "node drain", Title: "Drain a node", Help: "Drains one node.", Target: "node", Effect: "mutating", Args: []service.ArgInfo{
			{Name: "reason", Kind: "string", Required: true, Help: "why"},
		}},
	}
}

func entry(id, env, state string) service.FleetEntry {
	return service.FleetEntry{
		ID: id, Name: strings.ToUpper(id), Environment: env, Cluster: id + "-1", State: state, Health: "unknown",
		Connection: service.ConnectionInfo{State: "unknown"}, IntervalSeconds: 900,
		Versions: map[string]string{}, ActiveRuns: []service.ActiveRunInfo{}, Checks: []service.CheckInfo{},
	}
}

func measured(e service.FleetEntry, health string, ago time.Duration, stale bool, checks ...service.CheckInfo) service.FleetEntry {
	e.Health, e.MeasuredAt, e.Trigger, e.Stale = health, past(ago), "scheduled", stale
	e.Connection = service.ConnectionInfo{State: "reachable", MeasuredAt: past(ago)}
	for _, c := range checks {
		c.MeasuredAt = testNow.Add(-ago)
		e.Checks = append(e.Checks, c)
	}
	return e
}

var (
	sshOK   = service.CheckInfo{Check: "ssh.endpoint", Severity: "critical", Status: "ok", DurationMS: 40}
	kubeBad = service.CheckInfo{Check: "kube.endpoint", Severity: "critical", Status: "failed", Detail: "dial tcp 203.0.113.11:6443: connection refused", DurationMS: 12}
	diskBad = service.CheckInfo{Check: "disk.free", Severity: "warning", Status: "failed", Detail: "/var/lib 91% used", DurationMS: 30}
)

// mixedFleet is fifteen customers in every attention state, in the service's order.
func mixedFleet() service.FleetInfo {
	zulu := entry("zulu", "", "invalid")
	zulu.Problem = "cannot parse the customer record at line 3"
	mike := measured(entry("mike", "prod", "fail"), "fail", 5*time.Minute, false, sshOK, kubeBad)
	mike.Versions = map[string]string{"rke2": "v1.30.4+rke2r1", "rancher": "v2.9.1"}
	oscar := measured(entry("oscar", "staging", "fail"), "fail", 2*time.Hour, true, kubeBad)
	golf := entry("golf", "staging", "unreachable")
	golf.Stale, golf.Missed = true, 2
	golf.Connection = service.ConnectionInfo{State: "unreachable", Detail: "lookup golf-srv-1.example.test: no such host", MeasuredAt: past(3 * time.Hour)}
	golf.ActiveRuns = []service.ActiveRunInfo{{ID: "20260911T115000Z-00000009", Action: "cluster access"}}
	lima := measured(entry("lima", "test", "stale"), "ok", 7*time.Hour, true, sshOK)
	lima.Missed = 3
	alpha := measured(entry("alpha", "test", "ok"), "ok", 30*time.Second, false, sshOK)
	alpha.Checking = true
	bravo := measured(entry("bravo", "prod", "ok"), "ok", time.Minute, false, sshOK)
	bravo.ActiveRuns = []service.ActiveRunInfo{{ID: "20260911T115800Z-00000003", Action: "fake touch"}}
	return service.FleetInfo{Now: testNow, Customers: []service.FleetEntry{
		zulu, mike, oscar, golf,
		measured(entry("kilo", "prod", "stale"), "ok", 5*time.Hour, true, sshOK),
		lima,
		measured(entry("papa", "prod", "stale"), "warn", 50*time.Hour, true, diskBad),
		entry("hotel", "staging", "never"),
		entry("india", "test", "never"),
		measured(entry("echo", "prod", "warn"), "warn", 10*time.Minute, false, sshOK, diskBad),
		measured(entry("foxtrot", "staging", "warn"), "warn", 12*time.Minute, false, diskBad),
		alpha, bravo,
		measured(entry("charlie", "prod", "ok"), "ok", 2*time.Minute, false, sshOK),
		measured(entry("delta", "staging", "ok"), "ok", 3*time.Minute, false, sshOK),
	}}
}

func testRuns() []service.RunInfo {
	return []service.RunInfo{
		{ID: "20260911T114000Z-00000001", Action: "fake check", Target: service.Target{Kind: "customer", Customer: "mike"}, Status: "succeeded",
			StartedAt: testNow.Add(-20 * time.Minute), LastEventID: 2,
			Result: &service.ResultInfo{Status: "succeeded", Summary: "checked 2 endpoints", FinishedAt: testNow.Add(-19 * time.Minute)}},
		{ID: "20260911T115000Z-00000002", Action: "fake quick", Target: service.Target{Kind: "global"}, Status: "succeeded",
			StartedAt: testNow.Add(-10 * time.Minute), LastEventID: 1,
			Result: &service.ResultInfo{Status: "succeeded", Summary: "quick done", FinishedAt: testNow.Add(-10 * time.Minute)}},
		{ID: "20260911T115800Z-00000003", Action: "fake touch", Target: service.Target{Kind: "customer", Customer: "bravo"}, Status: "running",
			StartedAt: testNow.Add(-2 * time.Minute), LastEventID: 3},
	}
}

func events(n int, step string) []service.Event {
	out := make([]service.Event, n)
	for i := range out {
		out[i] = service.Event{ID: uint64(i + 1), Time: testNow, Step: step, Message: fmt.Sprintf("event %d", i+1)}
	}
	return out
}

func newFake() *fakeClient {
	fleet := mixedFleet()
	var customers []service.CustomerInfo
	for _, c := range fleet.Customers {
		customers = append(customers, service.CustomerInfo{ID: c.ID, Environment: c.Environment, Cluster: c.Cluster, Nodes: 3, Problem: c.Problem})
	}
	runs := testRuns()
	return &fakeClient{
		hello:     service.Hello{Service: "ktags", Version: "test", PID: 42, ActiveRuns: 1},
		fleet:     fleet,
		customers: customers,
		actions:   testActions(),
		runs:      runs,
		events: map[string][]service.Event{
			runs[0].ID: events(2, "check"),
			runs[2].ID: events(3, "touch"),
		},
		final:   map[string]service.RunInfo{runs[0].ID: runs[0], runs[2].ID: runs[2]},
		dropAt:  map[string]uint64{},
		invalid: map[string]bool{},
		hold:    map[string]bool{runs[2].ID: true},
	}
}

func emptyFake() *fakeClient {
	f := newFake()
	f.fleet, f.customers, f.runs = service.FleetInfo{Now: testNow, Customers: []service.FleetEntry{}}, nil, nil
	return f
}

// driver runs a model like Bubble Tea does: commands run on goroutines and their messages
// come back through one channel, but every Update happens on the test goroutine.
type driver struct {
	t    *testing.T
	m    Model
	msgs chan tea.Msg
	quit bool
}

func newZones(t *testing.T) *zone.Manager {
	z := zone.New()
	t.Cleanup(z.Close)
	return z
}

func newDriver(t *testing.T, c Client, opts ...func(*Options)) *driver {
	t.Helper()
	o := Options{Client: c, Version: "test", Now: func() time.Time { return testNow }, Zones: newZones(t)}
	for _, f := range opts {
		f(&o)
	}
	d := &driver{t: t, m: New(o), msgs: make(chan tea.Msg, 1024)}
	d.apply(tea.WindowSizeMsg{Width: 100, Height: 30})
	d.run(d.m.Init())
	return d
}

// connected starts a driver and waits for the first refresh.
func connected(t *testing.T, c Client, opts ...func(*Options)) *driver {
	t.Helper()
	d := newDriver(t, c, opts...)
	d.until("connected", func(m Model) bool { return m.conn == connConnected })
	return d
}

func (d *driver) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if msg := cmd(); msg != nil {
			d.msgs <- msg
		}
	}()
}

func (d *driver) apply(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, cmd := range msg {
			d.run(cmd)
		}
		return
	case tea.QuitMsg:
		d.quit = true
		return
	}
	next, cmd := d.m.Update(msg)
	d.m = next.(Model)
	d.run(cmd)
}

// until applies messages until cond holds.
func (d *driver) until(what string, cond func(Model) bool) {
	d.t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond(d.m) {
		select {
		case msg := <-d.msgs:
			d.apply(msg)
		case <-deadline:
			d.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// drain applies the messages that are already waiting, then those that arrive within a
// short grace period: enough for a synchronous fake to answer.
func (d *driver) drain() {
	for {
		select {
		case msg := <-d.msgs:
			d.apply(msg)
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
}

func keyMsg(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(name)
	if len(r) != 1 {
		panic("unknown key " + name)
	}
	return tea.KeyPressMsg{Code: r[0], Text: name}
}

func (d *driver) key(names ...string) {
	for _, name := range names {
		d.apply(keyMsg(name))
	}
}

func (d *driver) typeText(s string) {
	for _, r := range s {
		if r == ' ' {
			d.key("space")
			continue
		}
		d.key(string(r))
	}
}

// click renders a frame with a fresh zone manager, so only this frame's zones exist, and
// clicks the first cell of zone id.
func (d *driver) click(id string, button tea.MouseButton) {
	d.t.Helper()
	d.m.zones = newZones(d.t)
	_ = d.m.View()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if z := d.m.zones.Get(id); !z.IsZero() {
			d.apply(tea.MouseClickMsg{X: z.StartX, Y: z.StartY, Button: button})
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("zone %q is not in the frame:\n%s", id, d.frame())
		}
		<-time.After(time.Millisecond)
	}
}

func (d *driver) wheel(id string, button tea.MouseButton) {
	d.t.Helper()
	d.m.zones = newZones(d.t)
	_ = d.m.View()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if z := d.m.zones.Get(id); !z.IsZero() {
			d.apply(tea.MouseWheelMsg{X: z.StartX + 1, Y: z.StartY + 1, Button: button})
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("zone %q is not in the frame", id)
		}
		<-time.After(time.Millisecond)
	}
}

func (d *driver) selectCustomer(id string) {
	d.t.Helper()
	i := slices.IndexFunc(d.m.customers(), func(c service.FleetEntry) bool { return c.ID == id })
	if i < 0 {
		d.t.Fatalf("no customer %s", id)
	}
	d.m.focus = focusList
	d.key("home")
	for range i {
		d.key("down")
	}
	if d.m.selected != id {
		d.t.Fatalf("selected %q, want %q", d.m.selected, id)
	}
}

func (d *driver) frame() string {
	return stripped(d.m.render())
}
