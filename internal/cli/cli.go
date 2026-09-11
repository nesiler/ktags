// Package cli adapts command-line input and output to the application runtime.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/service"
)

const unavailable = "ktags: nothing to run yet; see README.md and the open GitHub issues\n"

// Exit codes (docs/guides/development.md §3).
const (
	exitOK      = 0
	exitUsage   = 1
	exitRefused = 2
)

const serviceUsage = `usage: ktags service <command>

The ktags service runs actions and schedules for this operator; CLI and TUI are its clients.
Closing a terminal only detaches from a run; stopping the service is an explicit command.

commands:
  start                start the service in the background (macOS: launchd); does nothing
                       when it already runs
  status               show PID, version, protocol, socket and active runs; exit 1 when it
                       is not running
  stop [--cancel-runs] stop the service; refused with exit 2 while runs are active, unless
                       --cancel-runs cancels them and records their results first
  run                  run the service in the foreground (what launchd executes)

next: ktags service start
`

// Runtime is the application behaviour used by the command-line adapter.
type Runtime interface {
	Version() domain.Build
	// Service returns the control of the local ktags service. It fails when the ktags roots
	// cannot be resolved.
	Service() (ServiceControl, error)
}

// ServiceControl starts, inspects, stops and runs the local ktags service.
type ServiceControl interface {
	Start(ctx context.Context) (st service.Status, started bool, err error)
	Status(ctx context.Context) (service.Status, error)
	Stop(ctx context.Context, cancelRuns bool) (service.StopResult, error)
	// Run serves in the foreground until the service is stopped or signalled.
	Run(ctx context.Context, stdout io.Writer) error
}

// Run dispatches command-line arguments and returns the process exit code.
func Run(runtime Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		build := runtime.Version()
		_, _ = fmt.Fprintln(stdout, build.Name, build.Version)
		return exitOK
	}
	if len(args) > 0 && args[0] == "service" {
		return runService(runtime, args[1:], stdout, stderr)
	}

	_, _ = fmt.Fprint(stderr, unavailable)
	return 2
}

func runService(runtime Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		_, _ = fmt.Fprint(stdout, serviceUsage)
		return exitOK
	}
	cancelRuns := false
	switch {
	case len(args) == 1 && (args[0] == "start" || args[0] == "status" || args[0] == "run"):
	case len(args) == 1 && args[0] == "stop":
	case len(args) == 2 && args[0] == "stop" && args[1] == "--cancel-runs":
		cancelRuns = true
	default:
		_, _ = fmt.Fprintf(stderr, "ktags: unknown service command %q\n\n%s", strings.Join(args, " "), serviceUsage)
		return exitUsage
	}

	control, err := runtime.Service()
	if err != nil {
		return fail(stderr, err)
	}
	ctx := context.Background()
	switch args[0] {
	case "start":
		st, started, err := control.Start(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		if started {
			_, _ = fmt.Fprintf(stdout, "ktags service started (pid %d)\n", st.PID)
		} else {
			_, _ = fmt.Fprintf(stdout, "ktags service is already running (pid %d)\n", st.PID)
		}
		details(stdout, st)
		return exitOK
	case "status":
		st, err := control.Status(ctx)
		if err != nil {
			return fail(stderr, err)
		}
		if !st.Running {
			_, _ = fmt.Fprintf(stdout, "ktags service is not running\n  socket    %s\n  next: ktags service start\n", st.Socket)
			return exitUsage
		}
		_, _ = fmt.Fprintf(stdout, "ktags service is running (pid %d)\n", st.PID)
		details(stdout, st)
		return exitOK
	case "stop":
		return stop(ctx, control, cancelRuns, stdout, stderr)
	default:
		if err := control.Run(ctx, stdout); err != nil {
			return fail(stderr, err)
		}
		return exitOK
	}
}

func stop(ctx context.Context, control ServiceControl, cancelRuns bool, stdout, stderr io.Writer) int {
	res, err := control.Stop(ctx, cancelRuns)
	var pe *service.Error
	if errors.As(err, &pe) && pe.Code == service.CodeConflict {
		_, _ = fmt.Fprintf(stderr, "ktags: refusing to stop the service: %s\n  next: wait for the runs to end, or run: ktags service stop --cancel-runs\n", pe.Message)
		return exitRefused
	}
	if err != nil {
		return fail(stderr, err)
	}
	if !res.WasRunning {
		_, _ = fmt.Fprintln(stdout, "ktags service is not running")
		return exitOK
	}
	_, _ = fmt.Fprintln(stdout, "ktags service stopped")
	for _, r := range res.Cancelled {
		_, _ = fmt.Fprintf(stdout, "  cancelled run %s (%s), status %s\n", r.ID, r.Action, r.Status)
	}
	return exitOK
}

func details(w io.Writer, st service.Status) {
	_, _ = fmt.Fprintf(w, "  version   %s\n  protocol  %d\n  socket    %s\n  runs      %d active\n", st.Version, st.Protocol, st.Socket, st.ActiveRuns)
}

func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "ktags: %v\n", err)
	return exitUsage
}
