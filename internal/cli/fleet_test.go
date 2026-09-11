package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/service"
)

var fleetNow = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

func at(ago time.Duration) *time.Time {
	t := fleetNow.Add(-ago)
	return &t
}

// testFleet is a service reply in attention order, which is not alphabetical: the CLI must keep
// the service's order.
func testFleet() service.FleetInfo {
	return service.FleetInfo{Now: fleetNow, Customers: []service.FleetEntry{
		{ID: "zulu", State: "invalid", Problem: "cannot parse the customer record at line 3", Health: "unknown", Stale: true,
			Connection: service.ConnectionInfo{State: "unknown"}, Versions: map[string]string{}, ActiveRuns: []service.ActiveRunInfo{}, Checks: []service.CheckInfo{}},
		{ID: "mike", Environment: "prod", State: "fail", Health: "fail", MeasuredAt: at(5 * time.Minute), Trigger: "scheduled", IntervalSeconds: 900,
			Connection: service.ConnectionInfo{State: "reachable", MeasuredAt: at(5 * time.Minute)}, Versions: map[string]string{"rke2": "v1.30.4+rke2r1", "rancher": "v2.9.1"},
			ActiveRuns: []service.ActiveRunInfo{}, Checks: []service.CheckInfo{
				{Check: "ssh.endpoint", Severity: "critical", Status: "ok", MeasuredAt: *at(5 * time.Minute), DurationMS: 40},
				{Check: "kube.endpoint", Severity: "critical", Status: "failed", Detail: "dial tcp 203.0.113.11:6443: connection refused", MeasuredAt: *at(5 * time.Minute), DurationMS: 12},
			}},
		{ID: "golf", Environment: "staging", State: "unreachable", Health: "unknown", Stale: true, Missed: 2,
			Connection: service.ConnectionInfo{State: "unreachable", Detail: "lookup golf-srv-1.example.test: no such host", MeasuredAt: at(3 * time.Hour)},
			Versions:   map[string]string{}, ActiveRuns: []service.ActiveRunInfo{{ID: "r9", Action: "cluster access"}}, Checks: []service.CheckInfo{}},
		{ID: "alpha", Environment: "test", State: "ok", Health: "ok", MeasuredAt: at(30 * time.Second), Checking: true,
			Connection: service.ConnectionInfo{State: "reachable"}, Versions: map[string]string{}, ActiveRuns: []service.ActiveRunInfo{}, Checks: []service.CheckInfo{
				{Check: "ssh.endpoint", Severity: "critical", Status: "ok", MeasuredAt: *at(30 * time.Second), DurationMS: 8},
			}},
	}}
}

func order(t *testing.T, name, text string, wants ...string) {
	t.Helper()
	last := -1
	for _, want := range wants {
		i := strings.Index(text, want)
		if i < 0 || i < last {
			t.Fatalf("%s: %q missing or out of order:\n%s", name, want, text)
		}
		last = i
	}
}

// #16-K1, #16-K2: the human summary keeps the service's order and shows every field the issue
// names, then every check as evidence.
func TestFleetListHuman(t *testing.T) {
	control, client := newFake()
	client.fleet = testFleet()
	code, stdout, stderr := ktags(control, call{}, "fleet", "list")
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if first := strings.SplitN(stdout, "\n", 2)[0]; first != "4 customer(s): 1 invalid, 1 fail, 1 unreachable, 1 ok" {
		t.Fatalf("summary line %q", first)
	}
	order(t, "rows", stdout, "  invalid      zulu", "  fail         mike", "  unreachable  golf", "  ok           alpha", "evidence:")
	contains(t, "stdout", stdout,
		"STATE", "CONNECTION", "HEALTH", "MEASURED", "FRESHNESS", "VERSIONS", "ACTIVE",
		"5m ago", "30s ago", "never", "stale, 2 missed", "fresh",
		"rancher=v2.9.1,rke2=v1.30.4+rke2r1", "r9 (cluster access)", "health check",
		"zulu record refused: cannot parse the customer record at line 3",
		"golf connection unreachable (measured 2026-09-11T09:00:00Z): lookup golf-srv-1.example.test: no such host",
		"mike kube.endpoint failed (critical, measured 2026-09-11T11:55:00Z, 12ms): dial tcp 203.0.113.11:6443: connection refused",
		"next: ktags action list")
	if client.fleetCalls != 1 {
		t.Fatalf("fleet requested %d times, want 1", client.fleetCalls)
	}
}

// #16-K2: with --json the document is the service reply unchanged, and the human text written
// to stderr by the same invocation (one request) carries every check of that document.
func TestFleetListJSONMatchesHuman(t *testing.T) {
	control, client := newFake()
	client.fleet = testFleet()
	code, stdout, stderr := ktags(control, call{}, "fleet", "list", "--json")
	if code != 0 || client.fleetCalls != 1 {
		t.Fatalf("exit %d, fleet requested %d times", code, client.fleetCalls)
	}
	doc := decodeOne(t, stdout)
	raw, err := json.Marshal(client.fleet)
	if err != nil {
		t.Fatal(err)
	}
	var want any
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if doc.Kind != "fleet.list" || !reflect.DeepEqual(doc.Data, want) {
		t.Fatalf("kind %q, data\n%v\nwant the service reply\n%v", doc.Kind, doc.Data, want)
	}
	customers := field(t, doc, "customers").([]any)
	var ids []string
	for _, c := range customers {
		entry := c.(map[string]any)
		ids = append(ids, "  "+entry["state"].(string))
		for _, k := range entry["checks"].([]any) {
			check := k.(map[string]any)
			line := entry["id"].(string) + " " + check["check"].(string) + " " + check["status"].(string)
			if !strings.Contains(stderr, line) {
				t.Fatalf("stderr lacks the evidence %q of the JSON document:\n%s", line, stderr)
			}
			if d, _ := check["detail"].(string); d != "" && !strings.Contains(stderr, d) {
				t.Fatalf("stderr lacks the detail %q:\n%s", d, stderr)
			}
		}
	}
	order(t, "stderr", stderr, ids...)
}

func TestFleetListEmptyAndUnknownState(t *testing.T) {
	control, client := newFake()
	client.fleet = service.FleetInfo{Now: fleetNow, Customers: []service.FleetEntry{}}
	code, stdout, _ := ktags(control, call{}, "fleet", "list")
	if code != 0 || !strings.HasPrefix(stdout, "0 customer(s)\n") || strings.Contains(stdout, "evidence:") {
		t.Fatalf("exit %d, stdout %q", code, stdout)
	}
	code, stdout, _ = ktags(control, call{}, "fleet", "list", "--json")
	if doc := decodeOne(t, stdout); code != 0 || !reflect.DeepEqual(field(t, doc, "customers"), []any{}) {
		t.Fatalf("exit %d, doc %+v", code, doc)
	}

	// A state from a newer service is counted under its own name, never folded into ok.
	client.fleet = service.FleetInfo{Now: fleetNow, Customers: []service.FleetEntry{
		{ID: "a", State: "purple", Health: "ok"}, {ID: "b", State: "ok", Health: "ok"}, {ID: "c", State: "purple", Health: "ok"},
	}}
	_, stdout, _ = ktags(control, call{}, "fleet", "list")
	if first := strings.SplitN(stdout, "\n", 2)[0]; first != "3 customer(s): 1 ok, 2 purple" {
		t.Fatalf("summary line %q", first)
	}
	// No customer has evidence, so there is no evidence header.
	if strings.Contains(stdout, "evidence:") {
		t.Fatalf("evidence header without evidence:\n%s", stdout)
	}
}

func TestFleetCells(t *testing.T) {
	tests := []struct{ name, got, want string }{
		{"no versions", versions(nil), "-"},
		{"versions sorted", versions(map[string]string{"rke2": "v1", "cilium": "v2", "rancher": "v3", "argo": "v4"}), "argo=v4,cilium=v2,rancher=v3,rke2=v1"},
		{"nothing active", active(service.FleetEntry{}), "-"},
		{"checking and runs", active(service.FleetEntry{Checking: true, ActiveRuns: []service.ActiveRunInfo{{ID: "r1", Action: "a"}, {ID: "r2", Action: "b"}}}), "health check, r1 (a), r2 (b)"},
		{"fresh", freshness(service.FleetEntry{}), "fresh"},
		{"stale", freshness(service.FleetEntry{Stale: true}), "stale"},
		{"missed", freshness(service.FleetEntry{Missed: 3}), "fresh, 3 missed"},
		{"no time", stampPtr(nil), "-"},
		{"time", stampPtr(at(0)), "2026-09-11T12:00:00Z"},
	}
	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestMeasuredAge(t *testing.T) {
	tests := []struct {
		at   *time.Time
		want string
	}{
		{nil, "never"},
		{at(-time.Second), "in the future"},
		{at(0), "0s ago"},
		{at(59 * time.Second), "59s ago"},
		{at(time.Minute), "1m ago"},
		{at(59 * time.Minute), "59m ago"},
		{at(time.Hour), "1h ago"},
		{at(47 * time.Hour), "47h ago"},
		{at(48 * time.Hour), "2d ago"},
	}
	for _, tc := range tests {
		if got := measured(fleetNow, tc.at); got != tc.want {
			t.Errorf("measured(%v) = %q, want %q", tc.at, got, tc.want)
		}
	}
}
