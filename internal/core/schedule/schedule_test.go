package schedule

import (
	"context"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 11, 9, 0, 0, 0, time.UTC)

// fakeClock moves only when the test advances it. Every After call is reported on waits, so a
// test knows when a job is idle.
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

// recorder is a job that records the clock time of each call. Calls listed in hold block until
// release is closed.
type recorder struct {
	clock   Clock
	mu      sync.Mutex
	calls   []time.Time
	hold    map[int]bool
	release chan struct{}
	started chan int
}

func newRecorder(clock Clock, hold ...int) *recorder {
	r := &recorder{clock: clock, hold: map[int]bool{}, release: make(chan struct{}), started: make(chan int, 64)}
	for _, n := range hold {
		r.hold[n] = true
	}
	return r
}

func (r *recorder) run(ctx context.Context) error {
	r.mu.Lock()
	r.calls = append(r.calls, r.clock.Now())
	n := len(r.calls)
	r.mu.Unlock()
	r.started <- n
	if r.hold[n] {
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (r *recorder) times() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.calls...)
}

func start(t *testing.T, s *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	t.Cleanup(func() {
		cancel()
		s.Wait()
	})
}

func only(t *testing.T, s *Scheduler) State {
	t.Helper()
	states := s.States()
	if len(states) != 1 {
		t.Fatalf("states %+v, want one", states)
	}
	return states[0]
}

func TestDecide(t *testing.T) {
	const i, g = 15 * time.Minute, time.Minute
	next := t0.Add(i)
	tests := []struct {
		name string
		now  time.Time
		want decision
	}{
		{"before the slot", next.Add(-time.Second), decision{next: next}},
		{"on the slot", next, decision{run: true, next: next.Add(i)}},
		{"at the end of the grace window", next.Add(g), decision{run: true, next: next.Add(i)}},
		{"just after the grace window: missed, catch-up now", next.Add(g + time.Second),
			decision{run: true, missed: 1, missedFrom: next, missedTo: next, next: next.Add(g + time.Second + i)}},
		{"sleep ending on a slot: missed, the slot runs on the grid", next.Add(3 * i),
			decision{run: true, missed: 3, missedFrom: next, missedTo: next.Add(2 * i), next: next.Add(4 * i)}},
		{"sleep ending within grace of a slot", next.Add(3*i + g),
			decision{run: true, missed: 3, missedFrom: next, missedTo: next.Add(2 * i), next: next.Add(4 * i)}},
		{"sleep ending between slots: catch-up restarts the grid", next.Add(3*i + 7*time.Minute),
			decision{run: true, missed: 4, missedFrom: next, missedTo: next.Add(3 * i), next: next.Add(4*i + 7*time.Minute)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decide(next, tc.now, i, g); got != tc.want {
				t.Fatalf("decide = %+v\nwant     %+v", got, tc.want)
			}
		})
	}
}

// #12-K2: a sleep of 3h07m marks the slots it covered as missed, runs nothing for them, starts
// one catch-up run at wake and reports the job stale until that run has ended.
func TestSleepMarksSlotsMissedWithoutRunningThem(t *testing.T) {
	clock := newFakeClock()
	job := newRecorder(clock, 2)
	s, err := New(clock, []Job{{Name: "health acme", Interval: 15 * time.Minute, Run: job.run}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	start(t, s)
	clock.idle(t, 1)
	if st := only(t, s); st.Runs != 1 || st.Missed != 0 || st.Stale || !st.Next.Equal(t0.Add(15*time.Minute)) {
		t.Fatalf("after the first slot: %+v", st)
	}

	sleep := 3*time.Hour + 7*time.Minute
	clock.Advance(sleep)
	job.waitCall(t, 2)
	wake := t0.Add(sleep)
	st := only(t, s)
	if !st.Running || !st.Stale || st.Runs != 1 {
		t.Fatalf("during the catch-up run: %+v, want running, stale and still one run", st)
	}
	if st.Missed != 12 || !st.MissedFrom.Equal(t0.Add(15*time.Minute)) || !st.MissedTo.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("missed %d from %s to %s, want 12 slots from 09:15 to 12:00", st.Missed, st.MissedFrom, st.MissedTo)
	}
	close(job.release)
	clock.idle(t, 1)

	st = only(t, s)
	if st.Runs != 2 || st.Running || st.Stale || !st.LastRun.Equal(wake) || !st.Next.Equal(wake.Add(15*time.Minute)) {
		t.Fatalf("after the catch-up run: %+v, want 2 runs, fresh, next at %s", st, wake.Add(15*time.Minute))
	}
	calls := job.times()
	if len(calls) != 2 || !calls[0].Equal(t0) || !calls[1].Equal(wake) {
		t.Fatalf("job ran at %v, want only %s and %s: a missed slot must not run", calls, t0, wake)
	}
}
