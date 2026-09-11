package cli

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/core/fleet"
	"github.com/nesiler/ktags/internal/service"
)

const fleetUsage = `usage: ktags fleet <command> [--json]

The fleet summary shows every customer's cached state in the ktags service: connection,
aggregate health, component versions, when it was last measured, freshness and active runs.
Listing reads the service's cache; it measures nothing and makes no network request.

Customers that need attention come first, in this order: invalid record, fail, unreachable,
stale, never measured, warn, ok; within a state, by customer. Ages are relative to the
service clock ("now" in the JSON document).

commands:
  list    show the fleet summary, then the evidence of every check

--json data: list {"now","customers":[{"id","name","environment","cluster","problem","state","runbook",
"connection":{"state","detail","measured_at"},"health","measured_at","trigger","stale",
"interval_seconds","missed","checking","versions":{},"active_runs":[{"id","action"}],
"checks":[{"check","severity","status","detail","runbook","measured_at","duration_ms"}]}]}
"runbook" names the runbook page of the check behind the state; it is absent when all is ok.

next: ktags customer list
`

func (e *env) fleetCmd() *cobra.Command {
	return group("fleet", fleetUsage, &cobra.Command{
		Use:  "list",
		Long: "usage: ktags fleet list [--json]\n\n" + fleetUsage,
		Args: positional(),
		RunE: func(*cobra.Command, []string) error { return e.fleetList() },
	})
}

// fleetList prints the service's fleet summary in the order the service sent it. The human
// text and the JSON document come from the one reply.
func (e *env) fleetList() error {
	client, err := e.client()
	if err != nil {
		return err
	}
	info, err := client.Fleet(e.ctx)
	if err != nil {
		return err
	}
	w := e.text()
	_, _ = fmt.Fprintln(w, fleetSummary(info.Customers))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  STATE\tCUSTOMER\tENV\tCONNECTION\tHEALTH\tRUNBOOK\tMEASURED\tFRESHNESS\tVERSIONS\tACTIVE")
	for _, c := range info.Customers {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			c.State, c.ID, dash(c.Environment), c.Connection.State, c.Health, dash(c.Runbook),
			measured(info.Now, c.MeasuredAt), freshness(c), versions(c.Versions), active(c))
	}
	_ = tw.Flush()
	evidence := false
	for _, c := range info.Customers {
		lines := evidenceLines(c)
		if len(lines) == 0 {
			continue
		}
		if !evidence {
			_, _ = fmt.Fprintln(w, "evidence:")
			evidence = true
		}
		for _, line := range lines {
			_, _ = fmt.Fprintln(w, "  "+line)
		}
	}
	_, _ = fmt.Fprintln(w, "next: ktags action list")
	return e.emit("fleet.list", info)
}

// fleetSummary is the first line: the customer count and how many are in each state, most
// urgent first. A state this build does not know is counted under its own name at the end.
func fleetSummary(customers []service.FleetEntry) string {
	counts := map[string]int{}
	var unknown []string
	for _, c := range customers {
		if counts[c.State] == 0 && !slices.Contains(fleet.States, fleet.State(c.State)) {
			unknown = append(unknown, c.State)
		}
		counts[c.State]++
	}
	var parts []string
	for _, s := range fleet.States {
		if n := counts[string(s)]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	for _, s := range unknown {
		parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
	}
	line := fmt.Sprintf("%d customer(s)", len(customers))
	if len(parts) > 0 {
		line += ": " + strings.Join(parts, ", ")
	}
	return line
}

// evidenceLines are the facts behind one customer's state: a refused record, the connection
// failure and every check with its status, severity, measured time and detail.
func evidenceLines(c service.FleetEntry) []string {
	var lines []string
	if c.Problem != "" {
		lines = append(lines, fmt.Sprintf("%s record refused: %s", c.ID, c.Problem))
	}
	if c.Connection.Detail != "" {
		lines = append(lines, fmt.Sprintf("%s connection %s (measured %s): %s", c.ID, c.Connection.State, stampPtr(c.Connection.MeasuredAt), c.Connection.Detail))
	}
	for _, k := range c.Checks {
		line := fmt.Sprintf("%s %s %s (%s, measured %s, %dms)", c.ID, k.Check, k.Status, k.Severity, stamp(k.MeasuredAt), k.DurationMS)
		if k.Runbook != "" {
			line += " [runbook " + k.Runbook + "]"
		}
		if k.Detail != "" {
			line += ": " + k.Detail
		}
		lines = append(lines, line)
	}
	return lines
}

// measured is the age of a measurement relative to the service clock.
func measured(now time.Time, at *time.Time) string {
	if at == nil {
		return "never"
	}
	d := now.Sub(*at)
	switch {
	case d < 0:
		return "in the future"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}

func freshness(c service.FleetEntry) string {
	if !c.Stale {
		// A fresh measurement resets the missed count; the service sends none.
		return "fresh"
	}
	if c.Missed > 0 {
		return fmt.Sprintf("stale, %d missed", c.Missed)
	}
	return "stale"
}

func versions(v map[string]string) string {
	if len(v) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(v))
	for _, component := range slices.Sorted(maps.Keys(v)) {
		parts = append(parts, component+"="+v[component])
	}
	return strings.Join(parts, ",")
}

func active(c service.FleetEntry) string {
	var parts []string
	if c.Checking {
		parts = append(parts, "health check")
	}
	for _, r := range c.ActiveRuns {
		parts = append(parts, r.ID+" ("+r.Action+")")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func stampPtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return stamp(*t)
}
