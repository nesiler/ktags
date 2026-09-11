package cli

import (
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/service"
)

const actionUsage = `usage: ktags action <command> [--json]

Actions come from the one action registry in the ktags service; the TUI palette shows the same.

commands:
  list                     list every action with its target and effect
  show <action>            show the usage, help and arguments of one action
  run <action> [<customer> [<node>]] [--<argument> <value>]... [--detach] [--yes]
                           start the action and follow its events to the final result

run confirms by danger level first: read-only actions start at once; changes outside prod
ask [y/N] (--yes answers it); any change on prod and destructive actions need the customer
name typed in a terminal, and --yes never replaces it. An interrupt (Ctrl-C) or --detach
leaves the run going in the service; reconnect with: ktags run watch <run>.
run exits 0 when the run succeeded or was detached, 3 when it failed or was cancelled.
--json, --yes and --detach are read as flags wherever they stand, also after --<argument>;
give an argument such a value in one word: --<argument>=--yes.

--json data: list {"actions":[action]} · show {"action","usage"} · run {"run","events"}
(kind run.result), {"run"} with --detach (kind run.started), {"run_id","last_event_id"}
after an interrupt (kind run.detached)

next: ktags action list
`

type actionListDoc struct {
	Actions []service.ActionInfo `json:"actions"`
}

type actionShowDoc struct {
	Action service.ActionInfo `json:"action"`
	Usage  string             `json:"usage"`
}

// targetArity is the number of positional target arguments of each target kind.
var targetArity = map[string][]string{
	string(actions.TargetGlobal):   nil,
	string(actions.TargetCustomer): {"<customer>"},
	string(actions.TargetNode):     {"<customer>", "<node>"},
}

func (e *env) actionCmd() *cobra.Command {
	return group("action", actionUsage,
		&cobra.Command{
			Use:  "list",
			Long: "usage: ktags action list [--json]\n\n" + actionUsage,
			Args: positional(),
			RunE: func(*cobra.Command, []string) error { return e.actionList() },
		},
		&cobra.Command{
			Use:  "show",
			Long: "usage: ktags action show <action> [--json]\n\n" + actionUsage,
			Args: func(cmd *cobra.Command, args []string) error {
				if len(args) == 0 {
					return usageError(cmd, "missing <action>")
				}
				return nil
			},
			RunE: func(_ *cobra.Command, args []string) error { return e.actionShow(args) },
		},
		&cobra.Command{
			Use:  "run",
			Long: "usage: ktags action run <action> [<customer> [<node>]] [--<argument> <value>]... [--detach] [--yes] [--json]\n\n" + actionUsage,
			// Action arguments are only known from the service, so run parses its own flags.
			DisableFlagParsing: true,
			RunE:               e.actionRun,
		},
	)
}

func (e *env) actionList() error {
	client, err := e.client()
	if err != nil {
		return err
	}
	list, err := client.Actions(e.ctx)
	if err != nil {
		return err
	}
	w := e.text()
	_, _ = fmt.Fprintf(w, "%d action(s)\n", len(list))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  ACTION\tTARGET\tEFFECT\tTITLE")
	for _, a := range list {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", a.ID, a.Target, a.Effect, a.Title)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(w, "next: ktags action show <action>")
	return e.emit("action.list", actionListDoc{Actions: nonNil(list)})
}

func (e *env) actionShow(words []string) error {
	client, err := e.client()
	if err != nil {
		return err
	}
	list, err := client.Actions(e.ctx)
	if err != nil {
		return err
	}
	info, n, ok := resolveAction(list, words)
	if !ok || n != len(words) {
		return unknownAction(strings.Join(words, " "))
	}
	w := e.text()
	_, _ = fmt.Fprintf(w, "%s: %s\n  usage   ktags action run %s\n  target  %s\n  effect  %s\n\n%s\n", info.ID, info.Title, usageLine(info), info.Target, info.Effect, info.Help)
	if len(info.Args) > 0 {
		_, _ = fmt.Fprintln(w, "\narguments:")
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, a := range info.Args {
			required := "optional"
			if a.Required {
				required = "required"
			}
			_, _ = fmt.Fprintf(tw, "  --%s\t%s\t%s\t%s\n", a.Name, a.Kind, required, a.Help)
		}
		_ = tw.Flush()
	}
	return e.emit("action.show", actionShowDoc{Action: info, Usage: "ktags action run " + usageLine(info)})
}

func (e *env) actionRun(cmd *cobra.Command, tokens []string) error {
	var detach bool
	var rest []string
	for _, t := range tokens {
		switch t {
		case "-h", "--help":
			return cmd.Help()
		case "--json":
			e.json = true
		case "--yes":
			e.yes = true
		case "--detach":
			detach = true
		default:
			rest = append(rest, t)
		}
	}
	lead := 0
	for lead < len(rest) && !strings.HasPrefix(rest[lead], "-") {
		lead++
	}
	if lead == 0 {
		return usageError(cmd, "missing <action>")
	}

	client, err := e.client()
	if err != nil {
		return err
	}
	list, err := client.Actions(e.ctx)
	if err != nil {
		return err
	}
	info, n, ok := resolveAction(list, rest[:lead])
	if !ok {
		return unknownAction(strings.Join(rest[:lead], " "))
	}
	targetArgs, values, err := parseArgs(info, rest[n:])
	if err != nil {
		return err
	}
	target, customer, err := e.resolveTarget(client, info, targetArgs)
	if err != nil {
		return err
	}

	summary := fmt.Sprintf("run %q on %s", info.ID, describeTarget(target, customer))
	name := target.Customer
	if name == "" {
		name = info.ID
	}
	if err := e.confirm(danger(info.Effect, customer), summary, name); err != nil {
		return err
	}
	started, err := client.Start(e.ctx, info.ID, target, values)
	if err != nil {
		return err
	}
	if detach {
		_, _ = fmt.Fprintf(e.text(), "started run %s (%s)\n  next: ktags run watch %s\n", started.ID, info.ID, started.ID)
		return e.emit("run.started", runDoc{Run: started})
	}
	_, _ = fmt.Fprintf(e.text(), "started run %s (%s); Ctrl-C detaches, the run continues\n", started.ID, info.ID)
	return e.follow(client, started.ID, 0, true)
}

// resolveAction finds the action whose ID is the longest prefix of words, so that
// "cluster install acme" names the action "cluster install" with the target "acme". It returns
// the number of words the ID took.
func resolveAction(list []service.ActionInfo, words []string) (service.ActionInfo, int, bool) {
	for n := len(words); n > 0; n-- {
		id := strings.Join(words[:n], " ")
		for _, a := range list {
			if a.ID == id {
				return a, n, true
			}
		}
	}
	return service.ActionInfo{}, 0, false
}

func unknownAction(name string) error {
	return &exitError{exit: exitUsage, code: "unknown_action", message: fmt.Sprintf("no such action %q", name), hint: "ktags action list"}
}

func argError(info service.ActionInfo, code, message string) error {
	return &exitError{exit: exitUsage, code: code, message: message, hint: "ktags action show " + info.ID}
}

// parseArgs splits the tokens after the action ID into target arguments and typed action
// arguments. A string or int argument takes the next token or the part after "=", a bool
// argument is true when named alone.
func parseArgs(info service.ActionInfo, tokens []string) ([]string, map[string]any, error) {
	var targets []string
	values := map[string]any{}
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if !strings.HasPrefix(token, "-") {
			targets = append(targets, token)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(token, "--"), "=")
		var arg *service.ArgInfo
		for j := range info.Args {
			if info.Args[j].Name == name {
				arg = &info.Args[j]
			}
		}
		if arg == nil {
			return nil, nil, argError(info, "unknown_argument", fmt.Sprintf("action %q has no argument %q", info.ID, token))
		}
		if _, dup := values[name]; dup {
			return nil, nil, argError(info, "invalid_argument", fmt.Sprintf("argument --%s is given twice", name))
		}
		if arg.Kind == string(actions.ArgBool) {
			if !hasValue {
				values[name] = true
				continue
			}
			b, err := strconv.ParseBool(value)
			if err != nil {
				return nil, nil, argError(info, "invalid_argument", fmt.Sprintf("argument --%s must be true or false, got %q", name, value))
			}
			values[name] = b
			continue
		}
		if !hasValue {
			if i+1 >= len(tokens) {
				return nil, nil, argError(info, "invalid_argument", fmt.Sprintf("argument --%s needs a %s value", name, arg.Kind))
			}
			i++
			value = tokens[i]
		}
		switch arg.Kind {
		case string(actions.ArgInt):
			n, err := strconv.Atoi(value)
			if err != nil {
				return nil, nil, argError(info, "invalid_argument", fmt.Sprintf("argument --%s must be an int, got %q", name, value))
			}
			values[name] = n
		default:
			values[name] = value
		}
	}
	for _, a := range info.Args {
		if _, ok := values[a.Name]; a.Required && !ok {
			return nil, nil, argError(info, "invalid_argument", fmt.Sprintf("missing required argument --%s", a.Name))
		}
	}
	return targets, values, nil
}

// resolveTarget builds the run target and checks that its customer is in the inventory with a
// readable record. The service checks only the shape of a target.
func (e *env) resolveTarget(client Client, info service.ActionInfo, args []string) (service.Target, *service.CustomerInfo, error) {
	names, known := targetArity[info.Target]
	if !known {
		return service.Target{}, nil, &exitError{exit: exitUsage, code: "unsupported_target", message: fmt.Sprintf("action %q has target kind %q, which this ktags does not know", info.ID, info.Target), hint: "run the client and the service from the same ktags build"}
	}
	hint := "ktags action run " + usageLine(info)
	switch {
	case len(args) < len(names):
		return service.Target{}, nil, &exitError{exit: exitUsage, code: "invalid_argument", message: fmt.Sprintf("action %q needs %s", info.ID, strings.Join(names[len(args):], " ")), hint: hint}
	case len(args) > len(names):
		return service.Target{}, nil, &exitError{exit: exitUsage, code: "invalid_argument", message: fmt.Sprintf("action %q: unexpected argument %q", info.ID, args[len(names)]), hint: hint}
	}
	target := service.Target{Kind: info.Target}
	if len(args) == 0 {
		return target, nil, nil
	}
	customer, err := e.customer(client, args[0])
	if err != nil {
		return service.Target{}, nil, err
	}
	target.Customer = customer.ID
	if len(args) == 2 {
		target.Node = args[1]
	}
	return target, customer, nil
}

// customer finds a customer by ID in the inventory.
func (e *env) customer(client Client, id string) (*service.CustomerInfo, error) {
	customers, err := client.Customers(e.ctx)
	if err != nil {
		return nil, err
	}
	for i := range customers {
		c := &customers[i]
		if c.ID != id {
			continue
		}
		if c.Problem != "" {
			return nil, &exitError{exit: exitUsage, code: "customer_refused", message: fmt.Sprintf("the record of customer %q is refused: %s", id, c.Problem), hint: "fix the customer record, then check it with: ktags customer list"}
		}
		return c, nil
	}
	return nil, &exitError{exit: exitUsage, code: "unknown_customer", message: fmt.Sprintf("no customer %q in the inventory", id), hint: "ktags customer list"}
}

// danger is the confirmation level of an action's effect on a customer (docs/guides/ui.md §7).
// An effect this build does not know, and a customer whose environment is unknown, get the
// strictest level.
func danger(effect string, customer *service.CustomerInfo) actions.Danger {
	switch actions.Effect(effect) {
	case actions.EffectReadOnly, actions.EffectMutating, actions.EffectDestructive:
	default:
		return actions.DangerTypeName
	}
	return actions.Descriptor{Effect: actions.Effect(effect)}.Danger(customer != nil && customer.Environment != "staging" && customer.Environment != "test")
}

// confirm asks for the confirmation that level requires (ADR-0003 §3). summary says what will
// run; name is what a typed confirmation must match.
func (e *env) confirm(level actions.Danger, summary, name string) error {
	switch level {
	case actions.DangerNone:
		return nil
	case actions.DangerConfirm:
		if e.yes {
			return nil
		}
		if !e.s.Terminal {
			return &exitError{exit: exitRefused, code: "confirmation_required", message: summary + " needs confirmation and stdin is not a terminal; nothing was started", hint: "run it in a terminal, or add --yes"}
		}
		_, _ = fmt.Fprintf(e.s.Err, "%s\nContinue? [y/N] ", summary)
		answer := strings.ToLower(e.readLine())
		if answer == "y" || answer == "yes" {
			return nil
		}
		return &exitError{exit: exitRefused, code: "not_confirmed", message: "not confirmed; nothing was started", hint: "run it again and answer y"}
	default:
		if !e.s.Terminal {
			return &exitError{exit: exitRefused, code: "confirmation_required", message: summary + " needs the name typed in a terminal; --yes does not replace it; nothing was started", hint: "run it in a terminal"}
		}
		_, _ = fmt.Fprintf(e.s.Err, "%s\nType %q to continue: ", summary, name)
		if e.readLine() == name {
			return nil
		}
		return &exitError{exit: exitRefused, code: "not_confirmed", message: "the typed name does not match; nothing was started", hint: fmt.Sprintf("run it again and type %q", name)}
	}
}

func (e *env) readLine() string {
	line, _ := e.in.ReadString('\n')
	return strings.TrimSpace(line)
}

// usageLine renders the invocation of an action after "ktags action run".
func usageLine(info service.ActionInfo) string {
	parts := append([]string{info.ID}, targetArity[info.Target]...)
	for _, a := range info.Args {
		flag := "--" + a.Name
		if a.Kind != string(actions.ArgBool) {
			flag += " <" + a.Kind + ">"
		}
		if !a.Required {
			flag = "[" + flag + "]"
		}
		parts = append(parts, flag)
	}
	return strings.Join(parts, " ")
}

func describeTarget(t service.Target, customer *service.CustomerInfo) string {
	switch {
	case t.Customer == "":
		return "this laptop (global)"
	case t.Node != "":
		return fmt.Sprintf("node %s of %s (%s)", t.Node, t.Customer, customer.Environment)
	default:
		return fmt.Sprintf("%s (%s)", t.Customer, customer.Environment)
	}
}
