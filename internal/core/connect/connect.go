package connect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"time"
)

const (
	// defaultTimeout bounds one check unless Options.Timeout says otherwise.
	defaultTimeout = 10 * time.Second
	// defaultFreshFor is how long a result counts as current unless Options.FreshFor says otherwise.
	defaultFreshFor = 5 * time.Minute
)

// Kind classifies a failed check, so that the operator sees which layer broke.
type Kind string

// Failure kinds.
const (
	KindDNS              Kind = "dns"
	KindTCP              Kind = "tcp"
	KindHostKey          Kind = "host-key"
	KindSSHAuth          Kind = "ssh-auth"
	KindSudo             Kind = "sudo"
	KindKubeAPI          Kind = "kube-api"
	KindKubeAuth         Kind = "kube-auth"
	KindKubeForbidden    Kind = "kube-forbidden"
	KindRancherAPI       Kind = "rancher-api"
	KindRancherAuth      Kind = "rancher-auth"
	KindRancherForbidden Kind = "rancher-forbidden"
	// KindTimeout: the check did not finish within its timeout.
	KindTimeout Kind = "timeout"
	// KindCancelled: the caller cancelled the check.
	KindCancelled Kind = "cancelled"
	// KindUnclassified: the adapter gave no kind that belongs to the check.
	KindUnclassified Kind = "unclassified"
)

// Failure is the error an adapter returns for a classified failure.
type Failure struct {
	Kind Kind
	// Detail is the measured fact, for example "lookup srv-1.example.test: no such host". It is
	// redacted before it reaches a Result.
	Detail string
}

func (f *Failure) Error() string { return string(f.Kind) + ": " + f.Detail }

// SSHTarget is one node reached over SSH.
type SSHTarget struct {
	Customer string
	Node     string
	Host     string
	Port     int
	User     string
}

func (t SSHTarget) address() string { return net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) }

func (t SSHTarget) String() string {
	return fmt.Sprintf("%s/%s ssh %s@%s", t.Customer, t.Node, t.User, t.address())
}

func (t SSHTarget) validate() error {
	switch {
	case t.Customer == "":
		return &Error{Problem: "SSH target has no customer", Next: "build the target from the customer inventory"}
	case t.Host == "":
		return &Error{Problem: fmt.Sprintf("SSH target %s/%s has no host", t.Customer, t.Node), Next: "set the node address in the customer inventory"}
	case t.Port < 1 || t.Port > 65535:
		return &Error{Problem: fmt.Sprintf("SSH target %s/%s has port %d, outside 1-65535", t.Customer, t.Node, t.Port), Next: "set the SSH port in the customer inventory"}
	}
	return nil
}

// APITarget is a Kubernetes API server or a Rancher server.
type APITarget struct {
	Customer string
	URL      string
}

func (t APITarget) String() string { return t.Customer + " " + t.URL }

func (t APITarget) validate() error {
	if t.Customer == "" {
		return &Error{Problem: "API target has no customer", Next: "build the target from the customer inventory"}
	}
	u, err := url.Parse(t.URL)
	switch {
	case err != nil || u.Host == "":
		return &Error{Problem: fmt.Sprintf("API target of %s has no usable URL", t.Customer), Next: "set an https://host[:port] URL in the customer inventory"}
	case u.User != nil:
		// Credentials in a URL would travel into every result; they belong in the secret store.
		return &Error{Problem: fmt.Sprintf("API target of %s carries credentials in its URL", t.Customer), Next: "remove the user part from the URL; credentials belong in the secret store"}
	}
	return nil
}

// SSH is the adapter for SSH checks. Every method honours ctx.
type SSH interface {
	// Reach resolves the host, opens TCP and verifies the host key against the customer's
	// known_hosts. It fails with KindDNS, KindTCP or KindHostKey.
	Reach(ctx context.Context, t SSHTarget) error
	// Authenticate logs in with the operator's key. It fails with KindSSHAuth.
	Authenticate(ctx context.Context, t SSHTarget) error
	// Sudo proves passwordless sudo without changing anything. It fails with KindSudo.
	Sudo(ctx context.Context, t SSHTarget) error
}

// API is the adapter for a Kubernetes or Rancher API. Every method honours ctx.
type API interface {
	// Reach resolves the host, opens TCP and gets an answer from the server. It fails with
	// KindDNS, KindTCP or the API kind (KindKubeAPI, KindRancherAPI).
	Reach(ctx context.Context, t APITarget) error
	// Authenticate presents the stored credentials. It fails with KindKubeAuth or KindRancherAuth.
	Authenticate(ctx context.Context, t APITarget) error
	// Authorize proves the minimal read permission. It fails with KindKubeForbidden or
	// KindRancherForbidden.
	Authorize(ctx context.Context, t APITarget) error
}

// Status is the outcome of one check.
type Status string

// Statuses. Skipped is never green: the check did not run.
const (
	StatusOK      Status = "ok"
	StatusFailed  Status = "failed"
	StatusSkipped Status = "skipped"
)

// Result is one check's outcome. Target, Detail and Next have passed the redactor.
type Result struct {
	// Check is the check name, for example "ssh.endpoint".
	Check  string
	Target string
	Status Status
	// Kind is set when Status is failed.
	Kind   Kind
	Detail string
	// MeasuredAt is when the check started (or was skipped); Duration is how long it ran.
	MeasuredAt time.Time
	Duration   time.Duration
	// FreshUntil is when the result stops describing the present.
	FreshUntil time.Time
	// Next is the read-only next step for the operator.
	Next string
}

// Fresh reports whether the result still describes the present at now. A result measured after
// now (the clock went back) is not fresh.
func (r Result) Fresh(now time.Time) bool {
	return !now.Before(r.MeasuredAt) && now.Before(r.FreshUntil)
}

// Options configure a Runner.
type Options struct {
	SSH        SSH
	Kubernetes API
	Rancher    API
	// Redact masks secret material in free text; required (use mask.Mask).
	Redact func(string) string
	// Now is the clock; required.
	Now func() time.Time
	// Timeout bounds one check; zero means 10s.
	Timeout time.Duration
	// FreshFor is how long a result stays fresh; zero means 5m.
	FreshFor time.Duration
}

// Error is an operator-facing refusal: what is wrong and the next step.
type Error struct {
	Problem string
	Next    string
}

func (e *Error) Error() string { return "connect: " + e.Problem }

// Runner runs the connection checks.
type Runner struct {
	ssh      SSH
	kube     API
	rancher  API
	redact   func(string) string
	now      func() time.Time
	timeout  time.Duration
	freshFor time.Duration
}

// New checks the options. It refuses a missing adapter, redactor or clock and negative durations.
func New(opts Options) (*Runner, error) {
	switch {
	case opts.Redact == nil:
		return nil, &Error{Problem: "no redactor configured", Next: "pass the secret mask as Options.Redact; check results never hold unmasked text"}
	case opts.Now == nil:
		return nil, &Error{Problem: "no clock configured", Next: "pass a clock as Options.Now"}
	case opts.SSH == nil || opts.Kubernetes == nil || opts.Rancher == nil:
		return nil, &Error{Problem: "an adapter is missing", Next: "pass SSH, Kubernetes and Rancher adapters"}
	case opts.Timeout < 0 || opts.FreshFor < 0:
		return nil, &Error{Problem: "timeout and freshness must not be negative", Next: "pass zero for the defaults"}
	}
	r := &Runner{ssh: opts.SSH, kube: opts.Kubernetes, rancher: opts.Rancher, redact: opts.Redact, now: opts.Now, timeout: opts.Timeout, freshFor: opts.FreshFor}
	if r.timeout == 0 {
		r.timeout = defaultTimeout
	}
	if r.freshFor == 0 {
		r.freshFor = defaultFreshFor
	}
	return r, nil
}

// step is one named check with the failure kinds it may report.
type step struct {
	name  string
	kinds []Kind
	run   func(ctx context.Context) error
}

// SSH runs ssh.endpoint, ssh.auth and ssh.sudo against t.
func (r *Runner) SSH(ctx context.Context, t SSHTarget) ([]Result, error) {
	if err := t.validate(); err != nil {
		return nil, r.refusal(err)
	}
	return r.run(ctx, t.String(), t.Customer, t.address(), []step{
		{"ssh.endpoint", []Kind{KindDNS, KindTCP, KindHostKey}, func(ctx context.Context) error { return r.ssh.Reach(ctx, t) }},
		{"ssh.auth", []Kind{KindSSHAuth}, func(ctx context.Context) error { return r.ssh.Authenticate(ctx, t) }},
		{"ssh.sudo", []Kind{KindSudo}, func(ctx context.Context) error { return r.ssh.Sudo(ctx, t) }},
	}), nil
}

// refusal masks a target refusal: it quotes inventory names, which pass the redactor like every
// result field.
func (r *Runner) refusal(err error) error {
	var e *Error
	if !errors.As(err, &e) {
		return err
	}
	return &Error{Problem: r.redact(e.Problem), Next: r.redact(e.Next)}
}

// Kubernetes runs kube.endpoint, kube.auth and kube.authz against t.
func (r *Runner) Kubernetes(ctx context.Context, t APITarget) ([]Result, error) {
	return r.api(ctx, "kube", r.kube, t, KindKubeAPI, KindKubeAuth, KindKubeForbidden)
}

// Rancher runs rancher.endpoint, rancher.auth and rancher.authz against t.
func (r *Runner) Rancher(ctx context.Context, t APITarget) ([]Result, error) {
	return r.api(ctx, "rancher", r.rancher, t, KindRancherAPI, KindRancherAuth, KindRancherForbidden)
}

func (r *Runner) api(ctx context.Context, prefix string, a API, t APITarget, reach, auth, authz Kind) ([]Result, error) {
	if err := t.validate(); err != nil {
		return nil, r.refusal(err)
	}
	return r.run(ctx, t.String(), t.Customer, t.URL, []step{
		{prefix + ".endpoint", []Kind{KindDNS, KindTCP, reach}, func(ctx context.Context) error { return a.Reach(ctx, t) }},
		{prefix + ".auth", []Kind{auth}, func(ctx context.Context) error { return a.Authenticate(ctx, t) }},
		{prefix + ".authz", []Kind{authz}, func(ctx context.Context) error { return a.Authorize(ctx, t) }},
	}), nil
}

func (r *Runner) run(ctx context.Context, target, customer, where string, steps []step) []Result {
	results := make([]Result, 0, len(steps))
	blocked := ""
	for _, s := range steps {
		res := Result{Check: s.name, Target: r.redact(target), MeasuredAt: r.now()}
		switch {
		case blocked != "":
			res.Status = StatusSkipped
			res.Detail = "not run: " + blocked + " failed"
			res.Next = "fix " + blocked + " first; its result names the next step"
		default:
			cctx, cancel := context.WithTimeout(ctx, r.timeout)
			err := s.run(cctx)
			ctxErr := cctx.Err()
			cancel()
			res.Duration = r.now().Sub(res.MeasuredAt)
			if err == nil && errors.Is(ctxErr, context.DeadlineExceeded) {
				// The deadline is the contract: an answer after it is a timeout, never ok.
				err = errors.New("answered after the deadline")
			}
			if err == nil {
				res.Status = StatusOK
				res.Next = "none; re-check once the result is no longer fresh"
				break
			}
			res.Status = StatusFailed
			res.Kind, res.Detail = r.classify(ctxErr, err, s.kinds)
			res.Next = nextStep(res.Kind, customer, where)
			blocked = s.name
		}
		res.Detail = r.redact(res.Detail)
		res.Next = r.redact(res.Next)
		res.FreshUntil = res.MeasuredAt.Add(r.freshFor)
		results = append(results, res)
	}
	return results
}

// classify trusts an adapter's kind only when it belongs to the check.
func (r *Runner) classify(ctxErr, err error, kinds []Kind) (Kind, string) {
	switch {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return KindTimeout, fmt.Sprintf("no answer within %s: %v", r.timeout, err)
	case ctxErr != nil:
		return KindCancelled, "cancelled: " + err.Error()
	}
	var f *Failure
	if errors.As(err, &f) && slices.Contains(kinds, f.Kind) {
		return f.Kind, f.Detail
	}
	return KindUnclassified, err.Error()
}
