package actions

import (
	"context"
	"fmt"
)

// TargetKind says what an action operates on. The kinds match the palette groups in
// docs/guides/ui.md §6.
type TargetKind string

// Target kinds.
const (
	TargetGlobal   TargetKind = "global"
	TargetCustomer TargetKind = "customer"
	TargetNode     TargetKind = "node"
)

// ArgKind is the value type of an action argument.
type ArgKind string

// Argument kinds.
const (
	ArgString ArgKind = "string"
	ArgBool   ArgKind = "bool"
	ArgInt    ArgKind = "int"
)

// Arg declares one named argument of an action.
type Arg struct {
	Name     string
	Kind     ArgKind
	Required bool
	Help     string
}

// Effect says what an action may change. It is the risk metadata from which the confirmation
// level is derived.
type Effect string

// Effects.
const (
	// EffectReadOnly changes nothing.
	EffectReadOnly Effect = "read-only"
	// EffectMutating changes a customer or the laptop state.
	EffectMutating Effect = "mutating"
	// EffectDestructive removes or rotates something that cannot be restored by re-running
	// (node remove, decommission, secret rotate).
	EffectDestructive Effect = "destructive"
)

// Danger is the confirmation level from docs/guides/ui.md §7.
type Danger string

// Danger levels.
const (
	DangerNone     Danger = "none"
	DangerConfirm  Danger = "confirm"
	DangerTypeName Danger = "type-name"
)

// Descriptor is the static definition of an action. Clients render help, palette entries and
// CLI subcommands from it.
type Descriptor struct {
	// ID is the verb as the operator types it: lowercase words separated by single spaces,
	// for example "doctor" or "cluster install".
	ID     string
	Title  string
	Help   string
	Target TargetKind
	Args   []Arg
	Effect Effect
}

// Danger resolves the confirmation level for a run against a production or non-production
// target, following docs/guides/ui.md §7.
func (d Descriptor) Danger(production bool) Danger {
	switch {
	case d.Effect == EffectReadOnly:
		return DangerNone
	case d.Effect == EffectDestructive || production:
		return DangerTypeName
	default:
		return DangerConfirm
	}
}

// Target identifies what one run operates on. Customer is set for customer and node targets;
// Node is set only for node targets.
type Target struct {
	Kind     TargetKind
	Customer string
	Node     string
}

// Request is one resolved invocation of an action. Args values are string, bool or int,
// matching the declared ArgKind.
type Request struct {
	Target Target
	Args   map[string]any
}

// Event is one progress report from a running action.
type Event struct {
	Step    string
	Message string
}

// Progress receives events while an action runs.
type Progress interface {
	Report(Event)
}

// ProgressFunc adapts a function to Progress.
type ProgressFunc func(Event)

// Report calls f.
func (f ProgressFunc) Report(event Event) { f(event) }

// Status is the outcome of a finished run.
type Status string

// Statuses.
const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

// Result is the final outcome of a run.
type Result struct {
	Status  Status
	Summary string
}

// Action is the interface every action implements, read-only or mutating alike.
type Action interface {
	Descriptor() Descriptor
	// Check verifies the preconditions for req and returns an error when the action must not
	// start. It must not change anything.
	Check(ctx context.Context, req Request) error
	// Run executes the action, reports progress and returns the final result.
	Run(ctx context.Context, req Request, progress Progress) (Result, error)
}

// Error is an operator-facing error from the action contract: what failed and the next step
// (docs/guides/development.md §4).
type Error struct {
	// Action is the action ID, empty when the error is not tied to one action.
	Action  string
	Problem string
	Hint    string
}

func (e *Error) Error() string {
	if e.Action == "" {
		return "actions: " + e.Problem
	}
	return fmt.Sprintf("action %q: %s", e.Action, e.Problem)
}
