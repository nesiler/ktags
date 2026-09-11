package cli

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/doctor"
)

var redReport = doctor.Report{Status: doctor.StatusFail, Checks: []doctor.Result{
	{ID: "platform", Title: "platform", Status: doctor.StatusOK, Evidence: "darwin/arm64"},
	{ID: "roots.config", Title: "the config root is private", Status: doctor.StatusFail, Evidence: "/k/config has mode 0755; want 0700", Fix: "chmod 700 '/k/config'"},
	{ID: "service.protocol", Title: "protocol", Status: doctor.StatusWarn, Evidence: "not measured", Fix: "ktags service start"},
}}

// #61-K2: the human report, its exit code, and that doctor never touches the service control.
func TestDoctorHuman(t *testing.T) {
	control, _ := newFake()
	code, stdout, stderr := runCLI(fakeRuntime{control: control, report: redReport}, "doctor")
	if code != 4 || stderr != "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	want := `doctor: 1 failing, 1 warning of 3 checks
  ok    platform  platform
        darwin/arm64
  fail  roots.config  the config root is private
        /k/config has mode 0755; want 0700
        fix: chmod 700 '/k/config'
  warn  service.protocol  protocol
        not measured
        fix: ktags service start
next: run each fix, then ktags doctor again
`
	if stdout != want {
		t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	if len(control.calls) != 0 {
		t.Fatalf("doctor used the service control: %v", control.calls)
	}

	code, stdout, _ = runCLI(fakeRuntime{control: control}, "doctor")
	if code != 0 || stdout != "doctor: all 1 checks ok\n  ok    platform  platform\n        darwin/arm64\n" {
		t.Fatalf("green: exit %d\n%s", code, stdout)
	}
}

// #68-K1 D6: an identical fix is printed once and names the rows it fixes; later rows point
// back to it; a different fix is printed as it is; --json keeps one fix per row.
func TestDoctorDeduplicatesFixes(t *testing.T) {
	report := doctor.Report{Status: doctor.StatusFail, Checks: []doctor.Result{
		{ID: "service.socket", Title: "socket", Status: doctor.StatusFail, Evidence: "no service", Fix: "ktags service start"},
		{ID: "roots.config", Title: "config", Status: doctor.StatusFail, Evidence: "mode 0755", Fix: "chmod 700 '/k/config'"},
		{ID: "service.protocol", Title: "protocol", Status: doctor.StatusWarn, Evidence: "not measured", Fix: "ktags service start"},
		{ID: "inventory.list", Title: "inventory", Status: doctor.StatusWarn, Evidence: "not measured", Fix: "ktags service start"},
	}}
	control, _ := newFake()
	code, stdout, _ := runCLI(fakeRuntime{control: control, report: report}, "doctor")
	want := `doctor: 2 failing, 2 warning of 4 checks
  fail  service.socket  socket
        no service
        fix: ktags service start
        (fixes service.socket, service.protocol, inventory.list)
  fail  roots.config  config
        mode 0755
        fix: chmod 700 '/k/config'
  warn  service.protocol  protocol
        not measured
        fix: as for service.socket
  warn  inventory.list  inventory
        not measured
        fix: as for service.socket
next: run each fix, then ktags doctor again
`
	if code != 4 || stdout != want {
		t.Fatalf("exit %d, stdout:\n%s\nwant:\n%s", code, stdout, want)
	}
	_, stdout, _ = runCLI(fakeRuntime{control: control, report: report}, "doctor", "--json")
	var doc struct {
		Data doctor.Report `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(doc.Data, report) {
		t.Fatalf("json report %+v, want every row with its own fix %+v", doc.Data, report)
	}
}

// #61-K2: exit codes: 0 green, 0 with warnings only, 4 with a failing check, 1 when the report
// cannot be made or the command is misused.
func TestDoctorExitCodes(t *testing.T) {
	warnOnly := doctor.Report{Status: doctor.StatusWarn, Checks: redReport.Checks[2:]}
	tests := []struct {
		name string
		rt   fakeRuntime
		args []string
		code int
		kind string
	}{
		{"green", fakeRuntime{}, nil, 0, "doctor"},
		{"warnings only", fakeRuntime{report: warnOnly}, nil, 0, "doctor"},
		{"a check fails", fakeRuntime{report: redReport}, nil, 4, "doctor"},
		{"roots cannot be resolved", fakeRuntime{doctorErr: errors.New("KTAGS_HOME: must be an absolute path")}, nil, 1, "error"},
		{"extra argument", fakeRuntime{}, []string{"acme"}, 1, "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			control, _ := newFake()
			tc.rt.control = control
			args := append([]string{"doctor"}, tc.args...)
			code, _, _ := runCLI(tc.rt, args...)
			if code != tc.code {
				t.Fatalf("human: exit %d, want %d", code, tc.code)
			}
			code, stdout, _ := runCLI(tc.rt, append(args, "--json")...)
			if doc := decodeOne(t, stdout); code != tc.code || doc.Kind != tc.kind {
				t.Fatalf("json: exit %d kind %q, want %d %q", code, doc.Kind, tc.code, tc.kind)
			}
		})
	}
}

// #61-K2: the --json document shape is pinned: schema, kind and the exact fields of a check.
func TestDoctorJSONShape(t *testing.T) {
	control, _ := newFake()
	code, stdout, stderr := runCLI(fakeRuntime{control: control, report: redReport}, "doctor", "--json")
	if code != 4 || !strings.HasPrefix(stderr, "doctor: 1 failing") {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"schema": "ktags.cli/v1",
		"kind":   "doctor",
		"data": map[string]any{
			"status": "fail",
			"checks": []any{
				map[string]any{"id": "platform", "title": "platform", "status": "ok", "evidence": "darwin/arm64"},
				map[string]any{"id": "roots.config", "title": "the config root is private", "status": "fail", "evidence": "/k/config has mode 0755; want 0700", "fix": "chmod 700 '/k/config'"},
				map[string]any{"id": "service.protocol", "title": "protocol", "status": "warn", "evidence": "not measured", "fix": "ktags service start"},
			},
		},
	}
	if !reflect.DeepEqual(doc, want) {
		t.Fatalf("document %v\nwant %v", doc, want)
	}
}
