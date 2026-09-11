package actions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/core/health"
)

type fakeHealthRunner struct {
	reports []health.Report
	err     error
	asked   []string
}

func (f *fakeHealthRunner) Run(_ context.Context, customers ...string) ([]health.Report, error) {
	f.asked = append(f.asked, customers...)
	return f.reports, f.err
}

func runHealth(t *testing.T, runner *fakeHealthRunner) (Result, []Event, error) {
	ctx := context.Background()
	t.Helper()
	registry, err := NewRegistry(NewHealth(runner))
	if err != nil {
		t.Fatalf("the health action does not register: %v", err)
	}
	var events []Event
	req := Request{Target: Target{Kind: TargetCustomer, Customer: "acme"}}
	res, err := registry.Execute(ctx, "health", req, ProgressFunc(func(e Event) { events = append(events, e) }))
	return res, events, err
}

func report(h health.Health, checks ...health.CheckResult) []health.Report {
	return []health.Report{{Customer: "acme", Health: h, Checks: checks}}
}

var (
	okCheck   = health.CheckResult{Check: "ssh.endpoint", Severity: health.SeverityCritical, Status: health.StatusOK}
	warnCheck = health.CheckResult{Check: "ssh.auth", Severity: health.SeverityWarning, Status: health.StatusFailed, Detail: "auth refused"}
	failCheck = health.CheckResult{Check: "ssh.endpoint", Severity: health.SeverityCritical, Status: health.StatusFailed, Detail: "connection refused"}
)

func TestHealthActionIsReadOnly(t *testing.T) {
	d := NewHealth(&fakeHealthRunner{}).Descriptor()
	if d.ID != "health" || d.Target != TargetCustomer || d.Effect != EffectReadOnly || d.Danger(true) != DangerNone {
		t.Fatalf("descriptor %+v, want a read-only customer action without confirmation", d)
	}
}

func TestHealthActionReportsEveryCheck(t *testing.T) {
	runner := &fakeHealthRunner{reports: report(health.HealthWarn, okCheck, warnCheck)}
	res, events, err := runHealth(t, runner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusSucceeded || res.Summary != "acme health warn: 1 of 2 checks ok" {
		t.Fatalf("result %+v, want succeeded with the warn summary", res)
	}
	want := []string{"measuring acme", "ssh.endpoint ok (critical)", "ssh.auth failed (warning): auth refused"}
	if len(events) != len(want) {
		t.Fatalf("events %+v, want %v", events, want)
	}
	for i, w := range want {
		if events[i].Message != w {
			t.Fatalf("event %d is %q, want %q", i, events[i].Message, w)
		}
	}
	if len(runner.asked) != 1 || runner.asked[0] != "acme" {
		t.Fatalf("measured %v, want only acme", runner.asked)
	}
}

// A red health result is never a successful run.
func TestHealthActionFailsOnRedHealth(t *testing.T) {
	res, _, err := runHealth(t, &fakeHealthRunner{reports: report(health.HealthFail, failCheck)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusFailed || res.Summary != "acme health fail: 0 of 1 checks ok" {
		t.Fatalf("result %+v, want failed", res)
	}
}

// A customer the engine does not know is refused with the restart that picks it up.
func TestHealthActionRefusalNamesTheRestart(t *testing.T) {
	_, _, err := runHealth(t, &fakeHealthRunner{err: errors.New(`health: customer "acme" is not in the fleet`)})
	if err == nil || !strings.Contains(err.Error(), `"acme" is not in the fleet`) || !strings.Contains(err.Error(), "next: ktags service stop, then ktags service start") {
		t.Fatalf("error %v, want the refusal and the restart", err)
	}
}

// A run cut short returns the engine's error as it is, so the service records a cancellation.
func TestHealthActionCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	registry, err := NewRegistry(NewHealth(&fakeHealthRunner{err: context.Canceled}))
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Target: Target{Kind: TargetCustomer, Customer: "acme"}}
	h, _ := registry.Lookup("health")
	cancel()
	_, err = h.Run(ctx, req, ProgressFunc(func(Event) {}))
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "next:") {
		t.Fatalf("error %v, want the bare cancellation", err)
	}
}

func TestHealthActionRefusesAWrongReportCount(t *testing.T) {
	_, _, err := runHealth(t, &fakeHealthRunner{})
	if err == nil || !strings.Contains(err.Error(), "got 0 reports for one customer") {
		t.Fatalf("error %v, want the report count refusal", err)
	}
}
