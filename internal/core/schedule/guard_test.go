package schedule

import (
	"context"
	"strings"
	"testing"
	"time"
)

// waitCall waits until call n of the job has started.
func (r *recorder) waitCall(t *testing.T, n int) {
	t.Helper()
	for {
		select {
		case got := <-r.started:
			if got >= n {
				return
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("call %d did not start", n)
		}
	}
}

func TestNewRefusesBadJobs(t *testing.T) {
	run := func(context.Context) error { return nil }
	tests := []struct {
		name string
		jobs []Job
		opts Options
		want string
	}{
		{"no name", []Job{{Interval: time.Minute, Run: run}}, Options{}, "no name"},
		{"twice", []Job{{Name: "a", Interval: time.Minute, Run: run}, {Name: "a", Interval: time.Hour, Run: run}}, Options{}, "defined twice"},
		{"zero interval", []Job{{Name: "a", Run: run}}, Options{}, "positive interval"},
		{"negative interval", []Job{{Name: "a", Interval: -time.Minute, Run: run}}, Options{}, "positive interval"},
		{"nothing to run", []Job{{Name: "a", Interval: time.Minute}}, Options{}, "nothing to run"},
		{"grace as long as the interval", []Job{{Name: "a", Interval: time.Minute, Run: run}}, Options{Grace: time.Minute}, "shorter than the interval"},
		{"interval too short for any grace", []Job{{Name: "a", Interval: time.Nanosecond, Run: run}}, Options{}, "shorter than the interval"},
		{"negative grace", nil, Options{Grace: -1}, "must not be negative"},
		{"negative wait", nil, Options{MaxWait: -1}, "must not be negative"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(newFakeClock(), tc.jobs, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New: %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := New(nil, nil, Options{}); err == nil {
		t.Fatal("New without a clock succeeded")
	}
}

// The system clock must not carry a monotonic reading: comparisons would then ignore the time
// the laptop slept.
func TestSystemClockHasNoMonotonicReading(t *testing.T) {
	if now := System().Now(); strings.Contains(now.String(), "m=") {
		t.Fatalf("System().Now() = %s, carries a monotonic reading", now)
	}
}

// A job never waits longer than MaxWait at once, so a wall-clock jump is noticed within it even
// when the timer itself paused during sleep.
func TestWaitsAreCapped(t *testing.T) {
	clock := newFakeClock()
	job := newRecorder(clock)
	s, err := New(clock, []Job{{Name: "a", Interval: time.Hour, Run: job.run}}, Options{MaxWait: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	start(t, s)
	if got := clock.idle(t, 1); got[0] != time.Minute {
		t.Fatalf("first wait %s, want the one-minute cap for a one-hour interval", got[0])
	}
	clock.Advance(time.Minute)
	clock.idle(t, 1)
	if calls := job.times(); len(calls) != 1 {
		t.Fatalf("job ran %d times after one minute of a one-hour interval, want 1", len(calls))
	}
}

// A job whose run hangs shows as stale once its interval and grace have passed, and it does not
// hold up another job.
func TestHungJobIsStaleAndDoesNotBlockOthers(t *testing.T) {
	clock := newFakeClock()
	hung := newRecorder(clock, 1)
	other := newRecorder(clock)
	s, err := New(clock, []Job{
		{Name: "hung", Interval: 10 * time.Minute, Run: hung.run},
		{Name: "other", Interval: 10 * time.Minute, Run: other.run},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	start(t, s)
	hung.waitCall(t, 1)
	clock.idle(t, 1) // other is idle after its first run
	for i := 0; i < 12; i++ {
		clock.Advance(time.Minute)
		clock.idle(t, 1)
	}
	if calls := other.times(); len(calls) != 2 {
		t.Fatalf("the other job ran %d times in 12 minutes, want 2", len(calls))
	}
	states := s.States()
	if states[0].Name != "hung" || !states[0].Running || !states[0].Stale || states[0].Runs != 0 {
		t.Fatalf("hung job %+v, want running, stale, no run ended", states[0])
	}
	if states[1].Stale {
		t.Fatalf("other job %+v is stale", states[1])
	}
	close(hung.release)
}

// A run that ends with an error is a real run: it is counted and its error kept.
func TestRunErrorIsRecorded(t *testing.T) {
	clock := newFakeClock()
	s, err := New(clock, []Job{{Name: "a", Interval: time.Hour, Run: func(context.Context) error {
		return errString("ssh: connection refused")
	}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	start(t, s)
	clock.idle(t, 1)
	if st := only(t, s); st.Runs != 1 || st.LastErr != "ssh: connection refused" {
		t.Fatalf("state %+v, want one run with its error", st)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// The default grace window fits short intervals: half of a one-minute interval.
func TestDefaultGraceFitsShortIntervals(t *testing.T) {
	if _, err := New(newFakeClock(), []Job{{Name: "a", Interval: time.Minute, Run: func(context.Context) error { return nil }}}, Options{}); err != nil {
		t.Fatalf("New with a one-minute interval: %v", err)
	}
}

// A run cut short by the scheduler's shutdown is not a result: it is neither counted nor
// recorded as an error.
func TestShutdownRunIsNotCounted(t *testing.T) {
	clock := newFakeClock()
	job := newRecorder(clock, 1)
	s, err := New(clock, []Job{{Name: "a", Interval: time.Hour, Run: job.run}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)
	job.waitCall(t, 1)
	cancel()
	s.Wait()
	if st := only(t, s); st.Runs != 0 || st.LastErr != "" {
		t.Fatalf("state %+v, want no run counted", st)
	}
}
