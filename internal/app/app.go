// Package app is the executable's composition root.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/cli"
	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/core/health"
	coreruntime "github.com/nesiler/ktags/internal/core/runtime"
	"github.com/nesiler/ktags/internal/doctor"
	"github.com/nesiler/ktags/internal/launchd"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/run"
	"github.com/nesiler/ktags/internal/service"
	"github.com/nesiler/ktags/internal/tui"
)

// Run wires the application and executes the command-line adapter.
func Run(version string, args []string, stdout, stderr io.Writer) int {
	return runWith(version, args, stdout, stderr, deps{
		env:       paths.OSEnv(),
		launcher:  platformLauncher(goruntime.GOOS, os.Executable),
		healthFor: realHealth,
		stdin:     os.Stdin,
		terminal:  isTerminal(os.Stdin),
		screen:    isTerminal(os.Stdin) && isTerminal(os.Stdout),
		interrupt: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, os.Interrupt)
		},
	})
}

// isTerminal reports whether f is a terminal, so a confirmation can be typed into it. A
// character device such as /dev/null is not one.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// deps are what the composition root takes from the machine; tests replace them.
type deps struct {
	env      paths.Env
	launcher func(paths.Roots) service.Launcher
	// actions are registered in the service in addition to the built-in ones.
	actions []actions.Action
	// health composes the health engine and the health action into the service with the given
	// checks; tests set it. When nil, healthFor builds it from the real adapters; when both are
	// nil the service measures nothing.
	health *healthConfig
	// healthFor composes the real connection adapters over the resolved roots; Run sets it.
	healthFor func(roots paths.Roots, env paths.Env) (*healthConfig, error)
	// timeout bounds the lifecycle waits; zero takes the service default.
	timeout time.Duration
	// stdin answers confirmations when terminal is set; interrupt detaches a followed run.
	stdin     io.Reader
	terminal  bool
	interrupt func(context.Context) (context.Context, context.CancelFunc)
	// screen is set when stdin and stdout are terminals: `ktags` alone then opens the TUI.
	screen bool
	// lookPath finds executables for doctor; nil means exec.LookPath.
	lookPath func(file string) (string, error)
	// tui shows the TUI; nil means tui.Run.
	tui func(ctx context.Context, opts tui.Options, out io.Writer) error
}

func runWith(version string, args []string, stdout, stderr io.Writer, d deps) int {
	build := domain.Build{Name: "ktags", Version: version}
	if len(args) == 0 && d.screen {
		return runTUI(build, stdout, stderr, d)
	}
	versionAction := actions.NewVersion(build)
	streams := cli.Streams{In: d.stdin, Out: stdout, Err: stderr, Terminal: d.terminal, Interrupt: d.interrupt}
	return cli.Run(context.Background(), appRuntime{Runtime: coreruntime.New(versionAction), build: build, deps: d}, args, streams)
}

// runTUI opens the TUI on the service. Like an interactive CLI command it starts the service
// when none answers; a failed start opens the TUI in the service unavailable state with the
// measured cause, where Enter retries.
func runTUI(build domain.Build, stdout, stderr io.Writer, d deps) int {
	roots, err := paths.Resolve(d.env)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "ktags:", err)
		return 1
	}
	ctx := context.Background()
	control := serviceControl{roots: roots, build: build, deps: d}
	_, _, startErr := control.Start(ctx)
	show := d.tui
	if show == nil {
		show = tui.Run
	}
	opts := tui.Options{
		Client:   service.Client{Socket: service.SocketPath(roots.Runtime.Path)},
		Version:  build.Version,
		StartErr: startErr,
	}
	if err := show(ctx, opts, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, "ktags: the TUI failed:", err)
		return 1
	}
	return 0
}

// platformLauncher is launchd on macOS. Other systems have no service manager integration yet
// (ADR-0001); their start refuses and names the foreground command.
func platformLauncher(goos string, executable func() (string, error)) func(paths.Roots) service.Launcher {
	return func(roots paths.Roots) service.Launcher {
		if goos != "darwin" {
			return service.NoLauncher{GOOS: goos}
		}
		// A failure leaves the program empty, which Launch refuses with an explanation.
		program, err := executable()
		if err == nil {
			if resolved, rerr := filepath.EvalSymlinks(program); rerr == nil {
				program = resolved
			}
		}
		return launchd.New(program, roots)
	}
}

type appRuntime struct {
	coreruntime.Runtime
	build domain.Build
	deps  deps
}

func (a appRuntime) Service() (cli.ServiceControl, error) {
	roots, err := paths.Resolve(a.deps.env)
	if err != nil {
		return nil, err
	}
	return serviceControl{roots: roots, build: a.build, deps: a.deps}, nil
}

// Doctor runs the doctor checks on the resolved roots. It asks the service for its status and
// nothing else, so it never starts the service or creates a root.
func (a appRuntime) Doctor(ctx context.Context) (doctor.Report, error) {
	roots, err := paths.Resolve(a.deps.env)
	if err != nil {
		return doctor.Report{}, err
	}
	control := serviceControl{roots: roots, build: a.build, deps: a.deps}
	env := doctor.Env{
		Roots:         roots,
		GOOS:          goruntime.GOOS,
		GOARCH:        goruntime.GOARCH,
		UID:           os.Getuid(),
		LookPath:      a.deps.lookPath,
		ServiceStatus: control.Status,
	}
	if env.LookPath == nil {
		env.LookPath = exec.LookPath
	}
	switch launcher := a.deps.launcher(roots).(type) {
	case launchd.Agent:
		env.AgentDefinition = launcher.Definition
		env.AgentStale = launcher.Stale
	case service.NoLauncher:
		env.Foreground = true
	}
	return doctor.Run(ctx, env), nil
}

type serviceControl struct {
	roots paths.Roots
	build domain.Build
	deps  deps
}

func (c serviceControl) lifecycle() service.Lifecycle {
	return service.Lifecycle{
		Socket:   service.SocketPath(c.roots.Runtime.Path),
		Launcher: c.deps.launcher(c.roots),
		Timeout:  c.deps.timeout,
	}
}

// Start launches the service; the launched `ktags service run` creates and checks the roots.
func (c serviceControl) Start(ctx context.Context) (service.Status, bool, error) {
	return c.lifecycle().Start(ctx)
}

func (c serviceControl) Client() cli.Client {
	return service.Client{Socket: service.SocketPath(c.roots.Runtime.Path)}
}

func (c serviceControl) Status(ctx context.Context) (service.Status, error) {
	return c.lifecycle().Status(ctx)
}

func (c serviceControl) Stop(ctx context.Context, cancelRuns bool) (service.StopResult, error) {
	return c.lifecycle().Stop(ctx, cancelRuns)
}

// Run serves until a client stops the service or the process gets SIGTERM or SIGINT. SIGHUP is
// ignored: losing the terminal that started the service detaches, it does not stop runs.
func (c serviceControl) Run(ctx context.Context, stdout io.Writer) error {
	signal.Ignore(syscall.SIGHUP)
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()

	if err := paths.Ensure(ctx, c.roots); err != nil {
		return err
	}
	store, err := run.NewStore(c.roots.State.Path, run.Options{Redact: mask.Mask})
	if err != nil {
		return err
	}
	registered := c.deps.actions
	opts := service.Options{
		Store:    store,
		DataRoot: c.roots.Data.Path,
		Build:    c.build,
		Redact:   mask.Mask,
		// launchd writes the service's stdout to the service log.
		Logger: slog.New(slog.NewTextHandler(stdout, nil)),
	}
	cfg := c.deps.health
	if cfg == nil && c.deps.healthFor != nil {
		if cfg, err = c.deps.healthFor(c.roots, c.deps.env); err != nil {
			return err
		}
	}
	var engine *health.Engine
	if cfg != nil {
		if engine, err = newHealthEngine(ctx, cfg, c.roots.Data.Path); err != nil {
			return err
		}
		registered = append(slices.Clone(registered), actions.NewHealth(engine))
		opts.Fleet = healthSource{views: engine.Views, connection: cfg.connection}
		opts.Clock = cfg.clock
	}
	if opts.Registry, err = actions.NewRegistry(registered...); err != nil {
		return err
	}
	srv, err := service.Listen(ctx, c.roots.Runtime.Path, opts)
	if err != nil {
		return err
	}
	// The schedule starts once the socket is ours and ends with the service.
	if engine != nil {
		healthCtx, stopHealth := context.WithCancel(context.Background())
		engine.Start(healthCtx)
		defer engine.Wait()
		defer stopHealth()
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()
	socket := service.SocketPath(c.roots.Runtime.Path)
	_, _ = fmt.Fprintf(stdout, "ktags service %s listening on %s (pid %d)\n", c.build.Version, socket, os.Getpid())

	var serveErr error
	waitServe := true
	select {
	case <-ctx.Done():
	case <-srv.StopRequested():
	case serveErr = <-served:
		waitServe = false
	}
	closeErr := srv.Close()
	if waitServe {
		serveErr = <-served
	}
	_, _ = fmt.Fprintln(stdout, "ktags service stopped")
	return errors.Join(serveErr, closeErr)
}
