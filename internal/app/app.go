// Package app is the executable's composition root.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"syscall"
	"time"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/cli"
	"github.com/nesiler/ktags/internal/core/domain"
	coreruntime "github.com/nesiler/ktags/internal/core/runtime"
	"github.com/nesiler/ktags/internal/launchd"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/run"
	"github.com/nesiler/ktags/internal/service"
)

// Run wires the application and executes the command-line adapter.
func Run(version string, args []string, stdout, stderr io.Writer) int {
	return runWith(version, args, stdout, stderr, deps{
		env:      paths.OSEnv(),
		launcher: platformLauncher(goruntime.GOOS, os.Executable),
	})
}

// deps are what the composition root takes from the machine; tests replace them.
type deps struct {
	env      paths.Env
	launcher func(paths.Roots) service.Launcher
	// actions are registered in the service in addition to the built-in ones.
	actions []actions.Action
	// timeout bounds the lifecycle waits; zero takes the service default.
	timeout time.Duration
}

func runWith(version string, args []string, stdout, stderr io.Writer, d deps) int {
	build := domain.Build{Name: "ktags", Version: version}
	versionAction := actions.NewVersion(build)
	return cli.Run(appRuntime{Runtime: coreruntime.New(versionAction), build: build, deps: d}, args, stdout, stderr)
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
	registry, err := actions.NewRegistry(c.deps.actions...)
	if err != nil {
		return err
	}
	srv, err := service.Listen(ctx, c.roots.Runtime.Path, service.Options{
		Registry: registry,
		Store:    store,
		DataRoot: c.roots.Data.Path,
		Build:    c.build,
		Redact:   mask.Mask,
	})
	if err != nil {
		return err
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
