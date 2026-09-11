package health

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/mask"
)

var t0 = time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)

// fakeClock moves only when the test advances it. Every After call is reported on waits, so a
// test knows when a check or a scheduled job is waiting.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
	waits   chan time.Duration
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: t0, waits: make(chan time.Duration, 1024)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	if d <= 0 {
		ch <- c.now
	} else {
		c.waiters = append(c.waiters, waiter{at: c.now.Add(d), ch: ch})
	}
	c.mu.Unlock()
	c.waits <- d
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
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

// idle waits until n more waits have begun and returns their durations.
func (c *fakeClock) idle(t *testing.T, n int) []time.Duration {
	t.Helper()
	var out []time.Duration
	for i := 0; i < n; i++ {
		select {
		case d := <-c.waits:
			out = append(out, d)
		case <-time.After(10 * time.Second):
			t.Fatalf("wait %d of %d did not begin", i+1, n)
		}
	}
	return out
}

func ok(context.Context, string) error { return nil }

// hang ignores its context and returns only when block is closed.
func hang(block chan struct{}) func(context.Context, string) error {
	return func(context.Context, string) error {
		<-block
		return nil
	}
}

func newEngine(t *testing.T, clock *fakeClock, opts Options) *Engine {
	t.Helper()
	opts.Clock = clock
	if opts.Redact == nil {
		opts.Redact = mask.Mask
	}
	if opts.Interval == 0 {
		opts.Interval = 15 * time.Minute
	}
	e, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func start(t *testing.T, e *Engine) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)
	t.Cleanup(func() {
		cancel()
		e.Wait()
	})
}

func view(t *testing.T, e *Engine, customer string) View {
	t.Helper()
	for _, v := range e.Views() {
		if v.Customer == customer {
			return v
		}
	}
	t.Fatalf("no view for %s", customer)
	return View{}
}

// eventually waits until cond holds, yielding between tries.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		runtime.Gosched()
	}
}

// calls counts the calls of a check per customer.
type calls struct {
	mu sync.Mutex
	n  map[string]int
	at map[string][]time.Time
}

func (c *calls) record(clock *fakeClock, customer string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n, c.at = map[string]int{}, map[string][]time.Time{}
	}
	c.n[customer]++
	c.at[customer] = append(c.at[customer], clock.Now())
	return c.n[customer]
}

func (c *calls) times(customer string) []time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Time(nil), c.at[customer]...)
}

func TestNewRefusesBadOptions(t *testing.T) {
	check := Check{Name: "ssh", Severity: SeverityCritical, Run: ok}
	valid := func() Options {
		return Options{Clock: newFakeClock(), Redact: mask.Mask, Checks: []Check{check}, Customers: []string{"acme"}, Interval: time.Hour}
	}
	tests := []struct {
		name   string
		change func(*Options)
		want   string
	}{
		{"no clock", func(o *Options) { o.Clock = nil }, "health: no clock"},
		{"no redactor", func(o *Options) { o.Redact = nil }, "no redactor"},
		{"no checks", func(o *Options) { o.Checks = nil }, "no checks"},
		{"zero interval", func(o *Options) { o.Interval = 0 }, "default interval must be positive"},
		{"negative interval", func(o *Options) { o.Interval = -time.Minute }, "default interval must be positive"},
		{"negative concurrency", func(o *Options) { o.Concurrency = -1 }, "concurrency must not be negative"},
		{"check without name", func(o *Options) { o.Checks = []Check{{Severity: SeverityCritical, Run: ok}} }, "has no name"},
		{"check twice", func(o *Options) { o.Checks = []Check{check, check} }, "defined twice"},
		{"check without run", func(o *Options) { o.Checks = []Check{{Name: "ssh", Severity: SeverityCritical}} }, "nothing to run"},
		{"unknown severity", func(o *Options) { o.Checks = []Check{{Name: "ssh", Severity: "info", Run: ok}} }, "want critical or warning"},
		{"empty severity", func(o *Options) { o.Checks = []Check{{Name: "ssh", Run: ok}} }, "want critical or warning"},
		{"negative timeout", func(o *Options) {
			o.Checks = []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: -time.Second, Run: ok}}
		}, "negative timeout"},
		{"customer without name", func(o *Options) { o.Customers = []string{""} }, "customer has no name"},
		{"customer twice", func(o *Options) { o.Customers = []string{"acme", "acme"} }, "listed twice"},
		{"interval for a customer outside the fleet", func(o *Options) { o.Intervals = map[string]time.Duration{"globex": time.Hour} }, "not in the fleet"},
		{"zero customer interval", func(o *Options) { o.Intervals = map[string]time.Duration{"acme": 0} }, "interval of \"acme\" must be positive"},
		{"negative customer interval", func(o *Options) { o.Intervals = map[string]time.Duration{"acme": -time.Hour} }, "interval of \"acme\" must be positive"},
		{"interval the schedule refuses", func(o *Options) { o.Interval = time.Nanosecond }, "shorter than the interval"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := valid()
			tc.change(&opts)
			_, err := New(opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New: %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := New(valid()); err != nil {
		t.Fatalf("New with valid options: %v", err)
	}
}

func TestRunRefusesUnknownCustomer(t *testing.T) {
	c := &calls{}
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(context.Context, string) error {
		c.record(clock, "")
		return nil
	}}}})
	if _, err := e.Run(context.Background(), "acme", "globex"); err == nil || !strings.Contains(err.Error(), `"globex" is not in the fleet`) {
		t.Fatalf("Run: %v, want a refusal naming globex", err)
	}
	if got := len(c.times("")); got != 0 {
		t.Fatalf("the check ran %d times, want none: a refused run measures nothing", got)
	}
}

// #15-K1 manual: a manual run measures now, and its health is the worst failed severity.
func TestManualRun(t *testing.T) {
	fail := errors.New("disk 91% full")
	tests := []struct {
		name               string
		critical, warning  error
		want               Health
		wantCrit, wantWarn Status
	}{
		{"all pass", nil, nil, HealthOK, StatusOK, StatusOK},
		{"warning fails", nil, fail, HealthWarn, StatusOK, StatusFailed},
		{"critical fails", fail, nil, HealthFail, StatusFailed, StatusOK},
		{"both fail", fail, fail, HealthFail, StatusFailed, StatusFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := newFakeClock()
			e := newEngine(t, clock, Options{Customers: []string{"acme", "globex"}, Checks: []Check{
				{Name: "ssh", Severity: SeverityCritical, Run: func(context.Context, string) error { return tc.critical }},
				{Name: "disk", Severity: SeverityWarning, Run: func(context.Context, string) error { return tc.warning }},
			}})
			reports, err := e.Run(context.Background(), "acme")
			if err != nil || len(reports) != 1 {
				t.Fatalf("Run: %v %+v, want one report", err, reports)
			}
			r := reports[0]
			if r.Customer != "acme" || r.Trigger != TriggerManual || !r.MeasuredAt.Equal(t0) || r.Health != tc.want {
				t.Fatalf("report %+v, want acme, manual, measured at %s, health %s", r, t0, tc.want)
			}
			if len(r.Checks) != 2 || r.Checks[0].Status != tc.wantCrit || r.Checks[1].Status != tc.wantWarn {
				t.Fatalf("checks %+v, want %s then %s", r.Checks, tc.wantCrit, tc.wantWarn)
			}
			if tc.warning != nil && r.Checks[1].Detail != "disk 91% full" {
				t.Fatalf("detail %q, want the measured fact", r.Checks[1].Detail)
			}
			if v := view(t, e, "acme"); v.Health != tc.want || v.Stale || !v.MeasuredAt.Equal(t0) || v.Trigger != TriggerManual {
				t.Fatalf("acme view %+v, want the fresh manual result", v)
			}
		})
	}
}

// #15-K1 interval: each customer runs at its own interval, the default where none is set.
func TestIntervalRunsPerCustomer(t *testing.T) {
	c := &calls{}
	clock := newFakeClock()
	e := newEngine(t, clock, Options{
		Customers: []string{"acme", "globex"},
		Interval:  time.Hour,
		Intervals: map[string]time.Duration{"acme": 15 * time.Minute},
		Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(_ context.Context, customer string) error {
			c.record(clock, customer)
			return nil
		}}},
	})
	start(t, e)
	clock.idle(t, 4) // per customer: the check's timeout and the job's next wait
	for i := 1; i <= 15; i++ {
		clock.Advance(time.Minute)
		if i < 15 {
			clock.idle(t, 2)
		} else {
			clock.idle(t, 3) // acme's slot: its check and its next wait
		}
	}
	if got := c.times("acme"); len(got) != 2 || !got[1].Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("acme ran at %v, want 09:00 and 09:15", got)
	}
	if got := c.times("globex"); len(got) != 1 {
		t.Fatalf("globex ran at %v, want only 09:00 on its one-hour default", got)
	}
	v := view(t, e, "acme")
	if v.Interval != 15*time.Minute || v.Trigger != TriggerScheduled || !v.MeasuredAt.Equal(t0.Add(15*time.Minute)) || v.Stale || v.Health != HealthOK {
		t.Fatalf("acme view %+v", v)
	}
	if v := view(t, e, "globex"); v.Interval != time.Hour || !v.MeasuredAt.Equal(t0) || v.Stale {
		t.Fatalf("globex view %+v", v)
	}
}

// #15-K1 overdue after sleep, #15-K3: a sleep leaves the old result visible with its measured
// time, stale and with the missed slots, until one catch-up run has ended.
func TestOverdueAfterSleep(t *testing.T) {
	c := &calls{}
	release := make(chan struct{})
	started := make(chan int, 8)
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(ctx context.Context, customer string) error {
		n := c.record(clock, customer)
		started <- n
		if n == 2 {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return nil
	}}}})
	start(t, e)
	clock.idle(t, 2)
	if v := view(t, e, "acme"); v.Stale || !v.MeasuredAt.Equal(t0) {
		t.Fatalf("after the first slot: %+v", v)
	}

	sleep := 3*time.Hour + 7*time.Minute
	clock.Advance(sleep)
	for _, want := range []int{1, 2} {
		if n := <-started; n != want {
			t.Fatalf("call %d started, want call %d", n, want)
		}
	}
	v := view(t, e, "acme")
	if !v.Stale || !v.Running || v.Health != HealthOK || !v.MeasuredAt.Equal(t0) || v.Missed != 12 {
		t.Fatalf("during the catch-up run: %+v, want the 09:00 result, stale, running, 12 missed", v)
	}
	want := "acme ok measured 2026-09-11T09:00:00Z stale, 12 missed slots 2026-09-11T09:15:00Z to 2026-09-11T12:00:00Z, running"
	if got := v.String(); got != want {
		t.Fatalf("display\n got %q\nwant %q", got, want)
	}

	close(release)
	clock.idle(t, 2)
	wake := t0.Add(sleep)
	if v := view(t, e, "acme"); v.Stale || v.Running || !v.MeasuredAt.Equal(wake) {
		t.Fatalf("after the catch-up run: %+v, want fresh and measured at wake", v)
	}
	if got := c.times("acme"); len(got) != 2 || !got[1].Equal(wake) {
		t.Fatalf("the check ran at %v, want 09:00 and wake only: a missed slot never runs", got)
	}
}

// #15-K1 timeout: a check that ignores its context times out on the injected clock; later
// checks still run.
func TestCheckTimeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{
		{Name: "ssh", Severity: SeverityCritical, Timeout: 5 * time.Second, Run: hang(block)},
		{Name: "disk", Severity: SeverityWarning, Run: ok},
	}})
	done := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		done <- reports
	}()
	if got := clock.idle(t, 1); got[0] != 5*time.Second {
		t.Fatalf("waited %s for the first check, want its 5s timeout", got[0])
	}
	clock.Advance(5 * time.Second)
	var reports []Report
	select {
	case reports = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not end at the check's timeout")
	}
	if got := clock.idle(t, 1); got[0] != defaultTimeout {
		t.Fatalf("waited %s for the second check, want the %s default", got[0], defaultTimeout)
	}
	r := reports[0]
	if r.Health != HealthFail || len(r.Checks) != 2 {
		t.Fatalf("report %+v, want fail with both checks", r)
	}
	slow := r.Checks[0]
	if slow.Status != StatusTimeout || slow.Detail != "no answer within 5s" || slow.Duration != 5*time.Second {
		t.Fatalf("timed-out check %+v", slow)
	}
	if r.Checks[1].Status != StatusOK {
		t.Fatalf("second check %+v, want it run after the timeout", r.Checks[1])
	}
}

// #15-K2: a hung customer does not delay another customer's manual run.
func TestHungCustomerDoesNotBlockFleet(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	clock := newFakeClock()
	check := func(_ context.Context, customer string) error {
		if customer == "acme" {
			<-block
		}
		return nil
	}
	e := newEngine(t, clock, Options{Customers: []string{"acme", "globex"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: time.Minute, Run: check}}})
	hung := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		hung <- reports
	}()
	clock.idle(t, 1)
	reports, err := e.Run(context.Background(), "globex")
	if err != nil || len(reports) != 1 || reports[0].Health != HealthOK {
		t.Fatalf("globex while acme hangs: %v %+v", err, reports)
	}
	if v := view(t, e, "acme"); !v.Running || v.Health != HealthUnknown {
		t.Fatalf("acme view %+v, want running and still unknown", v)
	}
}

// #15-K2: with a single fleet slot, a hung customer releases it at its timeout, so the next
// customer is measured.
func TestHungCustomerReleasesItsSlot(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	clock := newFakeClock()
	check := func(_ context.Context, customer string) error {
		if customer == "acme" {
			<-block
		}
		return nil
	}
	e := newEngine(t, clock, Options{Concurrency: 1, Customers: []string{"acme", "globex"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: time.Minute, Run: check}}})
	acme := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		acme <- reports
	}()
	clock.idle(t, 1) // acme holds the only slot
	globex := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "globex")
		globex <- reports
	}()
	clock.Advance(time.Minute)
	for _, tc := range []struct {
		name string
		ch   chan []Report
		want Status
	}{{"acme", acme, StatusTimeout}, {"globex", globex, StatusOK}} {
		select {
		case reports := <-tc.ch:
			if len(reports) != 1 || reports[0].Checks[0].Status != tc.want {
				t.Fatalf("%s: %+v, want %s", tc.name, reports, tc.want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not end: the hung customer kept its slot", tc.name)
		}
	}
}

// #15-K2: a hung customer does not hold up the scheduler; the other customer keeps its slots.
// #68-K1 D2: the hung check is not started again at the next slot; that slot reports a timeout.
func TestHungCustomerDoesNotBlockScheduler(t *testing.T) {
	c := &calls{}
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme", "globex"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(_ context.Context, customer string) error {
		c.record(clock, customer)
		if customer == "acme" {
			<-block
		}
		return nil
	}}}})
	start(t, e)
	clock.idle(t, 3) // acme's hung check; globex's check and next wait
	for i := 1; i <= 15; i++ {
		clock.Advance(time.Minute)
		if i < 15 {
			clock.idle(t, 2)
		} else {
			clock.idle(t, 3) // acme's next wait; globex's check and next wait
		}
	}
	if got := c.times("globex"); len(got) != 2 || !got[1].Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("globex ran at %v, want 09:00 and 09:15", got)
	}
	if v := view(t, e, "globex"); v.Stale || v.Health != HealthOK {
		t.Fatalf("globex view %+v, want fresh and ok", v)
	}
	if got := c.times("acme"); len(got) != 1 {
		t.Fatalf("acme's check started at %v, want only at 09:00 while it hangs", got)
	}
	v := view(t, e, "acme")
	if v.Health != HealthFail || !v.MeasuredAt.Equal(t0.Add(15*time.Minute)) || v.Running ||
		v.Checks[0].Status != StatusTimeout || v.Checks[0].Detail != "not started: the previous run is still running" {
		t.Fatalf("acme view %+v, want the 09:15 slot reported as a timeout of the still-running check", v)
	}
}

// #68-K1 D2: at most one goroutine per hung check and customer. Once it returns, the check
// runs again; another customer's run of the same check is not affected.
func TestHungCheckNotRestarted(t *testing.T) {
	block := make(chan struct{})
	var started atomic.Int32
	clock := newFakeClock()
	running := make(chan struct{}, 4)
	e := newEngine(t, clock, Options{Customers: []string{"acme", "globex"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: time.Minute, Run: func(_ context.Context, customer string) error {
		if customer == "acme" {
			started.Add(1)
			running <- struct{}{}
			<-block
		}
		return nil
	}}}})
	first := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		first <- reports
	}()
	clock.idle(t, 1)
	<-running // the check itself has started, not only the wait for its timeout
	clock.Advance(time.Minute)
	if r := <-first; r[0].Checks[0].Detail != "no answer within 1m0s" {
		t.Fatalf("first run %+v, want its own timeout", r)
	}
	for i := 0; i < 3; i++ {
		reports, err := e.Run(context.Background(), "acme")
		if err != nil || reports[0].Checks[0].Status != StatusTimeout || reports[0].Checks[0].Detail != "not started: the previous run is still running" || reports[0].Checks[0].Duration != 0 {
			t.Fatalf("run %d while the check hangs: %+v %v", i, reports, err)
		}
	}
	if reports, err := e.Run(context.Background(), "globex"); err != nil || reports[0].Health != HealthOK {
		t.Fatalf("globex while acme's check hangs: %+v %v", reports, err)
	}
	if n := started.Load(); n != 1 {
		t.Fatalf("acme's check started %d times, want 1", n)
	}
	close(block)
	eventually(t, "the hung check returns", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.alive) == 0
	})
	reports, err := e.Run(context.Background(), "acme")
	if err != nil || reports[0].Health != HealthOK || started.Load() != 2 {
		t.Fatalf("after the check returned: %+v %v, %d starts; want a new ok run", reports, err, started.Load())
	}
}

// #68-K1 D2: a run cancelled while its check ignores the context also leaves that check
// running, so the next run does not start it again.
func TestCancelledHungCheckNotRestarted(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	var started atomic.Int32
	running := make(chan struct{}, 1)
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(context.Context, string) error {
		started.Add(1)
		running <- struct{}{}
		<-block
		return nil
	}}}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.Run(ctx, "acme")
		done <- err
	}()
	clock.idle(t, 1)
	<-running // the check itself has started, not only the wait for its timeout
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run: %v", err)
	}
	reports, err := e.Run(context.Background(), "acme")
	if err != nil || reports[0].Checks[0].Detail != "not started: the previous run is still running" || started.Load() != 1 {
		t.Fatalf("after the cancel: %+v %v, %d starts", reports, err, started.Load())
	}
}

// #68-K1 D1: a fresh measurement resets the missed count; only slots missed after it count, and
// a fresh view shows none.
func TestMissedResetsAfterFreshMeasurement(t *testing.T) {
	c := &calls{}
	release := make(chan struct{})
	third := make(chan struct{})
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(ctx context.Context, customer string) error {
		// The third run (the second catch-up) waits, so the view is read while it runs.
		if c.record(clock, customer) == 3 {
			close(third)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return nil
	}}}})
	start(t, e)
	clock.idle(t, 2)

	sleep := func(d time.Duration) {
		clock.Advance(d)
		clock.idle(t, 2) // the catch-up check and the next wait
	}
	// Before the catch-up ends, the view is stale with 12 missed; see TestOverdueAfterSleep.
	sleep(3*time.Hour + 7*time.Minute)
	v := view(t, e, "acme")
	if v.Stale || v.Missed != 0 || !v.MissedFrom.IsZero() || !v.MissedTo.IsZero() {
		t.Fatalf("after the catch-up: %+v, want fresh and no missed count", v)
	}
	if got := v.String(); strings.Contains(got, "missed") {
		t.Fatalf("display %q names missed slots on a fresh view", got)
	}

	// A second sleep counts only its own slots, while the result is stale.
	e.mu.Lock()
	base := e.missedBase["acme"]
	e.mu.Unlock()
	if base != 12 {
		t.Fatalf("baseline %d, want the 12 slots of the first sleep", base)
	}
	clock.Advance(time.Hour + 3*time.Minute)
	<-third
	v = view(t, e, "acme")
	if !v.Stale || v.Missed != 4 {
		t.Fatalf("second sleep during the catch-up: %+v, want stale and 4 missed", v)
	}
	close(release)
	clock.idle(t, 2)
	if v := view(t, e, "acme"); v.Stale || v.Missed != 0 {
		t.Fatalf("after the second catch-up: %+v, want fresh and no missed count", v)
	}
}

// #68-K1 D1: the baseline is the count when the cached measurement started. A manual run that
// began before a sleep and ends after it is cached while the catch-up still runs; its view is
// stale and counts the slots of the sleep.
func TestMissedBaseTakenAtStart(t *testing.T) {
	manual, catchUp := make(chan struct{}), make(chan struct{})
	held := make(chan string, 2)
	var aRuns, bRuns atomic.Int32
	// By D2 two runs never share a check, so the manual run is held in check b and the
	// catch-up in check a. The timeouts outlast the sleep.
	hold := func(name string, runs *atomic.Int32, at int32, gate chan struct{}) func(context.Context, string) error {
		return func(ctx context.Context, _ string) error {
			if runs.Add(1) == at {
				held <- name
				select {
				case <-gate:
				case <-ctx.Done():
				}
			}
			return nil
		}
	}
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{
		{Name: "a", Severity: SeverityCritical, Timeout: 24 * time.Hour, Run: hold("a", &aRuns, 3, catchUp)},
		{Name: "b", Severity: SeverityCritical, Timeout: 24 * time.Hour, Run: hold("b", &bRuns, 2, manual)},
	}})
	start(t, e)
	clock.idle(t, 3) // the first slot's two check timeouts, then the schedule's next wait
	done := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		done <- reports
	}()
	if got := <-held; got != "b" {
		t.Fatalf("held %s, want the manual run in b", got)
	}
	clock.idle(t, 2)
	clock.Advance(3*time.Hour + 7*time.Minute)
	if got := <-held; got != "a" {
		t.Fatalf("held %s, want the catch-up in a", got)
	}
	close(manual)
	<-done
	if v := view(t, e, "acme"); !v.Stale || v.Trigger != TriggerManual || v.Missed != 12 {
		t.Fatalf("manual result cached during the catch-up: %+v, want stale with the 12 slots of the sleep", v)
	}
	close(catchUp)
	eventually(t, "the catch-up result is cached", func() bool { return view(t, e, "acme").Trigger == TriggerScheduled })
	if v := view(t, e, "acme"); v.Stale || v.Missed != 0 || v.Health != HealthOK {
		t.Fatalf("after the catch-up: %+v, want fresh, ok and no missed count", v)
	}
}

// #68-K1 D2: a check is alive from its start until it returns, not only after a caller stopped
// waiting. A run that overlaps one still inside the check (a manual run during a scheduled one)
// does not start it again and reports it as a timeout; once it returns, it runs again.
func TestOverlappingRunDoesNotRestartCheck(t *testing.T) {
	block := make(chan struct{})
	var started atomic.Int32
	running := make(chan struct{}, 2)
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Concurrency: 2, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: time.Minute, Run: func(context.Context, string) error {
		if started.Add(1) == 1 {
			running <- struct{}{}
			<-block
		}
		return nil
	}}}})
	first := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		first <- reports
	}()
	clock.idle(t, 1)
	<-running // the first run is inside the check and still waits for it
	reports, err := e.Run(context.Background(), "acme")
	if err != nil || reports[0].Checks[0].Status != StatusTimeout || reports[0].Checks[0].Detail != "not started: the previous run is still running" {
		t.Fatalf("overlapping run: %+v %v", reports, err)
	}
	if n := started.Load(); n != 1 {
		t.Fatalf("the check started %d times while the first run was in it, want 1", n)
	}
	close(block)
	if r := <-first; r[0].Health != HealthOK {
		t.Fatalf("first run %+v, want its own ok result", r)
	}
	reports, err = e.Run(context.Background(), "acme")
	if err != nil || reports[0].Health != HealthOK || started.Load() != 2 {
		t.Fatalf("after the first run: %+v %v, %d starts; want a new ok run", reports, err, started.Load())
	}
}

// #68-K1 D1: a fresh view never carries a missed count, even when slots were missed after
// the cached result started (a measurement that outlived them).
func TestFreshViewShowsNoMissed(t *testing.T) {
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: ok}}})
	if _, err := e.Run(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.missedBase["acme"] = -3 // stands for three slots missed after the cached result started
	e.mu.Unlock()
	start(t, e)
	clock.idle(t, 2)
	if v := view(t, e, "acme"); v.Stale || v.Missed != 0 {
		t.Fatalf("fresh view %+v, want no missed count", v)
	}
}

// #15-K1 partial fleet, #15-K3: customers outside a run, or cut short in it, stay unknown and
// stale; they are never shown green.
func TestPartialFleetRun(t *testing.T) {
	clock := newFakeClock()
	globexStarted := make(chan struct{})
	e := newEngine(t, clock, Options{Customers: []string{"acme", "globex", "initech"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(ctx context.Context, customer string) error {
		if customer == "globex" {
			close(globexStarted)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}}})
	if _, err := e.Run(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		reports []Report
		err     error
	}
	done := make(chan result, 1)
	go func() {
		reports, err := e.Run(ctx, "globex", "initech")
		done <- result{reports, err}
	}()
	<-globexStarted
	eventually(t, "initech measured", func() bool { return view(t, e, "initech").Health == HealthOK })
	cancel()
	res := <-done
	if !errors.Is(res.err, context.Canceled) || len(res.reports) != 1 || res.reports[0].Customer != "initech" {
		t.Fatalf("cut-short run: %v %+v, want initech only and the cancellation", res.err, res.reports)
	}

	want := map[string]string{
		"acme":    "acme ok measured 2026-09-11T09:00:00Z fresh",
		"globex":  "globex unknown never measured stale",
		"initech": "initech ok measured 2026-09-11T09:00:00Z fresh",
	}
	for _, v := range e.Views() {
		if got := v.String(); got != want[v.Customer] {
			t.Fatalf("display of %s\n got %q\nwant %q", v.Customer, got, want[v.Customer])
		}
	}
}

// A run waiting for a fleet slot ends when its context does, even while the slot is held.
func TestWaitingForSlotHonoursContext(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Concurrency: 1, Customers: []string{"acme", "globex"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: time.Hour, Run: hang(block)}}})
	go func() { _, _ = e.Run(context.Background(), "acme") }()
	clock.idle(t, 1) // acme holds the slot
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.Run(ctx, "globex")
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v, want the cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled run kept waiting for the slot")
	}
}

// A cancelled run ends even while a check ignores its context, and nothing is cached.
func TestCancelStopsWaitingForHungCheck(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: time.Hour, Run: hang(block)}}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := e.Run(ctx, "acme")
		done <- err
	}()
	clock.idle(t, 1)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run: %v, want the cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled run kept waiting for a check that ignores its context")
	}
	if v := view(t, e, "acme"); v.Health != HealthUnknown || !v.Stale {
		t.Fatalf("view %+v, want unknown and stale", v)
	}
}

func TestFleetConcurrencyIsBounded(t *testing.T) {
	var inflight, peak atomic.Int32
	started := make(chan struct{}, 16)
	tokens := make(chan struct{})
	clock := newFakeClock()
	customers := []string{"a", "b", "c", "d", "e"}
	e := newEngine(t, clock, Options{Concurrency: 2, Customers: customers, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(context.Context, string) error {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		started <- struct{}{}
		<-tokens
		inflight.Add(-1)
		return nil
	}}}})
	done := make(chan struct{})
	go func() {
		_, _ = e.Run(context.Background(), customers...)
		close(done)
	}()
	<-started
	<-started
	// Two checks hold both slots and wait for a token. A correct bound never starts a third, so
	// this wait cannot fail falsely; without the bound the other three start at once.
	select {
	case <-started:
		t.Fatal("a third customer started while two held both fleet slots")
	case <-time.After(200 * time.Millisecond):
	}
	for range 3 {
		tokens <- struct{}{}
		<-started
	}
	tokens <- struct{}{}
	tokens <- struct{}{}
	<-done
	if got := peak.Load(); got != 2 {
		t.Fatalf("%d customers were measured at once, want at most 2", got)
	}
}

// #15-K3: a result is fresh for one interval plus the grace window, and stale after it or when
// it was measured after now.
func TestStaleness(t *testing.T) {
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme", "globex"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: ok}}})
	if v := view(t, e, "acme"); !v.Stale || v.Health != HealthUnknown || !v.MeasuredAt.IsZero() {
		t.Fatalf("never measured: %+v, want unknown and stale", v)
	}
	if _, err := e.Run(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(16 * time.Minute)
	if v := view(t, e, "acme"); v.Stale {
		t.Fatalf("at interval plus grace: %+v, want fresh", v)
	}
	clock.Advance(time.Nanosecond)
	if got, want := view(t, e, "acme").String(), "acme ok measured 2026-09-11T09:00:00Z stale"; got != want {
		t.Fatalf("past interval plus grace\n got %q\nwant %q", got, want)
	}
	clock.Advance(-time.Hour)
	if v := view(t, e, "acme"); !v.Stale {
		t.Fatalf("clock set back before the measurement: %+v, want stale", v)
	}
}

// A short interval gets half of itself as grace, like the schedule: one minute plus 30s.
func TestStalenessShortInterval(t *testing.T) {
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Interval: time.Minute, Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: ok}}})
	if _, err := e.Run(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	clock.Advance(90 * time.Second)
	if v := view(t, e, "acme"); v.Stale {
		t.Fatalf("at 90s: %+v, want fresh", v)
	}
	clock.Advance(time.Nanosecond)
	if v := view(t, e, "acme"); !v.Stale {
		t.Fatalf("past 90s: %+v, want stale", v)
	}
}

// Overlapping runs of one customer: an older measurement that ends later never replaces a
// newer one.
func TestNewerResultWins(t *testing.T) {
	release := make(chan struct{})
	started := make(chan int, 4)
	c := &calls{}
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Timeout: time.Hour, Run: func(_ context.Context, customer string) error {
		n := c.record(clock, customer)
		started <- n
		if n == 1 {
			<-release
			return errors.New("old failure")
		}
		return nil
	}}}})
	done := make(chan []Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		done <- reports
	}()
	<-started
	clock.Advance(time.Minute)
	if _, err := e.Run(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if old := <-done; len(old) != 1 || !old[0].MeasuredAt.Equal(t0) {
		t.Fatalf("older run %+v", old)
	}
	// By D2 the newer run did not start the check the older one was still in; its result is
	// that refusal, and the older run's later failure does not replace it.
	if v := view(t, e, "acme"); !v.MeasuredAt.Equal(t0.Add(time.Minute)) || v.Checks[0].Detail != "not started: the previous run is still running" {
		t.Fatalf("view %+v, want the newer 09:01 result", v)
	}
}

// security.md §1: check details pass the redactor before they are kept.
func TestDetailIsRedacted(t *testing.T) {
	raw := "login refused, password: example-not-a-secret"
	clock := newFakeClock()
	e := newEngine(t, clock, Options{Customers: []string{"acme"}, Checks: []Check{{Name: "ssh", Severity: SeverityCritical, Run: func(context.Context, string) error {
		return errors.New(raw)
	}}}})
	reports, err := e.Run(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	got := reports[0].Checks[0].Detail
	if strings.Contains(got, "example-not-a-secret") || got != mask.Mask(raw) {
		t.Fatalf("detail %q, want %q", got, mask.Mask(raw))
	}
	if v := view(t, e, "acme"); v.Checks[0].Detail != got {
		t.Fatalf("cached detail %q, want the redacted %q", v.Checks[0].Detail, got)
	}
}
