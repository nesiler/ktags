package fleet

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/core/health"
)

var now = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// measured is a customer measured ago with health h; stale and connection as given.
func measured(customer string, h health.Health, ago time.Duration, stale bool, conn Connection) Entry {
	return Entry{Customer: customer, Measurement: Measurement{
		Health:     health.View{Customer: customer, Health: h, MeasuredAt: now.Add(-ago), Stale: stale, Interval: time.Hour},
		Connection: ConnectionState{State: conn, MeasuredAt: now.Add(-ago)},
	}}
}

// fixture15 is the 15-customer fleet of #16-K1: green, warn, fail, stale, never-run, unreachable
// and one refused record. Names are chosen so that alphabetical order is not the answer.
func fixture15() []Entry {
	never := func(customer string) Entry { return Entry{Customer: customer, Measurement: Unmeasured(customer)} }
	return []Entry{
		measured("alpha", health.HealthOK, time.Minute, false, ConnReachable),
		measured("bravo", health.HealthOK, 2*time.Minute, false, ConnReachable),
		measured("charlie", health.HealthWarn, time.Minute, false, ConnReachable),
		measured("delta", health.HealthFail, time.Minute, false, ConnReachable),
		measured("echo", health.HealthOK, 5*time.Hour, true, ConnReachable),
		never("foxtrot"),
		measured("golf", health.HealthWarn, time.Minute, false, ConnUnreachable),
		measured("hotel", health.HealthOK, time.Minute, false, ConnUnknown),
		measured("india", health.HealthWarn, 5*time.Hour, true, ConnReachable),
		// A stale failure is still a failure.
		measured("juliet", health.HealthFail, 5*time.Hour, true, ConnReachable),
		never("kilo"),
		{Customer: "lima", Measurement: Measurement{Health: health.View{Customer: "lima", Health: health.HealthUnknown, Stale: true}, Connection: ConnectionState{State: ConnUnreachable, MeasuredAt: now}}},
		{Customer: "mike", Invalid: true, Measurement: Unmeasured("mike")},
		measured("november", health.HealthWarn, time.Minute, false, ConnReachable),
		measured("oscar", health.HealthOK, time.Minute, false, ConnReachable),
	}
}

// #16-K1: the order depends only on the entries: every permutation sorts the same way.
func TestSortIsDeterministic(t *testing.T) {
	want := []string{
		"mike",            // invalid
		"delta", "juliet", // fail
		"golf", "lima", // unreachable
		"echo", "india", // stale
		"foxtrot", "kilo", // never
		"charlie", "november", // warn
		"alpha", "bravo", "hotel", "oscar", // ok
	}
	wantStates := []State{StateInvalid, StateFail, StateFail, StateUnreachable, StateUnreachable, StateStale, StateStale,
		StateNever, StateNever, StateWarn, StateWarn, StateOK, StateOK, StateOK, StateOK}
	entries := fixture15()
	if len(entries) != 15 {
		t.Fatalf("fixture has %d customers, want 15", len(entries))
	}
	rng := rand.New(rand.NewPCG(16, 30))
	for i := 0; i < 50; i++ {
		shuffled := slices.Clone(entries)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })
		if i == 1 {
			slices.Reverse(shuffled)
		}
		Sort(shuffled)
		var got []string
		var states []State
		for _, e := range shuffled {
			got = append(got, e.Customer)
			states = append(states, StateOf(e))
		}
		if !slices.Equal(got, want) || !slices.Equal(states, wantStates) {
			t.Fatalf("permutation %d sorted to\n%v %v\nwant\n%v %v", i, got, states, want, wantStates)
		}
	}
}

// Absence, age and unknown values are never ok; each rule wins over the ones below it.
func TestStateOf(t *testing.T) {
	tests := []struct {
		name  string
		entry Entry
		want  State
	}{
		{"fresh ok", measured("a", health.HealthOK, 0, false, ConnReachable), StateOK},
		{"fresh warn", measured("a", health.HealthWarn, 0, false, ConnReachable), StateWarn},
		{"stale ok is stale", measured("a", health.HealthOK, 0, true, ConnReachable), StateStale},
		{"stale warn is stale", measured("a", health.HealthWarn, 0, true, ConnReachable), StateStale},
		{"stale fail is fail", measured("a", health.HealthFail, 0, true, ConnReachable), StateFail},
		{"fail outranks unreachable", measured("a", health.HealthFail, 0, false, ConnUnreachable), StateFail},
		{"unreachable outranks ok", measured("a", health.HealthOK, 0, false, ConnUnreachable), StateUnreachable},
		{"unreachable outranks stale", measured("a", health.HealthOK, 0, true, ConnUnreachable), StateUnreachable},
		{"never measured", Entry{Customer: "a", Measurement: Unmeasured("a")}, StateNever},
		{"never measured but not flagged stale", Entry{Customer: "a", Measurement: Measurement{Health: health.View{Customer: "a", Health: health.HealthOK}}}, StateNever},
		{"measured with unknown health", measured("a", health.HealthUnknown, 0, false, ConnReachable), StateFail},
		{"measured with a health value this build does not know", measured("a", health.Health("purple"), 0, false, ConnReachable), StateFail},
		{"invalid record outranks everything", Entry{Customer: "a", Invalid: true, Measurement: measured("a", health.HealthFail, 0, false, ConnUnreachable).Measurement}, StateInvalid},
		{"invalid record that is ok", Entry{Customer: "a", Invalid: true, Measurement: measured("a", health.HealthOK, 0, false, ConnReachable).Measurement}, StateInvalid},
	}
	for _, tc := range tests {
		if got := StateOf(tc.entry); got != tc.want {
			t.Errorf("%s: state %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestUnmeasured(t *testing.T) {
	m := Unmeasured("acme")
	if m.Health.Customer != "acme" || m.Health.Health != health.HealthUnknown || !m.Health.Stale || !m.Health.MeasuredAt.IsZero() || m.Connection.State != ConnUnknown {
		t.Fatalf("Unmeasured = %+v", m)
	}
}
