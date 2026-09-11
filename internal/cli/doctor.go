package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/doctor"
)

const doctorUsage = `usage: ktags doctor [--json]

Checks this laptop for what ktags needs: the platform, the ktags roots, the ktags service
and its launchd agent, the inventory and the tools ktags runs. Every check that is not ok
names exactly one command that fixes it; a command that fixes several checks is printed
once with the checks it fixes. Doctor only reads: it never starts the service,
takes no lock and changes nothing.

exit codes: 0 every check is ok or warns · 4 a check fails · 1 the roots cannot be resolved

--json data: {"status":"ok|warn|fail","checks":[{"id","title","status","evidence","fix"}]}
("fix" is absent on ok checks)

next: run each fix, then ktags doctor again
`

func (e *env) doctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "doctor",
		Long: doctorUsage,
		Args: positional(),
		RunE: func(*cobra.Command, []string) error { return e.doctor() },
	}
}

func (e *env) doctor() error {
	report, err := e.runtime.Doctor(e.ctx)
	if err != nil {
		return err
	}
	var failed, warned int
	for _, c := range report.Checks {
		switch c.Status {
		case doctor.StatusFail:
			failed++
		case doctor.StatusWarn:
			warned++
		}
	}
	w := e.text()
	if failed+warned == 0 {
		_, _ = fmt.Fprintf(w, "doctor: all %d checks ok\n", len(report.Checks))
	} else {
		_, _ = fmt.Fprintf(w, "doctor: %d failing, %d warning of %d checks\n", failed, warned, len(report.Checks))
	}
	// An identical fix is printed once, at its first row, with every row it applies to; later
	// rows point back to it. --json keeps one fix per row.
	rows := map[string][]string{}
	for _, c := range report.Checks {
		if c.Fix != "" {
			rows[c.Fix] = append(rows[c.Fix], c.ID)
		}
	}
	first := map[string]string{}
	for _, c := range report.Checks {
		_, _ = fmt.Fprintf(w, "  %-4s  %s  %s\n        %s\n", c.Status, c.ID, c.Title, c.Evidence)
		switch ids := rows[c.Fix]; {
		case c.Fix == "":
		case first[c.Fix] != "":
			_, _ = fmt.Fprintf(w, "        fix: as for %s\n", first[c.Fix])
		case len(ids) > 1:
			first[c.Fix] = c.ID
			_, _ = fmt.Fprintf(w, "        fix: %s\n        (fixes %s)\n", c.Fix, strings.Join(ids, ", "))
		default:
			first[c.Fix] = c.ID
			_, _ = fmt.Fprintf(w, "        fix: %s\n", c.Fix)
		}
	}
	if failed+warned > 0 {
		_, _ = fmt.Fprintln(w, "next: run each fix, then ktags doctor again")
	}
	if err := e.emit("doctor", report); err != nil {
		return err
	}
	if report.Status == doctor.StatusFail {
		return silentExit(exitCheckFailed)
	}
	return nil
}
