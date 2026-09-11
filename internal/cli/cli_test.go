package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/service"
)

type fakeRuntime struct {
	control *fakeControl
	err     error
}

func (fakeRuntime) Version() domain.Build { return domain.Build{Name: "ktags", Version: "test"} }

func (r fakeRuntime) Service() (ServiceControl, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.control, nil
}

type fakeControl struct {
	status     service.Status
	started    bool
	stop       service.StopResult
	err        error
	cancelRuns bool
	calls      []string
}

func (c *fakeControl) Start(context.Context) (service.Status, bool, error) {
	c.calls = append(c.calls, "start")
	return c.status, c.started, c.err
}

func (c *fakeControl) Status(context.Context) (service.Status, error) {
	c.calls = append(c.calls, "status")
	return c.status, c.err
}

func (c *fakeControl) Stop(_ context.Context, cancelRuns bool) (service.StopResult, error) {
	c.calls = append(c.calls, "stop")
	c.cancelRuns = cancelRuns
	return c.stop, c.err
}

func (c *fakeControl) Run(_ context.Context, w io.Writer) error {
	c.calls = append(c.calls, "run")
	_, _ = io.WriteString(w, "serving\n")
	return c.err
}

var running = service.Status{Running: true, Socket: "/tmp/kt/runtime/service.sock", Protocol: 1, PID: 4242, Version: "test", ActiveRuns: 2}

func runCLI(rt Runtime, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(rt, args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestServiceCommands(t *testing.T) {
	conflict := &service.Error{Code: service.CodeConflict, Message: "the ktags service has 1 active run(s): 20260911T090000Z-0000abcd", Hint: "protocol hint"}
	broken := &service.Error{Code: service.CodeUnavailable, Message: "launchctl bootstrap failed", Hint: "inspect the job"}
	tests := []struct {
		name    string
		args    []string
		control fakeControl
		code    int
		stdout  []string
		stderr  []string
		cancel  bool
	}{
		{"start launches", []string{"service", "start"}, fakeControl{status: running, started: true}, 0,
			[]string{"ktags service started (pid 4242)", "protocol  1", "socket    /tmp/kt/runtime/service.sock", "runs      2 active"}, nil, false},
		{"start finds it running", []string{"service", "start"}, fakeControl{status: running}, 0,
			[]string{"ktags service is already running (pid 4242)"}, nil, false},
		{"start fails", []string{"service", "start"}, fakeControl{err: broken}, 1,
			nil, []string{"ktags: launchctl bootstrap failed", "next: inspect the job"}, false},
		{"status running", []string{"service", "status"}, fakeControl{status: running}, 0,
			[]string{"ktags service is running (pid 4242)", "version   test"}, nil, false},
		{"status fails", []string{"service", "status"}, fakeControl{err: broken}, 1,
			nil, []string{"ktags: launchctl bootstrap failed"}, false},
		{"status not running", []string{"service", "status"}, fakeControl{status: service.Status{Socket: "/s.sock"}}, 1,
			[]string{"ktags service is not running", "socket    /s.sock", "next: ktags service start"}, nil, false},
		{"stop refused", []string{"service", "stop"}, fakeControl{err: conflict}, 2,
			nil, []string{"refusing to stop the service", "20260911T090000Z-0000abcd", "next: wait for the runs to end, or run: ktags service stop --cancel-runs"}, false},
		{"stop cancels", []string{"service", "stop", "--cancel-runs"},
			fakeControl{stop: service.StopResult{WasRunning: true, Cancelled: []service.RunInfo{{ID: "r1", Action: "health", Status: "cancelled"}}}}, 0,
			[]string{"ktags service stopped", "cancelled run r1 (health), status cancelled"}, nil, true},
		{"stop not running", []string{"service", "stop"}, fakeControl{}, 0, []string{"ktags service is not running"}, nil, false},
		{"stop fails", []string{"service", "stop"}, fakeControl{err: broken}, 1, nil, []string{"ktags: launchctl bootstrap failed"}, false},
		{"run", []string{"service", "run"}, fakeControl{}, 0, []string{"serving"}, nil, false},
		{"run fails", []string{"service", "run"}, fakeControl{err: broken}, 1, nil, []string{"ktags: launchctl bootstrap failed"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			control := tc.control
			code, stdout, stderr := runCLI(fakeRuntime{control: &control}, tc.args...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout %q\nstderr %q", code, tc.code, stdout, stderr)
			}
			for _, want := range tc.stdout {
				if !strings.Contains(stdout, want) {
					t.Fatalf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			for _, want := range tc.stderr {
				if !strings.Contains(stderr, want) {
					t.Fatalf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if control.cancelRuns != tc.cancel {
				t.Fatalf("cancelRuns %v, want %v", control.cancelRuns, tc.cancel)
			}
		})
	}
}

func TestServiceUsage(t *testing.T) {
	for _, args := range [][]string{
		{"service"}, {"service", "restart"}, {"service", "stop", "--force"}, {"service", "start", "--cancel-runs"},
		{"service", "stop", "--cancel-runs", "extra"},
	} {
		control := &fakeControl{}
		code, _, stderr := runCLI(fakeRuntime{control: control}, args...)
		if code != 1 || !strings.Contains(stderr, "usage: ktags service") || len(control.calls) != 0 {
			t.Fatalf("%q: exit %d, calls %v, stderr %q; want usage with exit 1 and no call", args, code, control.calls, stderr)
		}
	}
	for _, help := range []string{"help", "--help", "-h"} {
		code, stdout, _ := runCLI(fakeRuntime{control: &fakeControl{}}, "service", help)
		if code != 0 || !strings.Contains(stdout, "usage: ktags service") {
			t.Fatalf("service %s: exit %d, stdout %q", help, code, stdout)
		}
	}
}

func TestServiceRootsUnresolvable(t *testing.T) {
	code, _, stderr := runCLI(fakeRuntime{err: errors.New("KTAGS_HOME must be an absolute path")}, "service", "status")
	if code != 1 || !strings.Contains(stderr, "ktags: KTAGS_HOME must be an absolute path") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}
