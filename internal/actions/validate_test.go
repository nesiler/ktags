package actions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nesiler/ktags/internal/actions"
)

// validateStub fails the test if Validate ever runs or checks it.
type validateStub struct{ t *testing.T }

func (s validateStub) Descriptor() actions.Descriptor {
	return actions.Descriptor{
		ID: "stub", Title: "Stub", Help: "Validation only.",
		Target: actions.TargetGlobal, Effect: actions.EffectReadOnly,
		Args: []actions.Arg{{Name: "count", Kind: actions.ArgInt, Required: true, Help: "a number"}},
	}
}

func (s validateStub) Check(context.Context, actions.Request) error {
	s.t.Fatal("Validate called Check")
	return nil
}

func (s validateStub) Run(context.Context, actions.Request, actions.Progress) (actions.Result, error) {
	s.t.Fatal("Validate called Run")
	return actions.Result{}, nil
}

func TestValidateChecksWithoutRunning(t *testing.T) {
	registry, err := actions.NewRegistry(validateStub{t})
	if err != nil {
		t.Fatal(err)
	}
	global := actions.Target{Kind: actions.TargetGlobal}
	if err := registry.Validate("stub", actions.Request{Target: global, Args: map[string]any{"count": 1}}); err != nil {
		t.Fatalf("valid request refused: %v", err)
	}
	cases := map[string]struct {
		id  string
		req actions.Request
	}{
		"unknown action":   {"missing", actions.Request{Target: global}},
		"missing argument": {"stub", actions.Request{Target: global}},
		"wrong target":     {"stub", actions.Request{Target: actions.Target{Kind: actions.TargetCustomer, Customer: "acme"}, Args: map[string]any{"count": 1}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var ae *actions.Error
			if err := registry.Validate(tc.id, tc.req); !errors.As(err, &ae) {
				t.Fatalf("Validate = %v, want an actions.Error", err)
			}
		})
	}
}
