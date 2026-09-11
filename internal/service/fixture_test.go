package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/run"
)

var (
	acme       = Target{Kind: "customer", Customer: "acme"}
	global     = Target{Kind: "global"}
	errDetach  = errors.New("detach")
	missingRun = "20260101T000000Z-00000000"
)

// longAction reports two events, blocks until gate is closed or it is cancelled, then reports
// count more events (default 3).
type longAction struct{ gate chan struct{} }

func (a *longAction) Descriptor() actions.Descriptor {
	return actions.Descriptor{
		ID: "fake long", Title: "Fake long", Help: "Blocks after two events until released.",
		Target: actions.TargetCustomer, Effect: actions.EffectReadOnly,
		Args: []actions.Arg{{Name: "count", Kind: actions.ArgInt, Help: "events after the gate"}},
	}
}

func (a *longAction) Check(context.Context, actions.Request) error { return nil }

func (a *longAction) Run(ctx context.Context, req actions.Request, p actions.Progress) (actions.Result, error) {
	p.Report(actions.Event{Step: "start", Message: "event 1"})
	p.Report(actions.Event{Step: "start", Message: "event 2"})
	select {
	case <-a.gate:
	case <-ctx.Done():
		return actions.Result{}, ctx.Err()
	}
	count := 3
	if n, ok := req.Args["count"].(int); ok {
		count = n
	}
	for i := 0; i < count; i++ {
		p.Report(actions.Event{Step: "after", Message: fmt.Sprintf("event %d", i+3)})
	}
	return actions.Result{Status: actions.StatusSucceeded, Summary: "long done"}, nil
}

type quickAction struct{}

func (quickAction) Descriptor() actions.Descriptor {
	return actions.Descriptor{
		ID: "fake quick", Title: "Fake quick", Help: "Reports one event and succeeds.",
		Target: actions.TargetGlobal, Effect: actions.EffectReadOnly,
		Args: []actions.Arg{
			{Name: "note", Kind: actions.ArgString, Help: "free text"},
			{Name: "verbose", Kind: actions.ArgBool, Help: "more output"},
		},
	}
}

func (quickAction) Check(context.Context, actions.Request) error { return nil }

func (quickAction) Run(_ context.Context, _ actions.Request, p actions.Progress) (actions.Result, error) {
	p.Report(actions.Event{Step: "quick", Message: "event 1"})
	return actions.Result{Status: actions.StatusSucceeded, Summary: "quick done"}, nil
}

// failAction fails with an error that quotes a secret.
type failAction struct{}

func (failAction) Descriptor() actions.Descriptor {
	return actions.Descriptor{
		ID: "fake fail", Title: "Fake fail", Help: "Fails with a secret in its error.",
		Target: actions.TargetGlobal, Effect: actions.EffectMutating,
	}
}

func (failAction) Check(context.Context, actions.Request) error { return nil }

func (failAction) Run(context.Context, actions.Request, actions.Progress) (actions.Result, error) {
	return actions.Result{}, errors.New("remote said password: hunter2-example")
}

// shortDir is a private temp directory with a short path: t.TempDir can exceed the socket path
// limit on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func mkdir(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// chmod changes a mode for the rest of the test and restores 0700 so cleanup can remove it.
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

func withSys(t *testing.T, mutate func(*sysCalls)) {
	t.Helper()
	saved := sys
	mutate(&sys)
	t.Cleanup(func() { sys = saved })
}

type fixture struct {
	t                    *testing.T
	srv                  *Server
	client               Client
	runtime, state, data string
	long                 *longAction
	store                *run.Store
	registry             *actions.Registry
	conns                chan struct{}
}

type setup struct {
	store  run.Options
	before func(runtime string)
	server func(*Server)
}

func newFixture(t *testing.T, s setup) *fixture {
	t.Helper()
	base := shortDir(t)
	f := &fixture{
		t:       t,
		runtime: mkdir(t, base, "rt"),
		state:   mkdir(t, base, "st"),
		data:    mkdir(t, base, "da"),
		long:    &longAction{gate: make(chan struct{})},
		conns:   make(chan struct{}, 1024),
	}
	s.store.Redact = mask.Mask
	store, err := run.NewStore(f.state, s.store)
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	f.registry, err = actions.NewRegistry(f.long, quickAction{}, failAction{})
	if err != nil {
		t.Fatal(err)
	}
	if s.before != nil {
		s.before(f.runtime)
	}
	srv, err := Listen(context.Background(), f.runtime, f.options())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	srv.connDone = func() { f.conns <- struct{}{} }
	if s.server != nil {
		s.server(srv)
	}
	f.srv = srv
	f.client = Client{Socket: SocketPath(f.runtime)}
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return f
}

func (f *fixture) options() Options {
	return Options{
		Registry: f.registry,
		Store:    f.store,
		DataRoot: f.data,
		Build:    domain.Build{Name: "ktags", Version: "test"},
		Redact:   mask.Mask,
	}
}

// waitConns waits until the server has finished n more connections.
func (f *fixture) waitConns(n int) {
	f.t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-f.conns:
		case <-time.After(10 * time.Second):
			f.t.Fatalf("connection %d of %d did not finish", i+1, n)
		}
	}
}

func (f *fixture) start(action string, target Target, args map[string]any) RunInfo {
	f.t.Helper()
	info, err := f.client.Start(context.Background(), action, target, args)
	if err != nil {
		f.t.Fatalf("Start %s: %v", action, err)
	}
	return info
}

// follow returns the event IDs of run id after the cursor and the run state at the end.
func (f *fixture) follow(id string, after uint64, follow bool) ([]uint64, RunInfo) {
	f.t.Helper()
	var ids []uint64
	info, err := f.client.Events(context.Background(), id, after, follow, func(e Event) error {
		ids = append(ids, e.ID)
		return nil
	})
	if err != nil {
		f.t.Fatalf("Events %s after %d: %v", id, after, err)
	}
	return ids, info
}

// detachAfter follows run id and drops the connection once event last has arrived.
func (f *fixture) detachAfter(id string, last uint64) []uint64 {
	f.t.Helper()
	var ids []uint64
	_, err := f.client.Events(context.Background(), id, 0, true, func(e Event) error {
		ids = append(ids, e.ID)
		if e.ID == last {
			return errDetach
		}
		return nil
	})
	if !errors.Is(err, errDetach) {
		f.t.Fatalf("Events: %v, want the detach", err)
	}
	return ids
}

// wantCode returns the protocol error in err, by value so callers may ignore it.
func wantCode(t *testing.T, err error, code string) Error {
	t.Helper()
	var pe *Error
	if !errors.As(err, &pe) {
		t.Fatalf("error %v (%T), want protocol error %s", err, err, code)
	}
	if pe.Code != code {
		t.Fatalf("code %q (%v), want %q", pe.Code, pe, code)
	}
	return *pe
}

func staleSocket(t *testing.T, path string) {
	t.Helper()
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func ids(from, to uint64) []uint64 {
	var out []uint64
	for id := from; id <= to; id++ {
		out = append(out, id)
	}
	return out
}
