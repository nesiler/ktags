package connect

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/mask"
)

var t0 = time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)

// stepClock advances one second on every reading, so durations are exact.
type stepClock struct{ t time.Time }

func (c *stepClock) now() time.Time {
	now := c.t
	c.t = c.t.Add(time.Second)
	return now
}

var (
	sshTarget  = SSHTarget{Customer: "acme", Node: "srv-1", Host: "203.0.113.11", Port: 22, User: "ops"}
	kubeTarget = APITarget{Customer: "acme", URL: "https://203.0.113.11:6443"}
	rancTarget = APITarget{Customer: "acme", URL: "https://rancher.acme.example.test"}
)

func newRunner(t *testing.T, ssh SSH, kube, rancher API) *Runner {
	t.Helper()
	clock := &stepClock{t: t0}
	r, err := New(Options{SSH: ssh, Kubernetes: kube, Rancher: rancher, Redact: mask.Mask, Now: clock.now, FreshFor: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// runAll runs every check of every target and returns the results in order.
func runAll(t *testing.T, r *Runner) []Result {
	t.Helper()
	ctx := context.Background()
	var all []Result
	for _, f := range []func() ([]Result, error){
		func() ([]Result, error) { return r.SSH(ctx, sshTarget) },
		func() ([]Result, error) { return r.Kubernetes(ctx, kubeTarget) },
		func() ([]Result, error) { return r.Rancher(ctx, rancTarget) },
	} {
		res, err := f()
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, res...)
	}
	return all
}

func fail(k Kind) error { return &Failure{Kind: k, Detail: "fake " + string(k)} }

type scenario struct {
	name          string
	ssh           Script
	kube, rancher Script
	check         string
	kind          Kind
}

// scenarios holds one fake per failure class the operator must tell apart (#14-K1).
var scenarios = []scenario{
	{name: "dns", ssh: Script{Endpoint: fail(KindDNS)}, check: "ssh.endpoint", kind: KindDNS},
	{name: "tcp", ssh: Script{Endpoint: fail(KindTCP)}, check: "ssh.endpoint", kind: KindTCP},
	{name: "host key", ssh: Script{Endpoint: fail(KindHostKey)}, check: "ssh.endpoint", kind: KindHostKey},
	{name: "ssh auth", ssh: Script{Auth: fail(KindSSHAuth)}, check: "ssh.auth", kind: KindSSHAuth},
	{name: "sudo", ssh: Script{Authz: fail(KindSudo)}, check: "ssh.sudo", kind: KindSudo},
	{name: "kube dns", kube: Script{Endpoint: fail(KindDNS)}, check: "kube.endpoint", kind: KindDNS},
	{name: "kube api", kube: Script{Endpoint: fail(KindKubeAPI)}, check: "kube.endpoint", kind: KindKubeAPI},
	{name: "kube auth", kube: Script{Auth: fail(KindKubeAuth)}, check: "kube.auth", kind: KindKubeAuth},
	{name: "kube forbidden", kube: Script{Authz: fail(KindKubeForbidden)}, check: "kube.authz", kind: KindKubeForbidden},
	{name: "rancher tcp", rancher: Script{Endpoint: fail(KindTCP)}, check: "rancher.endpoint", kind: KindTCP},
	{name: "rancher api", rancher: Script{Endpoint: fail(KindRancherAPI)}, check: "rancher.endpoint", kind: KindRancherAPI},
	{name: "rancher auth", rancher: Script{Auth: fail(KindRancherAuth)}, check: "rancher.auth", kind: KindRancherAuth},
	{name: "rancher forbidden", rancher: Script{Authz: fail(KindRancherForbidden)}, check: "rancher.authz", kind: KindRancherForbidden},
}

func (s scenario) runner(t *testing.T) *Runner {
	return newRunner(t, FakeSSH{s.ssh}, FakeAPI{s.kube}, FakeAPI{s.rancher})
}

func TestAllGreen(t *testing.T) {
	results := runAll(t, newRunner(t, FakeSSH{}, FakeAPI{}, FakeAPI{}))
	want := []string{"ssh.endpoint", "ssh.auth", "ssh.sudo", "kube.endpoint", "kube.auth", "kube.authz", "rancher.endpoint", "rancher.auth", "rancher.authz"}
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d", len(results), len(want))
	}
	for i, res := range results {
		if res.Check != want[i] || res.Status != StatusOK || res.Kind != "" {
			t.Errorf("result %d = %s %s %q, want %s ok", i, res.Check, res.Status, res.Kind, want[i])
		}
	}
}

// #14-K1: every scenario fails exactly one check with its own (check, kind) pair.
func TestScenariosDistinguishFailures(t *testing.T) {
	seen := map[string]string{}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			var failed []Result
			for _, res := range runAll(t, sc.runner(t)) {
				if res.Status == StatusFailed {
					failed = append(failed, res)
				}
			}
			if len(failed) != 1 || failed[0].Check != sc.check || failed[0].Kind != sc.kind {
				t.Fatalf("failed checks = %+v, want only %s with kind %s", failed, sc.check, sc.kind)
			}
			key := sc.check + "/" + string(sc.kind)
			if other, ok := seen[key]; ok {
				t.Fatalf("scenario %q looks like %q (%s)", sc.name, other, key)
			}
			seen[key] = sc.name
		})
	}
}

func TestLaterChecksSkippedAfterFailure(t *testing.T) {
	r := newRunner(t, FakeSSH{Script{Endpoint: fail(KindTCP)}}, FakeAPI{}, FakeAPI{})
	results, err := r.SSH(context.Background(), sshTarget)
	if err != nil {
		t.Fatal(err)
	}
	want := []Status{StatusFailed, StatusSkipped, StatusSkipped}
	for i, res := range results {
		if res.Status != want[i] {
			t.Errorf("%s: status %s, want %s", res.Check, res.Status, want[i])
		}
		if res.Status == StatusSkipped && !strings.Contains(res.Detail, "ssh.endpoint failed") {
			t.Errorf("%s: detail %q does not name the blocking check", res.Check, res.Detail)
		}
	}
}

// A kind outside the check, or a plain error, is not trusted as a classification.
func TestUnclassifiedFailures(t *testing.T) {
	for name, err := range map[string]error{
		"kind of another check":         fail(KindSudo),
		"api kind of the other adapter": fail(KindRancherAPI),
		"plain error":                   errors.New("boom"),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRunner(t, FakeSSH{Script{Endpoint: err}}, FakeAPI{Script{Endpoint: err}}, FakeAPI{})
			for _, run := range []func() ([]Result, error){
				func() ([]Result, error) { return r.SSH(context.Background(), sshTarget) },
				func() ([]Result, error) { return r.Kubernetes(context.Background(), kubeTarget) },
			} {
				results, rerr := run()
				if rerr != nil {
					t.Fatal(rerr)
				}
				if results[0].Kind != KindUnclassified {
					t.Errorf("%s: kind %s, want unclassified", results[0].Check, results[0].Kind)
				}
			}
		})
	}
}

// #14-K2: every result states measured time, target, freshness and a next step.
func TestEveryResultIsComplete(t *testing.T) {
	all := append(slices.Clone(scenarios), scenario{name: "green"})
	for _, sc := range all {
		for _, res := range runAll(t, sc.runner(t)) {
			switch {
			case res.MeasuredAt.IsZero():
				t.Errorf("%s/%s: no measured time", sc.name, res.Check)
			case !strings.HasPrefix(res.Target, "acme"):
				t.Errorf("%s/%s: target %q", sc.name, res.Check, res.Target)
			case !res.FreshUntil.Equal(res.MeasuredAt.Add(time.Minute)):
				t.Errorf("%s/%s: fresh until %s, measured %s", sc.name, res.Check, res.FreshUntil, res.MeasuredAt)
			case res.Next == "":
				t.Errorf("%s/%s: no next step", sc.name, res.Check)
			case res.Status == StatusFailed && res.Duration != time.Second:
				t.Errorf("%s/%s: duration %s, want 1s", sc.name, res.Check, res.Duration)
			}
		}
	}
}

func TestSSHTargetNamesNodeAndAddress(t *testing.T) {
	results, err := newRunner(t, FakeSSH{}, FakeAPI{}, FakeAPI{}).SSH(context.Background(), sshTarget)
	if err != nil {
		t.Fatal(err)
	}
	if want := "acme/srv-1 ssh ops@203.0.113.11:22"; results[0].Target != want {
		t.Fatalf("target %q, want %q", results[0].Target, want)
	}
}

func TestNextStepsAreSafe(t *testing.T) {
	kinds := []Kind{KindDNS, KindTCP, KindHostKey, KindSSHAuth, KindSudo, KindKubeAPI, KindKubeAuth, KindKubeForbidden,
		KindRancherAPI, KindRancherAuth, KindRancherForbidden, KindTimeout, KindCancelled, KindUnclassified}
	unsafe := []string{"--accept-hostkey", "--yes", "--force", "ssh-keygen -R", "StrictHostKeyChecking", "rm "}
	seen := map[string]Kind{}
	for _, k := range kinds {
		next := nextStep(k, "acme", "203.0.113.11:22")
		for _, u := range unsafe {
			if strings.Contains(next, u) {
				t.Errorf("%s: next step %q contains %q", k, next, u)
			}
		}
		if other, ok := seen[next]; ok && k != KindKubeAuth && k != KindRancherAuth && k != KindKubeForbidden && k != KindRancherForbidden {
			t.Errorf("%s has the same next step as %s", k, other)
		}
		seen[next] = k
	}
	if !strings.Contains(nextStep(KindHostKey, "acme", "x"), "do not accept") {
		t.Error("host-key next step does not warn against accepting the key")
	}
}

// #14-K3: detail, errors and target pass the redactor before they reach a result.
func TestResultsAreRedacted(t *testing.T) {
	secretErr := &Failure{Kind: KindSSHAuth, Detail: "server said password: hunter2, Bearer abc.def"}
	plain := errors.New("dial with --token tok-123 failed")
	r := newRunner(t, FakeSSH{Script{Auth: secretErr}}, FakeAPI{Script{Endpoint: plain}}, FakeAPI{})
	target := sshTarget
	target.Node = "token=node-secret"
	sshRes, err := r.SSH(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	kubeRes, err := r.Kubernetes(context.Background(), kubeTarget)
	if err != nil {
		t.Fatal(err)
	}
	// The next step names the address; a secret there must be masked too.
	hostTarget := sshTarget
	hostTarget.Host = "token=host-secret"
	nextRes, err := newRunner(t, FakeSSH{Script{Endpoint: fail(KindTCP)}}, FakeAPI{}, FakeAPI{}).SSH(context.Background(), hostTarget)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range slices.Concat(sshRes, kubeRes, nextRes) {
		text := fmt.Sprintf("%s %s %s", res.Target, res.Detail, res.Next)
		for _, s := range []string{"hunter2", "abc.def", "tok-123", "node-secret", "host-secret"} {
			if strings.Contains(text, s) {
				t.Errorf("%s: %q leaks %q", res.Check, text, s)
			}
		}
	}
	if !strings.Contains(sshRes[1].Detail, mask.Placeholder) {
		t.Errorf("ssh.auth detail %q not masked", sshRes[1].Detail)
	}
}

func TestFreshness(t *testing.T) {
	res := Result{MeasuredAt: t0, FreshUntil: t0.Add(time.Minute)}
	for _, tc := range []struct {
		at   time.Time
		want bool
	}{
		{t0, true},
		{t0.Add(59 * time.Second), true},
		{t0.Add(time.Minute), false},
		{t0.Add(time.Hour), false},
		{t0.Add(-time.Second), false},
	} {
		if got := res.Fresh(tc.at); got != tc.want {
			t.Errorf("Fresh(%s) = %v, want %v", tc.at.Sub(t0), got, tc.want)
		}
	}
}

func TestCheckTimeout(t *testing.T) {
	r, err := New(Options{SSH: FakeSSH{Script{Hang: true}}, Kubernetes: FakeAPI{}, Rancher: FakeAPI{}, Redact: mask.Mask, Now: time.Now, Timeout: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	results, err := r.SSH(context.Background(), sshTarget)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Kind != KindTimeout || !strings.Contains(results[0].Detail, "10ms") {
		t.Fatalf("endpoint = %s %q, want timeout after 10ms", results[0].Kind, results[0].Detail)
	}
	if results[1].Status != StatusSkipped {
		t.Fatalf("ssh.auth = %s, want skipped", results[1].Status)
	}
}

func TestCancelledCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := newRunner(t, FakeSSH{Script{Hang: true}}, FakeAPI{}, FakeAPI{}).SSH(ctx, sshTarget)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Kind != KindCancelled {
		t.Fatalf("endpoint kind %s, want cancelled", results[0].Kind)
	}
}

func TestDefaults(t *testing.T) {
	clock := &stepClock{t: t0}
	r, err := New(Options{SSH: FakeSSH{}, Kubernetes: FakeAPI{}, Rancher: FakeAPI{}, Redact: mask.Mask, Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	results, err := r.SSH(context.Background(), sshTarget)
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Status != StatusOK {
		t.Fatalf("zero timeout: endpoint %s %q, want ok", results[0].Status, results[0].Detail)
	}
	if got := results[0].FreshUntil.Sub(results[0].MeasuredAt); got != defaultFreshFor {
		t.Fatalf("zero freshness: fresh for %s, want %s", got, defaultFreshFor)
	}
	if r.timeout != defaultTimeout {
		t.Fatalf("zero timeout became %s, want %s", r.timeout, defaultTimeout)
	}
}

func TestNewRefusesBadOptions(t *testing.T) {
	good := func() Options {
		return Options{SSH: FakeSSH{}, Kubernetes: FakeAPI{}, Rancher: FakeAPI{}, Redact: mask.Mask, Now: time.Now}
	}
	tests := []struct {
		name   string
		change func(*Options)
		want   string
	}{
		{"no redactor", func(o *Options) { o.Redact = nil }, "no redactor"},
		{"no clock", func(o *Options) { o.Now = nil }, "no clock"},
		{"no ssh", func(o *Options) { o.SSH = nil }, "adapter is missing"},
		{"no kubernetes", func(o *Options) { o.Kubernetes = nil }, "adapter is missing"},
		{"no rancher", func(o *Options) { o.Rancher = nil }, "adapter is missing"},
		{"negative timeout", func(o *Options) { o.Timeout = -1 }, "must not be negative"},
		{"negative freshness", func(o *Options) { o.FreshFor = -1 }, "must not be negative"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := good()
			tc.change(&opts)
			_, err := New(opts)
			var e *Error
			if !errors.As(err, &e) || !strings.Contains(e.Problem, tc.want) || e.Next == "" {
				t.Fatalf("New: %v, want an *Error containing %q with a next step", err, tc.want)
			}
		})
	}
	if _, err := New(good()); err != nil {
		t.Fatalf("good options refused: %v", err)
	}
}

func TestTargetsRefused(t *testing.T) {
	r := newRunner(t, FakeSSH{}, FakeAPI{}, FakeAPI{})
	ssh := func(change func(*SSHTarget)) SSHTarget { s := sshTarget; change(&s); return s }
	for name, tc := range map[string]struct {
		target SSHTarget
		want   string
	}{
		"no customer": {ssh(func(s *SSHTarget) { s.Customer = "" }), "no customer"},
		"no host":     {ssh(func(s *SSHTarget) { s.Host = "" }), "no host"},
		"port zero":   {ssh(func(s *SSHTarget) { s.Port = 0 }), "outside 1-65535"},
		"port high":   {ssh(func(s *SSHTarget) { s.Port = 65536 }), "outside 1-65535"},
	} {
		t.Run("ssh "+name, func(t *testing.T) {
			if _, err := r.SSH(context.Background(), tc.target); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("SSH: %v, want an error containing %q", err, tc.want)
			}
		})
	}
	for _, port := range []int{1, 65535} {
		if _, err := r.SSH(context.Background(), ssh(func(s *SSHTarget) { s.Port = port })); err != nil {
			t.Fatalf("port %d refused: %v", port, err)
		}
	}
	for name, tc := range map[string]struct {
		target APITarget
		want   string
	}{
		"no customer":        {APITarget{URL: "https://203.0.113.11:6443"}, "no customer"},
		"empty url":          {APITarget{Customer: "acme"}, "no usable URL"},
		"unparsable url":     {APITarget{Customer: "acme", URL: "https://[::1"}, "no usable URL"},
		"url without host":   {APITarget{Customer: "acme", URL: "203.0.113.11:6443"}, "no usable URL"},
		"credentials in url": {APITarget{Customer: "acme", URL: "https://admin:secret@203.0.113.11"}, "credentials"},
		"user only in url":   {APITarget{Customer: "acme", URL: "https://admin@203.0.113.11"}, "credentials"},
	} {
		t.Run("api "+name, func(t *testing.T) {
			for _, run := range []func(context.Context, APITarget) ([]Result, error){r.Kubernetes, r.Rancher} {
				if _, err := run(context.Background(), tc.target); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v, want an error containing %q", err, tc.want)
				}
			}
		})
	}
}
