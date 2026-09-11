package doctor

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/service"
)

// Status is the outcome of one check. Only fail makes doctor red.
type Status string

// Statuses.
const (
	StatusOK   Status = "ok"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

// Result is one row of the report. A result that is not ok carries exactly one fix command.
type Result struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status Status `json:"status"`
	// Evidence is one masked line: the measured fact.
	Evidence string `json:"evidence"`
	Fix      string `json:"fix,omitempty"`
}

// Report is the outcome of every registered check, in registry order.
type Report struct {
	// Status is the worst status of the checks.
	Status Status   `json:"status"`
	Checks []Result `json:"checks"`
}

// Env is what the checks measure. The composition root fills it; tests fake it.
type Env struct {
	Roots  paths.Roots
	GOOS   string
	GOARCH string
	// UID is the operator's user ID; the roots must belong to it.
	UID int
	// LookPath finds an executable on PATH.
	LookPath func(file string) (string, error)
	// ServiceStatus reports the service without starting it (service.Lifecycle.Status).
	ServiceStatus func(ctx context.Context) (service.Status, error)
	// AgentDefinition is the launchd job definition of the service; empty when the platform
	// starts the service without launchd.
	AgentDefinition string
	// Foreground is set when this platform cannot start the service in the background, so the
	// fix is `ktags service run`.
	Foreground bool
}

// Check is one registered check. Run returns one result per measured thing; a result without
// an ID or title takes the check's. A check that does not apply on this laptop returns none.
type Check struct {
	ID    string
	Title string
	// Order places the check in the report; ties are ordered by ID.
	Order int
	Run   func(ctx context.Context, env Env) []Result
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)

// registry holds the checks every component registers from its own file.
var registry []Check

func register(c Check) {
	registry = add(registry, c)
}

// add returns checks with c appended. A broken definition is a programming error found at
// start-up, so it panics.
func add(checks []Check, c Check) []Check {
	switch {
	case !idPattern.MatchString(c.ID):
		panic(fmt.Sprintf("doctor: check ID %q is not lower-case words joined by dots", c.ID))
	case strings.TrimSpace(c.Title) == "":
		panic(fmt.Sprintf("doctor: check %q has no title", c.ID))
	case c.Run == nil:
		panic(fmt.Sprintf("doctor: check %q has no Run", c.ID))
	}
	for _, existing := range checks {
		if existing.ID == c.ID {
			panic(fmt.Sprintf("doctor: check %q is registered more than once", c.ID))
		}
	}
	return append(checks, c)
}

// Checks returns the registered checks in report order.
func Checks() []Check {
	return ordered(registry)
}

func ordered(checks []Check) []Check {
	out := append([]Check(nil), checks...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Run runs every registered check.
func Run(ctx context.Context, env Env) Report {
	return run(ctx, env, registry)
}

func run(ctx context.Context, env Env, checks []Check) Report {
	// The service rows share one answer: a silent service then costs its timeout once.
	if status := env.ServiceStatus; status != nil {
		var once sync.Once
		var st service.Status
		var err error
		env.ServiceStatus = func(ctx context.Context) (service.Status, error) {
			once.Do(func() { st, err = status(ctx) })
			return st, err
		}
	}
	report := Report{Status: StatusOK, Checks: []Result{}}
	for _, c := range ordered(checks) {
		for _, r := range c.Run(ctx, env) {
			r = finish(c, r)
			report.Checks = append(report.Checks, r)
			report.Status = worse(report.Status, r.Status)
		}
	}
	return report
}

var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// finish fills a result from its check and makes it safe to print: evidence and fix are
// masked and kept to one line, and an ok result has no fix.
func finish(c Check, r Result) Result {
	if r.ID == "" {
		r.ID = c.ID
	}
	if r.Title == "" {
		r.Title = c.Title
	}
	r.Evidence = lineBreaks.Replace(mask.Mask(r.Evidence))
	r.Fix = lineBreaks.Replace(mask.Mask(r.Fix))
	if r.Status == StatusOK {
		r.Fix = ""
	}
	return r
}

func worse(a, b Status) Status {
	rank := map[Status]int{StatusOK: 0, StatusWarn: 1, StatusFail: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// quote makes s one word for a POSIX shell, so a fix can be pasted as it is printed.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// firstLine drops the "next:" lines ktags errors append; a check names its own fix.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
