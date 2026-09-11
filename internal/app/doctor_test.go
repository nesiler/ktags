package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/doctor"
	"github.com/nesiler/ktags/internal/launchd"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/service"
)

func doctorReport(t *testing.T, stdout string) doctor.Report {
	t.Helper()
	var doc struct {
		Kind string        `json:"kind"`
		Data doctor.Report `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.Kind != "doctor" {
		t.Fatalf("doctor document %q: %v", stdout, err)
	}
	return doc.Data
}

// #61-K1: on a fresh KTAGS_HOME whose roots an operator made with default permissions (or not
// at all), doctor is red with one fix per failing check; running exactly those fixes makes it
// green. #61-K3: doctor itself starts nothing and changes nothing.
func TestDoctorFixLoop(t *testing.T) {
	h := newHarness(t)
	h.deps.lookPath = func(string) (string, error) { return "/usr/bin/ssh", nil }
	// The umask decides the mode, as for an operator's mkdir.
	for _, name := range []string{"config", "data"} {
		if err := os.Mkdir(filepath.Join(h.home, name), 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(h.home, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	stdout, stderr := h.want(4, "doctor")
	contains(t, stdout, "fail  roots.config", "fix: chmod 700 ", "fail  service.socket", "fix: ktags service start")
	if stderr != "" {
		t.Fatalf("stderr %q", stderr)
	}
	stdout, _ = h.want(4, "doctor", "--json")
	report := doctorReport(t, stdout)

	statuses := map[string]doctor.Status{}
	var fixes []string
	for _, c := range report.Checks {
		statuses[c.ID] = c.Status
		if c.Status != doctor.StatusOK && !containsString(fixes, c.Fix) {
			fixes = append(fixes, c.Fix)
		}
	}
	for id, want := range map[string]doctor.Status{
		"platform": doctor.StatusOK, "roots.config": doctor.StatusFail, "roots.data": doctor.StatusFail,
		"roots.state": doctor.StatusFail, "roots.runtime": doctor.StatusFail, "service.socket": doctor.StatusFail,
		"service.protocol": doctor.StatusWarn, "inventory": doctor.StatusOK, "tools.ssh": doctor.StatusOK,
	} {
		if statuses[id] != want {
			t.Fatalf("%s is %q, want %q; report %+v", id, statuses[id], want, report)
		}
	}

	// Doctor changed nothing: nothing launched, the missing roots are still missing and the loose
	// modes are unchanged.
	if h.launcher.launches != 0 {
		t.Fatalf("doctor launched the service %d time(s)", h.launcher.launches)
	}
	if _, err := os.Stat(filepath.Join(h.home, "state")); !os.IsNotExist(err) {
		t.Fatalf("doctor created the state root: %v", err)
	}
	if info, err := os.Stat(filepath.Join(h.home, "config")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("doctor changed the config root: %v %v", info.Mode(), err)
	}

	// Run exactly the listed fixes, in order, as the operator would.
	for _, fix := range fixes {
		switch {
		case fix == "ktags service start":
			h.want(0, "service", "start")
		case strings.HasPrefix(fix, "mkdir -p -m 700 '"), strings.HasPrefix(fix, "chmod 700 '"):
			if out, err := exec.Command("/bin/sh", "-c", fix).CombinedOutput(); err != nil {
				t.Fatalf("fix %q: %v\n%s", fix, err, out)
			}
		default:
			t.Fatalf("unexpected fix %q on a fresh home", fix)
		}
	}

	stdout, _ = h.want(0, "doctor", "--json")
	report = doctorReport(t, stdout)
	if report.Status != doctor.StatusOK {
		t.Fatalf("after the fixes doctor is %q: %+v", report.Status, report)
	}
	for _, c := range report.Checks {
		if c.Status != doctor.StatusOK || c.Fix != "" {
			t.Fatalf("after the fixes: %+v", c)
		}
	}
	stdout, _ = h.want(0, "doctor")
	contains(t, stdout, "doctor: all 9 checks ok")

	h.want(0, "service", "stop")
	if err := h.launcher.exited(t); err != nil {
		t.Fatalf("service exit: %v", err)
	}
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// The composition root tells doctor how this platform starts the service: launchd gets the
// agent row, a platform without a launcher gets the foreground fix. Roots that cannot be
// resolved are an error with exit 1, not a report.
func TestDoctorLauncherAndRoots(t *testing.T) {
	h := newHarness(t)
	h.deps.lookPath = func(string) (string, error) { return "/usr/bin/ssh", nil }

	h.deps.launcher = func(roots paths.Roots) service.Launcher { return launchd.New("/usr/bin/true", roots) }
	stdout, _ := h.want(4, "doctor", "--json")
	agent := launchd.New("/usr/bin/true", mustRoots(t, h))
	var found bool
	for _, c := range doctorReport(t, stdout).Checks {
		if c.ID == "service.agent" {
			found = true
			if !strings.Contains(c.Evidence, agent.Definition) || c.Fix != "ktags service start" {
				t.Fatalf("agent row %+v, want the definition %s", c, agent.Definition)
			}
		}
	}
	if !found {
		t.Fatal("a launchd launcher gives no service.agent row")
	}

	h.deps.launcher = func(paths.Roots) service.Launcher { return service.NoLauncher{GOOS: "linux"} }
	stdout, _ = h.want(4, "doctor", "--json")
	for _, c := range doctorReport(t, stdout).Checks {
		if c.ID == "service.agent" {
			t.Fatalf("no launchd, but an agent row: %+v", c)
		}
		if c.ID == "service.socket" && c.Fix != "ktags service run" {
			t.Fatalf("socket fix %q, want the foreground command", c.Fix)
		}
	}

	h.deps.env = paths.Env{Getenv: func(key string) string {
		if key == "KTAGS_HOME" {
			return "relative/home"
		}
		return ""
	}}
	_, stderr := h.want(1, "doctor")
	contains(t, stderr, "KTAGS_HOME: must be an absolute path")
}

// #68-K1 D3: the composition root hands doctor the agent's comparison, so a definition that
// another build wrote is reported as stale with the stop-and-start fix.
func TestDoctorReportsStaleAgent(t *testing.T) {
	h := newHarness(t)
	h.deps.lookPath = func(string) (string, error) { return "/usr/bin/ssh", nil }
	older := launchd.New("/bin/sh", mustRoots(t, h))
	older.Launchctl = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "print" {
			return nil, errors.New("exit status 113")
		}
		return nil, nil
	}
	if err := older.Launch(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.deps.launcher = func(roots paths.Roots) service.Launcher { return launchd.New("/usr/bin/true", roots) }
	stdout, _ := h.want(4, "doctor", "--json")
	for _, c := range doctorReport(t, stdout).Checks {
		if c.ID == "service.agent" {
			if c.Status != "warn" || !strings.Contains(c.Evidence, "stale agent definition: "+older.Definition) || c.Fix != "ktags service stop && ktags service start" {
				t.Fatalf("agent row %+v, want a stale definition", c)
			}
			return
		}
	}
	t.Fatal("no service.agent row")
}

func mustRoots(t *testing.T, h *harness) paths.Roots {
	t.Helper()
	roots, err := paths.Resolve(h.deps.env)
	if err != nil {
		t.Fatal(err)
	}
	return roots
}
