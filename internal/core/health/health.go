package health

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/nesiler/ktags/internal/core/schedule"
)

const (
	// defaultTimeout bounds one check unless Check.Timeout says otherwise.
	defaultTimeout = 30 * time.Second
	// defaultConcurrency is how many customers are measured at once unless Options.Concurrency
	// says otherwise.
	defaultConcurrency = 4
	// defaultGrace matches the schedule package: a result stays fresh for one interval plus
	// this, or half the interval when that is shorter.
	defaultGrace = time.Minute
)

// Severity says how much a failed check weighs in the customer's health.
type Severity string

// Severities.
const (
	// SeverityCritical: a failure makes the customer's health fail.
	SeverityCritical Severity = "critical"
	// SeverityWarning: a failure makes the customer's health warn.
	SeverityWarning Severity = "warning"
)

// Status is the outcome of one check.
type Status string

// Statuses. Only ok is green.
const (
	StatusOK     Status = "ok"
	StatusFailed Status = "failed"
	// StatusTimeout: the check gave no answer within its timeout.
	StatusTimeout Status = "timeout"
)

// Health is a customer's aggregate state.
type Health string

// Health values. Unknown means no result exists; it is never green.
const (
	HealthOK      Health = "ok"
	HealthWarn    Health = "warn"
	HealthFail    Health = "fail"
	HealthUnknown Health = "unknown"
)

// Trigger says what started a run.
type Trigger string

// Triggers.
const (
	TriggerManual    Trigger = "manual"
	TriggerScheduled Trigger = "scheduled"
)

// Check is one named, read-only check.
type Check struct {
	// Name identifies the check, for example "ssh.endpoint"; unique within an Engine.
	Name     string
	Severity Severity
	// Timeout bounds one run of the check; zero means 30s.
	Timeout time.Duration
	// Run measures one customer. A nil error passes; the error text is the measured fact.
	Run func(ctx context.Context, customer string) error
}

// CheckResult is one check's outcome for one customer. Detail has passed the redactor.
type CheckResult struct {
	Check    string
	Severity Severity
	Status   Status
	Detail   string
	// MeasuredAt is when the check started; Duration is how long it ran.
	MeasuredAt time.Time
	Duration   time.Duration
}

// Report is the outcome of one run against one customer.
type Report struct {
	Customer string
	Trigger  Trigger
	// MeasuredAt is when the run started.
	MeasuredAt time.Time
	Health     Health
	Checks     []CheckResult
}

// Options configure an Engine.
type Options struct {
	// Clock drives the schedule and every timeout; required.
	Clock schedule.Clock
	// Redact masks secret material in check details; required (use mask.Mask).
	Redact func(string) string
	// Checks run in order against every customer; at least one.
	Checks []Check
	// Customers is the fleet.
	Customers []string
	// Interval is the global default time between two scheduled runs; required.
	Interval time.Duration
	// Intervals overrides Interval per customer. Every key must be in Customers.
	Intervals map[string]time.Duration
	// Concurrency is how many customers are measured at once; zero means 4.
	Concurrency int
}

// Engine runs the checks and caches the latest report of each customer.
type Engine struct {
	clock     schedule.Clock
	redact    func(string) string
	checks    []Check
	customers []string
	intervals map[string]time.Duration
	slots     chan struct{}
	sched     *schedule.Scheduler

	mu      sync.Mutex
	reports map[string]Report
	running map[string]int
}

// New validates the options. It refuses a missing clock or redactor, no checks, a check without
// a name, run or known severity, a repeated or empty name, a negative timeout or concurrency, an
// interval that is not positive and an interval override for a customer outside the fleet.
func New(opts Options) (*Engine, error) {
	switch {
	case opts.Clock == nil:
		return nil, errors.New("health: no clock")
	case opts.Redact == nil:
		return nil, errors.New("health: no redactor; check details are never kept unmasked")
	case len(opts.Checks) == 0:
		return nil, errors.New("health: no checks; an engine without checks would report nothing as healthy")
	case opts.Interval <= 0:
		return nil, fmt.Errorf("health: the default interval must be positive, got %s", opts.Interval)
	case opts.Concurrency < 0:
		return nil, fmt.Errorf("health: concurrency must not be negative, got %d", opts.Concurrency)
	}
	names := map[string]bool{}
	for _, c := range opts.Checks {
		switch {
		case c.Name == "":
			return nil, errors.New("health: a check has no name")
		case names[c.Name]:
			return nil, fmt.Errorf("health: check %q is defined twice", c.Name)
		case c.Run == nil:
			return nil, fmt.Errorf("health: check %q has nothing to run", c.Name)
		case c.Severity != SeverityCritical && c.Severity != SeverityWarning:
			return nil, fmt.Errorf("health: check %q has severity %q, want critical or warning", c.Name, c.Severity)
		case c.Timeout < 0:
			return nil, fmt.Errorf("health: check %q has a negative timeout", c.Name)
		}
		names[c.Name] = true
	}
	intervals := map[string]time.Duration{}
	for _, customer := range opts.Customers {
		switch {
		case customer == "":
			return nil, errors.New("health: a customer has no name")
		case intervals[customer] != 0:
			return nil, fmt.Errorf("health: customer %q is listed twice", customer)
		}
		intervals[customer] = opts.Interval
	}
	for customer, interval := range opts.Intervals {
		switch {
		case intervals[customer] == 0:
			return nil, fmt.Errorf("health: interval set for %q, which is not in the fleet", customer)
		case interval <= 0:
			return nil, fmt.Errorf("health: interval of %q must be positive, got %s", customer, interval)
		}
		intervals[customer] = interval
	}
	concurrency := opts.Concurrency
	if concurrency == 0 {
		concurrency = defaultConcurrency
	}
	e := &Engine{
		clock:     opts.Clock,
		redact:    opts.Redact,
		checks:    slices.Clone(opts.Checks),
		customers: slices.Sorted(slices.Values(opts.Customers)),
		intervals: intervals,
		slots:     make(chan struct{}, concurrency),
		reports:   map[string]Report{},
		running:   map[string]int{},
	}
	jobs := make([]schedule.Job, 0, len(e.customers))
	for _, customer := range e.customers {
		jobs = append(jobs, schedule.Job{Name: jobName(customer), Interval: intervals[customer], Run: func(ctx context.Context) error {
			e.measure(ctx, customer, TriggerScheduled)
			return nil
		}})
	}
	sched, err := schedule.New(opts.Clock, jobs, schedule.Options{})
	if err != nil {
		return nil, fmt.Errorf("health: %w", err)
	}
	e.sched = sched
	return e, nil
}

func jobName(customer string) string { return "health " + customer }

// Start runs every customer at its interval until ctx ends; the first slot is now.
func (e *Engine) Start(ctx context.Context) { e.sched.Start(ctx) }

// Wait returns when the schedule has ended.
func (e *Engine) Wait() { e.sched.Wait() }

// Run measures the given customers now, at most Options.Concurrency at once, and returns their
// reports in the given order. A customer the context cut short has no report; Run then also
// returns the context's error. It refuses a customer outside the fleet before measuring any.
func (e *Engine) Run(ctx context.Context, customers ...string) ([]Report, error) {
	for _, c := range customers {
		if _, ok := e.intervals[c]; !ok {
			return nil, fmt.Errorf("health: customer %q is not in the fleet", c)
		}
	}
	reports := make([]*Report, len(customers))
	var wg sync.WaitGroup
	for i, c := range customers {
		wg.Go(func() {
			if r, ok := e.measure(ctx, c, TriggerManual); ok {
				reports[i] = &r
			}
		})
	}
	wg.Wait()
	out := make([]Report, 0, len(customers))
	for _, r := range reports {
		if r != nil {
			out = append(out, *r)
		}
	}
	if len(out) < len(customers) {
		return out, ctx.Err()
	}
	return out, nil
}

// measure runs every check against customer in one fleet slot. It reports false when ctx ended
// first: a run cut short is not a result and is not cached.
func (e *Engine) measure(ctx context.Context, customer string, trigger Trigger) (Report, bool) {
	select {
	case e.slots <- struct{}{}:
	case <-ctx.Done():
		return Report{}, false
	}
	defer func() { <-e.slots }()

	e.mu.Lock()
	e.running[customer]++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.running[customer]--
		e.mu.Unlock()
	}()

	r := Report{Customer: customer, Trigger: trigger, MeasuredAt: e.clock.Now(), Health: HealthOK}
	for _, c := range e.checks {
		res := e.runCheck(ctx, c, customer)
		if ctx.Err() != nil {
			return Report{}, false
		}
		if res.Status != StatusOK {
			if c.Severity == SeverityCritical {
				r.Health = HealthFail
			} else if r.Health == HealthOK {
				r.Health = HealthWarn
			}
		}
		r.Checks = append(r.Checks, res)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	// Runs of one customer may overlap; an older measurement never replaces a newer one.
	if old, ok := e.reports[customer]; !ok || !r.MeasuredAt.Before(old.MeasuredAt) {
		e.reports[customer] = r
	}
	return r, true
}

// runCheck runs c and stops waiting at its timeout, even when c ignores its context. A check
// that ignores the context keeps its goroutine until it returns, but no longer holds the slot.
func (e *Engine) runCheck(ctx context.Context, c Check, customer string) CheckResult {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	res := CheckResult{Check: c.Name, Severity: c.Severity, MeasuredAt: e.clock.Now()}
	done := make(chan error, 1)
	go func() { done <- c.Run(cctx, customer) }()
	select {
	case err := <-done:
		res.Status = StatusOK
		if err != nil {
			res.Status = StatusFailed
			res.Detail = e.redact(err.Error())
		}
	case <-e.clock.After(timeout):
		res.Status = StatusTimeout
		res.Detail = fmt.Sprintf("no answer within %s", timeout)
	case <-ctx.Done():
		// The caller reads ctx and discards this result.
	}
	res.Duration = e.clock.Now().Sub(res.MeasuredAt)
	return res
}

// View is what a client shows for one customer: the latest result, when it was measured and
// whether it is stale.
type View struct {
	Customer string
	// Health is HealthUnknown when the customer has never been measured.
	Health Health
	// MeasuredAt is zero when the customer has never been measured.
	MeasuredAt time.Time
	Trigger    Trigger
	// Stale is set when there is no result, the result is older than one interval plus the grace
	// window, or it was measured after now (the clock went back).
	Stale    bool
	Interval time.Duration
	// Next is the next scheduled slot; zero before Start.
	Next time.Time
	// Missed counts the scheduled slots that could not run; the latest group spans MissedFrom
	// to MissedTo.
	Missed     int
	MissedFrom time.Time
	MissedTo   time.Time
	Running    bool
	Checks     []CheckResult
}

// Views returns one view per customer in the fleet, sorted by customer, evaluated now.
func (e *Engine) Views() []View {
	now := e.clock.Now()
	states := map[string]schedule.State{}
	for _, st := range e.sched.States() {
		states[st.Name] = st
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]View, 0, len(e.customers))
	for _, customer := range e.customers {
		interval := e.intervals[customer]
		v := View{Customer: customer, Health: HealthUnknown, Stale: true, Interval: interval, Running: e.running[customer] > 0}
		if r, ok := e.reports[customer]; ok {
			v.Health, v.MeasuredAt, v.Trigger = r.Health, r.MeasuredAt, r.Trigger
			v.Checks = slices.Clone(r.Checks)
			v.Stale = now.Before(r.MeasuredAt) || now.Sub(r.MeasuredAt) > interval+grace(interval)
		}
		if st, ok := states[jobName(customer)]; ok {
			v.Next, v.Missed, v.MissedFrom, v.MissedTo = st.Next, st.Missed, st.MissedFrom, st.MissedTo
		}
		out = append(out, v)
	}
	return out
}

func grace(interval time.Duration) time.Duration { return min(defaultGrace, interval/2) }

// String is the one-line display of a view. It always names when the result was measured (or
// that it never was) and whether it is fresh or stale.
func (v View) String() string {
	measured := "never measured"
	if !v.MeasuredAt.IsZero() {
		measured = "measured " + v.MeasuredAt.UTC().Format(time.RFC3339)
	}
	freshness := "fresh"
	if v.Stale {
		freshness = "stale"
	}
	s := fmt.Sprintf("%s %s %s %s", v.Customer, v.Health, measured, freshness)
	if v.Missed > 0 {
		s += fmt.Sprintf(", %d missed slots %s to %s", v.Missed, v.MissedFrom.UTC().Format(time.RFC3339), v.MissedTo.UTC().Format(time.RFC3339))
	}
	if v.Running {
		s += ", running"
	}
	return s
}
