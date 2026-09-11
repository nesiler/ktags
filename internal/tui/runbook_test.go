package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/service"
)

// #65 D4a and ui.md §3: the Health tab shows the row's runbook and each check's runbook; a not
// configured check reads as a warning in words, not as a failure.
func TestHealthTabShowsRunbooks(t *testing.T) {
	d := connected(t, newFake())
	bad := kubeBad
	bad.Runbook = "kube-api-unreachable"
	unconfigured := service.CheckInfo{Check: "kube.auth", Severity: "warning", Status: "not_configured", Runbook: "kube-not-configured", Detail: "not configured"}
	e := measured(entry("acme", "test", "fail"), "fail", time.Minute, false, sshOK, bad, unconfigured)
	e.Runbook = "kube-api-unreachable"
	health := stripped(strings.Join(d.m.healthLines(d.m.th, &e, 120), "\n"))
	contains(t, health, "runbook     kube-api-unreachable", "kube.endpoint", "kube-api-unreachable dial tcp", "⚠ not configured", "kube-not-configured")
	if strings.Contains(health, "✗ not_configured") {
		t.Fatalf("a not configured check reads as a failure:\n%s", health)
	}
	// No runbook, no runbook line: an ok customer shows none.
	ok := measured(entry("beta", "test", "ok"), "ok", time.Minute, false, sshOK)
	if lines := stripped(strings.Join(d.m.healthLines(d.m.th, &ok, 120), "\n")); strings.Contains(lines, "runbook") {
		t.Fatalf("an ok customer shows a runbook line:\n%s", lines)
	}
}
