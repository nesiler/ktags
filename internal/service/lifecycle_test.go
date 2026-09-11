package service

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/run"
)

// bareOptions returns a private runtime root and the options of a service on it, without
// starting one.
func bareOptions(t *testing.T) (string, Options, *longAction) {
	t.Helper()
	base := shortDir(t)
	store, err := run.NewStore(mkdir(t, base, "st"), run.Options{Redact: mask.Mask})
	if err != nil {
		t.Fatal(err)
	}
	long := &longAction{gate: make(chan struct{})}
	registry, err := actions.NewRegistry(long, quickAction{}, failAction{})
	if err != nil {
		t.Fatal(err)
	}
	return mkdir(t, base, "rt"), Options{
		Registry: registry,
		Store:    store,
		DataRoot: mkdir(t, base, "da"),
		Build:    domain.Build{Name: "ktags", Version: "test"},
		Redact:   mask.Mask,
	}, long
}

// inProcess launches the service inside the test process, the way `ktags service run` would:
// it serves until a client's stop has been carried out, then closes.
type inProcess struct {
	t       *testing.T
	runtime string
	opts    Options
	// fail is returned by Launch; silent launches nothing; keep ignores a client's stop; loaded
	// is what Loaded reports.
	fail                 error
	silent, keep, loaded bool
	launches             int
	unloads              int
	unloadFailures       error
}

func (l *inProcess) Launch(ctx context.Context) error {
	l.launches++
	if l.fail != nil || l.silent {
		return l.fail
	}
	srv, err := Listen(ctx, l.runtime, l.opts)
	if err != nil {
		return err
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()
	if !l.keep {
		go func() {
			<-srv.StopRequested()
			_ = srv.Close()
		}()
	}
	l.t.Cleanup(func() {
		_ = srv.Close()
		<-served
	})
	return nil
}

func (l *inProcess) Loaded(context.Context) bool { return l.loaded }

func (l *inProcess) Unload(context.Context) error {
	l.unloads++
	return l.unloadFailures
}

func newLifecycle(t *testing.T) (Lifecycle, *inProcess, *longAction) {
	t.Helper()
	runtime, opts, long := bareOptions(t)
	launcher := &inProcess{t: t, runtime: runtime, opts: opts}
	return Lifecycle{Socket: SocketPath(runtime), Launcher: launcher, Timeout: 5 * time.Second, Poll: 5 * time.Millisecond}, launcher, long
}

// #12-K1: start is idempotent and status reports PID, protocol and socket.
func TestStartIsIdempotent(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	ctx := context.Background()
	if st, err := lc.Status(ctx); err != nil || st.Running || st.Socket != lc.Socket {
		t.Fatalf("status before start: %+v, %v; want not running on %s", st, err, lc.Socket)
	}

	first, started, err := lc.Start(ctx)
	if err != nil || !started {
		t.Fatalf("Start: %+v, started %v, %v", first, started, err)
	}
	second, started, err := lc.Start(ctx)
	if err != nil || started {
		t.Fatalf("second Start: %+v, started %v, %v; want the running service reported", second, started, err)
	}
	if launcher.launches != 1 {
		t.Fatalf("%d launches, want 1", launcher.launches)
	}
	st, err := lc.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Status{Running: true, Socket: lc.Socket, Protocol: ProtocolVersion, PID: os.Getpid(), Service: "ktags", Version: "test"}
	if st != want || first != want || second != want {
		t.Fatalf("status %+v, start %+v, second start %+v; want %+v", st, first, second, want)
	}
}

// A service that answers with another protocol version is reported, never replaced.
func TestStartDoesNotReplaceAnotherVersion(t *testing.T) {
	client := fakeService(t, `{"protocol":2,"hello":{"service":"ktags"}}`)
	launcher := &inProcess{t: t}
	lc := Lifecycle{Socket: client.Socket, Launcher: launcher}
	_, started, err := lc.Start(context.Background())
	wantCode(t, err, CodeProtocolMismatch)
	if started || launcher.launches != 0 {
		t.Fatalf("started %v after %d launches; want no launch", started, launcher.launches)
	}
}

func TestStartReportsLaunchFailure(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	launcher.fail = errors.New("launchctl bootstrap failed")
	if _, started, err := lc.Start(context.Background()); started || err == nil || err.Error() != "launchctl bootstrap failed" {
		t.Fatalf("Start: started %v, %v; want the launcher's error", started, err)
	}
}

func TestStartWithoutLauncher(t *testing.T) {
	lc, _, _ := newLifecycle(t)
	lc.Launcher = nil
	_, _, err := lc.Start(context.Background())
	wantCode(t, err, CodeInternal)
}

// A launch that never brings up a service is an error after Timeout, not a success.
func TestStartTimesOutWhenNothingAnswers(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	launcher.silent = true
	lc.Timeout = 100 * time.Millisecond
	_, started, err := lc.Start(context.Background())
	if pe := wantCode(t, err, CodeUnavailable); started || !strings.Contains(pe.Message, "did not answer") || !strings.Contains(pe.Hint, "ktags service run") {
		t.Fatalf("Start: started %v, %q / %q", started, pe.Message, pe.Hint)
	}
}

// #68-K1 D3: a job the launcher still has loaded is left alone by Start, so when it does not
// answer, the error names the only fix: replace the job.
func TestStartNamesReplaceFixForLoadedJob(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	launcher.silent, launcher.loaded = true, true
	lc.Timeout = 100 * time.Millisecond
	_, started, err := lc.Start(context.Background())
	if pe := wantCode(t, err, CodeUnavailable); started || !strings.Contains(pe.Message, "its job is loaded") || pe.Hint != "replace the job with: "+StaleFix {
		t.Fatalf("Start: started %v, %q / %q", started, pe.Message, pe.Hint)
	}
}

func TestStartHonoursCancellation(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	launcher.silent = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := lc.Start(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start: %v, want the caller's deadline", err)
	}
}

// #12-K1: stop is refused while a run is active; the service keeps running and stays loaded.
// With cancelRuns the run is cancelled, the service goes away and is unloaded.
func TestStopRefusesThenCancels(t *testing.T) {
	lc, launcher, long := newLifecycle(t)
	ctx := context.Background()
	if _, _, err := lc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	client := Client{Socket: lc.Socket}
	info, err := client.Start(ctx, "fake long", acme, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = lc.Stop(ctx, false)
	wantCode(t, err, CodeConflict)
	if st, err := lc.Status(ctx); err != nil || !st.Running || st.ActiveRuns != 1 || launcher.unloads != 0 {
		t.Fatalf("after the refused stop: %+v, %v, %d unloads; want running with one active run, loaded", st, err, launcher.unloads)
	}

	res, err := lc.Stop(ctx, true)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !res.WasRunning || len(res.Cancelled) != 1 || res.Cancelled[0].ID != info.ID || res.Cancelled[0].Status != "cancelled" {
		t.Fatalf("stop result %+v, want run %s cancelled", res, info.ID)
	}
	if st, err := lc.Status(ctx); err != nil || st.Running || launcher.unloads != 1 {
		t.Fatalf("after the stop: %+v, %v, %d unloads; want not running, unloaded once", st, err, launcher.unloads)
	}
	close(long.gate)
}

// Stopping a service that is not running succeeds and still unloads a leftover definition.
func TestStopWhenNotRunning(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	res, err := lc.Stop(context.Background(), false)
	if err != nil || res.WasRunning || launcher.unloads != 1 {
		t.Fatalf("Stop: %+v, %v, %d unloads; want nothing stopped, unloaded once", res, err, launcher.unloads)
	}
}

func TestStopReportsUnloadFailure(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	launcher.unloadFailures = errors.New("launchctl bootout failed")
	if _, err := lc.Stop(context.Background(), false); err == nil || err.Error() != "launchctl bootout failed" {
		t.Fatalf("Stop: %v, want the unload error", err)
	}
}

// A service that accepts the stop but keeps answering is an error, and it is not unloaded.
func TestStopTimesOutWhenServiceStays(t *testing.T) {
	lc, launcher, _ := newLifecycle(t)
	launcher.keep = true
	lc.Timeout = 100 * time.Millisecond
	ctx := context.Background()
	if _, _, err := lc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := lc.Stop(ctx, false)
	if pe := wantCode(t, err, CodeUnavailable); !strings.Contains(pe.Message, "still answers") || launcher.unloads != 0 {
		t.Fatalf("Stop: %q, %d unloads", pe.Message, launcher.unloads)
	}
}

// A service that answers wrongly is an error for status and stop, never "not running".
func TestStatusAndStopReportForeignService(t *testing.T) {
	client := fakeService(t, `{"protocol":2,"hello":{"service":"ktags"}}`)
	launcher := &inProcess{t: t}
	lc := Lifecycle{Socket: client.Socket, Launcher: launcher}
	_, err := lc.Status(context.Background())
	wantCode(t, err, CodeProtocolMismatch)
	_, err = lc.Stop(context.Background(), true)
	wantCode(t, err, CodeProtocolMismatch)
	if launcher.unloads != 0 {
		t.Fatal("a stop that could not talk to the service unloaded it")
	}
}

// A service that accepts but never answers is reported as silent within Timeout: status, start
// and stop fail instead of hanging, nothing is launched and nothing unloaded.
func TestSilentServiceIsAnErrorNotAHang(t *testing.T) {
	client := fakeService(t, "-")
	launcher := &inProcess{t: t}
	lc := Lifecycle{Socket: client.Socket, Launcher: launcher, Timeout: 100 * time.Millisecond}
	ctx := context.Background()
	_, err := lc.Status(ctx)
	if pe := wantCode(t, err, CodeUnavailable); !strings.Contains(pe.Message, "did not answer within") || !strings.Contains(pe.Hint, "service.lock") {
		t.Fatalf("Status: %q / %q", pe.Message, pe.Hint)
	}
	if _, _, err := lc.Start(ctx); err == nil {
		t.Fatal("Start succeeded against a silent service")
	}
	if _, err := lc.Stop(ctx, true); err == nil {
		t.Fatal("Stop succeeded against a silent service")
	}
	if launcher.launches != 0 || launcher.unloads != 0 {
		t.Fatalf("%d launches and %d unloads, want none", launcher.launches, launcher.unloads)
	}
}

// The caller's cancellation is returned as itself, not as a silent service.
func TestStatusHonoursCallerCancellation(t *testing.T) {
	client := fakeService(t, "-")
	lc := Lifecycle{Socket: client.Socket, Timeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := lc.Status(ctx)
	var pe *Error
	if !errors.Is(err, context.Canceled) || errors.As(err, &pe) {
		t.Fatalf("Status: %v, want the caller's cancellation", err)
	}
}

func TestStopWithoutLauncher(t *testing.T) {
	lc, _, _ := newLifecycle(t)
	lc.Launcher = nil
	if res, err := lc.Stop(context.Background(), false); err != nil || res.WasRunning {
		t.Fatalf("Stop: %+v, %v; want nothing to stop and nothing to unload", res, err)
	}
}

// rawLauncher brings up a peer on the lifecycle socket that answers every request with reply.
type rawLauncher struct {
	t             *testing.T
	socket, reply string
}

func (l rawLauncher) Launch(context.Context) error {
	ln, err := net.Listen("unix", l.socket)
	if err != nil {
		return err
	}
	l.t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = bufio.NewReader(conn).ReadBytes('\n')
				_, _ = conn.Write([]byte(l.reply + "\n"))
			}()
		}
	}()
	return nil
}

func (rawLauncher) Unload(context.Context) error { return nil }

// staleLauncher is an in-process launcher whose installed definition reports stale, or fails
// to compare.
type staleLauncher struct {
	*inProcess
	stale bool
	err   error
}

func (s staleLauncher) Stale() (bool, error) { return s.stale, s.err }

// #68-K1 D3: status and start of a running service report a stale launcher definition; start
// launches nothing for it.
func TestStatusReportsStaleAgent(t *testing.T) {
	for _, tc := range []struct {
		name string
		l    staleLauncher
		want bool
	}{
		{"stale", staleLauncher{stale: true}, true},
		{"current", staleLauncher{}, false},
		{"cannot compare", staleLauncher{stale: true, err: errors.New("permission denied")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, opts, _ := bareOptions(t)
			tc.l.inProcess = &inProcess{t: t, runtime: runtime, opts: opts}
			lc := Lifecycle{Socket: SocketPath(runtime), Launcher: tc.l, Timeout: 5 * time.Second, Poll: 5 * time.Millisecond}
			ctx := context.Background()
			if _, _, err := lc.Start(ctx); err != nil {
				t.Fatal(err)
			}
			st, err := lc.Status(ctx)
			if err != nil || st.StaleAgent != (tc.want && tc.l.err == nil) {
				t.Fatalf("status %+v, %v; want stale agent %v", st, err, tc.want && tc.l.err == nil)
			}
			st, started, err := lc.Start(ctx)
			if err != nil || started || st.StaleAgent != (tc.want && tc.l.err == nil) || tc.l.launches != 1 {
				t.Fatalf("second start %+v started %v, %v, %d launches", st, started, err, tc.l.launches)
			}
		})
	}
}

// A service that does not answer carries no stale mark: nothing runs the old definition.
func TestStatusNotRunningNoStaleAgent(t *testing.T) {
	runtime, opts, _ := bareOptions(t)
	lc := Lifecycle{Socket: SocketPath(runtime), Launcher: staleLauncher{inProcess: &inProcess{t: t, runtime: runtime, opts: opts}, stale: true}}
	st, err := lc.Status(context.Background())
	if err != nil || st.Running || st.StaleAgent {
		t.Fatalf("status %+v, %v", st, err)
	}
}

// A launch that brings up a service of another protocol version ends the wait with the mismatch.
func TestStartReportsMismatchAfterLaunch(t *testing.T) {
	socket := filepath.Join(shortDir(t), "service.sock")
	lc := Lifecycle{Socket: socket, Launcher: rawLauncher{t: t, socket: socket, reply: `{"protocol":2}`}, Timeout: 5 * time.Second, Poll: 5 * time.Millisecond}
	_, started, err := lc.Start(context.Background())
	wantCode(t, err, CodeProtocolMismatch)
	if started {
		t.Fatal("a service of another protocol version counts as started")
	}
}

func TestNoLauncherRefuses(t *testing.T) {
	err := NoLauncher{GOOS: "linux"}.Launch(context.Background())
	if pe := wantCode(t, err, CodeUnavailable); !strings.Contains(pe.Message, "linux") || !strings.Contains(pe.Hint, "ktags service run") {
		t.Fatalf("refusal %q / %q", pe.Message, pe.Hint)
	}
	if err := (NoLauncher{}).Unload(context.Background()); err != nil {
		t.Fatalf("Unload: %v", err)
	}
}
