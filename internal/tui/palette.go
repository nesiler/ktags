package tui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/service"
)

// palette is the `:` command bar. Its stage is derived from the input on every keystroke
// (docs/guides/ui.md §6); the caret is always at the end.
type palette struct {
	input string
	sel   int
	// base and completed let a repeated Tab cycle through the suggestions of base.
	base, completed string
	history         []string
	// hist is the history entry shown, -1 when none.
	hist int
	err  string
}

type menu struct {
	customer string
	items    []service.ActionInfo
	sel      int
}

type stage int

const (
	stageVerb stage = iota
	stageTarget
	stageArg
)

// suggestion is one row under the command bar. text replaces the token under the caret.
type suggestion struct {
	text   string
	help   string
	group  string
	action *service.ActionInfo
}

// query is the parsed palette input.
type query struct {
	stage   stage
	action  *service.ActionInfo
	targets []string
	args    map[string]any
	token   string
	rows    []suggestion
	notice  string
	err     string
}

var groupOrder = []string{string(actions.TargetCustomer), string(actions.TargetNode), string(actions.TargetGlobal)}

func (m Model) openPalette() Model {
	m.overlay = overlayPalette
	m.pal = palette{history: m.pal.history, hist: -1}
	return m
}

// parse reads the palette input: the longest action ID the words start with, then target
// words and --arguments. The rows and Enter both use the result.
func (m Model) parse(input string) query {
	words := strings.Fields(input)
	trailing := strings.HasSuffix(input, " ")
	var q query
	for n := len(words); n > 0 && q.action == nil; n-- {
		id := strings.Join(words[:n], " ")
		if i := slices.IndexFunc(m.actions, func(a service.ActionInfo) bool { return a.ID == id }); i >= 0 && (n < len(words) || trailing) {
			q.action = &m.actions[i]
			words = words[n:]
		}
	}
	if q.action == nil {
		q.stage, q.token = stageVerb, strings.TrimSpace(input)
		q.rows = m.verbRows(q.token)
		if q.token != "" && len(q.rows) == 0 {
			q.notice = fmt.Sprintf("no such action %q", q.token)
			if closest := closestVerbs(m.actions, q.token, 3); len(closest) > 0 {
				q.notice += "; closest: " + strings.Join(closest, ", ")
			}
		}
		return q
	}
	complete := words
	if !trailing && len(words) > 0 {
		q.token, complete = words[len(words)-1], words[:len(words)-1]
	}
	q.targets, q.args, q.err = parseArgs(*q.action, complete)
	pendingValue := len(complete) > 0 && strings.HasPrefix(complete[len(complete)-1], "--") && q.err != "" && strings.Contains(q.err, "needs a")
	switch {
	case strings.HasPrefix(q.token, "-"):
		q.stage, q.err = stageArg, ""
		for _, arg := range q.action.Args {
			if _, given := q.args[arg.Name]; !given && strings.HasPrefix("--"+arg.Name, q.token) {
				q.rows = append(q.rows, suggestion{text: "--" + arg.Name, help: arg.Kind + " · " + arg.Help, group: "argument"})
			}
		}
	case pendingValue:
		q.stage, q.err = stageArg, ""
	default:
		q.stage = stageTarget
		if len(q.targets) == 0 && q.action.Target != string(actions.TargetGlobal) {
			q.rows = append(q.rows, m.customerRows(q.token)...)
		}
	}
	return q
}

// verbRows lists the actions matching token, best first; an empty token lists every action
// grouped by target kind.
func (m Model) verbRows(token string) []suggestion {
	row := func(a *service.ActionInfo) suggestion {
		return suggestion{text: a.ID, help: a.Title, group: a.Target, action: a}
	}
	var rows []suggestion
	if token == "" {
		for _, g := range groupOrder {
			for i := range m.actions {
				if m.actions[i].Target == g {
					rows = append(rows, row(&m.actions[i]))
				}
			}
		}
		for i := range m.actions {
			if !slices.Contains(groupOrder, m.actions[i].Target) {
				rows = append(rows, row(&m.actions[i]))
			}
		}
		return rows
	}
	data := make([]string, len(m.actions))
	for i, a := range m.actions {
		data[i] = a.ID + " " + a.Title
	}
	for _, match := range fuzzy.Find(token, data) {
		rows = append(rows, row(&m.actions[match.Index]))
	}
	// An action ID typed in full is the best match, whatever the fuzzy score says.
	if i := slices.IndexFunc(rows, func(r suggestion) bool { return r.text == token }); i > 0 {
		exact := rows[i]
		rows = append([]suggestion{exact}, slices.Delete(rows, i, i+1)...)
	}
	return rows
}

func (m Model) customerRows(token string) []suggestion {
	cs := m.customers()
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	var rows []suggestion
	add := func(i int) {
		rows = append(rows, suggestion{text: cs[i].ID, help: orDash(cs[i].Environment), group: "customer"})
	}
	if token == "" {
		for i := range cs {
			add(i)
		}
		return rows
	}
	for _, match := range fuzzy.Find(token, ids) {
		add(match.Index)
	}
	return rows
}

// parseArgs splits the words after the action ID into target words and typed arguments, like
// the CLI's `action run`: a string or int argument takes the next word or the part after "=",
// a bool argument is true when named alone.
func parseArgs(a service.ActionInfo, words []string) ([]string, map[string]any, string) {
	var targets []string
	values := map[string]any{}
	for i := 0; i < len(words); i++ {
		word := words[i]
		if !strings.HasPrefix(word, "-") {
			targets = append(targets, word)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(word, "--"), "=")
		j := slices.IndexFunc(a.Args, func(arg service.ArgInfo) bool { return arg.Name == name })
		if j < 0 {
			return targets, values, fmt.Sprintf("action %q has no argument %q", a.ID, word)
		}
		arg := a.Args[j]
		if _, dup := values[name]; dup {
			return targets, values, fmt.Sprintf("argument --%s is given twice", name)
		}
		if arg.Kind == string(actions.ArgBool) {
			b := true
			if hasValue {
				var err error
				if b, err = strconv.ParseBool(value); err != nil {
					return targets, values, fmt.Sprintf("argument --%s must be true or false, got %q", name, value)
				}
			}
			values[name] = b
			continue
		}
		if !hasValue {
			if i+1 >= len(words) {
				return targets, values, fmt.Sprintf("argument --%s needs a %s value", name, arg.Kind)
			}
			i++
			value = words[i]
		}
		if arg.Kind == string(actions.ArgInt) {
			n, err := strconv.Atoi(value)
			if err != nil {
				return targets, values, fmt.Sprintf("argument --%s must be an int, got %q", name, value)
			}
			values[name] = n
			continue
		}
		values[name] = value
	}
	return targets, values, ""
}

// closestVerbs are the n action IDs with the smallest edit distance to token.
func closestVerbs(list []service.ActionInfo, token string, n int) []string {
	ids := make([]string, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	slices.SortStableFunc(ids, func(a, b string) int {
		if d := distance(a, token) - distance(b, token); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	return ids[:min(n, len(ids))]
}

func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func (m Model) paletteKey(k, text string) (tea.Model, tea.Cmd) {
	p := &m.pal
	switch k {
	case "esc":
		m.overlay = overlayNone
		return m, nil
	case "enter":
		return m.paletteRun()
	case "tab":
		m.complete()
		return m, nil
	case "up":
		if (p.input == "" || p.hist >= 0) && len(p.history) > 0 {
			p.hist = min(len(p.history)-1, p.hist+1)
			p.input = p.history[len(p.history)-1-p.hist]
			return m, nil
		}
		p.sel = max(0, p.sel-1)
		return m, nil
	case "down":
		if p.hist >= 0 {
			p.hist--
			p.input = ""
			if p.hist >= 0 {
				p.input = p.history[len(p.history)-1-p.hist]
			}
			return m, nil
		}
		p.sel = min(max(0, len(m.parse(p.input).rows)-1), p.sel+1)
		return m, nil
	case "backspace":
		if r := []rune(p.input); len(r) > 0 {
			p.input = string(r[:len(r)-1])
		}
	default:
		if text == "" {
			return m, nil
		}
		p.input += text
	}
	p.sel, p.hist, p.base, p.completed, p.err = 0, -1, "", "", ""
	return m, nil
}

// complete is Tab: replace the token under the caret with the selected suggestion; a repeated
// Tab cycles through the suggestions.
func (m *Model) complete() {
	p := &m.pal
	base, sel := p.input, p.sel
	if p.base != "" && p.input == p.completed {
		base = p.base
		if n := len(m.parse(base).rows); n > 0 {
			sel = (p.sel + 1) % n
		}
	}
	q := m.parse(base)
	if sel >= len(q.rows) {
		return
	}
	if q.stage == stageVerb {
		p.input = q.rows[sel].text + " "
	} else {
		p.input = replaceToken(base, q.token, q.rows[sel].text) + " "
	}
	p.base, p.completed, p.sel = base, p.input, sel
}

func replaceToken(input, token, text string) string {
	if token == "" {
		return input + text
	}
	return strings.TrimSuffix(input, token) + text
}

// paletteRun is Enter: complete a partial verb or customer with the selected suggestion, then
// hand the request to the pipeline.
func (m Model) paletteRun() (tea.Model, tea.Cmd) {
	p := &m.pal
	input := p.input
	q := m.parse(input)
	switch {
	case q.stage == stageVerb && p.sel < len(q.rows):
		input = q.rows[p.sel].text + " "
		q = m.parse(input)
	case q.stage == stageTarget && q.token != "" && p.sel < len(q.rows):
		input = replaceToken(input, q.token, q.rows[p.sel].text) + " "
		q = m.parse(input)
	}
	if q.action == nil {
		p.err = q.notice
		if p.err == "" {
			p.err = "type an action, or Esc to close"
		}
		return m, nil
	}
	targets, args, err := parseArgs(*q.action, strings.Fields(strings.TrimPrefix(strings.Join(strings.Fields(input), " "), q.action.ID)))
	if err != "" {
		p.err = err
		return m, nil
	}
	// A target kind this build does not know has no arity; the pipeline refuses it.
	arity, known := map[string]int{string(actions.TargetGlobal): 0, string(actions.TargetCustomer): 1, string(actions.TargetNode): 2}[q.action.Target]
	if known && len(targets) > arity {
		p.err = fmt.Sprintf("unexpected %q; usage: %s", targets[arity], usage(*q.action))
		return m, nil
	}
	req := request{action: *q.action, args: args}
	if len(req.args) == 0 {
		req.args = nil
	}
	if q.action.Target != string(actions.TargetGlobal) {
		req.customer = m.selected
	}
	if len(targets) > 0 {
		req.customer = targets[0]
	}
	if len(targets) > 1 {
		req.node = targets[1]
	}
	p.history = append(p.history, strings.TrimSpace(input))
	m.overlay = overlayNone
	return m.begin(req)
}
