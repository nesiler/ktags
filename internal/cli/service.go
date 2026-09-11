package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/service"
)

const serviceUsage = `usage: ktags service <command> [--json]

The ktags service runs actions and schedules for this operator; CLI and TUI are its clients.
Closing a terminal only detaches from a run; stopping the service is an explicit command.

commands:
  start                start the service in the background (macOS: launchd); does nothing
                       when it already runs
  status               show PID, version, protocol, socket and active runs; exit 1 when it
                       is not running
  stop [--cancel-runs] stop the service; refused with exit 2 while runs are active, unless
                       --cancel-runs cancels them and records their results first
  run                  run the service in the foreground (what launchd executes); no --json

--json data: start {"started","service"} · status {"running","socket","protocol","pid",
"service","version","active_runs"} · stop {"was_running","cancelled":[run]}

next: ktags service start
`

type serviceDoc struct {
	Running    bool   `json:"running"`
	Socket     string `json:"socket"`
	Protocol   int    `json:"protocol,omitempty"`
	PID        int    `json:"pid,omitempty"`
	Service    string `json:"service,omitempty"`
	Version    string `json:"version,omitempty"`
	ActiveRuns int    `json:"active_runs"`
}

type startDoc struct {
	Started bool       `json:"started"`
	Service serviceDoc `json:"service"`
}

type stopDoc struct {
	WasRunning bool              `json:"was_running"`
	Cancelled  []service.RunInfo `json:"cancelled"`
}

func toServiceDoc(st service.Status) serviceDoc {
	return serviceDoc{Running: st.Running, Socket: st.Socket, Protocol: st.Protocol, PID: st.PID, Service: st.Service, Version: st.Version, ActiveRuns: st.ActiveRuns}
}

func (e *env) serviceCmd() *cobra.Command {
	var cancelRuns bool
	stop := &cobra.Command{
		Use:  "stop",
		Long: "usage: ktags service stop [--cancel-runs] [--json]\n\n" + serviceUsage,
		Args: positional(),
		RunE: func(*cobra.Command, []string) error { return e.serviceStop(cancelRuns) },
	}
	stop.Flags().BoolVar(&cancelRuns, "cancel-runs", false, "cancel active runs and record their results before stopping")
	return group("service", serviceUsage,
		&cobra.Command{
			Use:  "start",
			Long: "usage: ktags service start [--json]\n\n" + serviceUsage,
			Args: positional(),
			RunE: func(*cobra.Command, []string) error { return e.serviceStart() },
		},
		&cobra.Command{
			Use:  "status",
			Long: "usage: ktags service status [--json]\n\n" + serviceUsage,
			Args: positional(),
			RunE: func(*cobra.Command, []string) error { return e.serviceStatus() },
		},
		stop,
		&cobra.Command{
			Use:  "run",
			Long: "usage: ktags service run\n\n" + serviceUsage,
			Args: positional(),
			RunE: func(cmd *cobra.Command, _ []string) error {
				// The foreground service writes its log lines to stdout, which would break the
				// one-document contract of --json.
				if e.json {
					return usageError(cmd, "ktags service run has no JSON output")
				}
				control, err := e.runtime.Service()
				if err != nil {
					return err
				}
				return control.Run(e.ctx, e.s.Out)
			},
		},
	)
}

func (e *env) serviceStart() error {
	control, err := e.runtime.Service()
	if err != nil {
		return err
	}
	st, started, err := control.Start(e.ctx)
	if err != nil {
		return err
	}
	w := e.text()
	if started {
		_, _ = fmt.Fprintf(w, "ktags service started (pid %d)\n", st.PID)
	} else {
		_, _ = fmt.Fprintf(w, "ktags service is already running (pid %d)\n", st.PID)
	}
	details(w, st)
	return e.emit("service.start", startDoc{Started: started, Service: toServiceDoc(st)})
}

func (e *env) serviceStatus() error {
	control, err := e.runtime.Service()
	if err != nil {
		return err
	}
	st, err := control.Status(e.ctx)
	if err != nil {
		return err
	}
	w := e.text()
	if !st.Running {
		_, _ = fmt.Fprintf(w, "ktags service is not running\n  socket    %s\n  next: ktags service start\n", st.Socket)
		if err := e.emit("service.status", toServiceDoc(st)); err != nil {
			return err
		}
		return silentExit(exitUsage)
	}
	_, _ = fmt.Fprintf(w, "ktags service is running (pid %d)\n", st.PID)
	details(w, st)
	return e.emit("service.status", toServiceDoc(st))
}

func (e *env) serviceStop(cancelRuns bool) error {
	control, err := e.runtime.Service()
	if err != nil {
		return err
	}
	res, err := control.Stop(e.ctx, cancelRuns)
	var pe *service.Error
	if errors.As(err, &pe) && pe.Code == service.CodeConflict {
		return &exitError{exit: exitRefused, code: pe.Code, message: "refusing to stop the service: " + pe.Message, hint: "wait for the runs to end, or run: ktags service stop --cancel-runs"}
	}
	if err != nil {
		return err
	}
	w := e.text()
	switch {
	case !res.WasRunning:
		_, _ = fmt.Fprintln(w, "ktags service is not running")
	default:
		_, _ = fmt.Fprintln(w, "ktags service stopped")
		for _, r := range res.Cancelled {
			_, _ = fmt.Fprintf(w, "  cancelled run %s (%s), status %s\n", r.ID, r.Action, r.Status)
		}
	}
	return e.emit("service.stop", stopDoc{WasRunning: res.WasRunning, Cancelled: nonNil(res.Cancelled)})
}

func details(w io.Writer, st service.Status) {
	_, _ = fmt.Fprintf(w, "  version   %s\n  protocol  %d\n  socket    %s\n  runs      %d active\n", st.Version, st.Protocol, st.Socket, st.ActiveRuns)
}
