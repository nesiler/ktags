package actions

import (
	"context"
	"fmt"

	"github.com/nesiler/ktags/internal/core/health"
)

// HealthRunner measures customers now; *health.Engine implements it.
type HealthRunner interface {
	Run(ctx context.Context, customers ...string) ([]health.Report, error)
}

// Health runs the health checks against one customer as an ordinary run, so the operator can
// detach from it and read its result later. It changes nothing and takes no lock.
type Health struct {
	engine HealthRunner
}

// NewHealth creates the health action on engine.
func NewHealth(engine HealthRunner) Health {
	return Health{engine: engine}
}

// Descriptor describes the health action.
func (Health) Descriptor() Descriptor {
	return Descriptor{
		ID:     "health",
		Title:  "Run health checks",
		Help:   "Runs every health check against the customer now and caches the result in the fleet summary.",
		Target: TargetCustomer,
		Effect: EffectReadOnly,
	}
}

// Check has nothing to verify: a health run is read-only. A customer outside the engine's fleet
// is refused by Run.
func (Health) Check(context.Context, Request) error { return nil }

// Run measures the customer and reports each check as an event. A failing critical check fails
// the run: a red health result is never reported as a success.
func (h Health) Run(ctx context.Context, req Request, progress Progress) (Result, error) {
	customer := req.Target.Customer
	progress.Report(Event{Step: "health", Message: "measuring " + customer})
	reports, err := h.engine.Run(ctx, customer)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, err
		}
		// The run summary is the error text, so the next step is part of the problem.
		const next = "the service measures the customers it found when it started; next: ktags service stop, then ktags service start"
		return Result{}, &Error{Action: "health", Problem: err.Error() + "; " + next, Hint: next}
	}
	if len(reports) != 1 {
		return Result{}, &Error{Action: "health", Problem: fmt.Sprintf("got %d reports for one customer", len(reports)), Hint: "report this as a bug"}
	}
	r := reports[0]
	passed := 0
	for _, c := range r.Checks {
		if c.Status == health.StatusOK {
			passed++
		}
		msg := fmt.Sprintf("%s %s (%s)", c.Check, c.Status, c.Severity)
		if c.Detail != "" {
			msg += ": " + c.Detail
		}
		progress.Report(Event{Step: "check", Message: msg})
	}
	result := Result{Status: StatusSucceeded, Summary: fmt.Sprintf("%s health %s: %d of %d checks ok", customer, r.Health, passed, len(r.Checks))}
	if r.Health == health.HealthFail {
		result.Status = StatusFailed
	}
	return result, nil
}
