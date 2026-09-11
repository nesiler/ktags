package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/service"
)

// testEnv is a laptop where every check is green except the roots, which do not exist yet.
// The home holds a space and a quote, so a fix that is not quoted breaks when it is run.
func testEnv(t *testing.T) Env {
	t.Helper()
	home := filepath.Join(t.TempDir(), "it's a home")
	roots, err := paths.Resolve(paths.Env{Getenv: func(key string) string {
		if key == "KTAGS_HOME" {
			return home
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	return Env{
		Roots:  roots,
		GOOS:   "darwin",
		GOARCH: "arm64",
		UID:    os.Getuid(),
		LookPath: func(string) (string, error) {
			return "/usr/bin/ssh", nil
		},
		ServiceStatus: func(context.Context) (service.Status, error) {
			return service.Status{Running: true, Socket: "/k/runtime/service.sock", Protocol: service.ProtocolVersion, PID: 7, Version: "test"}, nil
		},
	}
}

// runCheck runs one registered check through the runner, as doctor prints it.
func runCheck(t *testing.T, id string, env Env) []Result {
	t.Helper()
	for _, c := range Checks() {
		if c.ID == id {
			return run(context.Background(), env, []Check{c}).Checks
		}
	}
	t.Fatalf("no check %q is registered", id)
	return nil
}

func only(t *testing.T, results []Result) Result {
	t.Helper()
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1: %+v", len(results), results)
	}
	return results[0]
}

// want asserts the status and the contract of a row: one line, and exactly one fix when it is
// not ok. fix, when set, must be the fix.
func want(t *testing.T, r Result, status Status, fix string) {
	t.Helper()
	if r.Status != status {
		t.Fatalf("%s: status %q, want %q (evidence %q, fix %q)", r.ID, r.Status, status, r.Evidence, r.Fix)
	}
	switch {
	case status == StatusOK && r.Fix != "":
		t.Fatalf("%s: an ok row has the fix %q", r.ID, r.Fix)
	case status != StatusOK && r.Fix == "":
		t.Fatalf("%s: a %s row has no fix", r.ID, status)
	case fix != "" && r.Fix != fix:
		t.Fatalf("%s: fix %q, want %q", r.ID, r.Fix, fix)
	case r.ID == "" || r.Title == "" || r.Evidence == "":
		t.Fatalf("row without id, title or evidence: %+v", r)
	case strings.ContainsAny(r.Evidence+r.Fix, "\r\n"):
		t.Fatalf("%s: row spans lines: %+v", r.ID, r)
	}
}

// sh runs a fix the way the operator pastes it.
func sh(t *testing.T, command string, env ...string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fix %q failed: %v\n%s", command, err, out)
	}
}

// #61-K3: the Phase 0 checks are registered by their own files, in report order.
func TestRegistryHoldsPhase0Checks(t *testing.T) {
	var ids []string
	for _, c := range Checks() {
		ids = append(ids, c.ID)
	}
	want := []string{"platform", "roots.config", "roots.data", "roots.state", "roots.runtime", "service.agent", "service.socket", "service.protocol", "inventory", "tools.ssh"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("checks %v, want %v", ids, want)
	}
}

// #61-K3: a check is added by registering it; nothing else changes.
func TestAddedCheckReachesTheReport(t *testing.T) {
	extra := Check{ID: "extra.thing", Title: "an extra thing", Order: 45, Run: func(context.Context, Env) []Result {
		return []Result{{Status: StatusWarn, Evidence: "measured", Fix: "fix it"}}
	}}
	report := run(context.Background(), testEnv(t), add(registry, extra))
	var ids []string
	for _, r := range report.Checks {
		ids = append(ids, r.ID)
	}
	if got := strings.Join(ids, " "); !strings.Contains(got, "inventory extra.thing tools.ssh") {
		t.Fatalf("the extra check is not in its place: %s", got)
	}
}

func TestAddRefusesBrokenChecks(t *testing.T) {
	ok := func(context.Context, Env) []Result { return nil }
	tests := []struct {
		name  string
		check Check
		want  string
	}{
		{"empty id", Check{Title: "t", Run: ok}, "is not lower-case"},
		{"upper case id", Check{ID: "Roots", Title: "t", Run: ok}, "is not lower-case"},
		{"empty segment", Check{ID: "roots..config", Title: "t", Run: ok}, "is not lower-case"},
		{"no title", Check{ID: "x", Title: " ", Run: ok}, "has no title"},
		{"no run", Check{ID: "x", Title: "t"}, "has no Run"},
		{"duplicate", Check{ID: "platform", Title: "t", Run: ok}, "registered more than once"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				got := fmt.Sprint(recover())
				if !strings.Contains(got, tc.want) {
					t.Fatalf("panic %q, want %q", got, tc.want)
				}
			}()
			add(Checks(), tc.check)
		})
	}
}

// The runner fills a row from its check, masks it, keeps it on one line and drops the fix of
// an ok row.
func TestRunnerFinishesRows(t *testing.T) {
	checks := []Check{
		{ID: "a", Title: "check a", Order: 1, Run: func(context.Context, Env) []Result {
			return []Result{{Status: StatusFail, Evidence: "first\nsecond password=hunter2hunter2", Fix: "run\r\nthis"}}
		}},
		{ID: "b", Title: "check b", Order: 2, Run: func(context.Context, Env) []Result {
			return []Result{{ID: "b.one", Title: "own title", Status: StatusOK, Evidence: "fine", Fix: "not shown"}}
		}},
	}
	got := run(context.Background(), Env{}, checks)
	wantRows := []Result{
		{ID: "a", Title: "check a", Status: StatusFail, Evidence: "first second password=" + mask.Placeholder, Fix: "run this"},
		{ID: "b.one", Title: "own title", Status: StatusOK, Evidence: "fine"},
	}
	if got.Status != StatusFail || !reflect.DeepEqual(got.Checks, wantRows) {
		t.Fatalf("report %+v\nwant rows %+v", got, wantRows)
	}
}

func TestReportStatusIsTheWorst(t *testing.T) {
	row := func(s Status) Check {
		return Check{ID: "c" + string(s), Title: "t", Run: func(context.Context, Env) []Result {
			return []Result{{Status: s, Evidence: "e", Fix: "f"}}
		}}
	}
	tests := []struct {
		checks []Check
		want   Status
	}{
		{nil, StatusOK},
		{[]Check{row(StatusOK)}, StatusOK},
		{[]Check{row(StatusOK), row(StatusWarn)}, StatusWarn},
		{[]Check{row(StatusFail), row(StatusWarn), row(StatusOK)}, StatusFail},
	}
	for _, tc := range tests {
		if got := run(context.Background(), Env{}, tc.checks); got.Status != tc.want {
			t.Fatalf("%d checks: status %q, want %q", len(tc.checks), got.Status, tc.want)
		}
	}
	if got := run(context.Background(), Env{}, nil); got.Checks == nil {
		t.Fatal("no checks encode as null, want []")
	}
}

// A silent service would cost its timeout per service row; the rows share one answer.
func TestServiceStatusIsAskedOnce(t *testing.T) {
	env := testEnv(t)
	calls := 0
	env.ServiceStatus = func(context.Context) (service.Status, error) {
		calls++
		return service.Status{Socket: "/s"}, nil
	}
	Run(context.Background(), env)
	if calls != 1 {
		t.Fatalf("the service status was asked %d times, want 1", calls)
	}
}

// #61-K3: doctor changes nothing, even where every root is missing.
func TestRunChangesNothing(t *testing.T) {
	env := testEnv(t)
	report := Run(context.Background(), env)
	if report.Status != StatusFail {
		t.Fatalf("status %q with missing roots", report.Status)
	}
	if _, err := os.Stat(filepath.Dir(env.Roots.Config.Path)); !os.IsNotExist(err) {
		t.Fatalf("doctor created the ktags home: %v", err)
	}
}
