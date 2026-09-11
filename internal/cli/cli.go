// Package cli adapts command-line input and output to the application runtime. It is a cobra
// command tree over the local ktags service (ADR-0001): every command except `service` is a
// client of the service. Output is human text, or with --json one versioned JSON document on
// stdout while all human text goes to stderr (docs/guides/development.md §3).
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/service"
)

const rootUsage = `usage: ktags <command> [--json]

ktags installs and operates customer clusters through the local ktags service.
Human output goes to stdout. With --json, stdout holds exactly one JSON document
{"schema":"ktags.cli/v1","kind":"<kind>","data":{...}} and all other text goes to stderr.

commands:
  service    start, inspect and stop the local ktags service
  customer   list the customers in the inventory
  fleet      show which customers need attention, from the service's cached state
  action     discover and run actions
  run        list, watch and cancel runs
  version    print the ktags version

Commands other than service start the ktags service when it is not running. With --json
they never start it; they fail with exit 1 instead, so a document never describes a service
the command itself started.

exit codes: 0 success · 1 usage, validation or environment · 2 confirmation refused, or a
conflict with the service's state (stop with active runs, cancel of a finished run) · 3 the
run failed or was cancelled

next: ktags action list
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
	// Client talks to the service on its socket.
	Client() Client
}

// Client is the service protocol as the CLI uses it; service.Client implements it.
type Client interface {
	Customers(ctx context.Context) ([]service.CustomerInfo, error)
	Fleet(ctx context.Context) (service.FleetInfo, error)
	Actions(ctx context.Context) ([]service.ActionInfo, error)
	Runs(ctx context.Context) ([]service.RunInfo, error)
	Start(ctx context.Context, action string, target service.Target, args map[string]any) (service.RunInfo, error)
	Cancel(ctx context.Context, id string) (service.RunInfo, error)
	Status(ctx context.Context, id string) (service.RunInfo, error)
	Events(ctx context.Context, id string, after uint64, follow bool, fn func(service.Event) error) (service.RunInfo, error)
}

// Streams are the process's standard streams and terminal facts.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	// Terminal reports whether In is an interactive terminal, so a confirmation can be asked.
	Terminal bool
	// Interrupt returns a context that ends when the operator interrupts (Ctrl-C). It is used
	// while following a run, where an interrupt detaches. Nil means no interrupt source.
	Interrupt func(context.Context) (context.Context, context.CancelFunc)
}

// env is the state of one invocation.
type env struct {
	ctx     context.Context
	runtime Runtime
	s       Streams
	in      *bufio.Reader
	json    bool
	yes     bool
	version bool
}

// Run dispatches command-line arguments and returns the process exit code.
func Run(ctx context.Context, runtime Runtime, args []string, streams Streams) int {
	if streams.In == nil {
		streams.In = strings.NewReader("")
	}
	if streams.Interrupt == nil {
		streams.Interrupt = context.WithCancel
	}
	// --json is known before parsing, so even a parse error is reported as a JSON document.
	e := &env{ctx: ctx, runtime: runtime, s: streams, in: bufio.NewReader(streams.In), json: slices.Contains(args, "--json")}
	root := e.root()
	root.SetArgs(args)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)
	if err := root.ExecuteContext(ctx); err != nil {
		return e.report(err)
	}
	return exitOK
}

func (e *env) root() *cobra.Command {
	root := &cobra.Command{
		Use:               "ktags",
		Long:              rootUsage,
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usageError(cmd, fmt.Sprintf("unknown command %q", args[0]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if e.version {
				return e.printVersion()
			}
			return usageError(cmd, "the ktags TUI needs a terminal on stdin and stdout; use one of the commands")
		},
	}
	root.PersistentFlags().BoolVar(&e.json, "json", e.json, "print one JSON document on stdout; human text goes to stderr")
	root.Flags().BoolVar(&e.version, "version", false, "print the version")
	// Help is text too: with --json it goes to stderr and stdout still holds one document.
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_, _ = io.WriteString(e.text(), cmd.Long)
		_ = e.emit("help", helpDoc{Usage: cmd.Long})
	})
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError(cmd, err.Error())
	})
	root.AddCommand(e.serviceCmd(), e.customerCmd(), e.fleetCmd(), e.actionCmd(), e.runCmd(), &cobra.Command{
		Use:  "version",
		Long: "usage: ktags version [--json]\n\nprints the ktags name and version.\n--json data: {\"name\",\"version\"}\n",
		Args: positional(),
		RunE: func(*cobra.Command, []string) error { return e.printVersion() },
	})
	return root
}

func (e *env) printVersion() error {
	build := e.runtime.Version()
	_, _ = fmt.Fprintln(e.text(), build.Name, build.Version)
	return e.emit("version", versionDoc{Name: build.Name, Version: build.Version})
}

// group is a command whose only job is to hold subcommands. It prints its usage for `help` and
// refuses anything else with it.
func group(use, long string, children ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:  use,
		Long: long,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case len(args) == 0:
				return usageError(cmd, "missing "+use+" command")
			case len(args) == 1 && args[0] == "help":
				return cmd.Help()
			}
			return usageError(cmd, fmt.Sprintf("unknown %s command %q", use, strings.Join(args, " ")))
		},
	}
	cmd.AddCommand(children...)
	return cmd
}

// positional accepts exactly the named positional arguments.
func positional(names ...string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) < len(names):
			return usageError(cmd, "missing "+strings.Join(names[len(args):], " "))
		case len(args) > len(names):
			return usageError(cmd, fmt.Sprintf("unexpected argument %q", args[len(names)]))
		}
		return nil
	}
}

// client connects to the service. An interactive command starts the service when it is not
// running. A --json command never does: its document would then describe a service this
// command created, which a script cannot tell apart from the one it meant to ask.
func (e *env) client() (Client, error) {
	control, err := e.runtime.Service()
	if err != nil {
		return nil, err
	}
	st, err := control.Status(e.ctx)
	if err != nil {
		return nil, err
	}
	if !st.Running {
		if e.json {
			return nil, &exitError{exit: exitUsage, code: "service_not_running", message: "the ktags service is not running; --json commands do not start it", hint: "ktags service start"}
		}
		st, started, err := control.Start(e.ctx)
		if err != nil {
			return nil, err
		}
		if started {
			_, _ = fmt.Fprintf(e.s.Err, "ktags: started the ktags service (pid %d)\n", st.PID)
		}
	}
	return control.Client(), nil
}

// text is where human text goes: stdout, or stderr when stdout is reserved for JSON.
func (e *env) text() io.Writer {
	if e.json {
		return e.s.Err
	}
	return e.s.Out
}
