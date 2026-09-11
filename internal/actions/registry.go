package actions

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

var (
	idPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*( [a-z][a-z0-9-]*)*$`)
	argPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
)

// reservedArgs are names already taken by positional targets and by the global CLI flags in
// docs/guides/development.md §3. An argument with one of these names could not be expressed.
var reservedArgs = []string{"customer", "node", "dry-run", "yes", "json", "no-color", "v", "help"} //nolint:misspell // "no-color" is the CLI flag name

// Registry holds the validated set of actions. It is immutable after NewRegistry.
type Registry struct {
	byID map[string]Action
	ids  []string
}

// NewRegistry validates every action definition and returns the registry. Every problem found
// is reported, so one start shows all broken definitions.
func NewRegistry(actions ...Action) (*Registry, error) {
	registry := &Registry{byID: make(map[string]Action, len(actions))}
	var problems []error
	for index, action := range actions {
		if action == nil {
			problems = append(problems, &Error{
				Problem: fmt.Sprintf("action %d is nil", index),
				Hint:    "remove the nil entry from the registry list",
			})
			continue
		}
		descriptor := action.Descriptor()
		if errs := validateDescriptor(descriptor); len(errs) > 0 {
			problems = append(problems, errs...)
			continue
		}
		if _, exists := registry.byID[descriptor.ID]; exists {
			problems = append(problems, &Error{
				Action:  descriptor.ID,
				Problem: "registered more than once",
				Hint:    "rename one of the actions; action IDs must be unique",
			})
			continue
		}
		registry.byID[descriptor.ID] = action
		registry.ids = append(registry.ids, descriptor.ID)
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	sort.Strings(registry.ids)
	return registry, nil
}

func validateDescriptor(d Descriptor) []error {
	var problems []error
	fail := func(problem, hint string) {
		problems = append(problems, &Error{Action: d.ID, Problem: problem, Hint: hint})
	}
	if !idPattern.MatchString(d.ID) {
		fail("invalid ID", `use lowercase words separated by single spaces, for example "cluster install"`)
	}
	if strings.TrimSpace(d.Title) == "" || strings.TrimSpace(d.Help) == "" {
		fail("title or help is empty", "set both; help and the palette are generated from them")
	}
	switch d.Target {
	case TargetGlobal, TargetCustomer, TargetNode:
	default:
		fail(fmt.Sprintf("unknown target kind %q", d.Target), `use "global", "customer" or "node"`)
	}
	switch d.Effect {
	case EffectReadOnly, EffectMutating, EffectDestructive:
	default:
		fail(fmt.Sprintf("unknown effect %q", d.Effect), `use "read-only", "mutating" or "destructive"`)
	}
	seen := make(map[string]bool, len(d.Args))
	for _, arg := range d.Args {
		switch {
		case !argPattern.MatchString(arg.Name):
			fail(fmt.Sprintf("invalid argument name %q", arg.Name), "use one lowercase word, hyphens allowed")
		case slices.Contains(reservedArgs, arg.Name):
			fail(fmt.Sprintf("argument %q is reserved", arg.Name),
				"rename it; reserved names are "+strings.Join(reservedArgs, ", "))
		case seen[arg.Name]:
			fail(fmt.Sprintf("argument %q is declared twice", arg.Name), "remove or rename the duplicate")
		}
		seen[arg.Name] = true
		switch arg.Kind {
		case ArgString, ArgBool, ArgInt:
		default:
			fail(fmt.Sprintf("argument %q has unknown kind %q", arg.Name, arg.Kind), `use "string", "bool" or "int"`)
		}
		if strings.TrimSpace(arg.Help) == "" {
			fail(fmt.Sprintf("argument %q has no help", arg.Name), "describe the argument; help is generated from it")
		}
	}
	return problems
}

// List returns the descriptors of all actions, sorted by ID.
func (r *Registry) List() []Descriptor {
	descriptors := make([]Descriptor, 0, len(r.ids))
	for _, id := range r.ids {
		descriptors = append(descriptors, r.byID[id].Descriptor())
	}
	return descriptors
}

// Lookup returns the action registered under id.
func (r *Registry) Lookup(id string) (Action, bool) {
	action, ok := r.byID[id]
	return action, ok
}

// Execute validates req against the action's descriptor, checks its preconditions and runs it.
// Every action, read-only or mutating, goes through this one path. Confirmation is the
// client's step before Execute, using Descriptor.Danger.
func (r *Registry) Execute(ctx context.Context, id string, req Request, progress Progress) (Result, error) {
	if err := r.Validate(id, req); err != nil {
		return Result{}, err
	}
	action := r.byID[id]
	if err := action.Check(ctx, req); err != nil {
		return Result{}, fmt.Errorf("action %q precondition failed: %w", id, err)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("action %q not started: %w", id, err)
	}
	if progress == nil {
		progress = ProgressFunc(func(Event) {})
	}
	result, err := action.Run(ctx, req, progress)
	if err != nil {
		return result, fmt.Errorf("action %q: %w", id, err)
	}
	if result.Status != StatusSucceeded && result.Status != StatusFailed {
		return result, &Error{
			Action:  id,
			Problem: fmt.Sprintf("returned result with unknown status %q", result.Status),
			Hint:    `the action must return status "succeeded" or "failed"`,
		}
	}
	return result, nil
}

// Validate checks that id is registered and that req matches its descriptor, without running
// anything. Execute performs the same check; a caller such as the service uses Validate to
// refuse a request before it records a run.
func (r *Registry) Validate(id string, req Request) error {
	action, ok := r.byID[id]
	if !ok {
		return &Error{
			Action:  id,
			Problem: "no such action",
			Hint:    "registered actions: " + strings.Join(r.ids, ", "),
		}
	}
	return validateRequest(action.Descriptor(), req)
}

func validateRequest(d Descriptor, req Request) error {
	fail := func(problem, hint string) error {
		return &Error{Action: d.ID, Problem: problem, Hint: hint}
	}
	target := req.Target
	if target.Kind != d.Target {
		return fail(fmt.Sprintf("target is %q, want %q", target.Kind, d.Target), usage(d))
	}
	switch d.Target {
	case TargetGlobal:
		if target.Customer != "" || target.Node != "" {
			return fail("takes no customer or node", usage(d))
		}
	case TargetCustomer:
		if target.Customer == "" || target.Node != "" {
			return fail("needs exactly one customer and no node", usage(d))
		}
	case TargetNode:
		if target.Customer == "" || target.Node == "" {
			return fail("needs a customer and a node", usage(d))
		}
	}
	for name := range req.Args {
		if !slices.ContainsFunc(d.Args, func(arg Arg) bool { return arg.Name == name }) {
			return fail(fmt.Sprintf("unknown argument %q", name), usage(d))
		}
	}
	for _, arg := range d.Args {
		value, present := req.Args[arg.Name]
		if !present {
			if arg.Required {
				return fail(fmt.Sprintf("missing required argument %q", arg.Name), usage(d))
			}
			continue
		}
		if !kindMatches(arg.Kind, value) {
			return fail(fmt.Sprintf("argument %q must be %s, got %T", arg.Name, arg.Kind, value), usage(d))
		}
	}
	return nil
}

func kindMatches(kind ArgKind, value any) bool {
	switch kind {
	case ArgString:
		_, ok := value.(string)
		return ok
	case ArgBool:
		_, ok := value.(bool)
		return ok
	case ArgInt:
		_, ok := value.(int)
		return ok
	}
	return false
}

// usage renders the expected invocation, for example "usage: node drain <customer> <node> --reason <string>".
func usage(d Descriptor) string {
	parts := []string{"usage:", d.ID}
	switch d.Target {
	case TargetCustomer:
		parts = append(parts, "<customer>")
	case TargetNode:
		parts = append(parts, "<customer>", "<node>")
	}
	for _, arg := range d.Args {
		flag := "--" + arg.Name
		if arg.Kind != ArgBool {
			flag += " <" + string(arg.Kind) + ">"
		}
		if !arg.Required {
			flag = "[" + flag + "]"
		}
		parts = append(parts, flag)
	}
	return strings.Join(parts, " ")
}
