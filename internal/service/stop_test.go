package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func stopRequested(srv *Server) bool {
	select {
	case <-srv.StopRequested():
		return true
	default:
		return false
	}
}

// #12-K1: a stop without --cancel-runs is refused while a run is active; the run and the
// service go on as if nothing happened.
func TestStopRefusedWhileRunActive(t *testing.T) {
	f := newFixture(t, setup{})
	ctx := context.Background()
	info := f.start("fake long", acme, nil)
	if hello, err := f.client.Hello(ctx); err != nil || hello.ActiveRuns != 1 {
		t.Fatalf("hello %+v, %v; want one active run", hello, err)
	}

	_, err := f.client.Stop(ctx, false)
	pe := wantCode(t, err, CodeConflict)
	if !strings.Contains(pe.Message, info.ID) || !strings.Contains(pe.Hint, "--cancel-runs") {
		t.Fatalf("refusal %q / %q, want the run ID and the --cancel-runs hint", pe.Message, pe.Hint)
	}
	if stopRequested(f.srv) {
		t.Fatal("a refused stop still asked the service to stop")
	}
	if st, err := f.client.Status(ctx, info.ID); err != nil || st.Status != "running" {
		t.Fatalf("run after the refused stop: %+v, %v; want running", st, err)
	}

	close(f.long.gate)
	if _, final := f.follow(info.ID, 0, true); final.Status != "succeeded" {
		t.Fatalf("run ended %q, want succeeded", final.Status)
	}
	f.start("fake quick", global, nil) // the service still takes new runs
}

// #12-K1: with cancel_runs the stop cancels the active run, waits for its result, answers with
// the final state and refuses new runs.
func TestStopCancelsRunsWhenAsked(t *testing.T) {
	f := newFixture(t, setup{})
	ctx := context.Background()
	info := f.start("fake long", acme, nil)

	runs, err := f.client.Stop(ctx, true)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != info.ID || runs[0].Status != "cancelled" || runs[0].Result == nil ||
		runs[0].Result.Summary != errStopRun.Error() {
		t.Fatalf("stop reply %+v, want run %s cancelled with %q", runs, info.ID, errStopRun)
	}
	if !stopRequested(f.srv) {
		t.Fatal("the stop did not ask the service to stop")
	}
	_, err = f.client.Start(ctx, "fake quick", global, nil)
	wantCode(t, err, CodeUnavailable)
	_, err = f.client.Stop(ctx, true)
	if pe := wantCode(t, err, CodeUnavailable); !strings.Contains(pe.Message, "already stopping") {
		t.Fatalf("second stop: %q", pe.Message)
	}
}

func TestStopWithoutRuns(t *testing.T) {
	f := newFixture(t, setup{})
	runs, err := f.client.Stop(context.Background(), false)
	if err != nil || len(runs) != 0 {
		t.Fatalf("Stop: %+v, %v; want no runs and no error", runs, err)
	}
	if !stopRequested(f.srv) {
		t.Fatal("the stop did not ask the service to stop")
	}
}

// testClock is a clock that moves only when the test advances it; waits reports every wait.
type testClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []testWaiter
	waits   chan struct{}
}

type testWaiter struct {
	at time.Time
	ch chan time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC), waits: make(chan struct{}, 1024)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.mu.Lock()
	c.waiters = append(c.waiters, testWaiter{at: c.now.Add(d), ch: ch})
	c.mu.Unlock()
	c.waits <- struct{}{}
	return ch
}

func (c *testClock) advance(d time.Duration) {
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

func (c *testClock) idle(t *testing.T) {
	t.Helper()
	select {
	case <-c.waits:
	case <-time.After(10 * time.Second):
		t.Fatal("the schedule did not become idle")
	}
}

// #12-K2 through the service: slots missed during a simulated sleep create no run; one run
// starts at wake and the schedule reports the missed slots.
func TestScheduleCreatesNoRunForMissedSlots(t *testing.T) {
	clock := newTestClock()
	f := newFixture(t, setup{opts: func(o *Options) {
		o.Clock = clock
		o.Schedule = []Scheduled{{Name: "quick", Action: "fake quick", Target: global, Interval: 15 * time.Minute}}
	}})
	clock.idle(t)
	clock.advance(3*time.Hour + 7*time.Minute)
	clock.idle(t)

	runs, err := f.client.Runs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("%d runs after the sleep, want 2 (the first slot and one at wake)", len(runs))
	}
	for _, r := range runs {
		if r.Status != "succeeded" || r.Action != "fake quick" {
			t.Fatalf("run %+v, want a succeeded fake quick run", r)
		}
	}
	states := f.srv.Schedule()
	if len(states) != 1 || states[0].Runs != 2 || states[0].Missed != 12 || states[0].Stale || states[0].LastErr != "" {
		t.Fatalf("schedule %+v, want 2 runs, 12 missed slots, fresh", states)
	}
}

// A scheduled run that fails is recorded as the schedule's error, not as success.
func TestScheduledFailureIsRecorded(t *testing.T) {
	clock := newTestClock()
	f := newFixture(t, setup{opts: func(o *Options) {
		o.Clock = clock
		o.Schedule = []Scheduled{{Name: "fail", Action: "fake fail", Target: global, Interval: time.Hour}}
	}})
	clock.idle(t)
	st := f.srv.Schedule()[0]
	if st.Runs != 1 || !strings.Contains(st.LastErr, "failed") || strings.Contains(st.LastErr, "hunter2") {
		t.Fatalf("schedule %+v, want one failed run with a masked error", st)
	}
}

// A schedule entry the registry would refuse, or a bad interval, stops Listen before the lock.
func TestListenRefusesBadSchedule(t *testing.T) {
	tests := []struct {
		name  string
		entry Scheduled
		want  string
	}{
		{"unknown action", Scheduled{Name: "x", Action: "fake nothing", Target: global, Interval: time.Hour}, "scheduled entry"},
		{"wrong target", Scheduled{Name: "x", Action: "fake quick", Target: acme, Interval: time.Hour}, "scheduled entry"},
		{"no interval", Scheduled{Name: "x", Action: "fake quick", Target: global}, "positive interval"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runtime, opts, _ := bareOptions(t)
			opts.Schedule = []Scheduled{tc.entry}
			_, err := Listen(context.Background(), runtime, opts)
			if pe := wantCode(t, err, CodeInvalid); !strings.Contains(pe.Message, tc.want) {
				t.Fatalf("Listen: %q, want %q", pe.Message, tc.want)
			}
			opts.Schedule = nil
			srv, err := Listen(context.Background(), runtime, opts)
			if err != nil {
				t.Fatalf("Listen after the refusal: %v (was the lock left behind?)", err)
			}
			_ = srv.Close()
		})
	}
}

// Closing the service during a scheduled run does not wait on the schedule forever: the job lets
// go on shutdown, and the run is cancelled with its result recorded.
func TestCloseDuringScheduledRun(t *testing.T) {
	clock := newTestClock()
	runtime, opts, _ := bareOptions(t)
	opts.Clock = clock
	opts.Schedule = []Scheduled{{Name: "long", Action: "fake long", Target: acme, Interval: time.Hour}}
	srv, err := Listen(context.Background(), runtime, opts)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		runs, err := opts.Store.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) == 1 && runs[0].LastEventID >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the scheduled run did not start")
		}
		<-time.After(5 * time.Millisecond)
	}
	closed := make(chan error, 1)
	go func() { closed <- srv.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close hangs on a scheduled run")
	}
	runs, err := opts.Store.List(context.Background())
	if err != nil || len(runs) != 1 || runs[0].Status != "cancelled" {
		t.Fatalf("runs %+v, %v; want the scheduled run cancelled", runs, err)
	}
}
