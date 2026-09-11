package probe

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/nesiler/ktags/internal/core/connect"
	"github.com/nesiler/ktags/internal/core/fleet"
	"github.com/nesiler/ktags/internal/core/health"
)

// checkTimeout bounds one health check. It covers the connection checks a check runs in
// order (ssh.sudo also reaches and logs in), each bounded by the runner's own timeout.
const checkTimeout = 45 * time.Second

// RunbookInventoryInvalid is the runbook of a customer whose inventory does not give a target.
const RunbookInventoryInvalid = "inventory-invalid"

// Target is what the probe measures for one customer.
type Target struct {
	// Nodes are reached over SSH.
	Nodes []connect.SSHTarget
	// SSHUnsupported, when set, says why this build cannot reach the nodes (for example jump
	// access); the SSH checks then report not configured.
	SSHUnsupported string
	// Kube and Rancher are nil when the inventory does not configure them.
	Kube    *connect.APITarget
	Rancher *connect.APITarget
}

// Options configure a Probe.
type Options struct {
	// Runner runs the connection checks; required.
	Runner *connect.Runner
	// Target reads a customer's target; required. It is called on every check run, so an edited
	// inventory is measured without a restart.
	Target func(ctx context.Context, customer string) (Target, error)
	// Now stamps the connection state; required.
	Now func() time.Time
}

// Probe builds the health checks and keeps the connection state they measured.
type Probe struct {
	runner *connect.Runner
	target func(ctx context.Context, customer string) (Target, error)
	now    func() time.Time

	mu    sync.Mutex
	reach map[string]*reach
}

// reach is the latest endpoint evidence of one customer.
type reach struct {
	sshAt       time.Time
	sshMeasured bool
	sshReached  bool
	sshDetail   string

	// kubeConfigured is the latest target's: without a Kubernetes API, SSH alone decides.
	kubeConfigured bool
	kubeAt         time.Time
	kubeMeasured   bool
	kubeReached    bool
	kubeDetail     string
}

// New checks the options.
func New(opts Options) (*Probe, error) {
	if opts.Runner == nil || opts.Target == nil || opts.Now == nil {
		return nil, errors.New("probe: a runner, a target source and a clock are required")
	}
	return &Probe{runner: opts.Runner, target: opts.Target, now: opts.Now, reach: map[string]*reach{}}, nil
}

// Checks are the health checks, in the order the engine runs them. The SSH checks and the
// Kubernetes endpoint are critical; Rancher and every check that needs a secret warn.
func (p *Probe) Checks() []health.Check {
	check := func(name string, severity health.Severity, runbook string, run func(ctx context.Context, customer string) error) health.Check {
		return health.Check{Name: name, Severity: severity, Timeout: checkTimeout, Runbook: runbook, Run: run}
	}
	return []health.Check{
		check("ssh.endpoint", health.SeverityCritical, connect.RunbookSSHUnreachable, p.ssh("ssh.endpoint")),
		check("ssh.auth", health.SeverityCritical, connect.RunbookSSHUnreachable, p.ssh("ssh.auth")),
		check("ssh.sudo", health.SeverityCritical, connect.RunbookSSHUnreachable, p.ssh("ssh.sudo")),
		check("kube.endpoint", health.SeverityCritical, connect.RunbookKubeAPIUnreachable, p.api("kube", "kube.endpoint")),
		check("kube.auth", health.SeverityWarning, connect.RunbookKubeAPIUnreachable, p.api("kube", "kube.auth")),
		check("kube.authz", health.SeverityWarning, connect.RunbookKubeAPIUnreachable, p.api("kube", "kube.authz")),
		check("rancher.endpoint", health.SeverityWarning, connect.RunbookRancherUnreachable, p.api("rancher", "rancher.endpoint")),
		check("rancher.auth", health.SeverityWarning, connect.RunbookRancherUnreachable, p.api("rancher", "rancher.auth")),
		check("rancher.authz", health.SeverityWarning, connect.RunbookRancherUnreachable, p.api("rancher", "rancher.authz")),
	}
}

func (p *Probe) ssh(check string) func(ctx context.Context, customer string) error {
	return func(ctx context.Context, customer string) error {
		t, err := p.target(ctx, customer)
		if err != nil {
			return refused(ctx, err)
		}
		if t.SSHUnsupported != "" {
			if check == "ssh.endpoint" {
				p.recordSSH(customer, t, nil)
			}
			return &finding{runbook: connect.RunbookSSHNotConfigured, detail: t.SSHUnsupported, notConfigured: true}
		}
		results := make([]connect.Result, len(t.Nodes))
		errs := make([]error, len(t.Nodes))
		var wg sync.WaitGroup
		for i, node := range t.Nodes {
			wg.Go(func() {
				rs, err := p.runner.SSHThrough(ctx, node, check)
				if err != nil {
					errs[i] = err
					return
				}
				results[i] = outcome(rs)
			})
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := errors.Join(errs...); err != nil {
			return refused(ctx, err)
		}
		if check == "ssh.endpoint" {
			p.recordSSH(customer, t, results)
		}
		return summarise(results)
	}
}

func (p *Probe) api(layer, check string) func(ctx context.Context, customer string) error {
	return func(ctx context.Context, customer string) error {
		t, err := p.target(ctx, customer)
		if err != nil {
			return refused(ctx, err)
		}
		target, run, field := t.Kube, p.runner.KubernetesThrough, "ktags_cluster.kube_api_url"
		if layer == "rancher" {
			target, run, field = t.Rancher, p.runner.RancherThrough, "ktags_cluster.rancher_url"
		}
		if target == nil {
			if check == "kube.endpoint" {
				p.recordKube(customer, nil)
			}
			runbook := connect.RunbookKubeNotConfigured
			if layer == "rancher" {
				runbook = connect.RunbookRancherNotConfigured
			}
			return &finding{runbook: runbook, detail: field + " is not set in the customer inventory", notConfigured: true}
		}
		rs, err := run(ctx, *target, check)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return refused(ctx, err)
		}
		res := outcome(rs)
		if check == "kube.endpoint" {
			p.recordKube(customer, &res)
		}
		return summarise([]connect.Result{res})
	}
}

// outcome is the result of the last check run; a skipped one takes the status and kind of the
// check that blocked it, so a check after a failure fails and one after a not configured check
// is not configured. It is never ok.
func outcome(rs []connect.Result) connect.Result {
	last := rs[len(rs)-1]
	if last.Status != connect.StatusSkipped {
		return last
	}
	for _, r := range rs {
		if r.Status == connect.StatusFailed || r.Status == connect.StatusNotConfigured {
			last.Status, last.Kind, last.Next = r.Status, r.Kind, r.Next
			return last
		}
	}
	last.Status = connect.StatusFailed
	return last
}

// summarise turns the results of one check across targets into the health check's error: a
// failure outranks not configured; nil only when every result is ok.
func summarise(results []connect.Result) error {
	var failed, unconfigured []connect.Result
	for _, r := range results {
		switch r.Status {
		case connect.StatusOK:
		case connect.StatusNotConfigured:
			unconfigured = append(unconfigured, r)
		default:
			failed = append(failed, r)
		}
	}
	switch {
	case len(failed) > 0:
		return &finding{runbook: failed[0].Runbook, detail: describe(failed)}
	case len(unconfigured) > 0:
		return &finding{runbook: unconfigured[0].Runbook, detail: describe(unconfigured), notConfigured: true}
	}
	return nil
}

// describe names each target with its measured fact and next step. The runner has redacted
// every part.
func describe(results []connect.Result) string {
	parts := make([]string, 0, len(results))
	for _, r := range results {
		part := r.Target + ": "
		if r.Kind != "" {
			part += string(r.Kind) + ": "
		}
		parts = append(parts, part+r.Detail+"; next: "+r.Next)
	}
	return strings.Join(parts, " | ")
}

// refused is a target the inventory cannot give or the runner refused: every check of the
// customer fails with the inventory runbook.
func refused(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	detail := err.Error()
	var ce *connect.Error
	if errors.As(err, &ce) {
		detail = ce.Problem + "; next: " + ce.Next
	}
	return &finding{runbook: RunbookInventoryInvalid, detail: detail}
}

// finding is a health check error that names its runbook.
type finding struct {
	runbook       string
	detail        string
	notConfigured bool
}

func (f *finding) Error() string   { return f.detail }
func (f *finding) Runbook() string { return f.runbook }

// Unwrap marks a not configured finding for the health engine.
func (f *finding) Unwrap() error {
	if f.notConfigured {
		return health.ErrNotConfigured
	}
	return nil
}

func (p *Probe) entry(customer string) *reach {
	r := p.reach[customer]
	if r == nil {
		r = &reach{}
		p.reach[customer] = r
	}
	return r
}

func (p *Probe) recordSSH(customer string, t Target, results []connect.Result) {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.entry(customer)
	r.sshAt, r.sshMeasured, r.sshReached, r.sshDetail = now, false, false, ""
	r.kubeConfigured = t.Kube != nil
	var failed []connect.Result
	for _, res := range results {
		switch res.Status {
		case connect.StatusOK:
			r.sshMeasured, r.sshReached = true, true
		case connect.StatusFailed:
			r.sshMeasured = true
			failed = append(failed, res)
		}
	}
	if len(failed) > 0 {
		r.sshDetail = describe(failed)
	}
}

func (p *Probe) recordKube(customer string, res *connect.Result) {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.entry(customer)
	r.kubeAt, r.kubeConfigured, r.kubeMeasured, r.kubeReached, r.kubeDetail = now, res != nil, false, false, ""
	if res == nil {
		return
	}
	switch res.Status {
	case connect.StatusOK:
		r.kubeMeasured, r.kubeReached = true, true
	case connect.StatusFailed:
		r.kubeMeasured = true
		r.kubeDetail = describe([]connect.Result{*res})
	}
}

// Connection is the customer's latest connection state. It reads memory only. A customer is
// reachable when any measured endpoint answered and unreachable only when every measured one
// failed: each node's SSH endpoint and, when configured, the Kubernetes API. A configured API
// that has not been measured yet leaves the state unknown rather than guessing.
func (p *Probe) Connection(customer string) fleet.ConnectionState {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.reach[customer]
	if r == nil {
		return fleet.ConnectionState{State: fleet.ConnUnknown}
	}
	at := r.sshAt
	if r.kubeAt.After(at) {
		at = r.kubeAt
	}
	switch {
	case r.sshReached || r.kubeReached:
		return fleet.ConnectionState{State: fleet.ConnReachable, MeasuredAt: at}
	case !r.sshMeasured && !r.kubeMeasured, r.kubeConfigured && !r.kubeMeasured:
		return fleet.ConnectionState{State: fleet.ConnUnknown}
	}
	detail := r.sshDetail
	if r.kubeDetail != "" {
		detail = strings.TrimPrefix(detail+" | "+r.kubeDetail, " | ")
	}
	return fleet.ConnectionState{State: fleet.ConnUnreachable, Detail: detail, MeasuredAt: at}
}
