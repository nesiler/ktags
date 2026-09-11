package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/launchd"
	"github.com/nesiler/ktags/internal/service"
)

// installAgent writes the launchd definition exactly as `ktags service start` does for a job
// launchd does not have loaded.
func installAgent(t *testing.T, env *Env, program string) launchd.Agent {
	t.Helper()
	agent := launchd.New(program, env.Roots)
	agent.Launchctl = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "print" {
			return nil, errors.New("exit status 113")
		}
		return nil, nil
	}
	if err := agent.Launch(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.AgentDefinition = agent.Definition
	env.AgentStale = agent.Stale
	return agent
}

func executable(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "k tags")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAgent(t *testing.T) {
	t.Run("no launchd on this platform: no row", func(t *testing.T) {
		if got := runCheck(t, "service.agent", testEnv(t)); len(got) != 0 {
			t.Fatalf("rows %+v", got)
		}
	})
	t.Run("ok", func(t *testing.T) {
		env := testEnv(t)
		program := executable(t, 0o755)
		installAgent(t, &env, program)
		r := only(t, runCheck(t, "service.agent", env))
		want(t, r, StatusOK, "")
		if r.Evidence != "the launchd agent starts "+program {
			t.Fatalf("evidence %q", r.Evidence)
		}
	})
	t.Run("not installed", func(t *testing.T) {
		env := testEnv(t)
		env.AgentDefinition = filepath.Join(env.Roots.State.Path, "launchd", "x.plist")
		r := only(t, runCheck(t, "service.agent", env))
		want(t, r, StatusFail, "ktags service start")
		if !strings.Contains(r.Evidence, "no launchd agent is installed at "+env.AgentDefinition) {
			t.Fatalf("evidence %q", r.Evidence)
		}
	})
	t.Run("unreadable definition", func(t *testing.T) {
		env := testEnv(t)
		env.AgentDefinition = filepath.Join(t.TempDir(), "x.plist")
		if err := os.WriteFile(env.AgentDefinition, []byte("<plist><dict>"), 0o600); err != nil {
			t.Fatal(err)
		}
		r := only(t, runCheck(t, "service.agent", env))
		want(t, r, StatusFail, restartFix)
		if !strings.Contains(r.Evidence, "cannot read the launchd agent") {
			t.Fatalf("evidence %q", r.Evidence)
		}
	})
	t.Run("binary gone", func(t *testing.T) {
		env := testEnv(t)
		program := executable(t, 0o755)
		installAgent(t, &env, program)
		if err := os.Remove(program); err != nil {
			t.Fatal(err)
		}
		r := only(t, runCheck(t, "service.agent", env))
		want(t, r, StatusFail, restartFix)
		if !strings.Contains(r.Evidence, "points at "+program+", which cannot be found") {
			t.Fatalf("evidence %q", r.Evidence)
		}
	})
	// #68-K1 D3: a definition another build wrote is reported, with the stop-and-start fix.
	t.Run("stale definition", func(t *testing.T) {
		env := testEnv(t)
		agent := installAgent(t, &env, executable(t, 0o755))
		newer := agent
		newer.Program = executable(t, 0o755)
		env.AgentStale = newer.Stale
		r := only(t, runCheck(t, "service.agent", env))
		want(t, r, StatusWarn, "ktags service stop && ktags service start")
		if !strings.Contains(r.Evidence, "stale agent definition: "+agent.Definition) {
			t.Fatalf("evidence %q", r.Evidence)
		}
	})
	t.Run("no comparison available", func(t *testing.T) {
		env := testEnv(t)
		installAgent(t, &env, executable(t, 0o755))
		env.AgentStale = nil
		want(t, only(t, runCheck(t, "service.agent", env)), StatusOK, "")
	})
	t.Run("definition cannot be compared", func(t *testing.T) {
		env := testEnv(t)
		installAgent(t, &env, executable(t, 0o755))
		env.AgentStale = func() (bool, error) { return false, errors.New("permission denied") }
		r := only(t, runCheck(t, "service.agent", env))
		want(t, r, StatusWarn, restartFix)
		if !strings.Contains(r.Evidence, "cannot compare the launchd agent") || !strings.Contains(r.Evidence, "permission denied") {
			t.Fatalf("evidence %q", r.Evidence)
		}
	})
	for name, program := range map[string]func(t *testing.T) string{
		"binary not executable": func(t *testing.T) string { return executable(t, 0o644) },
		"binary is a directory": func(t *testing.T) string { return t.TempDir() },
	} {
		t.Run(name, func(t *testing.T) {
			env := testEnv(t)
			installAgent(t, &env, program(t))
			r := only(t, runCheck(t, "service.agent", env))
			want(t, r, StatusFail, restartFix)
			if !strings.Contains(r.Evidence, "which is not an executable file") {
				t.Fatalf("evidence %q", r.Evidence)
			}
		})
	}
}

var (
	mismatchErr = &service.Error{Code: service.CodeProtocolMismatch, Message: "the service speaks ktags protocol 2, this side speaks 1", Hint: "this ktags is older than the running service: upgrade ktags to the build that started the service"}
	silentErr   = &service.Error{Code: service.CodeUnavailable, Message: "the ktags service on /s did not answer within 10s", Hint: "end the silent service with SIGTERM to the PID in /k/service.lock"}
)

func withStatus(t *testing.T, st service.Status, err error) Env {
	env := testEnv(t)
	env.ServiceStatus = func(context.Context) (service.Status, error) { return st, err }
	return env
}

func TestSocket(t *testing.T) {
	stopped := service.Status{Socket: "/k/runtime/service.sock"}
	tests := []struct {
		name       string
		st         service.Status
		err        error
		foreground bool
		status     Status
		fix        string
		evidence   string
	}{
		{"running", service.Status{Running: true, Socket: "/s", PID: 7, Version: "v1"}, nil, false, StatusOK, "", "ktags service v1 (pid 7) answers on /s"},
		{"not running", stopped, nil, false, StatusFail, "ktags service start", "no ktags service answers on /k/runtime/service.sock"},
		{"not running, no launchd", stopped, nil, true, StatusFail, "ktags service run", "no ktags service answers"},
		{"another protocol answers", stopped, mismatchErr, false, StatusOK, "", "a ktags service answers on /k/runtime/service.sock"},
		{"silent service", stopped, silentErr, false, StatusFail, silentErr.Hint, silentErr.Message},
		{"error without a hint", stopped, errors.New("boom\n  next: x"), false, StatusFail, "ktags service start", "boom"},
		{"socket path over the limit", longSocket(104), nil, false, StatusFail, "set KTAGS_RUNTIME_DIR to a shorter absolute path", "is 104 bytes; the limit is 103"},
		{"socket path at the limit", longSocket(103), nil, false, StatusFail, "ktags service start", "no ktags service answers"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := withStatus(t, tc.st, tc.err)
			env.Foreground = tc.foreground
			r := only(t, runCheck(t, "service.socket", env))
			want(t, r, tc.status, tc.fix)
			if !strings.Contains(r.Evidence, tc.evidence) || strings.Contains(r.Evidence, "next:") {
				t.Fatalf("evidence %q, want %q", r.Evidence, tc.evidence)
			}
		})
	}
}

func TestProtocol(t *testing.T) {
	tests := []struct {
		name     string
		st       service.Status
		err      error
		status   Status
		fix      string
		evidence string
	}{
		{"match", service.Status{Running: true, Protocol: 1}, nil, StatusOK, "", "protocol 1 on both sides"},
		{"mismatch", service.Status{}, mismatchErr, StatusFail, mismatchErr.Hint, mismatchErr.Message},
		{"not running", service.Status{}, nil, StatusWarn, "ktags service start", "not measured: no ktags service answers"},
		{"silent service", service.Status{}, silentErr, StatusWarn, silentErr.Hint, "not measured"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := only(t, runCheck(t, "service.protocol", withStatus(t, tc.st, tc.err)))
			want(t, r, tc.status, tc.fix)
			if !strings.Contains(r.Evidence, tc.evidence) {
				t.Fatalf("evidence %q, want %q", r.Evidence, tc.evidence)
			}
		})
	}
}
