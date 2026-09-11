package fleet

import (
	"testing"

	"github.com/nesiler/ktags/internal/core/health"
)

// #65 D4a: the row shows the runbook of the check behind its state.
func TestRunbook(t *testing.T) {
	check := func(name string, severity health.Severity, status health.Status, runbook string) health.CheckResult {
		return health.CheckResult{Check: name, Severity: severity, Status: status, Runbook: runbook}
	}
	tests := []struct {
		name   string
		checks []health.CheckResult
		want   string
	}{
		{"no checks", nil, ""},
		{"all ok", []health.CheckResult{check("ssh.endpoint", health.SeverityCritical, health.StatusOK, "")}, ""},
		{"a failing critical check wins over an earlier warning", []health.CheckResult{
			check("kube.auth", health.SeverityWarning, health.StatusNotConfigured, "kube-not-configured"),
			check("rancher.endpoint", health.SeverityWarning, health.StatusFailed, "rancher-unreachable"),
			check("ssh.endpoint", health.SeverityCritical, health.StatusFailed, "ssh-unreachable"),
		}, "ssh-unreachable"},
		{"a critical timeout counts", []health.CheckResult{
			check("kube.auth", health.SeverityWarning, health.StatusNotConfigured, "kube-not-configured"),
			check("ssh.sudo", health.SeverityCritical, health.StatusTimeout, "ssh-unreachable"),
		}, "ssh-unreachable"},
		// Not configured weighs as a warning, so a critical one does not outrank a real warning.
		{"a critical not configured check is only the first warning", []health.CheckResult{
			check("rancher.endpoint", health.SeverityWarning, health.StatusFailed, "rancher-unreachable"),
			check("ssh.endpoint", health.SeverityCritical, health.StatusNotConfigured, "ssh-not-configured"),
		}, "rancher-unreachable"},
		{"a failure without a runbook is passed over", []health.CheckResult{
			check("disk.free", health.SeverityCritical, health.StatusFailed, ""),
			check("kube.auth", health.SeverityWarning, health.StatusNotConfigured, "kube-not-configured"),
		}, "kube-not-configured"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := Entry{Customer: "acme", Measurement: Measurement{Health: health.View{Customer: "acme", Checks: tc.checks}}}
			if got := Runbook(e); got != tc.want {
				t.Fatalf("Runbook = %q, want %q", got, tc.want)
			}
		})
	}
}
