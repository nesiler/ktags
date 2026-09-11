package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/launchd"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/run"
	"github.com/nesiler/ktags/internal/service"
)

// childEnv makes the test binary act as `ktags service run`, the process launchd would start.
const childEnv = "KTAGS_TEST_SERVICE_PROCESS"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		os.Exit(runWith("test", []string{"service", "run"}, os.Stdout, os.Stderr, deps{
			env:     paths.OSEnv(),
			actions: []actions.Action{blockAction{}, touchAction{}},
		}))
	}
	os.Exit(m.Run())
}

// blockAction reports one event and waits until the process gets SIGUSR1 or the run is
// cancelled.
type blockAction struct{}

func (blockAction) Descriptor() actions.Descriptor {
	return actions.Descriptor{
		ID: "test block", Title: "Test block", Help: "Waits for SIGUSR1.",
		Target: actions.TargetGlobal, Effect: actions.EffectReadOnly,
	}
}

func (blockAction) Check(context.Context, actions.Request) error { return nil }

func (blockAction) Run(ctx context.Context, _ actions.Request, p actions.Progress) (actions.Result, error) {
	release := make(chan os.Signal, 1)
	signal.Notify(release, syscall.SIGUSR1)
	defer signal.Stop(release)
	p.Report(actions.Event{Step: "block", Message: "waiting"})
	select {
	case <-release:
		return actions.Result{Status: actions.StatusSucceeded, Summary: "released"}, nil
	case <-ctx.Done():
		return actions.Result{}, ctx.Err()
	}
}

// processLauncher starts the service as a separate process of this test binary, detached from
// the test's session like a launchd job.
type processLauncher struct {
	t        *testing.T
	home     string
	launches int
	unloads  int
	done     chan struct{}
	waitErr  error
}

func (l *processLauncher) Launch(context.Context) error {
	l.launches++
	log, err := os.Create(filepath.Join(l.home, "service.log"))
	if err != nil {
		return err
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{childEnv + "=1", "KTAGS_HOME=" + l.home, "PATH=" + os.Getenv("PATH")}
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	l.done = make(chan struct{})
	go func() {
		l.waitErr = cmd.Wait()
		_ = log.Close()
		close(l.done)
	}()
	l.t.Cleanup(func() {
		select {
		case <-l.done:
		default:
			_ = cmd.Process.Kill()
			<-l.done
		}
	})
	return nil
}

func (l *processLauncher) Unload(context.Context) error {
	l.unloads++
	return nil
}

// exited waits for the service process to end and returns its exit error.
func (l *processLauncher) exited(t *testing.T) error {
	t.Helper()
	select {
	case <-l.done:
		return l.waitErr
	case <-time.After(20 * time.Second):
		t.Fatal("the service process did not exit")
		return nil
	}
}

type harness struct {
	t        *testing.T
	home     string
	socket   string
	launcher *processLauncher
	deps     deps
	client   service.Client
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	// A short home keeps the socket path within the macOS limit.
	home, err := os.MkdirTemp("", "kt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	launcher := &processLauncher{t: t, home: home}
	socket := filepath.Join(home, "runtime", "service.sock")
	return &harness{
		t: t, home: home, socket: socket, launcher: launcher,
		client: service.Client{Socket: socket},
		deps: deps{
			env: paths.Env{Getenv: func(key string) string {
				if key == "KTAGS_HOME" {
					return home
				}
				return ""
			}},
			launcher: func(paths.Roots) service.Launcher { return launcher },
			timeout:  20 * time.Second,
		},
	}
}

func (h *harness) ktags(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runWith("test", args, &stdout, &stderr, h.deps)
	return code, stdout.String(), stderr.String()
}

func (h *harness) want(code int, args ...string) (string, string) {
	h.t.Helper()
	got, stdout, stderr := h.ktags(args...)
	if got != code {
		h.t.Fatalf("ktags %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), got, code, stdout, stderr)
	}
	return stdout, stderr
}

func contains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("output lacks %q:\n%s", want, text)
		}
	}
}

// startBlock starts a blocking run and waits until it has reported its first event, so the
// action is ready for SIGUSR1.
func (h *harness) startBlock() service.RunInfo {
	h.t.Helper()
	ctx := context.Background()
	info, err := h.client.Start(ctx, "test block", service.Target{Kind: "global"}, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		st, err := h.client.Status(ctx, info.ID)
		if err != nil {
			h.t.Fatal(err)
		}
		if st.LastEventID >= 1 {
			return info
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("run %s reported no event", info.ID)
		}
		<-time.After(10 * time.Millisecond)
	}
}

func (h *harness) startService() int {
	h.t.Helper()
	stdout, _ := h.want(0, "service", "start")
	var pid int
	if _, err := fmt.Sscanf(stdout, "ktags service started (pid %d)", &pid); err != nil {
		h.t.Fatalf("start output %q: %v", stdout, err)
	}
	return pid
}

// #12-K1 offline integration, with the service as a separate process: start is idempotent,
// status reports PID, protocol and socket, stop is refused while a run is active and cancels
// it only when asked. Losing the terminal (SIGHUP) stops neither the service nor the run.
func TestServiceLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	stdout, _ := h.want(1, "service", "status")
	contains(t, stdout, "ktags service is not running", h.socket, "next: ktags service start")

	pid := h.startService()
	stdout, _ = h.want(0, "service", "start")
	contains(t, stdout, fmt.Sprintf("ktags service is already running (pid %d)", pid))
	if h.launcher.launches != 1 {
		t.Fatalf("%d launches after two starts, want 1", h.launcher.launches)
	}
	stdout, _ = h.want(0, "service", "status")
	contains(t, stdout, fmt.Sprintf("ktags service is running (pid %d)", pid), "version   test", "protocol  1", "socket    "+h.socket, "runs      0 active")
	_, stderr := h.want(1, "service", "run")
	contains(t, stderr, fmt.Sprintf("another ktags service (pid %d) is already running", pid))

	info := h.startBlock()
	_, stderr = h.want(2, "service", "stop")
	contains(t, stderr, "refusing to stop the service", info.ID, "ktags service stop --cancel-runs")
	stdout, _ = h.want(0, "service", "status")
	contains(t, stdout, fmt.Sprintf("(pid %d)", pid), "runs      1 active")

	// SIGHUP first, then the release: had SIGHUP ended the process, the run could not succeed.
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
		t.Fatal(err)
	}
	final, err := h.client.Events(ctx, info.ID, 0, true, func(service.Event) error { return nil })
	if err != nil || final.Status != "succeeded" {
		t.Fatalf("run after SIGHUP: %+v, %v; want succeeded", final, err)
	}
	stdout, _ = h.want(0, "service", "status")
	contains(t, stdout, fmt.Sprintf("(pid %d)", pid), "runs      0 active")

	info = h.startBlock()
	stdout, _ = h.want(0, "service", "stop", "--cancel-runs")
	contains(t, stdout, "ktags service stopped", "cancelled run "+info.ID+" (test block), status cancelled")
	if err := h.launcher.exited(t); err != nil {
		t.Fatalf("service process: %v, want exit 0 so launchd does not restart it", err)
	}
	if h.launcher.unloads != 1 {
		t.Fatalf("%d unloads after the stop, want 1", h.launcher.unloads)
	}
	store, err := run.NewStore(filepath.Join(h.home, "state"), run.Options{Redact: mask.Mask})
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := store.Load(ctx, info.ID)
	if err != nil || recorded.Status != run.StatusCancelled || recorded.Result == nil || !strings.Contains(recorded.Result.Summary, "operator stopped") {
		t.Fatalf("recorded run %+v, %v; want cancelled by the operator's stop", recorded, err)
	}

	h.want(1, "service", "status")
	stdout, _ = h.want(0, "service", "stop")
	contains(t, stdout, "ktags service is not running")
}

// SIGTERM (launchctl bootout, logout) closes the service: the active run is cancelled with its
// result recorded, and the process exits 0.
func TestServiceStopsOnSIGTERM(t *testing.T) {
	h := newHarness(t)
	pid := h.startService()
	info := h.startBlock()
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := h.launcher.exited(t); err != nil {
		t.Fatalf("service process: %v, want exit 0", err)
	}
	store, err := run.NewStore(filepath.Join(h.home, "state"), run.Options{Redact: mask.Mask})
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := store.Load(context.Background(), info.ID)
	if err != nil || recorded.Status != run.StatusCancelled || recorded.Result == nil || !strings.Contains(recorded.Result.Summary, "service stopped") {
		t.Fatalf("recorded run %+v, %v; want cancelled because the service stopped", recorded, err)
	}
}

func TestPlatformLauncher(t *testing.T) {
	roots, err := paths.Resolve(paths.Env{Getenv: func(key string) string {
		if key == "KTAGS_HOME" {
			return "/opt/kt"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := platformLauncher("linux", os.Executable)(roots).(service.NoLauncher); !ok || l.GOOS != "linux" {
		t.Fatalf("linux launcher %#v, want NoLauncher", l)
	}
	// The definition names the real executable, not a symlink that an upgrade may move.
	link := filepath.Join(t.TempDir(), "ktags")
	if err := os.Symlink("/usr/bin/true", link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	exe := func() (string, error) { return link, nil }
	l, ok := platformLauncher("darwin", exe)(roots).(launchd.Agent)
	if !ok || l.Program != resolved {
		t.Fatalf("darwin launcher %#v, want launchd with program %s", l, resolved)
	}
	broken := func() (string, error) { return "", os.ErrNotExist }
	if l, ok := platformLauncher("darwin", broken)(roots).(launchd.Agent); !ok || l.Program != "" {
		t.Fatalf("launcher without an executable %#v, want an empty program that Launch refuses", l)
	}
}

// `ktags service run` refuses a runtime root that others can enter, before it listens.
func TestServiceRunRefusesLooseRoots(t *testing.T) {
	h := newHarness(t)
	runtime := filepath.Join(h.home, "runtime")
	if err := os.Mkdir(runtime, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runtime, 0o755); err != nil {
		t.Fatal(err)
	}
	_, stderr := h.want(1, "service", "run")
	contains(t, stderr, "group and others must have no access", "chmod 700")
	if _, err := os.Stat(h.socket); err == nil {
		t.Fatal("a socket appeared in the loose runtime root")
	}
}
