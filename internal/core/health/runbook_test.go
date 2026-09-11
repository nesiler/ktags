package health

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// runbookErr is a check error that names its runbook.
type runbookErr struct{ runbook string }

func (e runbookErr) Error() string   { return "measured fact" }
func (e runbookErr) Runbook() string { return e.runbook }

func runOnce(t *testing.T, checks ...Check) Report {
	t.Helper()
	e, err := New(Options{Clock: newFakeClock(), Redact: func(s string) string { return s }, Checks: checks, Customers: []string{"acme"}, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	reports, err := e.Run(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	return reports[0]
}

func fails(err error) func(context.Context, string) error {
	return func(context.Context, string) error { return err }
}

// #65 D3: not configured is a warning whatever the severity, and never ok.
func TestNotConfiguredWeighsAsAWarning(t *testing.T) {
	r := runOnce(t,
		Check{Name: "ssh.endpoint", Severity: SeverityCritical, Run: fails(fmt.Errorf("%w: jump access", ErrNotConfigured))},
		Check{Name: "kube.auth", Severity: SeverityWarning, Run: fails(ErrNotConfigured)},
	)
	if r.Health != HealthWarn {
		t.Fatalf("health %s, want warn", r.Health)
	}
	for _, c := range r.Checks {
		if c.Status != StatusNotConfigured {
			t.Fatalf("%s status %s, want not configured", c.Check, c.Status)
		}
	}
	// A real critical failure still fails next to it.
	r = runOnce(t,
		Check{Name: "kube.auth", Severity: SeverityWarning, Run: fails(ErrNotConfigured)},
		Check{Name: "ssh.endpoint", Severity: SeverityCritical, Run: fails(errors.New("refused"))},
	)
	if r.Health != HealthFail {
		t.Fatalf("health %s, want fail", r.Health)
	}
}

// #65 D4a: the runbook comes from the error, else from the check; an ok result has none.
func TestCheckRunbook(t *testing.T) {
	r := runOnce(t,
		Check{Name: "named", Severity: SeverityCritical, Runbook: "fallback", Run: fails(runbookErr{"ssh-unreachable"})},
		Check{Name: "plain", Severity: SeverityCritical, Runbook: "fallback", Run: fails(errors.New("refused"))},
		Check{Name: "empty", Severity: SeverityCritical, Runbook: "fallback", Run: fails(runbookErr{""})},
		Check{Name: "passes", Severity: SeverityCritical, Runbook: "fallback", Run: fails(nil)},
	)
	want := map[string]string{"named": "ssh-unreachable", "plain": "fallback", "empty": "fallback", "passes": ""}
	for _, c := range r.Checks {
		if c.Runbook != want[c.Check] {
			t.Fatalf("%s runbook %q, want %q", c.Check, c.Runbook, want[c.Check])
		}
	}
}

// A check that times out carries the check's runbook.
func TestTimeoutRunbook(t *testing.T) {
	clock := newFakeClock()
	e, err := New(Options{Clock: clock, Redact: func(s string) string { return s }, Customers: []string{"acme"}, Interval: time.Hour, Checks: []Check{{
		Name: "ssh.endpoint", Severity: SeverityCritical, Timeout: time.Second, Runbook: "ssh-unreachable",
		Run: func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() },
	}}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan Report, 1)
	go func() {
		reports, _ := e.Run(context.Background(), "acme")
		done <- reports[0]
	}()
	<-clock.waits
	clock.Advance(2 * time.Second)
	r := <-done
	if c := r.Checks[0]; c.Status != StatusTimeout || c.Runbook != "ssh-unreachable" {
		t.Fatalf("check %+v, want a timeout with the check's runbook", c)
	}
}
