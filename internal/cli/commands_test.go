package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/service"
)

// "cluster" is a prefix of other IDs, so resolving the action must take the longest match.
var testActions = []service.ActionInfo{
	{ID: "cluster", Title: "Cluster", Help: "Shows the cluster.", Target: "global", Effect: "read-only"},
	{ID: "cluster decommission", Title: "Decommission", Help: "Removes the cluster.", Target: "customer", Effect: "destructive"},
	{ID: "cluster upgrade", Title: "Upgrade", Help: "Upgrades the cluster.", Target: "customer", Effect: "mutating"},
	{ID: "doctor", Title: "Doctor", Help: "Checks the laptop.", Target: "global", Effect: "read-only"},
	{ID: "mystery", Title: "Mystery", Help: "Has an effect this build does not know.", Target: "global", Effect: "explosive"},
	{ID: "node drain", Title: "Drain", Help: "Drains a node.", Target: "node", Effect: "mutating"},
	{ID: "test echo", Title: "Echo", Help: "Echoes the text.", Target: "customer", Effect: "read-only", Args: []service.ArgInfo{
		{Name: "text", Kind: "string", Required: true, Help: "what to echo"},
		{Name: "count", Kind: "int", Help: "how often"},
		{Name: "loud", Kind: "bool", Help: "shout"},
	}},
	{ID: "weird", Title: "Weird", Help: "Has a target kind this build does not know.", Target: "cluster", Effect: "read-only"},
}

var testCustomers = []service.CustomerInfo{
	{ID: "acme", Name: "Acme", Environment: "prod", Cluster: "acme-1", Nodes: 3},
	{ID: "beta", Name: "Beta", Environment: "staging", Cluster: "beta-1", Nodes: 1},
	{ID: "broken", Problem: "cannot parse the customer record at line 3"},
	// Inventory validation refuses an unknown environment today; the CLI must still treat it
	// as prod, not as "not prod".
	{ID: "gamma", Name: "Gamma", Environment: "", Cluster: "gamma-1", Nodes: 1},
}

// newFake returns a running service whose next run reports two events and succeeds, and which
// holds r7 (finished, three events) and rB (active on beta) and rP (active on acme, prod).
func newFake() (*fakeControl, *fakeClient) {
	c := &fakeClient{
		actions:   testActions,
		customers: testCustomers,
		runs:      map[string]*fakeRun{},
		script: fakeRun{
			events: []service.Event{{ID: 1, Step: "check", Message: "first"}, {ID: 2, Message: "second"}},
			final:  service.RunInfo{Status: "succeeded", Result: &service.ResultInfo{Status: "succeeded", Summary: "done"}},
		},
	}
	r7 := service.RunInfo{ID: "r7", Action: "doctor", Target: service.Target{Kind: "global"}, Status: "succeeded", LastEventID: 3,
		Result: &service.ResultInfo{Status: "succeeded", Summary: "all green"}}
	c.runs["r7"] = &fakeRun{info: r7, final: r7, events: []service.Event{{ID: 1, Message: "e1"}, {ID: 2, Message: "e2"}, {ID: 3, Message: "e3"}}}
	for id, customer := range map[string]string{"rB": "beta", "rP": "acme", "rG": "gone", "rU": "gamma"} {
		info := service.RunInfo{ID: id, Action: "cluster upgrade", Target: service.Target{Kind: "customer", Customer: customer}, Status: "running", LastEventID: 1}
		c.runs[id] = &fakeRun{info: info, block: true, events: []service.Event{{ID: 1, Message: "upgrading"}}}
	}
	return &fakeControl{status: running, client: c}, c
}

type call struct {
	stdin    string
	terminal bool
}

// ktags runs the CLI against control. Interrupt is wired to the fake: a followed run that
// blocks is interrupted, as if the operator pressed Ctrl-C.
func ktags(control *fakeControl, in call, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	streams := Streams{
		In: strings.NewReader(in.stdin), Out: &stdout, Err: &stderr, Terminal: in.terminal,
		Interrupt: func(ctx context.Context) (context.Context, context.CancelFunc) {
			ictx, cancel := context.WithCancel(ctx)
			control.client.onBlock = cancel
			return ictx, cancel
		},
	}
	code := Run(context.Background(), fakeRuntime{control: control}, args, streams)
	return code, stdout.String(), stderr.String()
}

func contains(t *testing.T, name, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("%s lacks %q:\n%s", name, want, got)
		}
	}
}

// decodeOne decodes stdout as exactly one CLI document.
func decodeOne(t *testing.T, stdout string) document {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stdout))
	var doc struct {
		Schema string          `json:"schema"`
		Kind   string          `json:"kind"`
		Data   json.RawMessage `json:"data"`
	}
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, stdout)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON document (%v):\n%s", err, stdout)
	}
	if doc.Schema != schema || doc.Kind == "" || len(doc.Data) == 0 {
		t.Fatalf("document header schema=%q kind=%q data=%s, want schema %q, a kind and data", doc.Schema, doc.Kind, doc.Data, schema)
	}
	var data any
	if err := json.Unmarshal(doc.Data, &data); err != nil {
		t.Fatal(err)
	}
	return document{Schema: doc.Schema, Kind: doc.Kind, Data: data}
}

func field(t *testing.T, doc document, path ...string) any {
	t.Helper()
	v := doc.Data
	for _, key := range path {
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("%s: %q is not reachable in %v", doc.Kind, strings.Join(path, "."), doc.Data)
		}
		v = m[key]
	}
	return v
}

// #13-K1: discovery.
func TestActionDiscovery(t *testing.T) {
	control, _ := newFake()
	code, stdout, _ := ktags(control, call{}, "action", "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	contains(t, "stdout", stdout, "8 action(s)", "test echo", "customer", "read-only", "node drain", "mutating")

	code, stdout, _ = ktags(control, call{}, "action", "show", "test", "echo")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	contains(t, "stdout", stdout, "usage   ktags action run test echo <customer> --text <string> [--count <int>] [--loud]", "Echoes the text.", "--text", "required")

	code, stdout, _ = ktags(control, call{}, "--json", "action", "show", "node", "drain")
	doc := decodeOne(t, stdout)
	if code != 0 || doc.Kind != "action.show" || field(t, doc, "usage") != "ktags action run node drain <customer> <node>" {
		t.Fatalf("exit %d, doc %+v", code, doc)
	}
}

// #13-K1: start, progress and final result.
func TestActionRunFollowsToResult(t *testing.T) {
	control, client := newFake()
	code, stdout, stderr := ktags(control, call{}, "action", "run", "test", "echo", "beta", "--text", "hi", "--count=2", "--loud")
	if code != 0 {
		t.Fatalf("exit %d\nstdout %s\nstderr %s", code, stdout, stderr)
	}
	want := startCall{action: "test echo", target: service.Target{Kind: "customer", Customer: "beta"}, args: map[string]any{"text": "hi", "count": 2, "loud": true}}
	if len(client.started) != 1 || !reflect.DeepEqual(client.started[0], want) {
		t.Fatalf("started %+v, want %+v", client.started, want)
	}
	contains(t, "stdout", stdout, "started run run-1 (test echo)", "  1 check: first", "  2 second", "run run-1 succeeded: done")
	if !reflect.DeepEqual(client.after, []uint64{0}) {
		t.Fatalf("events cursor %v, want [0]", client.after)
	}

	_, _, _ = ktags(control, call{}, "action", "run", "test", "echo", "beta", "--loud=false", "--text=x y")
	if got := client.started[1].args; !reflect.DeepEqual(got, map[string]any{"text": "x y", "loud": false}) {
		t.Fatalf("args %v", got)
	}

	code, stdout, _ = ktags(control, call{}, "action", "run", "node", "drain", "beta", "n1", "--yes")
	if code != 0 || client.started[2].target != (service.Target{Kind: "node", Customer: "beta", Node: "n1"}) {
		t.Fatalf("exit %d, started %+v\n%s", code, client.started[2], stdout)
	}

	// A value that is itself a CLI flag stays a value when given in one word.
	_, _, _ = ktags(control, call{}, "action", "run", "test", "echo", "beta", "--text=--yes")
	if got := client.started[3].args; !reflect.DeepEqual(got, map[string]any{"text": "--yes"}) {
		t.Fatalf("args %v", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout is closed") }

// A --json document that cannot be written is a failure, never exit 0.
func TestDocumentWriteFails(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"run", "watch", "r7"}, {"service", "status"}} {
		control, _ := newFake()
		var stderr bytes.Buffer
		code := Run(context.Background(), fakeRuntime{control: control}, append(args, "--json"), Streams{Out: failingWriter{}, Err: &stderr})
		if code != 1 || !strings.Contains(stderr.String(), "ktags: stdout is closed") {
			t.Fatalf("%q: exit %d, stderr %q; want 1 and the write error", args, code, stderr.String())
		}
	}
	var stderr bytes.Buffer
	code := Run(context.Background(), fakeRuntime{control: &fakeControl{}}, []string{"service", "status", "--json"}, Streams{Out: failingWriter{}, Err: &stderr})
	if code != 1 || !strings.Contains(stderr.String(), "ktags: stdout is closed") {
		t.Fatalf("service not running: exit %d, stderr %q; want 1 and the write error", code, stderr.String())
	}
}

// #13-K1: the final result decides the exit code (docs/guides/development.md §3).
func TestFinalResultExitCodes(t *testing.T) {
	tests := []struct {
		status, summary, problem string
		code                     int
		line                     string
	}{
		{"succeeded", "done", "", 0, "run run-1 succeeded: done"},
		{"failed", "boom", "", 3, "run run-1 failed: boom"},
		{"cancelled", "cancelled by the operator", "", 3, "run run-1 cancelled: cancelled by the operator"},
		{"incomplete", "", "result.json is torn", 3, "run run-1 incomplete: result.json is torn"},
	}
	for _, tc := range tests {
		t.Run(tc.status, func(t *testing.T) {
			control, client := newFake()
			client.script.final = service.RunInfo{Status: tc.status, Problem: tc.problem}
			if tc.summary != "" {
				client.script.final.Result = &service.ResultInfo{Status: tc.status, Summary: tc.summary}
			}
			code, stdout, stderr := ktags(control, call{}, "action", "run", "doctor")
			if code != tc.code {
				t.Fatalf("exit %d, want %d\n%s\n%s", code, tc.code, stdout, stderr)
			}
			contains(t, "stdout", stdout, tc.line)
			if stderr != "" {
				t.Fatalf("a finished run prints no diagnostics, got %q", stderr)
			}
			code, stdout, _ = ktags(control, call{}, "action", "run", "doctor", "--json")
			doc := decodeOne(t, stdout)
			if code != tc.code || doc.Kind != "run.result" || field(t, doc, "run", "status") != tc.status {
				t.Fatalf("json: exit %d, doc %+v", code, doc)
			}
		})
	}
}

// #13-K1: detach returns at once and names the reconnect command.
func TestDetach(t *testing.T) {
	control, client := newFake()
	code, stdout, _ := ktags(control, call{}, "action", "run", "doctor", "--detach")
	if code != 0 || len(client.after) != 0 {
		t.Fatalf("exit %d, events calls %v; want 0 and no follow", code, client.after)
	}
	contains(t, "stdout", stdout, "started run run-1 (doctor)", "next: ktags run watch run-1")

	code, stdout, _ = ktags(control, call{}, "action", "run", "--json", "--detach", "doctor")
	doc := decodeOne(t, stdout)
	if code != 0 || doc.Kind != "run.started" || field(t, doc, "run", "id") != "run-2" {
		t.Fatalf("exit %d, doc %+v", code, doc)
	}
}

// #13-K1: an interrupt while following detaches; it never cancels the run.
func TestFollowInterruptDetaches(t *testing.T) {
	control, client := newFake()
	client.script.block = true
	client.script.events = client.script.events[:1]
	code, stdout, stderr := ktags(control, call{}, "action", "run", "doctor")
	if code != 0 || len(client.cancelled) != 0 {
		t.Fatalf("exit %d, cancelled %v; want 0 and no cancel", code, client.cancelled)
	}
	contains(t, "stdout", stdout, "  1 check: first")
	contains(t, "stderr", stderr, "detached from run run-1 at event 1; it continues", "next: ktags run watch run-1 --after 1")

	code, stdout, _ = ktags(control, call{}, "run", "watch", "rB", "--json")
	doc := decodeOne(t, stdout)
	if code != 0 || doc.Kind != "run.detached" || field(t, doc, "run_id") != "rB" || field(t, doc, "last_event_id") != float64(1) {
		t.Fatalf("exit %d, doc %+v", code, doc)
	}
}

// #13-K1: reconnect resumes after the client's cursor and ends with the stored result.
func TestWatchResumesFromCursor(t *testing.T) {
	control, client := newFake()
	code, stdout, _ := ktags(control, call{}, "run", "watch", "r7", "--after", "2")
	if code != 0 || !reflect.DeepEqual(client.after, []uint64{2}) {
		t.Fatalf("exit %d, cursor %v; want 0 and [2]", code, client.after)
	}
	contains(t, "stdout", stdout, "  3 e3", "run r7 succeeded: all green")
	if strings.Contains(stdout, "e1") || strings.Contains(stdout, "e2") {
		t.Fatalf("events before the cursor were printed:\n%s", stdout)
	}

	code, _, stderr := ktags(control, call{}, "run", "watch", "nope")
	if code != 1 {
		t.Fatalf("unknown run: exit %d", code)
	}
	contains(t, "stderr", stderr, "ktags: run: no run nope", "next: list the runs")

	code, stdout, _ = ktags(control, call{}, "run", "status", "r7")
	if code != 0 {
		t.Fatalf("status exit %d", code)
	}
	contains(t, "stdout", stdout, "run r7 succeeded", "result   succeeded: all green")
	code, stdout, _ = ktags(control, call{}, "run", "list")
	if code != 0 {
		t.Fatalf("list exit %d", code)
	}
	contains(t, "stdout", stdout, "2 run(s)", "r7", "rB", "beta")
}

// A stream that breaks mid-run names the reconnect cursor.
func TestWatchConnectionLost(t *testing.T) {
	control, client := newFake()
	client.lose = true
	code, stdout, stderr := ktags(control, call{}, "run", "watch", "r7")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	contains(t, "stdout", stdout, "  3 e3")
	contains(t, "stderr", stderr, "ktags: lost run r7 after event 3", "next: check the service with ktags service status, then run: ktags run watch r7 --after 3")
}

// #13-K1: cancel confirms like a change on the run's customer (docs/guides/ui.md §7), then
// shows the final result.
func TestCancel(t *testing.T) {
	tests := []struct {
		name      string
		run       string
		in        call
		args      []string
		code      int
		cancelled bool
		stderr    string
	}{
		{"staging with --yes", "rB", call{}, []string{"--yes"}, 0, true, ""},
		{"staging needs confirmation", "rB", call{}, nil, 2, false, "needs confirmation and stdin is not a terminal"},
		{"staging answered y", "rB", call{stdin: "y\n", terminal: true}, nil, 0, true, "cancel run rB (cluster upgrade on beta)\nContinue? [y/N]"},
		{"staging answered no", "rB", call{stdin: "n\n", terminal: true}, nil, 2, false, "not confirmed; nothing was started"},
		{"prod refuses --yes", "rP", call{}, []string{"--yes"}, 2, false, "--yes does not replace it"},
		{"prod typed name", "rP", call{stdin: "acme\n", terminal: true}, []string{"--yes"}, 0, true, `Type "acme" to continue`},
		{"prod wrong name", "rP", call{stdin: "beta\n", terminal: true}, nil, 2, false, "the typed name does not match"},
		{"prod empty answer", "rP", call{terminal: true}, nil, 2, false, "the typed name does not match"},
		{"prod prefix of the name", "rP", call{stdin: "ac\n", terminal: true}, nil, 2, false, "the typed name does not match"},
		{"prod name in another case", "rP", call{stdin: "ACME\n", terminal: true}, nil, 2, false, "the typed name does not match"},
		{"customer gone counts as prod", "rG", call{}, []string{"--yes"}, 2, false, "--yes does not replace it"},
		{"unknown environment counts as prod", "rU", call{}, []string{"--yes"}, 2, false, "--yes does not replace it"},
		{"finished run", "r7", call{}, []string{"--yes"}, 2, false, "run r7 is succeeded; only an active run can be cancelled"},
		{"unknown run", "nope", call{}, []string{"--yes"}, 1, false, "run: no run nope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			control, client := newFake()
			code, stdout, stderr := ktags(control, tc.in, append([]string{"run", "cancel", tc.run}, tc.args...)...)
			if code != tc.code || (len(client.cancelled) == 1) != tc.cancelled {
				t.Fatalf("exit %d, cancelled %v; want %d, %v\nstdout %s\nstderr %s", code, client.cancelled, tc.code, tc.cancelled, stdout, stderr)
			}
			contains(t, "stderr", stderr, tc.stderr)
			if tc.cancelled {
				contains(t, "stdout", stdout, "cancel requested for run "+tc.run, "run "+tc.run+" cancelled: cancelled by the operator")
			}
		})
	}
}

// ADR-0003 §3: action run confirms by danger level before anything starts.
func TestConfirmation(t *testing.T) {
	upgrade := func(customer string, extra ...string) []string {
		return append([]string{"action", "run", "cluster", "upgrade", customer}, extra...)
	}
	tests := []struct {
		name    string
		in      call
		args    []string
		code    int
		started bool
		stderr  string
	}{
		{"read-only starts at once", call{}, []string{"action", "run", "doctor"}, 0, true, ""},
		{"change needs confirmation", call{}, upgrade("beta"), 2, false, "needs confirmation and stdin is not a terminal; nothing was started"},
		{"--yes confirms outside prod", call{}, upgrade("beta", "--yes"), 0, true, ""},
		{"answered y", call{stdin: "y\n", terminal: true}, upgrade("beta"), 0, true, `run "cluster upgrade" on beta (staging)` + "\nContinue? [y/N]"},
		{"answered yes", call{stdin: "yes\n", terminal: true}, upgrade("beta"), 0, true, ""},
		{"answered no", call{stdin: "n\n", terminal: true}, upgrade("beta"), 2, false, "not confirmed"},
		{"answered a word starting with y", call{stdin: "yep\n", terminal: true}, upgrade("beta"), 2, false, "not confirmed"},
		{"no answer", call{terminal: true}, upgrade("beta"), 2, false, "not confirmed"},
		{"prod refuses --yes", call{}, upgrade("acme", "--yes"), 2, false, "--yes does not replace it"},
		{"prod typed name", call{stdin: "acme\n", terminal: true}, upgrade("acme", "--yes"), 0, true, `Type "acme" to continue`},
		{"prod answered y", call{stdin: "y\n", terminal: true}, upgrade("acme"), 2, false, "the typed name does not match"},
		{"prod empty answer", call{terminal: true}, upgrade("acme"), 2, false, "the typed name does not match"},
		{"prod prefix of the name", call{stdin: "ac\n", terminal: true}, upgrade("acme"), 2, false, "the typed name does not match"},
		{"prod name in another case", call{stdin: "ACME\n", terminal: true}, upgrade("acme"), 2, false, "the typed name does not match"},
		{"unknown environment counts as prod", call{}, upgrade("gamma", "--yes"), 2, false, "--yes does not replace it"},
		{"unknown environment typed name", call{stdin: "gamma\n", terminal: true}, upgrade("gamma"), 0, true, `Type "gamma" to continue`},
		{"destructive outside prod needs the name", call{stdin: "y\n", terminal: true}, []string{"action", "run", "cluster", "decommission", "beta"}, 2, false, `Type "beta" to continue`},
		{"destructive typed name", call{stdin: "beta\n", terminal: true}, []string{"action", "run", "cluster", "decommission", "beta"}, 0, true, ""},
		{"unknown effect needs the name", call{}, []string{"action", "run", "mystery", "--yes"}, 2, false, "--yes does not replace it"},
		{"unknown effect typed name", call{stdin: "mystery\n", terminal: true}, []string{"action", "run", "mystery"}, 0, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			control, client := newFake()
			code, stdout, stderr := ktags(control, tc.in, tc.args...)
			if code != tc.code || (len(client.started) == 1) != tc.started {
				t.Fatalf("exit %d, started %v; want %d, %v\nstdout %s\nstderr %s", code, client.started, tc.code, tc.started, stdout, stderr)
			}
			contains(t, "stderr", stderr, tc.stderr)
		})
	}
}

// #13-K3: unknown customer, action and argument errors name the next command and exit 1.
func TestUnknownInputs(t *testing.T) {
	echo := func(extra ...string) []string { return append([]string{"action", "run", "test", "echo"}, extra...) }
	tests := []struct {
		name    string
		args    []string
		errCode string
		stderr  []string
	}{
		{"unknown action", []string{"action", "run", "nope"}, "unknown_action", []string{`ktags: no such action "nope"`, "next: ktags action list"}},
		{"unknown action to show", []string{"action", "show", "test"}, "unknown_action", []string{`no such action "test"`, "next: ktags action list"}},
		{"show with extra words", []string{"action", "show", "test", "echo", "extra"}, "unknown_action", []string{`no such action "test echo extra"`}},
		{"show without an action", []string{"action", "show"}, "usage", []string{"missing <action>", "usage: ktags action show <action>"}},
		{"unknown customer", echo("zeta", "--text", "x"), "unknown_customer", []string{`ktags: no customer "zeta" in the inventory`, "next: ktags customer list"}},
		{"prefix of a customer", echo("ac", "--text", "x"), "unknown_customer", []string{`ktags: no customer "ac" in the inventory`}},
		{"abbreviated argument", echo("beta", "--text", "x", "--lou"), "unknown_argument", []string{`action "test echo" has no argument "--lou"`}},
		{"refused customer record", echo("broken", "--text", "x"), "customer_refused", []string{`the record of customer "broken" is refused: cannot parse`, "then check it with: ktags customer list"}},
		{"unknown argument", echo("beta", "--text", "x", "--colour", "red"), "unknown_argument", []string{`action "test echo" has no argument "--colour"`, "next: ktags action show test echo"}},
		{"single-dash argument", echo("beta", "--text", "x", "-v"), "unknown_argument", []string{`has no argument "-v"`}},
		{"missing required argument", echo("beta"), "invalid_argument", []string{"missing required argument --text", "next: ktags action show test echo"}},
		{"int argument", echo("beta", "--text", "x", "--count", "many"), "invalid_argument", []string{`argument --count must be an int, got "many"`}},
		{"missing value", echo("beta", "--text"), "invalid_argument", []string{"argument --text needs a string value"}},
		{"bool value", echo("beta", "--text", "x", "--loud=maybe"), "invalid_argument", []string{`argument --loud must be true or false, got "maybe"`}},
		{"argument twice", echo("beta", "--text", "a", "--text", "b"), "invalid_argument", []string{"argument --text is given twice"}},
		{"missing customer", echo("--text", "x"), "invalid_argument", []string{`action "test echo" needs <customer>`, "next: ktags action run test echo <customer> --text <string>"}},
		{"missing node", []string{"action", "run", "node", "drain", "beta"}, "invalid_argument", []string{`action "node drain" needs <node>`}},
		{"extra target", []string{"action", "run", "doctor", "extra"}, "invalid_argument", []string{`action "doctor": unexpected argument "extra"`, "next: ktags action run doctor"}},
		{"unknown target kind", []string{"action", "run", "weird"}, "unsupported_target", []string{`target kind "cluster"`}},
		{"missing action", []string{"action", "run", "--detach"}, "usage", []string{"missing <action>", "usage: ktags action run"}},
		{"no command", nil, "usage", []string{"the ktags TUI is not available yet", "usage: ktags <command>"}},
		{"unknown command", []string{"frob"}, "usage", []string{`unknown command "frob"`, "usage: ktags <command>"}},
		{"unknown action command", []string{"action", "frob"}, "usage", []string{`unknown action command "frob"`, "usage: ktags action"}},
		{"missing run", []string{"run", "watch"}, "usage", []string{"missing <run>", "usage: ktags run watch"}},
		{"bad cursor", []string{"run", "watch", "r7", "--after", "x"}, "usage", []string{`invalid argument "x"`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			control, client := newFake()
			code, stdout, stderr := ktags(control, call{}, tc.args...)
			if code != 1 || stdout != "" || len(client.started) != 0 {
				t.Fatalf("exit %d, started %v, stdout %q; want 1, nothing started, empty stdout\nstderr %s", code, client.started, stdout, stderr)
			}
			contains(t, "stderr", stderr, tc.stderr...)

			code, stdout, _ = ktags(control, call{}, append(tc.args, "--json")...)
			doc := decodeOne(t, stdout)
			if code != 1 || doc.Kind != "error" || field(t, doc, "code") != tc.errCode || field(t, doc, "exit") != float64(1) {
				t.Fatalf("json: exit %d, doc %+v; want exit 1, error %q", code, doc, tc.errCode)
			}
		})
	}
}

// #13-K2: every --json command prints exactly one versioned document on stdout; human text
// and diagnostics go to stderr.
func TestJSONStdoutOnly(t *testing.T) {
	tests := []struct {
		args []string
		kind string
		code int
	}{
		{[]string{"version"}, "version", 0},
		{[]string{"--version"}, "version", 0},
		{[]string{"service", "status"}, "service.status", 0},
		{[]string{"service", "start"}, "service.start", 0},
		{[]string{"service", "stop"}, "service.stop", 0},
		{[]string{"customer", "list"}, "customer.list", 0},
		{[]string{"action", "list"}, "action.list", 0},
		{[]string{"action", "show", "test", "echo"}, "action.show", 0},
		{[]string{"action", "run", "doctor"}, "run.result", 0},
		{[]string{"action", "run", "doctor", "--detach"}, "run.started", 0},
		{[]string{"run", "list"}, "run.list", 0},
		{[]string{"run", "status", "r7"}, "run.status", 0},
		{[]string{"run", "watch", "r7"}, "run.result", 0},
		{[]string{"run", "cancel", "rB", "--yes"}, "run.cancel", 0},
		{[]string{"action", "list", "--help"}, "help", 0},
		{[]string{"action", "run", "--help"}, "help", 0},
		{[]string{"action", "run", "nope"}, "error", 1},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			control, _ := newFake()
			code, stdout, stderr := ktags(control, call{}, append(tc.args, "--json")...)
			doc := decodeOne(t, stdout)
			if code != tc.code || doc.Kind != tc.kind {
				t.Fatalf("exit %d kind %q, want %d %q", code, doc.Kind, tc.code, tc.kind)
			}
			if strings.Contains(stderr, `"schema"`) {
				t.Fatalf("the JSON document leaked to stderr:\n%s", stderr)
			}
		})
	}

	control, _ := newFake()
	_, stdout, stderr := ktags(control, call{}, "action", "list", "--json")
	contains(t, "stderr", stderr, "8 action(s)")
	if ids := field(t, decodeOne(t, stdout), "actions"); len(ids.([]any)) != 8 {
		t.Fatalf("actions %v", ids)
	}
	control.client.customers = nil
	_, stdout, _ = ktags(control, call{}, "customer", "list", "--json")
	if got := field(t, decodeOne(t, stdout), "customers"); !reflect.DeepEqual(got, []any{}) {
		t.Fatalf("no customers encode as %v, want []", got)
	}
}

// Errors on the way to the service or from it reach the operator with their exit code, and
// nothing is started or cancelled.
func TestClientErrors(t *testing.T) {
	code, _, stderr := runCLI(fakeRuntime{err: errors.New("KTAGS_HOME must be an absolute path")}, "action", "list")
	if code != 1 {
		t.Fatalf("unresolvable roots: exit %d", code)
	}
	contains(t, "stderr", stderr, "ktags: KTAGS_HOME must be an absolute path")

	control, _ := newFake()
	control.err = &service.Error{Code: service.CodeProtocolMismatch, Message: "the service speaks ktags protocol 2", Hint: "upgrade ktags"}
	code, _, stderr = ktags(control, call{}, "action", "list")
	if code != 1 {
		t.Fatalf("status error: exit %d", code)
	}
	contains(t, "stderr", stderr, "ktags: the service speaks ktags protocol 2", "next: upgrade ktags")

	control, _ = newFake()
	control.status = service.Status{Socket: "/s.sock"}
	control.startErr = &service.Error{Code: service.CodeUnavailable, Message: "launchctl bootstrap failed", Hint: "inspect the job"}
	code, _, stderr = ktags(control, call{}, "action", "list")
	if code != 1 {
		t.Fatalf("autostart error: exit %d", code)
	}
	contains(t, "stderr", stderr, "ktags: launchctl bootstrap failed", "next: inspect the job")

	control, client := newFake()
	client.customersErr = errors.New("cannot list the customers directory")
	code, _, stderr = ktags(control, call{}, "action", "run", "test", "echo", "beta", "--text", "x")
	if code != 1 || len(client.started) != 0 {
		t.Fatalf("inventory error on run: exit %d, started %v", code, client.started)
	}
	contains(t, "stderr", stderr, "ktags: cannot list the customers directory")
	code, _, _ = ktags(control, call{}, "run", "cancel", "rP", "--yes")
	if code != 1 || len(client.cancelled) != 0 {
		t.Fatalf("inventory error on cancel: exit %d, cancelled %v", code, client.cancelled)
	}

	control, client = newFake()
	client.cancelErr = &service.Error{Code: service.CodeConflict, Message: "run rB is succeeded and not active in this service", Hint: "check the run"}
	code, _, stderr = ktags(control, call{}, "run", "cancel", "rB", "--yes")
	if code != 2 {
		t.Fatalf("cancel conflict: exit %d, want 2", code)
	}
	contains(t, "stderr", stderr, "not active in this service", "next: check the run")
}

// Every command hands a failure to reach the service, and a failure of each of its requests,
// to the operator with exit 1; nothing is started or cancelled.
func TestPassthroughErrors(t *testing.T) {
	commands := [][]string{
		{"customer", "list"}, {"fleet", "list"}, {"action", "list"}, {"action", "show", "doctor"}, {"action", "run", "doctor"},
		{"run", "list"}, {"run", "status", "r7"}, {"run", "watch", "r7"}, {"run", "cancel", "rB", "--yes"},
	}
	for _, args := range commands {
		control, client := newFake()
		control.err = &service.Error{Code: service.CodeProtocolMismatch, Message: "the service speaks ktags protocol 2", Hint: "upgrade ktags"}
		code, _, stderr := ktags(control, call{}, args...)
		if code != 1 || len(client.started)+len(client.cancelled) != 0 || !strings.Contains(stderr, "ktags: the service speaks ktags protocol 2") {
			t.Fatalf("%q with an unreachable service: exit %d, started %v, cancelled %v, stderr %q", args, code, client.started, client.cancelled, stderr)
		}
	}

	failing := errors.New("the service failed")
	tests := []struct {
		args []string
		set  func(*fakeClient)
	}{
		{[]string{"customer", "list"}, func(c *fakeClient) { c.customersErr = failing }},
		{[]string{"fleet", "list"}, func(c *fakeClient) { c.fleetErr = failing }},
		{[]string{"action", "list"}, func(c *fakeClient) { c.actionsErr = failing }},
		{[]string{"action", "show", "doctor"}, func(c *fakeClient) { c.actionsErr = failing }},
		{[]string{"action", "run", "doctor"}, func(c *fakeClient) { c.actionsErr = failing }},
		{[]string{"action", "run", "doctor"}, func(c *fakeClient) { c.startErr = failing }},
		{[]string{"run", "list"}, func(c *fakeClient) { c.runsErr = failing }},
		{[]string{"run", "status", "r7"}, func(c *fakeClient) { c.statusErr = failing }},
		{[]string{"run", "cancel", "rB", "--yes"}, func(c *fakeClient) { c.statusErr = failing }},
	}
	for _, tc := range tests {
		control, client := newFake()
		tc.set(client)
		code, stdout, stderr := ktags(control, call{}, tc.args...)
		if code != 1 || stdout != "" || len(client.started)+len(client.cancelled) != 0 || !strings.Contains(stderr, "ktags: the service failed") {
			t.Fatalf("%q: exit %d, stdout %q, started %v, cancelled %v, stderr %q", tc.args, code, stdout, client.started, client.cancelled, stderr)
		}
	}
}

// A --json command never starts the service; an interactive one does.
func TestJSONDoesNotStartService(t *testing.T) {
	control, _ := newFake()
	control.status = service.Status{Socket: "/s.sock"}
	up := running
	control.afterStart, control.started = &up, true

	code, stdout, stderr := ktags(control, call{}, "action", "list", "--json")
	doc := decodeOne(t, stdout)
	if code != 1 || doc.Kind != "error" || field(t, doc, "code") != "service_not_running" {
		t.Fatalf("exit %d, doc %+v", code, doc)
	}
	contains(t, "stderr", stderr, "the ktags service is not running; --json commands do not start it", "next: ktags service start")
	for _, c := range control.calls {
		if c == "start" {
			t.Fatalf("a --json command started the service: calls %v", control.calls)
		}
	}

	control.calls = nil
	code, stdout, stderr = ktags(control, call{}, "action", "list")
	if code != 0 || !reflect.DeepEqual(control.calls, []string{"status", "start"}) {
		t.Fatalf("exit %d, calls %v; want 0 and status, start", code, control.calls)
	}
	contains(t, "stderr", stderr, "ktags: started the ktags service (pid 4242)")
	contains(t, "stdout", stdout, "8 action(s)")
}
