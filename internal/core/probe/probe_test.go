package probe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/core/connect"
	"github.com/nesiler/ktags/internal/core/fleet"
	"github.com/nesiler/ktags/internal/core/health"
	"github.com/nesiler/ktags/internal/core/schedule"
	"github.com/nesiler/ktags/internal/mask"
)

// nodeSSH scripts the fake SSH adapter per node.
type nodeSSH map[string]connect.Script

func (n nodeSSH) Reach(ctx context.Context, t connect.SSHTarget) error {
	return connect.FakeSSH{Script: n[t.Node]}.Reach(ctx, t)
}

func (n nodeSSH) Authenticate(ctx context.Context, t connect.SSHTarget) error {
	return connect.FakeSSH{Script: n[t.Node]}.Authenticate(ctx, t)
}

func (n nodeSSH) Sudo(ctx context.Context, t connect.SSHTarget) error {
	return connect.FakeSSH{Script: n[t.Node]}.Sudo(ctx, t)
}

var (
	down          = connect.Script{Hang: true}
	noCredentials = connect.Script{Auth: connect.ErrNotConfigured}
	kubeDown      = connect.Script{Endpoint: &connect.Failure{Kind: connect.KindTCP, Detail: "dial tcp 203.0.113.11:6443: connection refused"}}
)

func node(id, host string) connect.SSHTarget {
	return connect.SSHTarget{Customer: "acme", Node: id, Host: host, Port: 22, User: "ops"}
}

func labTarget() Target {
	return Target{
		Nodes:   []connect.SSHTarget{node("srv-1", "203.0.113.11"), node("agt-1", "203.0.113.12")},
		Kube:    &connect.APITarget{Customer: "acme", URL: "https://203.0.113.11:6443"},
		Rancher: &connect.APITarget{Customer: "acme", URL: "https://rancher.acme.example.test"},
	}
}

type setup struct {
	nodes   nodeSSH
	kube    connect.Script
	rancher connect.Script
	target  func() (Target, error)
}

func newProbe(t *testing.T, s setup) *Probe {
	t.Helper()
	runner, err := connect.New(connect.Options{
		SSH:        s.nodes,
		Kubernetes: connect.FakeAPI{Script: s.kube},
		Rancher:    connect.FakeAPI{Script: s.rancher},
		Redact:     mask.Mask,
		Now:        time.Now,
		Timeout:    50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := s.target
	if target == nil {
		target = func() (Target, error) { return labTarget(), nil }
	}
	p, err := New(Options{Runner: runner, Now: time.Now, Target: func(context.Context, string) (Target, error) { return target() }})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// measure runs every check of p against acme through the health engine.
func measure(t *testing.T, p *Probe) (health.Report, map[string]health.CheckResult) {
	t.Helper()
	e, err := health.New(health.Options{Clock: schedule.System(), Redact: mask.Mask, Checks: p.Checks(), Customers: []string{"acme"}, Interval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	reports, err := e.Run(context.Background(), "acme")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]health.CheckResult{}
	for _, c := range reports[0].Checks {
		byName[c.Check] = c
	}
	return reports[0], byName
}

func wantCheck(t *testing.T, got map[string]health.CheckResult, name string, status health.Status, runbook string) health.CheckResult {
	t.Helper()
	c, ok := got[name]
	if !ok || c.Status != status || c.Runbook != runbook {
		t.Fatalf("%s = %+v, want %s with runbook %q", name, c, status, runbook)
	}
	return c
}

// #65-K1: every check that needs no secret is ok on a healthy customer; the secret checks are
// not configured and the customer warns, never ok.
func TestHealthyCustomer(t *testing.T) {
	p := newProbe(t, setup{kube: noCredentials, rancher: noCredentials})
	r, got := measure(t, p)
	for _, name := range []string{"ssh.endpoint", "ssh.auth", "ssh.sudo", "kube.endpoint", "rancher.endpoint"} {
		wantCheck(t, got, name, health.StatusOK, "")
	}
	wantCheck(t, got, "kube.auth", health.StatusNotConfigured, connect.RunbookKubeNotConfigured)
	wantCheck(t, got, "kube.authz", health.StatusNotConfigured, connect.RunbookKubeNotConfigured)
	wantCheck(t, got, "rancher.auth", health.StatusNotConfigured, connect.RunbookRancherNotConfigured)
	wantCheck(t, got, "rancher.authz", health.StatusNotConfigured, connect.RunbookRancherNotConfigured)
	if r.Health != health.HealthWarn {
		t.Fatalf("health %s, want warn: not configured checks are never ok", r.Health)
	}
	if c := p.Connection("acme"); c.State != fleet.ConnReachable || c.MeasuredAt.IsZero() {
		t.Fatalf("connection %+v, want reachable", c)
	}
}

// #65-K1 and D4b: a stopped node turns the customer red with that node's runbook; the customer
// stays reachable because the other node and the API answer.
func TestStoppedNode(t *testing.T) {
	p := newProbe(t, setup{nodes: nodeSSH{"agt-1": down}, kube: noCredentials, rancher: noCredentials})
	r, got := measure(t, p)
	if r.Health != health.HealthFail {
		t.Fatalf("health %s, want fail", r.Health)
	}
	endpoint := wantCheck(t, got, "ssh.endpoint", health.StatusFailed, connect.RunbookSSHUnreachable)
	if !strings.Contains(endpoint.Detail, "acme/agt-1") || strings.Contains(endpoint.Detail, "acme/srv-1") || !strings.Contains(endpoint.Detail, "timeout") || !strings.Contains(endpoint.Detail, "next: ") {
		t.Fatalf("ssh.endpoint detail %q, want only agt-1 with its timeout and next step", endpoint.Detail)
	}
	// The checks after the endpoint fail behind it, with its runbook.
	wantCheck(t, got, "ssh.auth", health.StatusFailed, connect.RunbookSSHUnreachable)
	wantCheck(t, got, "ssh.sudo", health.StatusFailed, connect.RunbookSSHUnreachable)
	wantCheck(t, got, "kube.endpoint", health.StatusOK, "")
	if c := p.Connection("acme"); c.State != fleet.ConnReachable {
		t.Fatalf("connection %+v, want reachable: one node answered", c)
	}
	entry := fleet.Entry{Customer: "acme", Measurement: fleet.Measurement{Health: health.View{Customer: "acme", Health: r.Health, MeasuredAt: r.MeasuredAt, Checks: r.Checks}, Connection: p.Connection("acme")}}
	if fleet.StateOf(entry) != fleet.StateFail || fleet.Runbook(entry) != connect.RunbookSSHUnreachable {
		t.Fatalf("row %s with runbook %q, want fail with %s", fleet.StateOf(entry), fleet.Runbook(entry), connect.RunbookSSHUnreachable)
	}
}

// D4b: unreachable only when every node's SSH endpoint and the Kubernetes API failed.
func TestConnectionState(t *testing.T) {
	allDown := nodeSSH{"srv-1": down, "agt-1": down}
	tests := []struct {
		name string
		s    setup
		want fleet.Connection
	}{
		{"every node and the API down", setup{nodes: allDown, kube: kubeDown}, fleet.ConnUnreachable},
		{"every node down, the API answers", setup{nodes: allDown, kube: noCredentials}, fleet.ConnReachable},
		{"one node down, the API down", setup{nodes: nodeSSH{"agt-1": down}, kube: kubeDown}, fleet.ConnReachable},
		{"every node down, no API configured", setup{nodes: allDown, target: func() (Target, error) {
			t := labTarget()
			t.Kube = nil
			return t, nil
		}}, fleet.ConnUnreachable},
		{"no SSH adapter, the API down", setup{kube: kubeDown, target: func() (Target, error) {
			t := labTarget()
			t.SSHUnsupported = "jump access"
			return t, nil
		}}, fleet.ConnUnreachable},
		// Nothing is measured: the nodes need another access and there is no API to ask.
		{"no SSH adapter, no API configured", setup{target: func() (Target, error) {
			t := labTarget()
			t.SSHUnsupported = "jump access"
			t.Kube = nil
			return t, nil
		}}, fleet.ConnUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := newProbe(t, tc.s)
			measure(t, p)
			c := p.Connection("acme")
			if c.State != tc.want {
				t.Fatalf("connection %+v, want %s", c, tc.want)
			}
			if tc.want == fleet.ConnUnreachable && (c.Detail == "" || c.MeasuredAt.IsZero()) {
				t.Fatalf("unreachable connection %+v lacks its detail or time", c)
			}
		})
	}
}

// Before the API of a customer is measured, failing nodes alone do not make it unreachable.
func TestConnectionUnknownUntilTheAPIIsMeasured(t *testing.T) {
	p := newProbe(t, setup{nodes: nodeSSH{"srv-1": down, "agt-1": down}, kube: kubeDown})
	if c := p.Connection("acme"); c.State != fleet.ConnUnknown {
		t.Fatalf("connection before any check %+v, want unknown", c)
	}
	if err := p.Checks()[0].Run(context.Background(), "acme"); err == nil {
		t.Fatal("ssh.endpoint passed with every node down")
	}
	if c := p.Connection("acme"); c.State != fleet.ConnUnknown {
		t.Fatalf("connection with only SSH measured %+v, want unknown", c)
	}
	// Without a Kubernetes API the nodes alone decide, at once.
	p = newProbe(t, setup{nodes: nodeSSH{"srv-1": down, "agt-1": down}, target: func() (Target, error) {
		t := labTarget()
		t.Kube = nil
		return t, nil
	}})
	if err := p.Checks()[0].Run(context.Background(), "acme"); err == nil {
		t.Fatal("ssh.endpoint passed with every node down")
	}
	if c := p.Connection("acme"); c.State != fleet.ConnUnreachable {
		t.Fatalf("connection with every node down and no API %+v, want unreachable", c)
	}
}

// A customer whose nodes this build cannot reach reports not configured SSH checks: a warning,
// never ok and never a node failure.
func TestUnsupportedAccess(t *testing.T) {
	p := newProbe(t, setup{kube: noCredentials, rancher: noCredentials, target: func() (Target, error) {
		t := labTarget()
		t.SSHUnsupported = "only direct SSH access is measured yet"
		return t, nil
	}})
	r, got := measure(t, p)
	for _, name := range []string{"ssh.endpoint", "ssh.auth", "ssh.sudo"} {
		c := wantCheck(t, got, name, health.StatusNotConfigured, connect.RunbookSSHNotConfigured)
		if c.Detail != "only direct SSH access is measured yet" {
			t.Fatalf("%s detail %q", name, c.Detail)
		}
	}
	if r.Health != health.HealthWarn {
		t.Fatalf("health %s, want warn", r.Health)
	}
	// The API answered, so the customer is reachable although no node was measured.
	if c := p.Connection("acme"); c.State != fleet.ConnReachable {
		t.Fatalf("connection %+v, want reachable", c)
	}
}

func TestAPIsNotConfigured(t *testing.T) {
	p := newProbe(t, setup{target: func() (Target, error) {
		t := labTarget()
		t.Kube, t.Rancher = nil, nil
		return t, nil
	}})
	_, got := measure(t, p)
	for _, name := range []string{"kube.endpoint", "kube.auth", "kube.authz"} {
		c := wantCheck(t, got, name, health.StatusNotConfigured, connect.RunbookKubeNotConfigured)
		if !strings.Contains(c.Detail, "ktags_cluster.kube_api_url") {
			t.Fatalf("%s detail %q, want the missing field named", name, c.Detail)
		}
	}
	wantCheck(t, got, "rancher.endpoint", health.StatusNotConfigured, connect.RunbookRancherNotConfigured)
}

// A refused inventory or target fails every check with the inventory runbook: nothing about the
// customer is measured.
func TestRefusedTarget(t *testing.T) {
	for name, target := range map[string]func() (Target, error){
		"inventory refused": func() (Target, error) { return Target{}, errors.New("the customer record is refused") },
		"target refused by the runner": func() (Target, error) {
			t := labTarget()
			t.Nodes[1].Port = 0
			t.Kube.URL = "not a url"
			t.Rancher.URL = "https://user:pw@rancher.example.test"
			return t, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, got := measure(t, newProbe(t, setup{target: target}))
			if r.Health != health.HealthFail {
				t.Fatalf("health %s, want fail", r.Health)
			}
			for _, name := range []string{"ssh.endpoint", "kube.endpoint", "rancher.endpoint"} {
				c := wantCheck(t, got, name, health.StatusFailed, RunbookInventoryInvalid)
				if strings.Contains(c.Detail, "pw@") {
					t.Fatalf("%s detail leaks the URL credentials: %q", name, c.Detail)
				}
			}
		})
	}
}

// A check cut short by its caller is not a result.
func TestCancelledCheck(t *testing.T) {
	p := newProbe(t, setup{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range p.Checks() {
		if err := c.Run(ctx, "acme"); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s = %v, want the context error", c.Name, err)
		}
	}
}

func TestNewRefusesMissingOptions(t *testing.T) {
	runner, err := connect.New(connect.Options{SSH: connect.FakeSSH{}, Kubernetes: connect.FakeAPI{}, Rancher: connect.FakeAPI{}, Redact: mask.Mask, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	target := func(context.Context, string) (Target, error) { return Target{}, nil }
	for name, o := range map[string]Options{
		"runner": {Target: target, Now: time.Now},
		"target": {Runner: runner, Now: time.Now},
		"clock":  {Runner: runner, Target: target},
	} {
		if _, err := New(o); err == nil {
			t.Fatalf("New without a %s succeeded", name)
		}
	}
}

// A failure on one target outranks a not configured one; only all ok passes.
func TestSummarise(t *testing.T) {
	ok := connect.Result{Status: connect.StatusOK}
	nc := connect.Result{Status: connect.StatusNotConfigured, Runbook: "kube-not-configured", Target: "a"}
	bad := connect.Result{Status: connect.StatusFailed, Kind: connect.KindTCP, Runbook: "ssh-unreachable", Target: "b"}
	if err := summarise([]connect.Result{ok, ok}); err != nil {
		t.Fatalf("all ok = %v", err)
	}
	err := summarise([]connect.Result{ok, nc, bad})
	var f *finding
	if !errors.As(err, &f) || f.notConfigured || f.runbook != "ssh-unreachable" || strings.Contains(f.detail, "a: ") {
		t.Fatalf("mixed = %+v, want the failure alone", f)
	}
	err = summarise([]connect.Result{ok, nc})
	if !errors.As(err, &f) || !f.notConfigured || !errors.Is(err, health.ErrNotConfigured) {
		t.Fatalf("not configured = %+v, want a not configured finding", f)
	}
}
