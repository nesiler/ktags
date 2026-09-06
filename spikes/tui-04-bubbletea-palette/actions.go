package main

// The action registry — the whole point of this spike.
//
// Every operation ktags offers is one entry in `registry` below. The command
// palette (`:`), the context menu (Enter/Space/right-click), the hint bar and
// the help overlay are all *derived* from that slice; none of them contains a
// list of commands. Adding an operation is therefore one struct literal and
// nothing else.
//
// `cluster.upgrade` is in here purely to prove that. It was written last, after
// every screen was finished, and no other file was touched: it shows up in the
// palette, in the context menu of a customer row, in the hint bar and in the
// help overlay on its own.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Kind is the sort of object an action acts on.
type Kind string

const (
	KindGlobal   Kind = "" // no target
	KindCustomer Kind = "customer"
	KindNode     Kind = "node"
)

// Label is the human name of a kind, used for palette groups and help sections.
func (k Kind) Label() string {
	if k == KindGlobal {
		return "global"
	}
	return string(k)
}

// kindOrder is the order kinds appear in the palette and the help overlay.
var kindOrder = []Kind{KindCustomer, KindNode, KindGlobal}

// Level is how much ceremony an action needs before it runs.
type Level int

const (
	DangerNone     Level = iota // just run it
	DangerConfirm               // y/N
	DangerTypeName              // on prod: type the customer name; elsewhere y/N
)

// Label is the word shown next to an action in the help overlay.
func (l Level) Label() string {
	switch l {
	case DangerConfirm:
		return "confirm"
	case DangerTypeName:
		return "type name on prod"
	}
	return ""
}

// Arg is one optional flag an action accepts. Bool args are written `--check`,
// the rest `--limit=value`.
type Arg struct {
	Name string
	Bool bool
	Help string
}

// Spec renders the arg the way the user types it.
func (a Arg) Spec() string {
	if a.Bool {
		return "--" + a.Name
	}
	return "--" + a.Name + "="
}

// Values holds the flags of one invocation.
type Values map[string]string

// Has reports whether a flag was given.
func (v Values) Has(name string) bool { _, ok := v[name]; return ok }

// Get returns the value of a `--name=value` flag ("" when absent).
func (v Values) Get(name string) string { return v[name] }

// String renders the flags back the way they were typed, for toasts.
func (v Values) String() string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if v[k] == "" {
			out = append(out, "--"+k)
			continue
		}
		out = append(out, "--"+k+"="+v[k])
	}
	return strings.Join(out, " ")
}

// Context is the resolved target of one invocation.
//
// M is the running app. The design sketch has `Run(ctx, args) tea.Cmd`, and a
// tea.Cmd cannot mutate the model, so actions that change a screen (open the
// run view, the form, an overlay) do it through this pointer. Update holds the
// model by value, so the pipeline passes &m and returns the same m afterwards.
type Context struct {
	M        *model
	Customer *Customer
	Node     *Node
}

// Action is one operation. This is the only description of it in the program.
type Action struct {
	ID      string   // "cluster.install"
	Verb    string   // what the user types
	Aliases []string // extra spellings the palette matches
	Title   string   // shown in the palette match list and the menu
	Help    string   // one line, shown under the title in help
	Target  Kind     // which selected object it needs
	Args    []Arg    // optional flags
	Danger  Level    // confirmation required before Run
	Long    bool     // streams into the run view instead of toasting
	Guard   func(ctx Context) error
	Run     func(ctx Context, args Values) tea.Cmd
}

// ArgSpec renders every flag of an action on one line, for the help overlay.
func (a *Action) ArgSpec() string {
	out := make([]string, 0, len(a.Args))
	for _, g := range a.Args {
		out = append(out, g.Spec())
	}
	return strings.Join(out, " ")
}

// AliasSpec renders the aliases, for the help overlay.
func (a *Action) AliasSpec() string { return strings.Join(a.Aliases, ", ") }

// ---------------------------------------------------------------------------
// Guards
// ---------------------------------------------------------------------------

// notLocked refuses an action while another operator holds the cluster.
func notLocked(ctx Context) error {
	if ctx.Customer != nil && ctx.Customer.Locked() {
		return errors.New(ctx.Customer.LockMessage())
	}
	return nil
}

// isLocked is the mirror image, for `unlock`.
func isLocked(ctx Context) error {
	if ctx.Customer != nil && !ctx.Customer.Locked() {
		return errors.New(ctx.Customer.Name + " is not locked")
	}
	return nil
}

// ---------------------------------------------------------------------------
// The registry
// ---------------------------------------------------------------------------

// registry lists every operation. Order is priority order: the hint bar shows
// the first three entries matching the focused kind, and the context menu keeps
// this order too.
var registry []*Action

// A plain `var registry = []*Action{…}` would be an initialisation cycle: an
// action's Run closure calls into the app, and the app's palette calls back
// into the registry. Filling the slice in init() breaks it. Registration is
// still one list in one file.
func init() {
	registry = []*Action{
		{
			ID:      "cluster.install",
			Verb:    "install",
			Aliases: []string{"i", "inst", "reconcile"},
			Title:   "Install / reconcile cluster",
			Help:    "run the full RKE2 playbook against every node of the cluster",
			Target:  KindCustomer,
			Args: []Arg{
				{Name: "check", Bool: true, Help: "dry run (--check --diff), change nothing"},
				{Name: "limit", Help: "only these hosts, e.g. --limit=acme-srv-1"},
			},
			Danger: DangerTypeName,
			Long:   true,
			Guard:  notLocked,
			Run: func(ctx Context, v Values) tea.Cmd {
				title := "install"
				if v.Has("check") {
					title = "install (check)"
				}
				return ctx.M.startRun(title, *ctx.Customer, installScript(*ctx.Customer))
			},
		},
		{
			ID:      "cluster.health",
			Verb:    "health",
			Aliases: []string{"hc", "check", "status"},
			Title:   "Run health checks",
			Help:    "read-only: nodes, versions, pods, etcd, disk, snapshots",
			Target:  KindCustomer,
			Long:    true,
			Run: func(ctx Context, _ Values) tea.Cmd {
				return ctx.M.startRun("health", *ctx.Customer, healthScript(*ctx.Customer))
			},
		},
		{
			ID:      "cluster.upgrade",
			Verb:    "upgrade",
			Aliases: []string{"ug", "up"},
			Title:   "Upgrade RKE2 in place",
			Help:    "cordon, drain, upgrade and uncordon every node in turn",
			Target:  KindCustomer,
			Args:    []Arg{{Name: "version", Help: "target RKE2 version, e.g. --version=v1.35.8+rke2r1"}},
			Danger:  DangerTypeName,
			Long:    true,
			Guard:   notLocked,
			Run: func(ctx Context, v Values) tea.Cmd {
				title := "upgrade"
				if ver := v.Get("version"); ver != "" {
					title = "upgrade → " + ver
				}
				return ctx.M.startRun(title, *ctx.Customer, upgradeScript(*ctx.Customer))
			},
		},
		{
			ID:      "cluster.backup",
			Verb:    "backup",
			Aliases: []string{"b", "snapshot"},
			Title:   "Take an etcd snapshot",
			Help:    "on-demand etcd snapshot, uploaded to the configured store",
			Target:  KindCustomer,
			Args:    []Arg{{Name: "full", Bool: true, Help: "also snapshot Longhorn volumes"}},
			Danger:  DangerTypeName,
			Guard:   notLocked,
			Run: func(ctx Context, v Values) tea.Cmd {
				what := "etcd snapshot"
				if v.Has("full") {
					what = "etcd + Longhorn snapshot"
				}
				ctx.M.status = "backup queued"
				return ctx.M.toastCmd(what+" started for "+ctx.Customer.Name+" (mock)", "ok")
			},
		},
		{
			ID:      "cluster.lock",
			Verb:    "lock",
			Aliases: []string{"hold"},
			Title:   "Lock the cluster",
			Help:    "claim the cluster so nobody else installs or backs it up",
			Target:  KindCustomer,
			Guard:   notLocked,
			Run: func(ctx Context, _ Values) tea.Cmd {
				ctx.Customer.LockedBy = "you"
				ctx.Customer.LockReason = "manual lock"
				ctx.M.syncCustomers()
				return ctx.M.toastCmd("locked "+ctx.Customer.Name, "ok")
			},
		},
		{
			ID:      "cluster.unlock",
			Verb:    "unlock",
			Aliases: []string{"release"},
			Title:   "Release the cluster lock",
			Help:    "drop the lock, including someone else's",
			Target:  KindCustomer,
			Danger:  DangerConfirm,
			Guard:   isLocked,
			Run: func(ctx Context, _ Values) tea.Cmd {
				who := ctx.Customer.LockedBy
				ctx.Customer.LockedBy, ctx.Customer.LockReason = "", ""
				ctx.M.syncCustomers()
				return ctx.M.toastCmd("unlocked "+ctx.Customer.Name+" (was held by "+who+")", "ok")
			},
		},
		{
			ID:      "node.add",
			Verb:    "node-add",
			Aliases: []string{"addnode", "na"},
			Title:   "Add a node to the cluster",
			Help:    "join one more server or agent to the selected customer",
			Target:  KindCustomer,
			Guard:   notLocked,
			Run: func(ctx Context, _ Values) tea.Cmd {
				return ctx.M.toastCmd("node sub-form for "+ctx.Customer.Name+" (mock)", "info")
			},
		},
		{
			ID:      "secret.show",
			Verb:    "secret",
			Aliases: []string{"sec", "reveal"},
			Title:   "Reveal a cluster secret",
			Help:    "show rke2_token or the Rancher password for 15 seconds",
			Target:  KindCustomer,
			Run: func(ctx Context, _ Values) tea.Cmd {
				ctx.M.secret = &secretState{customer: ctx.Customer.Name}
				ctx.M.overlay = overlaySecret
				return nil
			},
		},
		{
			ID:      "node.drain",
			Verb:    "drain",
			Aliases: []string{"cordon"},
			Title:   "Drain a node",
			Help:    "cordon the node and evict its workloads",
			Target:  KindNode,
			Args:    []Arg{{Name: "force", Bool: true, Help: "evict pods without a controller too"}},
			Danger:  DangerConfirm,
			Guard:   notLocked,
			Run: func(ctx Context, v Values) tea.Cmd {
				extra := ""
				if v.Has("force") {
					extra = " (forced)"
				}
				return ctx.M.toastCmd("draining "+ctx.Node.Name+extra+" (mock)", "ok")
			},
		},
		{
			ID:      "cluster.add",
			Verb:    "add",
			Aliases: []string{"new", "create"},
			Title:   "Add a customer",
			Help:    "the full new-cluster form: name, env, access, nodes, secrets",
			Target:  KindGlobal,
			Run: func(ctx Context, _ Values) tea.Cmd {
				m := ctx.M
				m.form = newAddForm(m.th, m.contentW(), m.contentH())
				m.screen = screenForm
				return m.form.form.Init()
			},
		},
		{
			ID:      "app.help",
			Verb:    "help",
			Aliases: []string{"?", "keys"},
			Title:   "Show the help overlay",
			Help:    "every action in the registry, grouped by target kind",
			Target:  KindGlobal,
			Run: func(ctx Context, _ Values) tea.Cmd {
				ctx.M.overlay = overlayHelp
				return nil
			},
		},
		{
			ID:      "app.mouse",
			Verb:    "mouse",
			Aliases: []string{"m"},
			Title:   "Toggle mouse reporting",
			Help:    "turn mouse off so the terminal's own text selection works",
			Target:  KindGlobal,
			Run: func(ctx Context, _ Values) tea.Cmd {
				ctx.M.mouseOn = !ctx.M.mouseOn
				return ctx.M.toastCmd("mouse "+onOff(ctx.M.mouseOn), "info")
			},
		},
		{
			ID:      "app.quit",
			Verb:    "quit",
			Aliases: []string{"q", "exit"},
			Title:   "Quit ktags",
			Help:    "leave the TUI; a running job is abandoned",
			Target:  KindGlobal,
			Run:     func(Context, Values) tea.Cmd { return tea.Quit },
		},
	}
}

// ---------------------------------------------------------------------------
// Lookup
// ---------------------------------------------------------------------------

// actionByVerb resolves an exact verb or alias.
func actionByVerb(s string) *Action {
	s = strings.ToLower(s)
	for _, a := range registry {
		if a.Verb == s {
			return a
		}
		for _, al := range a.Aliases {
			if al == s {
				return a
			}
		}
	}
	return nil
}

// actionByID resolves the stable id used by clickable zones.
func actionByID(id string) *Action {
	for _, a := range registry {
		if a.ID == id {
			return a
		}
	}
	return nil
}

// actionsFor returns every action targeting a kind, in registry order. This is
// the context menu, and (truncated to three) the hint bar.
func actionsFor(k Kind) []*Action {
	var out []*Action
	for _, a := range registry {
		if a.Target == k {
			out = append(out, a)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Fuzzy matching
// ---------------------------------------------------------------------------

// bestScore is the highest fuzzy score of pattern over cands. `bonus` biases a
// verb hit above an alias hit above a title hit, so typing "he" ranks `health`
// over "Reveal a cluster secret".
func bestScore(pattern string, cands []string, bonus []int) (int, bool) {
	best, found := 0, false
	for i, c := range cands {
		ms := fuzzy.Find(pattern, []string{strings.ToLower(c)})
		if len(ms) == 0 {
			continue
		}
		s := ms[0].Score + bonus[i]
		if !found || s > best {
			best, found = s, true
		}
	}
	return best, found
}

// rankActions filters and orders the registry for a palette query. An empty
// query returns everything in kind order, which is the "I am lost" listing.
func rankActions(q string) []*Action {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		var out []*Action
		for _, k := range kindOrder {
			out = append(out, actionsFor(k)...)
		}
		return out
	}

	type hit struct {
		a *Action
		s int
	}
	var hits []hit
	for _, a := range registry {
		cands := append([]string{a.Verb}, a.Aliases...)
		bonus := make([]int, len(cands))
		bonus[0] = 200
		for i := 1; i < len(cands); i++ {
			bonus[i] = 100
		}
		cands = append(cands, a.Title)
		bonus = append(bonus, 0)

		s, ok := bestScore(q, cands, bonus)
		if !ok {
			continue
		}
		// A literal prefix of the verb is what the user almost always means.
		if strings.HasPrefix(a.Verb, q) {
			s += 400
		}
		hits = append(hits, hit{a, s})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].s > hits[j].s })

	out := make([]*Action, len(hits))
	for i, h := range hits {
		out[i] = h.a
	}
	return out
}

// rankNames orders target names (customers or nodes) for a palette query.
func rankNames(q string, names []string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return names
	}
	ms := fuzzy.Find(q, lowerAll(names))
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, names[m.Index])
	}
	return out
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

// closestVerbs returns the n verbs nearest an unknown one, for the palette's
// "no such command" message. Fuzzy matching returns nothing at all for a real
// typo ("instal1"), so this falls back to plain edit distance.
func closestVerbs(q string, n int) []string {
	type hit struct {
		v string
		d int
	}
	var hits []hit
	for _, a := range registry {
		hits = append(hits, hit{a.Verb, editDistance(strings.ToLower(q), a.Verb)})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].d < hits[j].d })
	out := make([]string, 0, n)
	for i := 0; i < len(hits) && i < n; i++ {
		out = append(out, hits[i].v)
	}
	return out
}

// editDistance is a plain Levenshtein distance over runes.
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// ---------------------------------------------------------------------------
// Arg parsing
// ---------------------------------------------------------------------------

// parseArgs turns `--check --limit=acme-srv-1` into Values, rejecting flags the
// action does not declare.
func parseArgs(a *Action, toks []string) (Values, error) {
	v := Values{}
	for _, t := range toks {
		if !strings.HasPrefix(t, "--") {
			continue
		}
		name, val, _ := strings.Cut(strings.TrimPrefix(t, "--"), "=")
		if name == "" {
			continue
		}
		var def *Arg
		for i := range a.Args {
			if a.Args[i].Name == name {
				def = &a.Args[i]
			}
		}
		if def == nil {
			return nil, fmt.Errorf("%s has no --%s", a.Verb, name)
		}
		if !def.Bool && val == "" {
			return nil, fmt.Errorf("--%s needs a value (--%s=…)", name, name)
		}
		v[name] = val
	}
	return v, nil
}
