package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/nesiler/ktags/internal/service"
)

// span renders a duration the way tables show ages: 45s, 5m, 3h, 2d.
func span(d time.Duration) string {
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
	}
}

// ago is a relative time; a time after now is not an age.
func ago(now, t time.Time) string {
	switch {
	case t.IsZero():
		return "never"
	case t.After(now):
		return "in the future"
	}
	return span(now.Sub(t)) + " ago"
}

func agoPtr(now time.Time, t *time.Time) string {
	if t == nil {
		return "never"
	}
	return ago(now, *t)
}

// fit truncates s to w cells with … and pads it to exactly w, width-aware.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if n := ansi.StringWidth(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

func secs(n int64) time.Duration {
	return time.Duration(n) * time.Second
}

// runDuration is how long a run took, or has been running on the client clock.
func runDuration(r service.RunInfo, now time.Time) time.Duration {
	if r.Result != nil {
		return r.Result.FinishedAt.Sub(r.StartedAt)
	}
	return now.Sub(r.StartedAt)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func pluralRuns(n int) string {
	if n == 1 {
		return "1 run"
	}
	return fmt.Sprintf("%d runs", n)
}

// stateText is the symbol and word of a customer's attention state. Stale shows its age, and a
// customer whose scheduled checks were missed says missed, never ok.
func stateText(e service.FleetEntry, now time.Time) (string, tone) {
	switch e.State {
	case "ok":
		return "✓ ok", toneOK
	case "warn":
		return "⚠ warn", toneWarn
	case "fail":
		return "✗ fail", toneFail
	case "unreachable":
		return "✗ unreachable", toneFail
	case "invalid":
		return "! invalid", toneFail
	case "never":
		return "○ never", toneMuted
	case "stale":
		word := "stale"
		if e.Missed > 0 {
			word = "missed"
		}
		if e.MeasuredAt != nil {
			return fmt.Sprintf("○ %s · %s", word, span(now.Sub(*e.MeasuredAt))), toneMuted
		}
		return "○ " + word, toneMuted
	default:
		// A state this build does not know is never drawn as ok.
		return "? " + e.State, toneFail
	}
}

func envTone(environment string) tone {
	switch environment {
	case "prod":
		return toneFail
	case "staging":
		return toneWarn
	default:
		return toneMuted
	}
}

// checkText is the symbol and word of one check result; a failure of a warning-severity check
// warns.
func checkText(c service.CheckInfo) (string, tone) {
	switch {
	case c.Status == "ok":
		return "✓ ok", toneOK
	case c.Status == "not_configured":
		// Not configured weighs as a warning whatever the severity (core/health).
		return "⚠ not configured", toneWarn
	case c.Severity == "warning":
		return "⚠ " + warnWord(c.Status), toneWarn
	default:
		return "✗ " + failWord(c.Status), toneFail
	}
}

func warnWord(status string) string {
	if status == "failed" {
		return "warn"
	}
	return status
}

func failWord(status string) string {
	if status == "failed" {
		return "fail"
	}
	return status
}

func runText(status string) (string, tone) {
	switch status {
	case "succeeded":
		return "✓ succeeded", toneOK
	case "failed":
		return "✗ failed", toneFail
	case "cancelled":
		return "⚠ cancelled", toneWarn
	case "running":
		return "▶ running", toneAccent
	default:
		return "? " + status, toneFail
	}
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
