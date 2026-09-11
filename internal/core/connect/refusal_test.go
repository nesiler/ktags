package connect

import (
	"context"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/mask"
)

// A target refusal quotes inventory names; like every result field it passes the redactor.
func TestTargetRefusalsAreRedacted(t *testing.T) {
	r := newRunner(t, FakeSSH{}, FakeAPI{}, FakeAPI{})
	ctx := context.Background()
	ssh := sshTarget
	ssh.Node = "token=node-secret"
	ssh.Host = ""
	_, sshErr := r.SSH(ctx, ssh)
	api := APITarget{Customer: "password=customer-secret"}
	_, kubeErr := r.Kubernetes(ctx, api)
	_, rancherErr := r.Rancher(ctx, api)

	for name, tc := range map[string]struct {
		err            error
		problem, value string
	}{
		"ssh":        {sshErr, "has no host", "node-secret"},
		"kubernetes": {kubeErr, "has no usable URL", "customer-secret"},
		"rancher":    {rancherErr, "has no usable URL", "customer-secret"},
	} {
		if tc.err == nil {
			t.Fatalf("%s: no refusal", name)
		}
		msg := tc.err.Error()
		if strings.Contains(msg, tc.value) || !strings.Contains(msg, mask.Placeholder) || !strings.Contains(msg, tc.problem) {
			t.Errorf("%s refusal %q, want %q masked and the problem kept", name, msg, tc.value)
		}
	}
}
