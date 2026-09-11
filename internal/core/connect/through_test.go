package connect

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/mask"
)

// countingSSH records which SSH checks ran.
type countingSSH struct{ calls *[]string }

func (c countingSSH) Reach(context.Context, SSHTarget) error {
	*c.calls = append(*c.calls, "reach")
	return nil
}

func (c countingSSH) Authenticate(context.Context, SSHTarget) error {
	*c.calls = append(*c.calls, "auth")
	return nil
}

func (c countingSSH) Sudo(context.Context, SSHTarget) error {
	*c.calls = append(*c.calls, "sudo")
	return nil
}

func TestSSHThroughStopsAtTheNamedCheck(t *testing.T) {
	for check, want := range map[string]string{
		"ssh.endpoint": "reach",
		"ssh.auth":     "reach,auth",
		"ssh.sudo":     "reach,auth,sudo",
	} {
		var calls []string
		r := newRunner(t, countingSSH{&calls}, FakeAPI{}, FakeAPI{})
		res, err := r.SSHThrough(context.Background(), sshTarget, check)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(calls, ","); got != want || res[len(res)-1].Check != check {
			t.Fatalf("%s: ran %s ending with %s, want %s ending with %s", check, got, res[len(res)-1].Check, want, check)
		}
	}
}

func TestThroughRefusesAnUnknownCheck(t *testing.T) {
	r := newRunner(t, FakeSSH{}, FakeAPI{}, FakeAPI{})
	for _, f := range []func() ([]Result, error){
		func() ([]Result, error) { return r.SSHThrough(context.Background(), sshTarget, "kube.endpoint") },
		func() ([]Result, error) { return r.KubernetesThrough(context.Background(), kubeTarget, "ssh.auth") },
		func() ([]Result, error) { return r.RancherThrough(context.Background(), rancTarget, "rancher.nope") },
	} {
		res, err := f()
		var e *Error
		if !errors.As(err, &e) || res != nil {
			t.Fatalf("got %v, %v; want a refusal and no results", res, err)
		}
	}
}

// #65 D3: a check whose adapter has nothing to run with is not configured, never ok, and blocks
// the checks after it.
func TestNotConfiguredBlocksLaterChecks(t *testing.T) {
	r := newRunner(t, FakeSSH{}, FakeAPI{Script: Script{Auth: fmt.Errorf("%w: no credentials", ErrNotConfigured)}}, FakeAPI{})
	res, err := r.Kubernetes(context.Background(), kubeTarget)
	if err != nil {
		t.Fatal(err)
	}
	auth, authz := res[1], res[2]
	if auth.Status != StatusNotConfigured || auth.Kind != "" || auth.Runbook != RunbookKubeNotConfigured || !strings.Contains(auth.Detail, "no credentials") {
		t.Fatalf("kube.auth %+v, want not configured with its runbook", auth)
	}
	if authz.Status != StatusSkipped || authz.Detail != "not run: kube.auth is not configured" || authz.Runbook != RunbookKubeNotConfigured {
		t.Fatalf("kube.authz %+v, want skipped behind kube.auth", authz)
	}
}

// An adapter that answers "not configured" only after its deadline timed out: the timeout wins.
type lateNotConfigured struct{ FakeAPI }

func (lateNotConfigured) Reach(ctx context.Context, _ APITarget) error {
	<-ctx.Done()
	return ErrNotConfigured
}

func TestNotConfiguredAfterTheDeadlineIsATimeout(t *testing.T) {
	r, err := New(Options{SSH: FakeSSH{}, Kubernetes: lateNotConfigured{}, Rancher: FakeAPI{}, Redact: mask.Mask, Now: time.Now, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.Kubernetes(context.Background(), kubeTarget)
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Status != StatusFailed || res[0].Kind != KindTimeout || res[0].Runbook != RunbookKubeAPIUnreachable {
		t.Fatalf("kube.endpoint %+v, want a timeout", res[0])
	}
}

// Every failure kind a check can report names a runbook; only a cancelled check has none.
func TestEveryFailureNamesARunbook(t *testing.T) {
	kinds := []Kind{KindDNS, KindTCP, KindHostKey, KindSSHAuth, KindSudo, KindKubeAPI, KindKubeAuth, KindKubeForbidden, KindRancherAPI, KindRancherAuth, KindRancherForbidden, KindTimeout, KindUnclassified}
	for _, check := range []string{"ssh.endpoint", "kube.endpoint", "rancher.endpoint"} {
		for _, k := range kinds {
			if runbook(check, k) == "" {
				t.Fatalf("%s %s has no runbook", check, k)
			}
		}
		if rb := runbook(check, KindCancelled); rb != "" {
			t.Fatalf("a cancelled %s names runbook %q, want none", check, rb)
		}
	}
	for check, want := range map[string]string{"ssh.endpoint": RunbookSSHUnreachable, "kube.endpoint": RunbookKubeAPIUnreachable, "rancher.endpoint": RunbookRancherUnreachable} {
		if got := runbook(check, KindTCP); got != want {
			t.Fatalf("tcp failure of %s names %s, want %s", check, got, want)
		}
	}
}
