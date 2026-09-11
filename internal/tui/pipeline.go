package tui

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/service"
)

// request is one action invocation. The palette, the context menu and the hint bar all build
// one and hand it to begin; nothing else starts a run, so the three cannot drift apart.
type request struct {
	action   service.ActionInfo
	customer string
	node     string
	args     map[string]any
}

type dialogKind int

const (
	dialogConfirm dialogKind = iota
	dialogTypeName
	dialogError
)

// dialog is a confirmation or an error. A confirmation carries what it confirms.
type dialog struct {
	kind  dialogKind
	title string
	lines []string
	hint  string
	// name is what a type-name confirmation must match; typed is what was typed.
	name  string
	typed string
	wrong bool
	// start is the run a confirmation starts; cancel is the run it cancels.
	start  *pending
	cancel string
}

type pending struct {
	action string
	target service.Target
	args   map[string]any
}

// begin runs the pipeline: resolve the target, check the guards, confirm by danger level,
// then start the run in the service (docs/guides/ui.md §1.2).
func (m Model) begin(req request) (Model, tea.Cmd) {
	target, customer, refusal := m.resolve(req)
	if refusal != nil {
		return m.refuse("cannot start "+req.action.ID, *refusal), nil
	}
	p := &pending{action: req.action.ID, target: target, args: req.args}
	lines := confirmLines(req, customer)
	switch dangerOf(req.action.Effect, customer) {
	case actions.DangerNone:
		return m.start(p)
	case actions.DangerConfirm:
		m.dialog = dialog{kind: dialogConfirm, title: "Confirm", lines: lines, start: p}
	default:
		name := target.Customer
		if name == "" {
			name = req.action.ID
		}
		m.dialog = dialog{kind: dialogTypeName, title: "Confirm by typing the name", lines: lines, name: name, start: p}
	}
	m.overlay = overlayDialog
	return m, nil
}

// resolve builds the run target and checks the guards that hold before any prompt.
func (m Model) resolve(req request) (service.Target, *service.FleetEntry, *problem) {
	a := req.action
	if m.conn != connConnected {
		return service.Target{}, nil, &problem{message: "actions are disabled while the service connection is " + m.connWord(), hint: "wait until the top bar shows connected, or check: ktags service status"}
	}
	for _, arg := range a.Args {
		if _, ok := req.args[arg.Name]; arg.Required && !ok {
			return service.Target{}, nil, &problem{message: fmt.Sprintf("action %q needs --%s", a.ID, arg.Name), hint: "give it in the palette: :" + usage(a)}
		}
	}
	target := service.Target{Kind: a.Target}
	switch a.Target {
	case string(actions.TargetGlobal):
		return target, nil, nil
	case string(actions.TargetCustomer), string(actions.TargetNode):
	default:
		return service.Target{}, nil, &problem{message: fmt.Sprintf("action %q has target kind %q, which this ktags does not know", a.ID, a.Target), hint: "run the client and the service from the same ktags build"}
	}
	if req.customer == "" {
		return service.Target{}, nil, &problem{message: fmt.Sprintf("action %q needs a customer", a.ID), hint: "select one in the list, or type it: :" + usage(a)}
	}
	customer := m.entry(req.customer)
	switch {
	case customer == nil:
		return service.Target{}, nil, &problem{message: fmt.Sprintf("no customer %q in the inventory", req.customer), hint: "ktags customer list"}
	case customer.Problem != "":
		return service.Target{}, nil, &problem{message: fmt.Sprintf("the record of customer %q is refused: %s", req.customer, customer.Problem), hint: "fix the customer record, then check it with: ktags customer list"}
	}
	target.Customer = customer.ID
	if a.Target == string(actions.TargetNode) {
		if req.node == "" {
			return service.Target{}, nil, &problem{message: fmt.Sprintf("action %q needs a node", a.ID), hint: "type it in the palette: :" + usage(a)}
		}
		target.Node = req.node
	}
	return target, customer, nil
}

// dangerOf is the confirmation level of an action's effect on a customer, the same rule the
// CLI applies (docs/guides/ui.md §7). An unknown effect, and a customer whose environment is
// not staging or test, get the strictest level.
func dangerOf(effect string, customer *service.FleetEntry) actions.Danger {
	switch actions.Effect(effect) {
	case actions.EffectReadOnly, actions.EffectMutating, actions.EffectDestructive:
	default:
		return actions.DangerTypeName
	}
	return actions.Descriptor{Effect: actions.Effect(effect)}.Danger(customer != nil && !nonProd(customer.Environment))
}

func nonProd(environment string) bool {
	return environment == "staging" || environment == "test"
}

// confirmLines is what a prompt shows: action, customer, environment, targets and arguments.
func confirmLines(req request, customer *service.FleetEntry) []string {
	lines := []string{fmt.Sprintf("action       %s — %s", req.action.ID, req.action.Title), "effect       " + req.action.Effect}
	if customer != nil {
		lines = append(lines, "customer     "+customer.ID, "environment  "+orDash(customer.Environment))
	} else {
		lines = append(lines, "target       this laptop (global)")
	}
	if req.node != "" {
		lines = append(lines, "node         "+req.node)
	}
	for _, name := range slices.Sorted(maps.Keys(req.args)) {
		lines = append(lines, fmt.Sprintf("argument     --%s %v", name, req.args[name]))
	}
	return lines
}

// refuse shows a refusal before anything started (docs/guides/ui.md §8).
func (m Model) refuse(title string, p problem) Model {
	lines := []string{p.message}
	if p.code != service.CodeUnavailable {
		lines = append(lines, "nothing was started")
	}
	m.dialog = dialog{kind: dialogError, title: title, lines: lines, hint: p.hint}
	m.overlay = overlayDialog
	return m
}

func (m Model) start(p *pending) (Model, tea.Cmd) {
	m.overlay = overlayNone
	m.status = "starting " + p.action + "…"
	client := m.client
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		info, err := client.Start(ctx, p.action, p.target, p.args)
		return startedMsg{action: p.action, info: info, err: err}
	}
}

func (m Model) onStarted(msg startedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = ""
		return m.refuse("the service refused "+msg.action, describe(msg.err)), nil
	}
	m.status = "started run " + msg.info.ID
	if !slices.ContainsFunc(m.runs, func(r service.RunInfo) bool { return r.ID == msg.info.ID }) {
		m.runs = append(m.runs, msg.info)
	}
	return m.openRun(msg.info)
}

// beginCancel confirms a cancel like a change on the run's customer: cancelling a prod run
// needs the typed name; a customer that left the inventory counts as prod.
func (m Model) beginCancel(info service.RunInfo) Model {
	if m.conn != connConnected {
		return m.refuse("cannot cancel run "+info.ID, problem{message: "actions are disabled while the service connection is " + m.connWord(), hint: "wait until the top bar shows connected, or check: ktags service status"})
	}
	lines := []string{"run          " + info.ID, "action       " + info.Action, "target       " + targetString(info.Target)}
	level := actions.DangerConfirm
	if info.Target.Customer != "" {
		customer := m.entry(info.Target.Customer)
		if customer == nil || !nonProd(customer.Environment) {
			level = actions.DangerTypeName
		}
		if customer != nil {
			lines = append(lines, "environment  "+orDash(customer.Environment))
		}
	}
	m.dialog = dialog{kind: dialogConfirm, title: "Cancel run?", lines: lines, cancel: info.ID}
	if level == actions.DangerTypeName {
		m.dialog.kind, m.dialog.name = dialogTypeName, info.Target.Customer
	}
	m.overlay = overlayDialog
	return m
}

func (m Model) cancelRun(id string) (Model, tea.Cmd) {
	m.overlay = overlayNone
	m.status = "cancelling run " + id + "…"
	client := m.client
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		_, err := client.Cancel(ctx, id)
		return cancelledMsg{id: id, err: err}
	}
}

func (m Model) onCancelled(msg cancelledMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.status = ""
		return m.refuse("cannot cancel run "+msg.id, describe(msg.err)), nil
	}
	m.status = "cancel requested for run " + msg.id
	return m, nil
}

func (m Model) dialogKey(k, text string) (tea.Model, tea.Cmd) {
	d := &m.dialog
	switch d.kind {
	case dialogError:
		if k == "esc" || k == "enter" || k == "n" || k == "q" {
			m.overlay = overlayNone
		}
		return m, nil
	case dialogConfirm:
		switch k {
		case "y", "enter":
			return m.confirmed()
		case "n", "esc":
			return m.declined(), nil
		}
		return m, nil
	}
	switch k {
	case "esc":
		return m.declined(), nil
	case "enter":
		if d.typed != d.name {
			d.wrong = true
			return m, nil
		}
		return m.confirmed()
	case "backspace":
		if r := []rune(d.typed); len(r) > 0 {
			d.typed = string(r[:len(r)-1])
		}
	default:
		d.typed += text
	}
	d.wrong = false
	return m, nil
}

func (m Model) confirmed() (Model, tea.Cmd) {
	if m.dialog.cancel != "" {
		return m.cancelRun(m.dialog.cancel)
	}
	return m.start(m.dialog.start)
}

func (m Model) declined() Model {
	m.overlay = overlayNone
	m.status = "not confirmed; nothing was started"
	if m.dialog.cancel != "" {
		m.status = "not confirmed; run " + m.dialog.cancel + " continues"
	}
	return m
}

// usage renders an action invocation without the leading "ktags action run", for example
// "cluster install <customer> [--check]".
func usage(a service.ActionInfo) string {
	parts := []string{a.ID}
	switch a.Target {
	case string(actions.TargetCustomer):
		parts = append(parts, "<customer>")
	case string(actions.TargetNode):
		parts = append(parts, "<customer>", "<node>")
	}
	for _, arg := range a.Args {
		flag := "--" + arg.Name
		if arg.Kind != string(actions.ArgBool) {
			flag += " <" + arg.Kind + ">"
		}
		if !arg.Required {
			flag = "[" + flag + "]"
		}
		parts = append(parts, flag)
	}
	return strings.Join(parts, " ")
}

func targetString(t service.Target) string {
	switch {
	case t.Customer == "":
		return t.Kind
	case t.Node != "":
		return t.Customer + "/" + t.Node
	default:
		return t.Customer
	}
}
