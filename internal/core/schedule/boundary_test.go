package schedule

import (
	"testing"
	"time"
)

// Stale means older than one interval plus the grace window: exactly that old is still fresh,
// one nanosecond later it is stale.
func TestStaleBoundary(t *testing.T) {
	clock := newFakeClock()
	job := newRecorder(clock, 1)
	s, err := New(clock, []Job{{Name: "health acme", Interval: 10 * time.Minute, Run: job.run}}, Options{Grace: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	start(t, s)
	job.waitCall(t, 1) // the first run hangs, so staleness counts from the first slot

	clock.Advance(11 * time.Minute)
	if st := only(t, s); st.Stale {
		t.Fatalf("exactly interval+grace after the first slot: %+v, want fresh", st)
	}
	clock.Advance(time.Nanosecond)
	if st := only(t, s); !st.Stale {
		t.Fatalf("one nanosecond past interval+grace: %+v, want stale", st)
	}
	close(job.release)
}
