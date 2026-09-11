package cli

import (
	"errors"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/service"
)

const runUsage = `usage: ktags run <command> [--json]

A run is one execution of an action in the ktags service. It outlives the terminal that
started it; leaving only detaches.

commands:
  list                     list every recorded run, oldest first
  status <run>             show the state and result of one run
  watch <run> [--after N]  replay events after event N, follow to the final result;
                           exit 0 when the run succeeded, 3 when it failed or was cancelled
  cancel <run> [--yes]     cancel an active run after confirmation, then show its result

--json data: list {"runs":[run]} · status {"run"} · watch {"run","events"} (kind run.result)
· cancel {"run","events"} (kind run.cancel) · {"run_id","last_event_id"} after an interrupt
(kind run.detached)

next: ktags run list
`

type runListDoc struct {
	Runs []service.RunInfo `json:"runs"`
}

type runDoc struct {
	Run    service.RunInfo `json:"run"`
	Events []service.Event `json:"events,omitempty"`
}

type detachedDoc struct {
	RunID       string `json:"run_id"`
	LastEventID uint64 `json:"last_event_id"`
}

func (e *env) runCmd() *cobra.Command {
	var after uint64
	watch := &cobra.Command{
		Use:  "watch",
		Long: "usage: ktags run watch <run> [--after N] [--json]\n\n" + runUsage,
		Args: positional("<run>"),
		RunE: func(_ *cobra.Command, args []string) error {
			client, err := e.client()
			if err != nil {
				return err
			}
			return e.follow(client, args[0], after, true)
		},
	}
	watch.Flags().Uint64Var(&after, "after", 0, "replay only events after this event ID")
	cancel := &cobra.Command{
		Use:  "cancel",
		Long: "usage: ktags run cancel <run> [--yes] [--json]\n\n" + runUsage,
		Args: positional("<run>"),
		RunE: func(_ *cobra.Command, args []string) error { return e.runCancel(args[0]) },
	}
	cancel.Flags().BoolVar(&e.yes, "yes", false, "answer the [y/N] confirmation; never replaces a typed name")
	return group("run", runUsage,
		&cobra.Command{
			Use:  "list",
			Long: "usage: ktags run list [--json]\n\n" + runUsage,
			Args: positional(),
			RunE: func(*cobra.Command, []string) error { return e.runList() },
		},
		&cobra.Command{
			Use:  "status",
			Long: "usage: ktags run status <run> [--json]\n\n" + runUsage,
			Args: positional("<run>"),
			RunE: func(_ *cobra.Command, args []string) error { return e.runStatus(args[0]) },
		},
		watch,
		cancel,
	)
}

func (e *env) runList() error {
	client, err := e.client()
	if err != nil {
		return err
	}
	runs, err := client.Runs(e.ctx)
	if err != nil {
		return err
	}
	w := e.text()
	_, _ = fmt.Fprintf(w, "%d run(s)\n", len(runs))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  RUN\tSTATUS\tACTION\tTARGET\tSTARTED")
	for _, r := range runs {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", r.ID, r.Status, r.Action, targetString(r.Target), stamp(r.StartedAt))
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(w, "next: ktags run status <run>")
	return e.emit("run.list", runListDoc{Runs: nonNil(runs)})
}

func (e *env) runStatus(id string) error {
	client, err := e.client()
	if err != nil {
		return err
	}
	info, err := client.Status(e.ctx, id)
	if err != nil {
		return err
	}
	w := e.text()
	_, _ = fmt.Fprintf(w, "run %s %s\n  action   %s\n  target   %s\n  started  %s\n  events   %d\n", info.ID, info.Status, info.Action, targetString(info.Target), stamp(info.StartedAt), info.LastEventID)
	if info.Result != nil {
		_, _ = fmt.Fprintf(w, "  result   %s: %s (finished %s)\n", info.Result.Status, info.Result.Summary, stamp(info.Result.FinishedAt))
	}
	if info.Problem != "" {
		_, _ = fmt.Fprintf(w, "  problem  %s\n", info.Problem)
	}
	if info.Status == "running" {
		_, _ = fmt.Fprintf(w, "next: ktags run watch %s\n", info.ID)
	}
	return e.emit("run.status", runDoc{Run: info})
}

// runCancel confirms like a change on the run's customer (docs/guides/ui.md §7: cancelling a
// prod run needs the typed name), cancels, then follows the run to its final state.
func (e *env) runCancel(id string) error {
	client, err := e.client()
	if err != nil {
		return err
	}
	info, err := client.Status(e.ctx, id)
	if err != nil {
		return err
	}
	if info.Status != "running" {
		return &exitError{exit: exitRefused, code: service.CodeConflict, message: fmt.Sprintf("run %s is %s; only an active run can be cancelled", id, info.Status), hint: "ktags run status " + id}
	}
	level := actions.DangerConfirm
	if info.Target.Customer != "" {
		customer, err := e.customer(client, info.Target.Customer)
		var ee *exitError
		switch {
		// A customer that left the inventory has no known environment; treat it as prod.
		case errors.As(err, &ee):
			level = actions.DangerTypeName
		case err != nil:
			return err
		case customer.Environment != "staging" && customer.Environment != "test":
			level = actions.DangerTypeName
		}
	}
	summary := fmt.Sprintf("cancel run %s (%s on %s)", id, info.Action, targetString(info.Target))
	if err := e.confirm(level, summary, info.Target.Customer); err != nil {
		return err
	}
	cancelled, err := client.Cancel(e.ctx, id)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(e.text(), "cancel requested for run %s\n", id)
	return e.follow(client, id, cancelled.LastEventID, false)
}

// follow streams the events of run id after the cursor until the run ends and prints its
// final state. An interrupt detaches and names the reconnect command; the run continues. With
// byResult the exit code is the run's outcome, otherwise 0 (cancel reports, it does not judge).
func (e *env) follow(client Client, id string, after uint64, byResult bool) error {
	ctx, stop := e.s.Interrupt(e.ctx)
	defer stop()
	w := e.text()
	last := after
	var events []service.Event
	final, err := client.Events(ctx, id, after, true, func(ev service.Event) error {
		last = ev.ID
		events = append(events, ev)
		if ev.Step != "" {
			_, _ = fmt.Fprintf(w, "  %d %s: %s\n", ev.ID, ev.Step, ev.Message)
		} else {
			_, _ = fmt.Fprintf(w, "  %d %s\n", ev.ID, ev.Message)
		}
		return nil
	})
	if err != nil && ctx.Err() != nil {
		_, _ = fmt.Fprintf(e.s.Err, "detached from run %s at event %d; it continues in the ktags service\n  next: ktags run watch %s --after %d\n", id, last, id, last)
		return e.emit("run.detached", detachedDoc{RunID: id, LastEventID: last})
	}
	if err != nil {
		x := classify(err)
		if x.code == service.CodeUnavailable {
			x.message = fmt.Sprintf("lost run %s after event %d: %s", id, last, x.message)
			x.hint = fmt.Sprintf("check the service with ktags service status, then run: ktags run watch %s --after %d", id, last)
		}
		return x
	}
	kind := "run.result"
	if !byResult {
		kind = "run.cancel"
	}
	switch {
	case final.Result != nil:
		_, _ = fmt.Fprintf(w, "run %s %s: %s\n", final.ID, final.Status, final.Result.Summary)
	case final.Problem != "":
		_, _ = fmt.Fprintf(w, "run %s %s: %s\n", final.ID, final.Status, final.Problem)
	default:
		_, _ = fmt.Fprintf(w, "run %s %s\n", final.ID, final.Status)
	}
	if err := e.emit(kind, runDoc{Run: final, Events: events}); err != nil {
		return err
	}
	if byResult && final.Status != "succeeded" {
		return silentExit(exitFailed)
	}
	return nil
}

func targetString(t service.Target) string {
	switch {
	case t.Customer == "":
		return t.Kind
	case t.Node != "":
		return t.Customer + "/" + t.Node
	default:
		return t.Customer
	}
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}
