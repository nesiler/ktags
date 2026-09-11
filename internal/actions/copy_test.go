package actions_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/actions"
)

func argNames(d actions.Descriptor) []string {
	names := make([]string, 0, len(d.Args))
	for _, arg := range d.Args {
		names = append(names, arg.Name)
	}
	return names
}

// The registry keeps the descriptor it validated. A descriptor that changes after NewRegistry,
// through the action or through a caller editing what List returned, changes neither List nor
// request validation.
func TestRegistryUsesValidatedDescriptor(t *testing.T) {
	fake := mutatingFake()
	registry := mustRegistry(t, fake)

	fake.descriptor.Args[2].Name = "yes" // the reserved flag, edited in the slice NewRegistry saw
	fake.descriptor.Args = append(fake.descriptor.Args, actions.Arg{Name: "json", Kind: actions.ArgBool, Help: "Reserved."})
	registry.List()[0].Args[0].Name = "changed"

	want := []string{"reason", "timeout", "force"}
	if got := argNames(registry.List()[0]); !slices.Equal(got, want) {
		t.Fatalf("List args %v, want the validated %v", got, want)
	}

	node := actions.Target{Kind: actions.TargetNode, Customer: "acme", Node: "acme-srv-1"}
	for _, name := range []string{"yes", "json"} {
		req := actions.Request{Target: node, Args: map[string]any{"reason": "r", name: true}}
		if err := registry.Validate("node drain", req); err == nil || !strings.Contains(err.Error(), "unknown argument") {
			t.Fatalf("Validate with --%s: %v, want an unknown argument refusal", name, err)
		}
		if _, err := registry.Execute(context.Background(), "node drain", req, nil); err == nil {
			t.Fatalf("Execute with --%s ran", name)
		}
	}
	if fake.ran != 0 {
		t.Fatalf("the action ran %d times, want none", fake.ran)
	}
	req := actions.Request{Target: node, Args: map[string]any{"reason": "r", "force": true}}
	if _, err := registry.Execute(context.Background(), "node drain", req, nil); err != nil {
		t.Fatalf("Execute with the validated --force: %v", err)
	}
}
