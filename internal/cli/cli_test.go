package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/doctor"
	"github.com/nesiler/ktags/internal/service"
)

type fakeRuntime struct {
	control *fakeControl
	err     error
	// report is what Doctor returns; doctorErr fails it.
	report    doctor.Report
	doctorErr error
}

func (fakeRuntime) Version() domain.Build { return domain.Build{Name: "ktags", Version: "test"} }

func (r fakeRuntime) Doctor(context.Context) (doctor.Report, error) {
	if r.doctorErr != nil {
		return doctor.Report{}, r.doctorErr
	}
	if r.report.Status == "" {
		return doctor.Report{Status: doctor.StatusOK, Checks: []doctor.Result{{ID: "platform", Title: "platform", Status: doctor.StatusOK, Evidence: "darwin/arm64"}}}, nil
	}
	return r.report, nil
}

func (r fakeRuntime) Service() (ServiceControl, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.control, nil
}

type fakeControl struct {
	status  service.Status
	started bool
	// afterStart, when set, is the status Start reports.
	afterStart *service.Status
	startErr   error
	stop       service.StopResult
	err        error
	cancelRuns bool
	calls      []string
	client     *fakeClient
}

func (c *fakeControl) Start(context.Context) (service.Status, bool, error) {
	c.calls = append(c.calls, "start")
	if c.startErr != nil {
		return service.Status{}, false, c.startErr
	}
	if c.afterStart != nil {
		return *c.afterStart, c.started, c.err
	}
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

func (c *fakeControl) Client() Client { return c.client }

// fakeClient is an in-memory ktags service: runs follow a script of events and a final state.
type fakeClient struct {
	actions   []service.ActionInfo
	customers []service.CustomerInfo
	runs      map[string]*fakeRun
	// script is the run the next Start creates.
	script    fakeRun
	started   []startCall
	cancelled []string
	// after records the cursor of every Events call.
	after []uint64
	// onBlock is called when a followed run blocks; tests use it to interrupt.
	onBlock func()
	// lose makes an event stream break after its events, like a service that went away.
	lose bool
	// customersErr, actionsErr, runsErr, startErr, statusErr and cancelErr fail one operation.
	customersErr error
	actionsErr   error
	runsErr      error
	startErr     error
	statusErr    error
	cancelErr    error
	// fleet is the fleet summary the service reports; fleetCalls counts the requests.
	fleet      service.FleetInfo
	fleetErr   error
	fleetCalls int
}

type fakeRun struct {
	info   service.RunInfo
	events []service.Event
	final  service.RunInfo
	// block makes a follow wait for its context after the events: the run is still working.
	block bool
}

type startCall struct {
	action string
	target service.Target
	args   map[string]any
}

func (c *fakeClient) Customers(context.Context) ([]service.CustomerInfo, error) {
	if c.customersErr != nil {
		return nil, c.customersErr
	}
	return c.customers, nil
}

func (c *fakeClient) Fleet(context.Context) (service.FleetInfo, error) {
	c.fleetCalls++
	if c.fleetErr != nil {
		return service.FleetInfo{}, c.fleetErr
	}
	return c.fleet, nil
}

func (c *fakeClient) Actions(context.Context) ([]service.ActionInfo, error) {
	if c.actionsErr != nil {
		return nil, c.actionsErr
	}
	return c.actions, nil
}

func (c *fakeClient) Runs(context.Context) ([]service.RunInfo, error) {
	if c.runsErr != nil {
		return nil, c.runsErr
	}
	var out []service.RunInfo
	for _, id := range []string{"r7", "rB"} {
		if r, ok := c.runs[id]; ok {
			out = append(out, r.info)
		}
	}
	return out, nil
}

func (c *fakeClient) Start(_ context.Context, action string, target service.Target, args map[string]any) (service.RunInfo, error) {
	if c.startErr != nil {
		return service.RunInfo{}, c.startErr
	}
	c.started = append(c.started, startCall{action: action, target: target, args: args})
	id := fmt.Sprintf("run-%d", len(c.started))
	r := c.script
	r.info = service.RunInfo{ID: id, Action: action, Target: target, Status: "running"}
	r.final.ID, r.final.Action, r.final.Target = id, action, target
	c.runs[id] = &r
	return r.info, nil
}

func (c *fakeClient) Cancel(_ context.Context, id string) (service.RunInfo, error) {
	c.cancelled = append(c.cancelled, id)
	if c.cancelErr != nil {
		return service.RunInfo{}, c.cancelErr
	}
	r, err := c.run(id)
	if err != nil {
		return service.RunInfo{}, err
	}
	r.block = false
	r.final = r.info
	r.final.Status = "cancelled"
	r.final.Result = &service.ResultInfo{Status: "cancelled", Summary: "cancelled by the operator"}
	info := r.info
	info.LastEventID = uint64(len(r.events))
	return info, nil
}

func (c *fakeClient) Status(_ context.Context, id string) (service.RunInfo, error) {
	if c.statusErr != nil {
		return service.RunInfo{}, c.statusErr
	}
	r, err := c.run(id)
	if err != nil {
		return service.RunInfo{}, err
	}
	return r.info, nil
}

func (c *fakeClient) Events(ctx context.Context, id string, after uint64, follow bool, fn func(service.Event) error) (service.RunInfo, error) {
	c.after = append(c.after, after)
	r, err := c.run(id)
	if err != nil {
		return service.RunInfo{}, err
	}
	for _, ev := range r.events {
		if ev.ID > after {
			if err := fn(ev); err != nil {
				return service.RunInfo{}, err
			}
		}
	}
	if c.lose {
		return service.RunInfo{}, &service.Error{Code: service.CodeUnavailable, Message: "the connection to the ktags service ended before its reply", Hint: "check that the ktags service is running, then retry"}
	}
	if r.block && follow {
		if c.onBlock != nil {
			c.onBlock()
		}
		<-ctx.Done()
		return service.RunInfo{}, ctx.Err()
	}
	return r.final, nil
}

func (c *fakeClient) run(id string) (*fakeRun, error) {
	r, ok := c.runs[id]
	if !ok {
		return nil, &service.Error{Code: service.CodeNotFound, Message: "run: no run " + id, Hint: "list the runs with run.list"}
	}
	return r, nil
}

var running = service.Status{Running: true, Socket: "/tmp/kt/runtime/service.sock", Protocol: 1, PID: 4242, Version: "test", ActiveRuns: 2}

func runCLI(rt Runtime, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), rt, args, Streams{Out: &stdout, Err: &stderr})
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
		{"service", "stop", "--cancel-runs", "extra"}, {"service", "run", "--json"},
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
