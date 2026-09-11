package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/nesiler/ktags/internal/core/connect"
	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/core/health"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/run"
	"github.com/nesiler/ktags/internal/service"
	"github.com/nesiler/ktags/internal/tui"
)

// t0 is the proof's wall clock at service start.
var t0 = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

// jumpClock is a wall clock that moves only when the test says so. A jump fires every wait
// that it passes, the way timers look to a process after the laptop wakes.
type jumpClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []jumpWaiter
}

type jumpWaiter struct {
	at time.Time
	ch chan time.Time
}

func newJumpClock() *jumpClock { return &jumpClock{now: t0} }

func (c *jumpClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *jumpClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, jumpWaiter{at: c.now.Add(d), ch: ch})
	return ch
}

func (c *jumpClock) Jump(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			kept = append(kept, w)
			continue
		}
		w.ch <- c.now
	}
	c.waiters = kept
}

// gate holds the checks of chosen customers until released; "*" holds every customer. A held
// check announces itself on entered.
type gate struct {
	mu      sync.Mutex
	held    map[string]bool
	open    chan struct{}
	entered chan string
}

func newGate() *gate { return &gate{entered: make(chan string, 64)} }

func (g *gate) hold(customers ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.held, g.open = map[string]bool{}, make(chan struct{})
	for _, c := range customers {
		g.held[c] = true
	}
}

func (g *gate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open != nil {
		close(g.open)
	}
	g.held, g.open = nil, nil
}

func (g *gate) pass(ctx context.Context, customer string) error {
	g.mu.Lock()
	open := g.open
	held := g.held[customer] || g.held["*"]
	g.mu.Unlock()
	if !held {
		return nil
	}
	g.entered <- customer
	select {
	case <-open:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// proofChecks are the deterministic fake adapters: the connect package's fake SSH adapter with
// one script per customer, behind the gate.
func proofChecks(g *gate, scripts map[string]connect.Script) []health.Check {
	ssh := func(customer string) connect.FakeSSH { return connect.FakeSSH{Script: scripts[customer]} }
	target := func(customer string) connect.SSHTarget {
		return connect.SSHTarget{Customer: customer, Node: "srv-1", Host: "203.0.113.11", Port: 22, User: "ops"}
	}
	return []health.Check{
		{Name: "ssh.endpoint", Severity: health.SeverityCritical, Run: func(ctx context.Context, c string) error {
			if err := g.pass(ctx, c); err != nil {
				return err
			}
			return ssh(c).Reach(ctx, target(c))
		}},
		{Name: "ssh.auth", Severity: health.SeverityWarning, Run: func(ctx context.Context, c string) error {
			return ssh(c).Authenticate(ctx, target(c))
		}},
	}
}

// inprocLauncher is the service manager of the proof: Launch starts `ktags service run` of this
// composition root on its own goroutine, with the proof's clock and adapters.
type inprocLauncher struct {
	deps     *deps
	launches int
	done     chan error
}

func (l *inprocLauncher) Launch(context.Context) error {
	l.launches++
	roots, err := paths.Resolve(l.deps.env)
	if err != nil {
		return err
	}
	control := serviceControl{roots: roots, build: domain.Build{Name: "ktags", Version: "test"}, deps: *l.deps}
	done := make(chan error, 1)
	l.done = done
	go func() { done <- control.Run(context.Background(), io.Discard) }()
	return nil
}

func (*inprocLauncher) Unload(context.Context) error { return nil }

// stopped waits until the service goroutine has returned.
func (l *inprocLauncher) stopped(t *testing.T) {
	t.Helper()
	select {
	case err := <-l.done:
		l.done = nil
		if err != nil {
			t.Fatalf("service run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the service did not stop")
	}
}

type proof struct {
	*harness
	clock    *jumpClock
	gate     *gate
	launcher *inprocLauncher
}

func newProof(t *testing.T, scripts map[string]connect.Script) *proof {
	t.Helper()
	p := &proof{harness: newHarness(t), clock: newJumpClock(), gate: newGate()}
	p.launcher = &inprocLauncher{deps: &p.deps}
	p.deps.launcher = func(paths.Roots) service.Launcher { return p.launcher }
	p.deps.interrupt = context.WithCancel
	p.deps.health = &healthConfig{checks: proofChecks(p.gate, scripts), interval: time.Hour, clock: p.clock}
	t.Cleanup(func() {
		p.gate.release()
		if code, _, _ := p.ktags("service", "stop", "--cancel-runs"); code == 0 && p.launcher.done != nil {
			select {
			case <-p.launcher.done:
			case <-time.After(20 * time.Second):
			}
		}
	})
	return p
}

// fleetUntil polls the fleet summary until cond holds.
func (p *proof) fleetUntil(what string, cond func(service.FleetInfo) bool) service.FleetInfo {
	p.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		info, err := p.client.Fleet(context.Background())
		if err == nil && cond(info) {
			return info
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("fleet never showed %s: %+v, %v", what, info, err)
		}
		<-time.After(5 * time.Millisecond)
	}
}

func each(pred func(service.FleetEntry) bool) func(service.FleetInfo) bool {
	return func(info service.FleetInfo) bool {
		for _, c := range info.Customers {
			if c.Problem == "" && !pred(c) {
				return false
			}
		}
		return len(info.Customers) > 0
	}
}

func entry(t *testing.T, info service.FleetInfo, id string) service.FleetEntry {
	t.Helper()
	for _, c := range info.Customers {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("customer %s is not in the fleet", id)
	return service.FleetEntry{}
}

// tuiFrame opens a TUI on the service, loads it once and returns the frame without styling.
func (p *proof) tuiFrame() string {
	p.t.Helper()
	zones := zone.New()
	p.t.Cleanup(zones.Close)
	m := tui.New(tui.Options{Client: p.client, Version: "test", Zones: zones, Now: p.clock.Now})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = settle(next.(tui.Model), m.Init())
	return ansi.Strip(m.View().Content)
}

// tuiState is how the TUI writes each attention state (tui/format.go stateText).
var tuiState = map[string]string{
	"ok": "✓ ok", "warn": "⚠ warn", "fail": "✗ fail", "invalid": "! invalid", "never": "○ never", "unreachable": "✗ unreachable",
}

// agree checks that the CLI's JSON document and the TUI list show the same customers in the
// same order with the same state. It returns the CLI's human output for the record.
func (p *proof) agree(stateText func(service.FleetEntry) string) string {
	p.t.Helper()
	stdout, _ := p.want(0, "fleet", "list", "--json")
	var doc struct {
		Kind string            `json:"kind"`
		Data service.FleetInfo `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.Kind != "fleet.list" {
		p.t.Fatalf("fleet list --json: %v\n%s", err, stdout)
	}
	frame := p.tuiFrame()
	lines := strings.Split(frame, "\n")
	last := -1
	for _, c := range doc.Data.Customers {
		row := -1
		for i, line := range lines {
			// Only rows of the customer list; the top bar also names the selection.
			if strings.HasPrefix(line, "┃") && strings.Contains(line, " "+c.ID+" ") {
				row = i
				break
			}
		}
		if row <= last {
			p.t.Fatalf("the TUI shows %s at row %d, not after row %d as the CLI orders it:\n%s", c.ID, row, last, frame)
		}
		if want := stateText(c); !strings.Contains(lines[row], want) {
			p.t.Fatalf("the TUI row of %s lacks %q (CLI state %s):\n%s", c.ID, want, c.State, frame)
		}
		last = row
	}
	human, _ := p.want(0, "fleet", "list")
	return human
}

// #18-K1: the packaged CLI, service and TUI against deterministic fake adapters for 15
// customers. The first command starts the service; the schedule measures every customer;
// a manual health run is detached from and reconnected to; its result survives a service
// restart; CLI and TUI agree; a laptop sleep shows stale data and recovers after wake.
func TestPhase0Proof(t *testing.T) {
	ids := make([]string, 0, 15)
	for i := 1; i <= 15; i++ {
		ids = append(ids, fmt.Sprintf("c%02d", i))
	}
	scripts := map[string]connect.Script{
		"c03": {Endpoint: &connect.Failure{Kind: connect.KindTCP, Detail: "dial tcp 203.0.113.11:22: connection refused"}},
		"c05": {Auth: &connect.Failure{Kind: connect.KindSSHAuth, Detail: "ssh: unable to authenticate"}},
	}
	p := newProof(t, scripts)
	data := filepath.Join(p.home, "data")
	for _, id := range ids {
		if id == "c09" {
			writeInvalid(t, data, id)
			continue
		}
		saveCustomer(t, data, id)
	}
	stateOf := map[string]string{"c03": "fail", "c05": "warn", "c09": "invalid"}
	want := func(id string) string {
		if s, ok := stateOf[id]; ok {
			return s
		}
		return "ok"
	}
	ctx := context.Background()

	// Autostart: the first interactive command starts the service, which lists the health action.
	stdout, stderr := p.want(0, "action", "list")
	contains(t, stderr, "ktags: started the ktags service")
	contains(t, stdout, "health")
	if p.launcher.launches != 1 {
		t.Fatalf("%d launches, want 1", p.launcher.launches)
	}

	// Scheduled health: the first slot measures every valid customer without any command.
	info := p.fleetUntil("every customer measured", each(func(c service.FleetEntry) bool { return c.MeasuredAt != nil }))
	for _, id := range ids {
		c := entry(t, info, id)
		if c.State != want(id) {
			t.Fatalf("%s is %s, want %s", id, c.State, want(id))
		}
		if c.Problem == "" && (c.Trigger != "scheduled" || !c.MeasuredAt.Equal(t0) || c.Stale) {
			t.Fatalf("%s: trigger %q measured %v stale %v; want a fresh scheduled result at %s", id, c.Trigger, c.MeasuredAt, c.Stale, t0)
		}
	}
	human := p.agree(func(c service.FleetEntry) string { return tuiState[c.State] })
	contains(t, human, "15 customer(s): 1 invalid, 1 fail, 1 warn, 12 ok", "c03 ssh.endpoint failed (critical", "connection refused")
	t.Logf("fleet after the first scheduled slot:\n%s", human)

	// Detach during a manual health run, then reconnect.
	p.gate.hold("c07")
	var out, errOut bytes.Buffer
	detach := make(chan struct{})
	d := p.deps
	d.interrupt = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-detach:
				cancel()
			case <-ctx.Done():
			}
		}()
		return ctx, cancel
	}
	exited := make(chan int, 1)
	go func() { exited <- runWith("test", []string{"action", "run", "health", "c07"}, &out, &errOut, d) }()
	select {
	case <-p.gate.entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the manual health run never reached its check")
	}
	info = p.fleetUntil("c07 checking with an active run", func(info service.FleetInfo) bool {
		c := entry(t, info, "c07")
		return c.Checking && len(c.ActiveRuns) == 1
	})
	id := entry(t, info, "c07").ActiveRuns[0].ID
	close(detach)
	if code := <-exited; code != 0 {
		t.Fatalf("detached health run exited %d, want 0\n%s\n%s", code, out.String(), errOut.String())
	}
	contains(t, out.String(), "started run "+id+" (health)", "1 health: measuring c07")
	contains(t, errOut.String(), "detached from run "+id+" at event 1; it continues in the ktags service", "next: ktags run watch "+id+" --after 1")
	t.Logf("detach:\n%s%s", out.String(), errOut.String())
	stdout, _ = p.want(0, "run", "status", id)
	contains(t, stdout, "run "+id+" running")

	p.gate.release()
	stdout, _ = p.want(0, "run", "watch", id, "--after", "1")
	contains(t, stdout, "2 check: ssh.endpoint ok (critical)", "3 check: ssh.auth ok (warning)", "c07 health ok: 2 of 2 checks ok")
	t.Logf("reconnect:\n%s", stdout)
	info = p.fleetUntil("c07 measured manually", func(info service.FleetInfo) bool { return entry(t, info, "c07").Trigger == "manual" })
	if c := entry(t, info, "c07"); c.State != "ok" || c.Checking || len(c.ActiveRuns) != 0 {
		t.Fatalf("c07 after the manual run %+v, want ok, idle", c)
	}
	// A red customer's health run fails, and says why.
	stdout, _ = p.want(3, "action", "run", "health", "c03")
	contains(t, stdout, "ssh.endpoint failed (critical): tcp: dial tcp 203.0.113.11:22: connection refused", "failed: c03 health fail: 1 of 2 checks ok")

	// Durable result: the run is on disk after the service stopped, and a restarted service
	// shows it.
	stdout, _ = p.want(0, "service", "stop")
	contains(t, stdout, "ktags service stopped")
	p.launcher.stopped(t)
	store, err := run.NewStore(filepath.Join(p.home, "state"), run.Options{Redact: mask.Mask})
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := store.Load(ctx, id)
	if err != nil || recorded.Status != run.StatusSucceeded || recorded.Result == nil || recorded.Result.Summary != "c07 health ok: 2 of 2 checks ok" {
		t.Fatalf("recorded run %+v, %v; want the succeeded health result on disk", recorded, err)
	}
	stdout, stderr = p.want(0, "run", "status", id)
	contains(t, stderr, "ktags: started the ktags service")
	contains(t, stdout, "run "+id+" succeeded", "result   succeeded: c07 health ok: 2 of 2 checks ok")
	if p.launcher.launches != 2 {
		t.Fatalf("%d launches, want 2", p.launcher.launches)
	}
	p.fleetUntil("every customer measured again", each(func(c service.FleetEntry) bool { return c.MeasuredAt != nil }))
	p.agree(func(c service.FleetEntry) string { return tuiState[c.State] })

	// Laptop sleep: the clock jumps three hours while every check is held. The missed slots are
	// counted and never run late; the data is stale while the catch-up run is in progress.
	p.gate.hold("*")
	p.clock.Jump(3 * time.Hour)
	// At most four customers are measured at once (the engine default); the rest wait for a slot.
	info = p.fleetUntil("stale and missed, four checking", func(info service.FleetInfo) bool {
		checking := 0
		for _, c := range info.Customers {
			if c.Checking {
				checking++
			}
		}
		return checking == 4 && each(func(c service.FleetEntry) bool { return c.Stale && c.Missed > 0 })(info)
	})
	for _, id := range ids {
		c := entry(t, info, id)
		switch {
		case c.Problem != "":
		case id == "c03":
			if c.State != "fail" {
				t.Fatalf("c03 is %s while stale, want fail: a failure outranks stale", c.State)
			}
		case c.State != "stale" || !c.MeasuredAt.Equal(t0):
			t.Fatalf("%s after the sleep: state %s measured %v, want stale data from %s", id, c.State, c.MeasuredAt, t0)
		}
	}
	human = p.agree(func(c service.FleetEntry) string {
		if c.State == "stale" {
			return "○ missed · 3h"
		}
		return tuiState[c.State]
	})
	contains(t, human, "15 customer(s): 1 invalid, 1 fail, 13 stale", "stale, 2 missed")
	t.Logf("fleet during the catch-up run after the sleep:\n%s", human)

	// Stale recovery: the catch-up run completes and every result is fresh at wake time.
	p.gate.release()
	woke := t0.Add(3 * time.Hour)
	info = p.fleetUntil("fresh after the catch-up", each(func(c service.FleetEntry) bool {
		return !c.Stale && !c.Checking && c.MeasuredAt != nil && c.MeasuredAt.Equal(woke)
	}))
	for _, id := range ids {
		c := entry(t, info, id)
		// #68-K1 D1: the fresh catch-up result resets the missed count.
		if c.State != want(id) || (c.Problem == "" && (c.Missed != 0 || c.Trigger != "scheduled")) {
			t.Fatalf("%s after the catch-up: state %s missed %d trigger %s; want %s, scheduled, with no missed count", id, c.State, c.Missed, c.Trigger, want(id))
		}
	}
	human = p.agree(func(c service.FleetEntry) string { return tuiState[c.State] })
	contains(t, human, "15 customer(s): 1 invalid, 1 fail, 1 warn, 12 ok")
	t.Logf("fleet after the catch-up run:\n%s", human)

	p.want(0, "service", "stop")
	p.launcher.stopped(t)
}
