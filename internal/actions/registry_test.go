package actions_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/actions"
)

// fakeAction records how it was called so tests can prove which path ran.
type fakeAction struct {
	descriptor actions.Descriptor
	checkErr   error
	result     actions.Result
	checked    int
	ran        int
	lastReq    actions.Request
}

func (f *fakeAction) Descriptor() actions.Descriptor { return f.descriptor }

func (f *fakeAction) Check(_ context.Context, _ actions.Request) error {
	f.checked++
	return f.checkErr
}

func (f *fakeAction) Run(_ context.Context, req actions.Request, progress actions.Progress) (actions.Result, error) {
	f.ran++
	f.lastReq = req
	progress.Report(actions.Event{Step: "start", Message: f.descriptor.ID})
	progress.Report(actions.Event{Step: "done", Message: f.descriptor.ID})
	return f.result, nil
}

func readOnlyFake() *fakeAction {
	return &fakeAction{
		descriptor: actions.Descriptor{
			ID: "health", Title: "Health", Help: "Check cluster health.",
			Target: actions.TargetCustomer, Effect: actions.EffectReadOnly,
		},
		result: actions.Result{Status: actions.StatusSucceeded, Summary: "all checks green"},
	}
}

func mutatingFake() *fakeAction {
	return &fakeAction{
		descriptor: actions.Descriptor{
			ID: "node drain", Title: "Drain node", Help: "Drain one node.",
			Target: actions.TargetNode, Effect: actions.EffectMutating,
			Args: []actions.Arg{
				{Name: "reason", Kind: actions.ArgString, Required: true, Help: "Why."},
				{Name: "timeout", Kind: actions.ArgInt, Help: "Seconds."},
				{Name: "force", Kind: actions.ArgBool, Help: "Ignore PDBs."},
			},
		},
		result: actions.Result{Status: actions.StatusSucceeded, Summary: "drained"},
	}
}

func mustRegistry(t *testing.T, list ...actions.Action) *actions.Registry {
	t.Helper()
	registry, err := actions.NewRegistry(list...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func TestRegistryExecutesReadOnlyAndMutatingThroughSameInterface(t *testing.T) {
	readOnly, mutating := readOnlyFake(), mutatingFake()
	registry := mustRegistry(t, mutating, readOnly)

	cases := []struct {
		name   string
		id     string
		fake   *fakeAction
		req    actions.Request
		effect actions.Effect
	}{
		{
			name: "read-only", id: "health", fake: readOnly, effect: actions.EffectReadOnly,
			req: actions.Request{Target: actions.Target{Kind: actions.TargetCustomer, Customer: "acme"}},
		},
		{
			name: "mutating", id: "node drain", fake: mutating, effect: actions.EffectMutating,
			req: actions.Request{
				Target: actions.Target{Kind: actions.TargetNode, Customer: "acme", Node: "acme-srv-1"},
				Args:   map[string]any{"reason": "kernel update", "timeout": 60, "force": true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, ok := registry.Lookup(tc.id)
			if !ok {
				t.Fatalf("Lookup(%q) not found", tc.id)
			}
			if got := action.Descriptor().Effect; got != tc.effect {
				t.Fatalf("effect = %q, want %q", got, tc.effect)
			}

			var events []actions.Event
			result, err := registry.Execute(context.Background(), tc.id, tc.req,
				actions.ProgressFunc(func(e actions.Event) { events = append(events, e) }))
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result != tc.fake.result {
				t.Fatalf("result = %+v, want %+v", result, tc.fake.result)
			}
			if tc.fake.checked != 1 || tc.fake.ran != 1 {
				t.Fatalf("checked=%d ran=%d, want 1 and 1", tc.fake.checked, tc.fake.ran)
			}
			if !reflect.DeepEqual(tc.fake.lastReq, tc.req) {
				t.Fatalf("request = %+v, want %+v", tc.fake.lastReq, tc.req)
			}
			wantEvents := []actions.Event{{Step: "start", Message: tc.id}, {Step: "done", Message: tc.id}}
			if !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("events = %+v, want %+v", events, wantEvents)
			}
		})
	}
}

func TestRegistryListIsSortedByID(t *testing.T) {
	registry := mustRegistry(t, mutatingFake(), readOnlyFake())
	var ids []string
	for _, descriptor := range registry.List() {
		ids = append(ids, descriptor.ID)
	}
	if want := []string{"health", "node drain"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("List IDs = %v, want %v", ids, want)
	}
	if _, ok := registry.Lookup("missing"); ok {
		t.Fatal("Lookup(missing) found an action")
	}
}

func TestExecuteNilProgressIsAllowed(t *testing.T) {
	registry := mustRegistry(t, readOnlyFake())
	req := actions.Request{Target: actions.Target{Kind: actions.TargetCustomer, Customer: "acme"}}
	if _, err := registry.Execute(context.Background(), "health", req, nil); err != nil {
		t.Fatalf("Execute with nil progress: %v", err)
	}
}

func TestNewRegistryRejectsInvalidDefinitions(t *testing.T) {
	withDescriptor := func(edit func(*actions.Descriptor)) *fakeAction {
		fake := mutatingFake()
		edit(&fake.descriptor)
		return fake
	}
	cases := []struct {
		name     string
		list     []actions.Action
		problem  string
		hint     string
		actionID string
	}{
		{
			name:     "duplicate id",
			list:     []actions.Action{readOnlyFake(), readOnlyFake()},
			problem:  "registered more than once",
			hint:     "rename one of the actions",
			actionID: "health",
		},
		{
			name:    "nil action",
			list:    []actions.Action{nil},
			problem: "action 0 is nil",
			hint:    "remove the nil entry",
		},
		{
			name:    "typed nil action",
			list:    []actions.Action{readOnlyFake(), (*fakeAction)(nil)},
			problem: "action 1 is nil",
			hint:    "remove the nil entry",
		},
		{
			name:     "invalid id",
			list:     []actions.Action{withDescriptor(func(d *actions.Descriptor) { d.ID = "Node_Drain" })},
			problem:  "invalid ID",
			hint:     "lowercase words",
			actionID: "Node_Drain",
		},
		{
			name:     "missing help",
			list:     []actions.Action{withDescriptor(func(d *actions.Descriptor) { d.Help = " " })},
			problem:  "title or help is empty",
			hint:     "set both",
			actionID: "node drain",
		},
		{
			name:     "unknown target kind",
			list:     []actions.Action{withDescriptor(func(d *actions.Descriptor) { d.Target = "cluster" })},
			problem:  `unknown target kind "cluster"`,
			hint:     `"global", "customer" or "node"`,
			actionID: "node drain",
		},
		{
			name:     "unknown effect",
			list:     []actions.Action{withDescriptor(func(d *actions.Descriptor) { d.Effect = "risky" })},
			problem:  `unknown effect "risky"`,
			hint:     `"read-only", "mutating" or "destructive"`,
			actionID: "node drain",
		},
		{
			name: "duplicate argument",
			list: []actions.Action{withDescriptor(func(d *actions.Descriptor) {
				d.Args = append(d.Args, actions.Arg{Name: "reason", Kind: actions.ArgString, Help: "Again."})
			})},
			problem:  `argument "reason" is declared twice`,
			hint:     "remove or rename",
			actionID: "node drain",
		},
		{
			name: "argument collides with target",
			list: []actions.Action{withDescriptor(func(d *actions.Descriptor) {
				d.Args = []actions.Arg{{Name: "node", Kind: actions.ArgString, Help: "Node."}}
			})},
			problem:  `argument "node" is reserved`,
			hint:     "reserved names are customer, node",
			actionID: "node drain",
		},
		{
			name: "argument collides with global flag",
			list: []actions.Action{withDescriptor(func(d *actions.Descriptor) {
				d.Args = []actions.Arg{{Name: "yes", Kind: actions.ArgBool, Help: "Skip."}}
			})},
			problem:  `argument "yes" is reserved`,
			hint:     "rename it",
			actionID: "node drain",
		},
		{
			name: "invalid argument name",
			list: []actions.Action{withDescriptor(func(d *actions.Descriptor) {
				d.Args = []actions.Arg{{Name: "--Reason", Kind: actions.ArgString, Help: "Why."}}
			})},
			problem:  `invalid argument name "--Reason"`,
			hint:     "one lowercase word",
			actionID: "node drain",
		},
		{
			name: "unknown argument kind",
			list: []actions.Action{withDescriptor(func(d *actions.Descriptor) {
				d.Args = []actions.Arg{{Name: "count", Kind: "float", Help: "Count."}}
			})},
			problem:  `argument "count" has unknown kind "float"`,
			hint:     `"string", "bool" or "int"`,
			actionID: "node drain",
		},
		{
			name: "argument without help",
			list: []actions.Action{withDescriptor(func(d *actions.Descriptor) {
				d.Args = []actions.Arg{{Name: "count", Kind: actions.ArgInt}}
			})},
			problem:  `argument "count" has no help`,
			hint:     "describe the argument",
			actionID: "node drain",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry, err := actions.NewRegistry(tc.list...)
			if err == nil {
				t.Fatalf("NewRegistry accepted invalid definitions: %+v", registry.List())
			}
			if registry != nil {
				t.Fatal("NewRegistry returned a registry together with an error")
			}
			actionErr := findError(t, err, tc.problem)
			if actionErr.Action != tc.actionID {
				t.Fatalf("error action = %q, want %q", actionErr.Action, tc.actionID)
			}
			if !strings.Contains(actionErr.Hint, tc.hint) {
				t.Fatalf("hint = %q, want it to contain %q", actionErr.Hint, tc.hint)
			}
		})
	}
}

func TestNewRegistryReportsEveryProblem(t *testing.T) {
	broken := mutatingFake()
	broken.descriptor.Target = "cluster"
	broken.descriptor.Effect = "risky"
	_, err := actions.NewRegistry(broken, readOnlyFake(), readOnlyFake())
	for _, problem := range []string{"unknown target kind", "unknown effect", "registered more than once"} {
		_ = findError(t, err, problem)
	}
}

func TestExecuteRejectsInvalidRequests(t *testing.T) {
	node := actions.Target{Kind: actions.TargetNode, Customer: "acme", Node: "acme-srv-1"}
	valid := map[string]any{"reason": "kernel update"}
	cases := []struct {
		name    string
		id      string
		req     actions.Request
		problem string
	}{
		{"unknown action", "nope", actions.Request{}, "no such action"},
		{"wrong target kind", "node drain",
			actions.Request{Target: actions.Target{Kind: actions.TargetCustomer, Customer: "acme"}, Args: valid},
			`target is "customer", want "node"`},
		{"node without customer", "node drain",
			actions.Request{Target: actions.Target{Kind: actions.TargetNode, Node: "acme-srv-1"}, Args: valid},
			"needs a customer and a node"},
		{"customer target with node", "health",
			actions.Request{Target: actions.Target{Kind: actions.TargetCustomer, Customer: "acme", Node: "x"}},
			"needs exactly one customer and no node"},
		{"missing required argument", "node drain",
			actions.Request{Target: node}, `missing required argument "reason"`},
		{"unknown argument", "node drain",
			actions.Request{Target: node, Args: map[string]any{"reason": "r", "colour": "red"}}, `unknown argument "colour"`},
		{"wrong argument type", "node drain",
			actions.Request{Target: node, Args: map[string]any{"reason": "r", "timeout": "60"}}, `argument "timeout" must be int`},
		{"wrong bool type", "node drain",
			actions.Request{Target: node, Args: map[string]any{"reason": "r", "force": "yes"}}, `argument "force" must be bool`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readOnly, mutating := readOnlyFake(), mutatingFake()
			registry := mustRegistry(t, readOnly, mutating)
			_, err := registry.Execute(context.Background(), tc.id, tc.req, nil)
			actionErr := findError(t, err, tc.problem)
			if actionErr.Hint == "" {
				t.Fatal("error has no hint")
			}
			if readOnly.checked+readOnly.ran+mutating.checked+mutating.ran != 0 {
				t.Fatal("an invalid request reached Check or Run")
			}
		})
	}
}

func TestExecuteUsageHintNamesArguments(t *testing.T) {
	registry := mustRegistry(t, mutatingFake())
	_, err := registry.Execute(context.Background(), "node drain",
		actions.Request{Target: actions.Target{Kind: actions.TargetNode, Customer: "acme", Node: "n1"}}, nil)
	want := "usage: node drain <customer> <node> --reason <string> [--timeout <int>] [--force]"
	if got := findError(t, err, "missing required").Hint; got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
}

func TestExecuteStopsOnFailedPrecondition(t *testing.T) {
	mutating := mutatingFake()
	refusal := errors.New("customer acme is locked by alice")
	mutating.checkErr = refusal
	registry := mustRegistry(t, mutating)
	req := actions.Request{
		Target: actions.Target{Kind: actions.TargetNode, Customer: "acme", Node: "acme-srv-1"},
		Args:   map[string]any{"reason": "r"},
	}
	_, err := registry.Execute(context.Background(), "node drain", req, nil)
	if !errors.Is(err, refusal) {
		t.Fatalf("err = %v, want it to wrap the precondition refusal", err)
	}
	if mutating.ran != 0 {
		t.Fatal("Run was called after a failed precondition")
	}
}

func TestExecuteDoesNotStartCancelledRun(t *testing.T) {
	readOnly := readOnlyFake()
	registry := mustRegistry(t, readOnly)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := registry.Execute(ctx,
		"health", actions.Request{Target: actions.Target{Kind: actions.TargetCustomer, Customer: "acme"}}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if readOnly.ran != 0 {
		t.Fatal("Run was called with a cancelled context")
	}
}

func TestExecuteRejectsResultWithoutStatus(t *testing.T) {
	readOnly := readOnlyFake()
	readOnly.result = actions.Result{Summary: "forgot status"}
	registry := mustRegistry(t, readOnly)
	_, err := registry.Execute(context.Background(),
		"health", actions.Request{Target: actions.Target{Kind: actions.TargetCustomer, Customer: "acme"}}, nil)
	_ = findError(t, err, "unknown status")
}

func TestDangerLevel(t *testing.T) {
	cases := []struct {
		effect     actions.Effect
		production bool
		want       actions.Danger
	}{
		{actions.EffectReadOnly, false, actions.DangerNone},
		{actions.EffectReadOnly, true, actions.DangerNone},
		{actions.EffectMutating, false, actions.DangerConfirm},
		{actions.EffectMutating, true, actions.DangerTypeName},
		{actions.EffectDestructive, false, actions.DangerTypeName},
		{actions.EffectDestructive, true, actions.DangerTypeName},
	}
	for _, tc := range cases {
		got := actions.Descriptor{Effect: tc.effect}.Danger(tc.production)
		if got != tc.want {
			t.Errorf("Danger(%q, production=%v) = %q, want %q", tc.effect, tc.production, got, tc.want)
		}
	}
}

// findError returns the *actions.Error in err whose problem contains problem.
func findError(t *testing.T, err error, problem string) *actions.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want one containing %q", problem)
	}
	var found *actions.Error
	walk(err, func(candidate error) {
		var actionErr *actions.Error
		if found == nil && errors.As(candidate, &actionErr) && strings.Contains(actionErr.Problem, problem) {
			found = actionErr
		}
	})
	if found == nil {
		t.Fatalf("error %q has no *actions.Error with problem containing %q", err, problem)
	}
	return found
}

func walk(err error, visit func(error)) {
	visit(err)
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, inner := range joined.Unwrap() {
			walk(inner, visit)
		}
	}
}
