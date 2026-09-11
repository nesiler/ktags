package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/run"
)

// #11-K1: a client that disconnects during a long action does not stop it; a new client resumes
// from its cursor, a fresh one replays everything, and both see the result.
func TestRunSurvivesClientDisconnect(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake long", acme, map[string]any{"count": 3})
	if started.Status != string(run.StatusRunning) {
		t.Fatalf("started status %q, want running", started.Status)
	}

	if got := f.detachAfter(started.ID, 2); !slices.Equal(got, ids(1, 2)) {
		t.Fatalf("first client saw %v, want [1 2]", got)
	}
	f.waitConns(2) // the start and the detached follow are both gone from the server

	replayed, info := f.follow(started.ID, 0, false)
	if !slices.Equal(replayed, ids(1, 2)) || info.Status != string(run.StatusRunning) {
		t.Fatalf("while detached: events %v status %q, want [1 2] running", replayed, info.Status)
	}

	close(f.long.gate)
	resumed, info := f.follow(started.ID, 2, true)
	if !slices.Equal(resumed, ids(3, 5)) {
		t.Fatalf("resumed events %v, want [3 4 5]", resumed)
	}
	if info.Status != string(run.StatusSucceeded) || info.Result == nil || info.Result.Summary != "long done" {
		t.Fatalf("final state %+v, want succeeded with the action's summary", info)
	}

	all, info := f.follow(started.ID, 0, true)
	if !slices.Equal(all, ids(1, 5)) || info.Status != string(run.StatusSucceeded) || info.LastEventID != 5 {
		t.Fatalf("replay events %v state %+v, want [1..5] succeeded", all, info)
	}
}

// A follower ends with the final state and the result, run after run.
func TestFollowEndsWithResult(t *testing.T) {
	f := newFixture(t, setup{})
	for i := 0; i < 50; i++ {
		started := f.start("fake quick", global, nil)
		got, info := f.follow(started.ID, 0, true)
		if !slices.Equal(got, ids(1, 1)) || info.Status != string(run.StatusSucceeded) || info.Result == nil {
			t.Fatalf("run %d: events %v state %+v, want [1] and a succeeded result", i, got, info)
		}
	}
}

func TestCancelRun(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake long", acme, nil)
	f.detachAfter(started.ID, 2)
	if _, err := f.client.Cancel(context.Background(), started.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	_, info := f.follow(started.ID, 0, true)
	if info.Status != string(run.StatusCancelled) || info.Result == nil || info.Result.Summary != errCancelled.Error() {
		t.Fatalf("state %+v, want cancelled by the operator", info)
	}
}

func TestCancelRefusals(t *testing.T) {
	f := newFixture(t, setup{})
	ctx := context.Background()
	_, err := f.client.Cancel(ctx, missingRun)
	wantCode(t, err, CodeNotFound)
	_, err = f.client.Cancel(ctx, "not-a-run")
	wantCode(t, err, CodeNotFound)

	done := f.start("fake quick", global, nil)
	f.follow(done.ID, 0, true)
	_, err = f.client.Cancel(ctx, done.ID)
	if pe := wantCode(t, err, CodeConflict); !strings.Contains(pe.Message, "succeeded") {
		t.Fatalf("message %q, want the run's final status", pe.Message)
	}
}

func TestFailedActionIsRecordedMasked(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake fail", global, nil)
	_, info := f.follow(started.ID, 0, true)
	if info.Status != string(run.StatusFailed) || info.Result == nil {
		t.Fatalf("state %+v, want failed", info)
	}
	if strings.Contains(info.Result.Summary, "hunter2") || !strings.Contains(info.Result.Summary, mask.Placeholder) {
		t.Fatalf("summary %q, want the password masked", info.Result.Summary)
	}
}

func TestStartRefusesInvalidRequest(t *testing.T) {
	cases := []struct {
		name   string
		action string
		target Target
		args   map[string]any
	}{
		{"unknown action", "fake nothing", global, nil},
		// Arguments skip the empty-arguments fast path, so decodeArgs must look up the action.
		{"unknown action with arguments", "fake nothing", global, map[string]any{"note": "x"}},
		{"wrong target kind", "fake quick", acme, nil},
		{"customer target without customer", "fake long", Target{Kind: "customer"}, nil},
		{"string for an int", "fake long", acme, map[string]any{"count": "three"}},
		{"int for a string", "fake quick", global, map[string]any{"note": 3}},
		{"string for a bool", "fake quick", global, map[string]any{"verbose": "yes"}},
		{"unknown argument", "fake quick", global, map[string]any{"colour": "red"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, setup{})
			_, err := f.client.Start(context.Background(), tc.action, tc.target, tc.args)
			wantCode(t, err, CodeInvalid)
			if runs, err := f.store.List(context.Background()); err != nil || len(runs) != 0 {
				t.Fatalf("runs after a refused start: %v %v, want none", runs, err)
			}
		})
	}
}

func TestStartWithoutTargetRefused(t *testing.T) {
	f := newFixture(t, setup{})
	_, pe := f.raw(t, `{"protocol":1,"op":"run.start","action":"fake quick"}`+"\n")
	if pe == nil || pe.Code != CodeInvalid {
		t.Fatalf("error %+v, want invalid", pe)
	}
}

func TestStartFailsWhenTheRunCannotBeRecorded(t *testing.T) {
	f := newFixture(t, setup{})
	chmod(t, f.state, 0o500)
	_, err := f.client.Start(context.Background(), "fake quick", global, nil)
	wantCode(t, err, CodeInternal)
}

func TestStartRefusedWhileStopping(t *testing.T) {
	f := newFixture(t, setup{})
	m := newManager(f.registry, f.store, f.data, mask.Mask)
	m.close()
	_, err := m.start(Request{Action: "fake quick", Target: &global})
	wantCode(t, err, CodeUnavailable)
}

func TestLargeArgumentsWithinTheLimit(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake quick", global, map[string]any{"note": strings.Repeat("n", 64<<10), "verbose": true})
	if _, info := f.follow(started.ID, 0, true); info.Status != string(run.StatusSucceeded) {
		t.Fatalf("state %+v, want succeeded", info)
	}
}

func TestStatusAndRuns(t *testing.T) {
	f := newFixture(t, setup{})
	ctx := context.Background()
	first := f.start("fake quick", global, nil)
	f.follow(first.ID, 0, true)
	second := f.start("fake quick", global, nil)
	f.follow(second.ID, 0, true)

	info, err := f.client.Status(ctx, first.ID)
	if err != nil || info.Status != string(run.StatusSucceeded) || info.Action != "fake quick" || info.Target != global {
		t.Fatalf("Status: %+v %v", info, err)
	}
	runs, err := f.client.Runs(ctx)
	// Run IDs start with the second they were started in; within one second the store's order is
	// that of the random suffix, so only membership is asserted here.
	if err != nil || len(runs) != 2 || !slices.ContainsFunc(runs, func(r RunInfo) bool { return r.ID == first.ID }) ||
		!slices.ContainsFunc(runs, func(r RunInfo) bool { return r.ID == second.ID }) {
		t.Fatalf("Runs: %+v %v, want both runs", runs, err)
	}
	_, err = f.client.Status(ctx, missingRun)
	wantCode(t, err, CodeNotFound)
}

func TestRunsUnreadable(t *testing.T) {
	f := newFixture(t, setup{})
	f.follow(f.start("fake quick", global, nil).ID, 0, true)
	chmod(t, filepath.Join(f.state, "runs"), 0o000)
	_, err := f.client.Runs(context.Background())
	wantCode(t, err, CodeInternal)
}

func TestEventsCursorBeyondTheEnd(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake quick", global, nil)
	f.follow(started.ID, 0, true)
	_, err := f.client.Events(context.Background(), started.ID, 2, false, func(Event) error { return nil })
	wantCode(t, err, CodeInvalid)
	_, err = f.client.Events(context.Background(), missingRun, 0, false, func(Event) error { return nil })
	wantCode(t, err, CodeNotFound)
}

func TestEventsSubscriberLimit(t *testing.T) {
	f := newFixture(t, setup{store: run.Options{MaxSubscribers: 1}})
	started := f.start("fake long", acme, nil)
	arrived, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := f.client.Events(context.Background(), started.ID, 0, true, func(e Event) error {
			if e.ID == 2 {
				close(arrived)
				<-release
				return errDetach
			}
			return nil
		})
		done <- err
	}()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the first follower never received event 2")
	}
	_, err := f.client.Events(context.Background(), started.ID, 0, true, func(Event) error { return nil })
	wantCode(t, err, CodeConflict)
	close(release)
	if err := <-done; !errors.Is(err, errDetach) {
		t.Fatalf("first follower: %v", err)
	}
}

func TestEventsDamagedReplay(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake quick", global, nil)
	_, info := f.follow(started.ID, 0, true)
	loaded, err := f.store.Load(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := os.OpenFile(filepath.Join(loaded.Dir, "events.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := events.WriteString("damaged\n"); err != nil {
		t.Fatal(err)
	}
	_ = events.Close()
	_, err = f.client.Events(context.Background(), started.ID, 0, false, func(Event) error { return nil })
	wantCode(t, err, CodeInternal)
}

func TestResultWriteFailureReported(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake long", acme, nil)
	f.detachAfter(started.ID, 2)
	loaded, err := f.store.Load(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	chmod(t, loaded.Dir, 0o500)
	// The gate opens only once the follower holds the active run: from cursor 1 it receives event
	// 2 through its subscription. Opened earlier, the run can finish and leave the manager before
	// the follower arrives, which then replays the run without the result write error.
	// Bounded, so a lost event 2 fails here instead of hanging until the go test timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = f.client.Events(ctx, started.ID, 1, true, func(e Event) error {
		if e.ID == 2 {
			close(f.long.gate)
		}
		return nil
	})
	if pe := wantCode(t, err, CodeInternal); !strings.Contains(pe.Message, "could not be written") {
		t.Fatalf("message %q, want the result write failure", pe.Message)
	}
}

func TestCloseCancelsActiveRuns(t *testing.T) {
	f := newFixture(t, setup{})
	started := f.start("fake long", acme, nil)
	f.detachAfter(started.ID, 2)
	if err := f.srv.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := f.store.Load(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != run.StatusCancelled || loaded.Result.Summary != errStopping.Error() {
		t.Fatalf("after Close: %+v, want cancelled because the service stopped", loaded)
	}
}

func TestHelloAndActions(t *testing.T) {
	f := newFixture(t, setup{})
	ctx := context.Background()
	hello, err := f.client.Hello(ctx)
	if err != nil || hello.Service != "ktags" || hello.Version != "test" || hello.PID != os.Getpid() {
		t.Fatalf("Hello: %+v %v", hello, err)
	}
	list, err := f.client.Actions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, a := range list {
		got = append(got, a.ID)
	}
	if !slices.Equal(got, []string{"fake fail", "fake long", "fake quick"}) {
		t.Fatalf("actions %v", got)
	}
	if list[1].Target != "customer" || len(list[1].Args) != 1 || list[1].Args[0].Kind != "int" {
		t.Fatalf("fake long described as %+v", list[1])
	}
}

func TestCustomers(t *testing.T) {
	f := newFixture(t, setup{})
	ctx := context.Background()
	if list, err := f.client.Customers(ctx); err != nil || len(list) != 0 {
		t.Fatalf("empty inventory: %+v %v", list, err)
	}

	acmeDir := inventory.CustomerDir(f.data, "acme")
	if err := os.MkdirAll(filepath.Dir(inventory.RecordPath(acmeDir)), 0o700); err != nil {
		t.Fatal(err)
	}
	record := inventory.Record{
		Schema:   inventory.SchemaVersion,
		Customer: inventory.Customer{ID: "acme", Name: "Acme Corp", Environment: inventory.EnvProd},
		Cluster: inventory.Cluster{
			ID: "acme-main", Name: "Acme main cluster", RancherURL: "https://rancher.acme.example.com/dashboard",
			Nodes:  []inventory.Node{{ID: "srv-1", Name: "acme-srv-1", Role: inventory.RoleServer, Address: "203.0.113.11"}},
			Access: inventory.Access{Direct: &inventory.DirectAccess{User: "ops", Port: 22}},
		},
	}
	if err := inventory.Save(ctx, acmeDir, record); err != nil {
		t.Fatal(err)
	}
	globexRecord := inventory.RecordPath(inventory.CustomerDir(f.data, "globex"))
	if err := os.MkdirAll(filepath.Dir(globexRecord), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globexRecord, []byte("ktags_schema: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inventory.CustomersDir(f.data), "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := f.client.Customers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := CustomerInfo{ID: "acme", Name: "Acme Corp", Environment: "prod", Cluster: "Acme main cluster", Nodes: 1}
	if len(list) != 2 || list[0] != want || list[1].ID != "globex" || list[1].Problem == "" {
		t.Fatalf("customers %+v, want acme and globex with its problem", list)
	}

	// A refused record's problem passes the redactor.
	m := newManager(f.registry, f.store, f.data, func(s string) string { return "R:" + s })
	defer m.close()
	direct, err := m.customers(ctx)
	if err != nil || !strings.HasPrefix(direct[1].Problem, "R:") {
		t.Fatalf("problem %+v %v, want it redacted", direct, err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.customers(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled listing: %v, want context.Canceled", err)
	}

	chmod(t, inventory.CustomersDir(f.data), 0o000)
	_, err = f.client.Customers(ctx)
	wantCode(t, err, CodeInternal)
}

func TestOutcome(t *testing.T) {
	cancelled, cancel := context.WithCancelCause(context.Background())
	cancel(errCancelled)
	live := context.Background()
	cases := []struct {
		name        string
		ctx         context.Context
		result      actions.Result
		err         error
		wantStatus  run.Status
		wantSummary string
	}{
		{"cancelled", cancelled, actions.Result{}, context.Canceled, run.StatusCancelled, errCancelled.Error()},
		{"error", live, actions.Result{}, errors.New("boom"), run.StatusFailed, "boom"},
		{"succeeded", live, actions.Result{Status: actions.StatusSucceeded, Summary: "ok"}, nil, run.StatusSucceeded, "ok"},
		{"failed result", live, actions.Result{Status: actions.StatusFailed, Summary: "nope"}, nil, run.StatusFailed, "nope"},
		{"success racing a cancel", cancelled, actions.Result{Status: actions.StatusSucceeded, Summary: "ok"}, nil, run.StatusSucceeded, "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, summary := outcome(tc.ctx, tc.result, tc.err)
			if status != tc.wantStatus || summary != tc.wantSummary {
				t.Fatalf("outcome %q %q, want %q %q", status, summary, tc.wantStatus, tc.wantSummary)
			}
		})
	}
}

func TestWireErrorMasksAndClassifies(t *testing.T) {
	s := &Server{redact: mask.Mask}
	cases := []struct {
		name               string
		err                error
		wantCode, wantHint string
		wantPrefix         string
	}{
		{"protocol error", fmt.Errorf("wrapped: %w", &Error{Code: CodeConflict, Message: "password: hunter2-example", Hint: "token: hunter2-example"}), CodeConflict, "token: " + mask.Placeholder, "password"},
		{"action error", &actions.Error{Action: "x", Problem: "password: hunter2-example", Hint: "h"}, CodeInvalid, "h", `action "x"`},
		{"run error", &run.Error{Problem: "password: hunter2-example", Next: "n"}, CodeInternal, "n", "run: password"},
		{"other error", errors.New("password: hunter2-example"), CodeInternal, "", "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.wireError(tc.err)
			if got.Code != tc.wantCode || got.Hint != tc.wantHint || !strings.HasPrefix(got.Message, tc.wantPrefix) {
				t.Fatalf("got %+v, want code %q hint %q message starting %q", got, tc.wantCode, tc.wantHint, tc.wantPrefix)
			}
			if strings.Contains(got.Message+got.Hint, "hunter2") {
				t.Fatalf("secret reached the client: %+v", got)
			}
		})
	}
}

// raw sends payload on a fresh connection and returns the protocol and error of the first reply.
// A reply that is not JSON fails the test: the service must never answer with malformed data.
func (f *fixture) raw(t *testing.T, payload string) (int, *Error) {
	t.Helper()
	conn, err := net.Dial("unix", f.client.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	go func() {
		_, _ = conn.Write([]byte(payload))
		if !strings.HasSuffix(payload, "\n") {
			_ = conn.(*net.UnixConn).CloseWrite()
		}
	}()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading the reply: %v", err)
	}
	var resp struct {
		Protocol int    `json:"protocol"`
		Error    *Error `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("reply %q is not JSON: %v", line, err)
	}
	return resp.Protocol, resp.Error
}

func TestServeReportsAcceptFailure(t *testing.T) {
	f := newFixture(t, setup{})
	s, err := Listen(context.Background(), mkdir(t, shortDir(t), "rt"), f.options())
	if err != nil {
		t.Fatal(err)
	}
	_ = s.listener.Close()
	wantCode(t, s.Serve(), CodeInternal)
	_ = s.Close()
}

func TestConnectionAcceptedDuringCloseIsDropped(t *testing.T) {
	f := newFixture(t, setup{server: func(s *Server) {
		s.accepted = func() { _ = s.Close() }
	}})
	_, err := f.client.Hello(context.Background())
	wantCode(t, err, CodeUnavailable)
}

func TestCloseDisconnectsIdleClients(t *testing.T) {
	entered := make(chan struct{}, 1)
	f := newFixture(t, setup{server: func(s *Server) {
		s.peerUID = func(*net.UnixConn) (int, error) {
			entered <- struct{}{}
			return os.Getuid(), nil
		}
	}})
	conn, err := net.Dial("unix", f.client.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the connection was never handled")
	}
	closed := make(chan error, 1)
	go func() { closed <- f.srv.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close waited for a client that never sent a request")
	}
}

func TestEventsCatchUpFailure(t *testing.T) {
	f := newFixture(t, setup{store: run.Options{Window: 1}})
	started := f.start("fake long", acme, nil)
	f.detachAfter(started.ID, 2)
	loaded, err := f.store.Load(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Event 1 is outside the one-event window, so a follower from 0 must read the damaged file.
	if err := os.WriteFile(filepath.Join(loaded.Dir, "events.jsonl"), []byte("damaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = f.client.Events(context.Background(), started.ID, 0, true, func(Event) error { return nil })
	wantCode(t, err, CodeInternal)
}

func TestListenHonoursCancelledContext(t *testing.T) {
	f := newFixture(t, setup{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Listen(ctx, mkdir(t, shortDir(t), "rt"), f.options()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Listen: %v, want context.Canceled", err)
	}
}
