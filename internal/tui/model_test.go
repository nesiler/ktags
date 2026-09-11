package tui

import (
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nesiler/ktags/internal/service"
)

func contains(t *testing.T, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

// dialogText is everything the open dialog says, before the box wraps it.
func dialogText(m Model) string {
	d := m.dialog
	return strings.Join(append(append([]string{d.title}, d.lines...), "next: "+d.hint), "\n")
}

func rowTexts(rows []suggestion) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.text
	}
	return out
}

func ids(lines []service.Event) []uint64 {
	out := make([]uint64, len(lines))
	for i, e := range lines {
		out[i] = e.ID
	}
	return out
}

func span1(from, to uint64) []uint64 {
	var out []uint64
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

// The synthetic key messages carry the names the model matches on.
func TestKeyNames(t *testing.T) {
	for _, name := range []string{"enter", "esc", "tab", "shift+tab", "up", "down", "left", "right", "pgup", "pgdown", "home", "end", "backspace", "space", "ctrl+c", ":", "?", "m", "q", "y"} {
		if got := keyMsg(name).String(); got != name {
			t.Errorf("key %q reads as %q", name, got)
		}
	}
}

// docs/guides/ui.md §4: Tab and Shift-Tab move focus; ← → switch tabs only while the detail
// panel has focus.
func TestFocusAndTabs(t *testing.T) {
	d := connected(t, newFake())
	if d.m.focus != focusList || d.m.tab != tabNodes {
		t.Fatalf("start focus %d tab %d, want the list and Nodes", d.m.focus, d.m.tab)
	}
	d.key("right")
	if d.m.tab != tabNodes {
		t.Fatal("→ switched tabs while the list had focus")
	}
	d.key("left")
	if d.m.tab != tabNodes {
		t.Fatal("← switched tabs while the list had focus")
	}
	d.key("tab", "right")
	if d.m.focus != focusDetail || d.m.tab != tabHealth {
		t.Fatalf("after tab →: focus %d tab %d, want detail and Health", d.m.focus, d.m.tab)
	}
	d.key("right", "right")
	if d.m.tab != tabNodes {
		t.Fatalf("→ past Runs gives tab %d, want Nodes again", d.m.tab)
	}
	d.key("left")
	if d.m.tab != tabRuns {
		t.Fatalf("← from Nodes gives tab %d, want Runs", d.m.tab)
	}
	d.key("shift+tab")
	if d.m.focus != focusList {
		t.Fatal("Shift-Tab did not move focus back to the list")
	}
}

// ↑ ↓ PgUp PgDn Home End and the silent j/k aliases move within the focused panel.
func TestListMovement(t *testing.T) {
	d := connected(t, newFake())
	steps := []struct {
		key, want string
	}{
		{"down", "mike"}, {"j", "oscar"}, {"k", "mike"}, {"up", "zulu"}, {"up", "zulu"},
		{"end", "delta"}, {"home", "zulu"}, {"pgdown", "delta"}, {"pgup", "zulu"},
	}
	for _, s := range steps {
		d.key(s.key)
		if d.m.selected != s.want {
			t.Fatalf("after %s: selected %q, want %q", s.key, d.m.selected, s.want)
		}
	}
}

// A list longer than the panel scrolls so the selection stays visible.
func TestListScrollsToTheSelection(t *testing.T) {
	f := newFake()
	fleet := mixedFleet()
	for i := range 20 {
		e := entry("extra"+strconv.Itoa(10+i), "test", "never")
		fleet.Customers = append(fleet.Customers, e)
	}
	f.fleet = fleet
	d := connected(t, f)
	d.key("end")
	last := fleet.Customers[len(fleet.Customers)-1].ID
	if d.m.listTop == 0 || !strings.Contains(d.frame(), last) {
		t.Fatalf("after End: list top %d; the last customer %s must be visible:\n%s", d.m.listTop, last, d.frame())
	}
	d.key("home")
	if d.m.listTop != 0 || !strings.Contains(d.frame(), "zulu") {
		t.Fatalf("after Home: list top %d, want 0", d.m.listTop)
	}
}

// The Runs tab lists the customer's runs newest first; ↑ ↓ move within it and Enter opens the
// run view with the stored result.
func TestRunsTabOpensARun(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("mike")
	d.key("tab", "right", "right", "down")
	if d.m.runCursor != 0 {
		t.Fatalf("↓ past the only run gives cursor %d", d.m.runCursor)
	}
	d.key("enter")
	if d.m.screen != screenRun || d.m.run.id != testRuns()[0].ID {
		t.Fatalf("Enter on the Runs tab: screen %d, want the run view of %s", d.m.screen, testRuns()[0].ID)
	}
	d.until("the replay", func(m Model) bool { return m.run.stop == nil })
	contains(t, d.frame(), "result    ✓ succeeded  checked 2 endpoints", "duration  1m", "next      ktags run status "+testRuns()[0].ID)
}

// Enter and Space open the context menu of the selected customer; it holds the registry's
// customer actions.
func TestEnterAndSpaceOpenTheMenu(t *testing.T) {
	for _, k := range []string{"enter", "space"} {
		d := connected(t, newFake())
		d.selectCustomer("delta")
		d.key(k)
		if d.m.overlay != overlayMenu {
			t.Fatalf("%s: overlay %d, want the menu", k, d.m.overlay)
		}
		var got []string
		for _, a := range d.m.menu.items {
			got = append(got, a.ID)
		}
		if want := []string{"fake check", "fake touch", "fake wipe"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("menu %v, want %v", got, want)
		}
		d.key("esc")
		if d.m.overlay != overlayNone {
			t.Fatal("Esc did not close the menu")
		}
	}
}

// `m` toggles the mouse; the top bar says which, and a click with the mouse off does nothing.
func TestMouseToggle(t *testing.T) {
	d := connected(t, newFake())
	if d.m.View().MouseMode != tea.MouseModeCellMotion || !strings.Contains(d.frame(), "mouse on") {
		t.Fatal("the mouse is not on by default")
	}
	d.key("m")
	if d.m.View().MouseMode != tea.MouseModeNone || !strings.Contains(d.frame(), "mouse off") {
		t.Fatal("m did not turn the mouse off")
	}
	d.click("row:mike", tea.MouseLeft)
	if d.m.selected != "zulu" {
		t.Fatalf("a click with the mouse off selected %q", d.m.selected)
	}
	d.key("m")
	d.click("row:mike", tea.MouseLeft)
	if d.m.selected != "mike" {
		t.Fatalf("a click with the mouse on selected %q, want mike", d.m.selected)
	}
}

// q and Ctrl-C quit from the fleet; with active runs the exit line says they continue.
func TestQuitSaysRunsContinue(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		d := connected(t, newFake())
		d.key(k)
		d.until("quit", func(Model) bool { return d.quit })
		if want := "1 run continue in the ktags service; reopen ktags or run: ktags run list"; d.m.ExitLine() != want {
			t.Fatalf("%s: exit line %q, want %q", k, d.m.ExitLine(), want)
		}
	}
	d := connected(t, emptyFake())
	d.key("q")
	d.until("quit", func(Model) bool { return d.quit })
	if d.m.ExitLine() != "" {
		t.Fatalf("exit line %q without active runs, want none", d.m.ExitLine())
	}
}

// ? shows help generated from the registry: every action with its CLI form; any key closes.
func TestHelpComesFromTheRegistry(t *testing.T) {
	d := connected(t, newFake())
	d.key("?")
	if d.m.overlay != overlayHelp || !strings.Contains(d.frame(), "━ Help ━") {
		t.Fatal("? did not open the help")
	}
	help := strings.Join(d.m.helpLines(), "\n")
	for _, a := range testActions() {
		contains(t, help, a.ID, "ktags action run "+usage(a))
	}
	d.key("x")
	if d.m.overlay != overlayNone {
		t.Fatal("a key did not close the help")
	}
}

// #17-K3, docs/guides/ui.md §13: the palette, the context menu and the hint bar reach the same
// confirmation for the same action, and confirming starts the same run.
func TestThreeEntryPointsOneConfirmation(t *testing.T) {
	type outcome struct {
		d       dialog
		started []startCall
	}
	via := map[string]func(d *driver){
		"palette": func(d *driver) {
			d.key(":")
			d.typeText("fake touch")
			d.key("enter")
		},
		"menu": func(d *driver) {
			d.key("enter", "down", "enter")
		},
		"hint": func(d *driver) {
			for i, h := range d.m.hints() {
				if h.action != nil && h.action.ID == "fake touch" {
					d.click("hint:"+strconv.Itoa(i), tea.MouseLeft)
					return
				}
			}
			d.t.Fatal("no hint for fake touch")
		},
	}
	got := map[string]outcome{}
	for name, enter := range via {
		f := newFake()
		d := connected(t, f)
		d.selectCustomer("delta")
		enter(d)
		if d.m.overlay != overlayDialog || d.m.dialog.kind != dialogConfirm || len(f.snapshot().started) != 0 {
			t.Fatalf("%s: overlay %d dialog %+v; want the [y/N] confirmation and nothing started", name, d.m.overlay, d.m.dialog)
		}
		dlg := d.m.dialog
		d.key("y")
		d.drain()
		got[name] = outcome{d: dlg, started: f.snapshot().started}
	}
	if !reflect.DeepEqual(got["palette"], got["menu"]) || !reflect.DeepEqual(got["palette"], got["hint"]) {
		t.Fatalf("entry points differ:\npalette %+v\nmenu    %+v\nhint    %+v", got["palette"], got["menu"], got["hint"])
	}
	if s := got["palette"].started; len(s) != 1 || s[0].action != "fake touch" || s[0].target != (service.Target{Kind: "customer", Customer: "delta"}) {
		t.Fatalf("started %+v, want one fake touch on delta", s)
	}
}

// docs/guides/ui.md §7: read-only starts at once; a change outside prod asks [y/N]; a change on
// prod, a destructive action anywhere and an unknown effect need the typed name.
func TestDangerLevels(t *testing.T) {
	extra := []service.ActionInfo{
		{ID: "fake odd", Title: "Fake odd", Help: "An effect this build does not know.", Target: "customer", Effect: "explosive"},
		{ID: "fake global", Title: "Fake global", Help: "Changes the laptop.", Target: "global", Effect: "mutating"},
	}
	cases := []struct {
		input string
		kind  dialogKind
		name  string
		now   bool
	}{
		{"fake check delta", 0, "", true},
		{"fake touch delta", dialogConfirm, "", false},
		{"fake touch india", dialogConfirm, "", false},
		{"fake touch mike", dialogTypeName, "mike", false},
		{"fake wipe delta", dialogTypeName, "delta", false},
		{"fake odd india", dialogTypeName, "india", false},
		{"fake global", dialogConfirm, "", false},
	}
	for _, c := range cases {
		f := newFake()
		f.actions = append(f.actions, extra...)
		d := connected(t, f)
		d.key(":")
		d.typeText(c.input)
		d.key("enter")
		d.drain()
		started := len(f.snapshot().started) == 1
		if c.now {
			if !started || d.m.overlay == overlayDialog {
				t.Fatalf("%s: started %v overlay %d, want it started without a prompt", c.input, started, d.m.overlay)
			}
			continue
		}
		if started || d.m.overlay != overlayDialog || d.m.dialog.kind != c.kind || d.m.dialog.name != c.name {
			t.Fatalf("%s: started %v dialog %+v, want kind %d name %q and nothing started", c.input, started, d.m.dialog, c.kind, c.name)
		}
	}
}

// A type-name confirmation starts only on the exact name: y is just a letter, a wrong or
// partial name is refused inline, Esc cancels.
func TestTypeNameConfirmation(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.key(":")
	d.typeText("fake touch mike")
	d.key("enter", "y", "enter")
	if len(f.snapshot().started) != 0 || !d.m.dialog.wrong || d.m.dialog.typed != "y" {
		t.Fatalf("y then Enter: started %v, dialog %+v; want nothing started and a mismatch", f.snapshot().started, d.m.dialog)
	}
	d.key("backspace")
	d.typeText("mik")
	d.key("enter")
	if len(f.snapshot().started) != 0 || !d.m.dialog.wrong {
		t.Fatal("a partial name started the run")
	}
	contains(t, d.frame(), "the typed name does not match")
	d.typeText("e")
	if d.m.dialog.wrong {
		t.Fatal("typing did not clear the mismatch")
	}
	d.key("enter")
	d.drain()
	if s := f.snapshot().started; len(s) != 1 || s[0].target.Customer != "mike" {
		t.Fatalf("the exact name started %+v, want one run on mike", s)
	}

	d = connected(t, f)
	d.key(":")
	d.typeText("fake touch mike")
	d.key("enter")
	d.typeText("mike")
	d.key("esc")
	if d.m.overlay != overlayNone || len(f.snapshot().started) != 1 || d.m.status != "not confirmed; nothing was started" {
		t.Fatalf("Esc: overlay %d started %d status %q; want closed, nothing more started", d.m.overlay, len(f.snapshot().started), d.m.status)
	}
}

// [y/N]: y and Enter confirm; n and Esc decline.
func TestConfirmKeys(t *testing.T) {
	for _, c := range []struct {
		key   string
		start bool
	}{{"y", true}, {"enter", true}, {"n", false}, {"esc", false}} {
		f := newFake()
		d := connected(t, f)
		d.key(":")
		d.typeText("fake touch delta")
		d.key("enter", c.key)
		d.drain()
		if started := len(f.snapshot().started) == 1; started != c.start || d.m.overlay == overlayDialog {
			t.Fatalf("%s: started %v overlay %d, want started %v and the dialog closed", c.key, started, d.m.overlay, c.start)
		}
	}
}

// A refusal before a run opens a dialog box with the cause and the next step; nothing starts.
func TestPipelineRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*fakeClient)
		prep  func(*driver)
		input string
		wants []string
	}{
		{name: "reconnecting", input: "fake check delta", prep: func(d *driver) { d.m.conn = connReconnecting },
			wants: []string{"actions are disabled while the service connection is reconnecting", "nothing was started", "next: wait until the top bar shows connected"}},
		{name: "refused record", input: "fake check zulu", wants: []string{`the record of customer "zulu" is refused: cannot parse`, "next: fix the customer record"}},
		{name: "unknown customer", input: "fake check nobody", wants: []string{`no customer "nobody" in the inventory`, "next: ktags customer list"}},
		{name: "node without node", input: "node drain delta --reason upgrade", wants: []string{`action "node drain" needs a node`, "next: type it in the palette: :node drain <customer> <node> --reason <string>"}},
		{name: "missing argument", input: "node drain delta srv-1", wants: []string{`action "node drain" needs --reason`}},
		{name: "unknown target kind", input: "fake cluster delta",
			setup: func(f *fakeClient) {
				f.actions = append(f.actions, service.ActionInfo{ID: "fake cluster", Title: "Fake cluster", Help: "x", Target: "cluster", Effect: "read-only"})
			},
			wants: []string{`has target kind "cluster", which this ktags does not know`, "next: run the client and the service from the same ktags build"}},
		{name: "service refusal", input: "fake check delta",
			setup: func(f *fakeClient) {
				f.startErr = &service.Error{Code: service.CodeInvalid, Message: `action "fake check" precondition failed: acme is locked by ops`, Hint: "wait for the lock owner"}
			},
			wants: []string{"the service refused fake check", "precondition failed: acme is locked by ops", "nothing was started", "next: wait for the lock owner"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFake()
			if c.setup != nil {
				c.setup(f)
			}
			d := connected(t, f)
			if c.prep != nil {
				c.prep(d)
			}
			d.key(":")
			d.typeText(c.input)
			d.key("enter")
			d.drain()
			if d.m.overlay != overlayDialog || d.m.dialog.kind != dialogError {
				t.Fatalf("overlay %d dialog %+v, want a refusal dialog", d.m.overlay, d.m.dialog)
			}
			if f.snapshot().started != nil && c.name != "service refusal" {
				t.Fatalf("started %+v", f.snapshot().started)
			}
			contains(t, dialogText(d.m), c.wants...)
			d.key("enter")
			if d.m.overlay != overlayNone {
				t.Fatal("Enter did not close the refusal")
			}
		})
	}
}

// Without a customer to select, a customer action from the palette names what is missing.
func TestCustomerActionWithoutCustomer(t *testing.T) {
	f := emptyFake()
	d := connected(t, f)
	d.key(":")
	d.typeText("fake check")
	d.key("enter")
	contains(t, dialogText(d.m), `action "fake check" needs a customer`)
}

// docs/guides/ui.md §6: an empty palette lists every action grouped by target kind; typing
// filters by fuzzy match; an unknown verb names the closest ones; a space moves on to the
// target, where the current selection is the default; -- offers the arguments.
func TestPaletteStages(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("delta")
	d.key(":")
	q := d.m.parse(d.m.pal.input)
	if want := []string{"fake check", "fake touch", "fake wipe", "node drain", "fake quick"}; !reflect.DeepEqual(rowTexts(q.rows), want) {
		t.Fatalf("empty palette %v, want %v grouped customer, node, global", rowTexts(q.rows), want)
	}
	d.typeText("tuch")
	if rows := d.m.parse(d.m.pal.input).rows; len(rows) == 0 || rows[0].text != "fake touch" {
		t.Fatalf("tuch matches %v, want fake touch first", rowTexts(rows))
	}
	d.key("backspace", "backspace", "backspace", "backspace")
	d.typeText("instal")
	q = d.m.parse(d.m.pal.input)
	if len(q.rows) != 0 || !strings.HasPrefix(q.notice, `no such action "instal"; closest: `) || strings.Count(q.notice, ",") != 2 {
		t.Fatalf("unknown verb: rows %v notice %q, want no rows and the three closest verbs", rowTexts(q.rows), q.notice)
	}
	d.key("enter")
	if d.m.overlay != overlayPalette || d.m.pal.err != q.notice {
		t.Fatalf("Enter on an unknown verb: overlay %d err %q; want the palette open with the notice", d.m.overlay, d.m.pal.err)
	}
	d.key("esc", ":")
	d.typeText("fake touch ")
	q = d.m.parse(d.m.pal.input)
	if q.stage != stageTarget || len(q.rows) != 15 {
		t.Fatalf("after the verb: stage %d with %d rows, want the target stage with every customer", q.stage, len(q.rows))
	}
	contains(t, d.m.commandBar(), ":fake touch ▏", "→ delta (staging)")
	d.typeText("mi")
	if rows := d.m.parse(d.m.pal.input).rows; len(rows) != 1 || rows[0].text != "mike" {
		t.Fatalf("mi matches %v, want mike", rowTexts(rows))
	}
	d.key("tab")
	if d.m.pal.input != "fake touch mike " {
		t.Fatalf("Tab completed to %q", d.m.pal.input)
	}
	d.key("esc", ":")
	d.typeText("fake quick --")
	q = d.m.parse(d.m.pal.input)
	if q.stage != stageArg || !reflect.DeepEqual(rowTexts(q.rows), []string{"--count", "--note", "--verbose"}) {
		t.Fatalf("-- offers %v at stage %d, want every argument", rowTexts(q.rows), q.stage)
	}
	d.typeText("n")
	d.key("tab")
	if d.m.pal.input != "fake quick --note " {
		t.Fatalf("Tab completed the argument to %q", d.m.pal.input)
	}
	d.typeText("hi ")
	if q := d.m.parse(d.m.pal.input); q.stage != stageTarget || !reflect.DeepEqual(q.args, map[string]any{"note": "hi"}) || len(q.rows) != 0 {
		t.Fatalf("after an argument value: stage %d args %v rows %v; a global action has no target rows", q.stage, q.args, rowTexts(q.rows))
	}
}

// Tab completes the selected suggestion; a repeated Tab cycles through the suggestions.
func TestPaletteTabCycles(t *testing.T) {
	d := connected(t, newFake())
	d.key(":")
	d.typeText("fake ")
	rows := d.m.parse(d.m.pal.input).rows
	if len(rows) != 4 {
		t.Fatalf("fake matches %v, want the four fake actions", rowTexts(rows))
	}
	var got, want []string
	for i := range 5 {
		d.key("tab")
		got = append(got, d.m.pal.input)
		want = append(want, rows[i%len(rows)].text+" ")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tab gives %q, want %q: the suggestions in order, then round again", got, want)
	}
	d.key("backspace")
	d.key("tab")
	if d.m.pal.input != want[4] {
		t.Fatalf("after editing, Tab gives %q, want the typed action %q first; editing must end the cycle", d.m.pal.input, want[4])
	}
}

// ↑ and ↓ move the selection; Enter runs the selected action on the default target.
func TestPaletteSelectAndRun(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.selectCustomer("delta")
	d.key(":", "down", "down", "up")
	if d.m.pal.sel != 1 {
		t.Fatalf("selection %d, want 1", d.m.pal.sel)
	}
	d.key("up", "up", "enter")
	d.drain()
	if s := f.snapshot().started; len(s) != 1 || s[0].action != "fake check" || s[0].target.Customer != "delta" {
		t.Fatalf("Enter on the first row started %+v, want fake check on delta", s)
	}
}

// ↑ on an empty palette walks the history of this session.
func TestPaletteHistory(t *testing.T) {
	d := connected(t, newFake())
	for _, input := range []string{"fake check delta", "fake check mike"} {
		d.key(":")
		d.typeText(input)
		d.key("enter")
		d.drain()
		d.key("esc")
	}
	d.key(":", "up")
	if d.m.pal.input != "fake check mike" {
		t.Fatalf("↑ gives %q, want the last entry", d.m.pal.input)
	}
	d.key("up", "up")
	if d.m.pal.input != "fake check delta" {
		t.Fatalf("↑↑ gives %q, want the first entry", d.m.pal.input)
	}
	d.key("down", "down")
	if d.m.pal.input != "" {
		t.Fatalf("↓ past the newest gives %q, want an empty input", d.m.pal.input)
	}
}

// Arguments are typed like the CLI's; a bad argument is shown inline and nothing starts.
func TestPaletteArguments(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.key(":")
	d.typeText("fake quick --note hi --verbose --count=3")
	d.key("enter")
	d.drain()
	if s := f.snapshot().started; len(s) != 1 || !reflect.DeepEqual(s[0].args, map[string]any{"note": "hi", "verbose": true, "count": 3}) {
		t.Fatalf("started %+v, want note, verbose and count", s)
	}
	for input, want := range map[string]string{
		"fake quick --count 1x":        `argument --count must be an int, got "1x"`,
		"fake quick --count x":         `argument --count must be an int, got "x"`,
		"fake quick --nope":            `action "fake quick" has no argument "--nope"`,
		"fake quick --note":            "argument --note needs a string value",
		"fake quick --verbose=maybe":   `argument --verbose must be true or false, got "maybe"`,
		"fake quick --note a --note b": "argument --note is given twice",
		"fake check delta one two":     `unexpected "one"; usage: fake check <customer>`,
		"fake check delta srv-1":       `unexpected "srv-1"; usage: fake check <customer>`,
		"fake quick delta":             `unexpected "delta"; usage: fake quick [--count <int>] [--note <string>] [--verbose]`,
		"fake quick --x=":              `action "fake quick" has no argument "--x="`,
	} {
		d.key(":")
		d.typeText(input)
		d.key("enter")
		if d.m.overlay != overlayPalette || d.m.pal.err != want {
			t.Fatalf("%s: overlay %d error %q, want the palette open with %q", input, d.m.overlay, d.m.pal.err, want)
		}
		contains(t, d.frame(), want)
		d.key("esc")
	}
	if n := len(f.snapshot().started); n != 1 {
		t.Fatalf("%d runs started, want only the valid one", n)
	}
}

// docs/guides/ui.md §13 "extra action": a registry entry added in the test appears in the
// palette, the menu, the hint bar and the help, with its CLI form, with no other change.
func TestExtraActionAppearsEverywhere(t *testing.T) {
	f := newFake()
	f.actions = append(f.actions, service.ActionInfo{ID: "fake extra", Title: "Fake extra", Help: "Added only in this test.", Target: "customer", Effect: "read-only"})
	d := connected(t, f)
	d.selectCustomer("delta")
	where := map[string]bool{}
	for _, s := range d.m.Surfaces() {
		if s.Action == "fake extra" {
			where[s.Where] = true
			if s.CLI != "ktags action run fake extra <customer>" {
				t.Fatalf("CLI form %q", s.CLI)
			}
		}
	}
	if !where["palette"] || !where["menu"] || !where["hint bar"] || !where["help"] {
		t.Fatalf("fake extra appears in %v, want palette, menu, hint bar and help", where)
	}
	d.key("?")
	contains(t, strings.Join(d.m.helpLines(), "\n"), "fake extra", "ktags action run fake extra <customer>")
	d.key("x", "enter")
	if !slices.ContainsFunc(d.m.menu.items, func(a service.ActionInfo) bool { return a.ID == "fake extra" }) {
		t.Fatal("the menu lacks fake extra")
	}
}

// Every action the screens offer comes from the service's action list.
func TestSurfacesComeFromTheService(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("delta")
	known := map[string]bool{}
	for _, a := range testActions() {
		known[a.ID] = true
	}
	for _, s := range d.m.Surfaces() {
		if s.Action != "" && !known[s.Action] {
			t.Fatalf("%s shows %q, which the service does not list", s.Where, s.Action)
		}
		if s.CLI == "" {
			t.Fatalf("%s shows %q without a CLI form", s.Where, s.Action)
		}
	}
}

func runFixture(f *fakeClient, n int, status string) string {
	id := "20260911T115900Z-0000000a"
	info := service.RunInfo{ID: id, Action: "fake touch", Target: service.Target{Kind: "customer", Customer: "delta"}, Status: "running", StartedAt: testNow.Add(-time.Minute)}
	f.runs = append(f.runs, info)
	f.events[id] = events(n, "step")
	final := info
	final.Status, final.LastEventID = status, uint64(n)
	final.Result = &service.ResultInfo{Status: status, Summary: "touched", FinishedAt: testNow}
	f.final[id] = final
	return id
}

func openDeltaRun(t *testing.T, f *fakeClient) *driver {
	t.Helper()
	d := connected(t, f)
	d.selectCustomer("delta")
	d.key("tab", "right", "right", "enter")
	if d.m.screen != screenRun {
		t.Fatal("the run view did not open")
	}
	return d
}

// docs/guides/ui.md §13 reconnect test: the service drops the stream mid-run; the run view
// resumes from its cursor without duplicate or missing lines.
func TestReconnectResumesFromTheCursor(t *testing.T) {
	f := newFake()
	id := runFixture(f, 6, "succeeded")
	f.dropAt[id] = 3
	d := openDeltaRun(t, f)
	d.until("the lost stream", func(m Model) bool { return m.run.lost })
	if got := ids(d.m.run.lines); !reflect.DeepEqual(got, span1(1, 3)) {
		t.Fatalf("before the drop: %v", got)
	}
	contains(t, d.m.run.notice, "stream of run "+id+" lost after event 3", "reconnecting")
	contains(t, d.frame(), "stream of run "+id+" lost after event 3")
	d.apply(tickMsg{})
	d.until("the result", func(m Model) bool { return m.run.info.Result != nil && m.run.stop == nil })
	if got := ids(d.m.run.lines); !reflect.DeepEqual(got, span1(1, 6)) {
		t.Fatalf("after the reconnect: %v, want 1..6 exactly once", got)
	}
	var afters []uint64
	for _, c := range f.snapshot().calls {
		afters = append(afters, c.after)
	}
	if !reflect.DeepEqual(afters, []uint64{0, 3}) {
		t.Fatalf("stream cursors %v, want 0 then 3", afters)
	}
}

// A successful refresh also resumes a lost stream.
func TestRefreshResumesALostStream(t *testing.T) {
	f := newFake()
	id := runFixture(f, 4, "succeeded")
	f.dropAt[id] = 2
	d := openDeltaRun(t, f)
	d.until("the lost stream", func(m Model) bool { return m.run.lost })
	d.m.refreshing = true
	d.run(d.m.refresh())
	d.until("the result", func(m Model) bool { return m.run.info.Result != nil && m.run.stop == nil && len(m.run.lines) == 4 })
}

// A service that replays events the client already has does not duplicate lines.
func TestDuplicateEventsAreDropped(t *testing.T) {
	f := newFake()
	id := runFixture(f, 6, "succeeded")
	f.dropAt[id] = 3
	f.ignoreCursor = true
	d := openDeltaRun(t, f)
	d.until("the lost stream", func(m Model) bool { return m.run.lost })
	d.apply(tickMsg{})
	d.until("the result", func(m Model) bool { return m.run.info.Result != nil && m.run.stop == nil })
	if got := ids(d.m.run.lines); !reflect.DeepEqual(got, span1(1, 6)) {
		t.Fatalf("lines %v, want 1..6 exactly once", got)
	}
}

// A cursor the service refuses reloads the run from the start and says so.
func TestInvalidCursorReloadsFromTheStart(t *testing.T) {
	f := newFake()
	id := runFixture(f, 5, "succeeded")
	f.dropAt[id] = 2
	f.invalid[id] = true
	d := openDeltaRun(t, f)
	d.until("the lost stream", func(m Model) bool { return m.run.lost })
	d.apply(tickMsg{})
	d.until("the result", func(m Model) bool { return m.run.info.Result != nil && m.run.stop == nil })
	if got := ids(d.m.run.lines); !reflect.DeepEqual(got, span1(1, 5)) {
		t.Fatalf("lines %v, want 1..5 after the reload", got)
	}
	contains(t, d.m.run.notice, "the event cursor 2 is no longer valid for run "+id+"; reloaded the run from the start")
	contains(t, d.frame(), "the event cursor 2 is no longer valid")
	var afters []uint64
	for _, c := range f.snapshot().calls {
		afters = append(afters, c.after)
	}
	if !reflect.DeepEqual(afters, []uint64{0, 2, 0}) {
		t.Fatalf("stream cursors %v, want 0, 2, then 0", afters)
	}
}

// A refusal at the start of a run is shown, not retried: there is no earlier cursor to fall
// back to. A missing run says so with the next step.
func TestStreamRefusalsAreShown(t *testing.T) {
	for _, e := range []*service.Error{
		{Code: service.CodeInvalid, Message: "cursor 0 is beyond the last event", Hint: "resume from the last event ID the client received"},
		{Code: service.CodeNotFound, Message: "run: no run 20260911T115900Z-0000000a", Hint: "ktags run list"},
	} {
		f := newFake()
		id := runFixture(f, 3, "succeeded")
		f.refuse = map[string]*service.Error{id: e}
		d := openDeltaRun(t, f)
		d.until("the stream end", func(m Model) bool { return m.run.stop == nil })
		d.apply(tickMsg{})
		d.drain()
		if n := len(f.snapshot().calls); n != 1 || d.m.run.lost {
			t.Fatalf("%s: %d stream calls, lost %v; want one call and no retry", e.Code, n, d.m.run.lost)
		}
		contains(t, d.frame(), e.Message+" — next: "+e.Hint)
	}
}

// Messages of a superseded stream are dropped.
func TestSupersededStreamIsIgnored(t *testing.T) {
	f := newFake()
	runFixture(f, 3, "succeeded")
	d := openDeltaRun(t, f)
	d.until("the replay", func(m Model) bool { return m.run.stop == nil })
	old := d.m.run.gen - 1
	d.apply(streamMsg{gen: old, event: &service.Event{ID: 99, Message: "stale"}})
	d.apply(streamMsg{gen: old, err: errors.New("stale end")})
	if got := ids(d.m.run.lines); !reflect.DeepEqual(got, span1(1, 3)) || d.m.run.notice != "" {
		t.Fatalf("after superseded messages: lines %v notice %q", got, d.m.run.notice)
	}
}

// The run view holds a bounded backlog and names where the rest is.
func TestBacklogIsBounded(t *testing.T) {
	f := newFake()
	id := runFixture(f, maxLines+100, "succeeded")
	d := openDeltaRun(t, f)
	d.until("the replay", func(m Model) bool { return m.run.stop == nil && m.run.info.Result != nil })
	r := d.m.run
	if len(r.lines) != maxLines || r.dropped != 100 || r.lines[0].ID != 101 {
		t.Fatalf("held %d lines from %d, dropped %d; want %d from 101, 100 dropped", len(r.lines), r.lines[0].ID, r.dropped, maxLines)
	}
	contains(t, d.frame(), "… 100 earlier event(s) are in the run record: ktags run watch "+id)
}

// Scrolling up pauses the follow and says how to resume; End follows again.
func TestRunViewScroll(t *testing.T) {
	f := newFake()
	runFixture(f, 60, "succeeded")
	d := openDeltaRun(t, f)
	d.until("the replay", func(m Model) bool { return m.run.stop == nil })
	d.key("up")
	if d.m.run.offset != 1 {
		t.Fatalf("↑ gives offset %d", d.m.run.offset)
	}
	contains(t, d.frame(), "paused, End to follow", "next      ktags run status")
	d.key("home")
	top := d.m.run.offset
	if top != 60-d.m.logRows() {
		t.Fatalf("Home gives offset %d, want %d", top, 60-d.m.logRows())
	}
	d.key("pgdown")
	if d.m.run.offset != max(0, top-d.m.logRows()) {
		t.Fatalf("PgDn gives offset %d", d.m.run.offset)
	}
	d.key("pgup", "down", "end")
	if d.m.run.offset != 0 || strings.Contains(d.frame(), "paused") {
		t.Fatalf("End gives offset %d", d.m.run.offset)
	}
	d.wheel("panel:log", tea.MouseWheelUp)
	if d.m.run.offset != 3 {
		t.Fatalf("wheel up over the log gives offset %d, want 3", d.m.run.offset)
	}
	d.key("end")
	d.key("up")
	d.apply(streamMsg{gen: d.m.run.gen, event: &service.Event{ID: 61, Message: "late"}})
	if d.m.run.offset != 2 {
		t.Fatalf("a new line while paused gives offset %d, want the view held at 2", d.m.run.offset)
	}
}

// Esc leaves the run view and detaches; the run continues. Reopening resumes from the cursor.
func TestEscDetachesAndReopenResumes(t *testing.T) {
	f := newFake()
	f.detached = make(chan string, 1)
	id := testRuns()[2].ID
	d := connected(t, f)
	d.selectCustomer("bravo")
	d.key("tab", "right", "right", "enter")
	d.until("three events", func(m Model) bool { return len(m.run.lines) == 3 })
	d.key("esc")
	select {
	case got := <-f.detached:
		if got != id {
			t.Fatalf("detached from %s, want %s", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Esc did not detach the stream")
	}
	if d.m.screen != screenFleet || d.m.status != "left run "+id+"; it continues in the ktags service — reopen it from the Runs tab" {
		t.Fatalf("after Esc: screen %d status %q", d.m.screen, d.m.status)
	}
	if len(f.snapshot().cancelled) != 0 {
		t.Fatal("leaving the run view cancelled the run")
	}
	d.drain()
	d.key("enter")
	d.drain()
	calls := f.snapshot().calls
	if last := calls[len(calls)-1]; last.after != 3 || len(d.m.run.lines) != 3 {
		t.Fatalf("reopen streamed after %d with %d lines, want after 3 keeping the 3 lines", last.after, len(d.m.run.lines))
	}
	if d.m.run.notice != "" || d.m.run.lost {
		t.Fatalf("after a detach and reopen: notice %q lost %v; a detach is not a lost stream", d.m.run.notice, d.m.run.lost)
	}
}

// Ctrl-C in the run view asks "Cancel run?" through the confirmation levels: a prod run and a
// run of a customer that left the inventory need the typed name; staging and global ask
// [y/N]. A finished run has nothing to cancel.
// A refresh stops at the first failing call: a later call that succeeds must not hide the
// error, so a protocol mismatch from any call drops the data.
func TestRefreshStopsAtTheFirstError(t *testing.T) {
	for _, call := range []string{"hello", "fleet", "customers", "actions"} {
		f := newFake()
		f.fail = map[string]error{call: &service.Error{Code: service.CodeProtocolMismatch, Message: "protocol 9 is not 1", Hint: "upgrade ktags"}}
		d := newDriver(t, f)
		d.until("the first refresh", func(m Model) bool { return m.conn != connConnecting })
		if d.m.conn != connMismatch || d.m.haveData {
			t.Fatalf("%s fails: conn %d haveData %v, want the mismatch state", call, d.m.conn, d.m.haveData)
		}
	}
}

// A click outside the dialog's buttons does nothing: what is behind a dialog is not reachable.
func TestDialogSwallowsOtherClicks(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.selectCustomer("delta")
	d.key(":")
	d.typeText("fake touch delta")
	d.key("enter")
	tabBefore, focusBefore := d.m.tab, d.m.focus
	d.click("tab:2", tea.MouseLeft)
	d.drain()
	if d.m.overlay != overlayDialog || d.m.tab != tabBefore || d.m.focus != focusBefore || len(f.snapshot().started) != 0 {
		t.Fatalf("click behind the dialog: overlay %d tab %d focus %d started %v, want the dialog kept and nothing changed", d.m.overlay, d.m.tab, d.m.focus, f.snapshot().started)
	}
}

// Ctrl-C over the palette, the context menu or a dialog quits like q on the fleet screen
// (docs/guides/ui.md §4); nothing is started or cancelled.
func TestCtrlCQuitsFromOverlays(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(*driver)
	}{
		{"palette", func(d *driver) { d.key(":"); d.typeText("fake touch delta") }},
		{"menu", func(d *driver) { d.selectCustomer("delta"); d.key("enter") }},
		{"confirm", func(d *driver) { d.key(":"); d.typeText("fake touch delta"); d.key("enter") }},
		{"type-name", func(d *driver) { d.key(":"); d.typeText("fake touch mike"); d.key("enter", "m") }},
		{"error", func(d *driver) { d.key(":"); d.typeText("fake check nobody"); d.key("enter") }},
	} {
		f := newFake()
		d := connected(t, f)
		c.open(d)
		if d.m.overlay == overlayNone || d.m.overlay == overlayHelp {
			t.Fatalf("%s: overlay %d did not open", c.name, d.m.overlay)
		}
		d.key("ctrl+c")
		d.drain()
		if !d.m.quitting || len(f.snapshot().started) != 0 || len(f.snapshot().cancelled) != 0 {
			t.Fatalf("%s: Ctrl-C gives quitting %v, started %v, cancelled %v; want a quit and nothing else", c.name, d.m.quitting, f.snapshot().started, f.snapshot().cancelled)
		}
	}

	d := connected(t, newFake())
	d.key("?", "ctrl+c")
	if d.m.quitting || d.m.overlay != overlayNone {
		t.Fatalf("Ctrl-C over help: quitting %v overlay %d, want help closed", d.m.quitting, d.m.overlay)
	}
}

func TestCtrlCCancelsWithConfirmation(t *testing.T) {
	cases := []struct {
		customer string
		kind     dialogKind
		answer   []string
	}{
		{"bravo", dialogTypeName, []string{"b", "r", "a", "v", "o", "enter"}},
		{"delta", dialogConfirm, []string{"y"}},
		{"", dialogConfirm, []string{"y"}},
		{"ghost", dialogTypeName, []string{"g", "h", "o", "s", "t", "enter"}},
	}
	for _, c := range cases {
		f := newFake()
		id := "20260911T115900Z-000000cc"
		target := service.Target{Kind: "customer", Customer: c.customer}
		if c.customer == "" {
			target = service.Target{Kind: "global"}
		}
		info := service.RunInfo{ID: id, Action: "fake touch", Target: target, Status: "running", StartedAt: testNow}
		f.runs = append(f.runs, info)
		f.hold[id] = true
		d := connected(t, f)
		m, cmd := d.m.openRun(info)
		d.m = m
		d.run(cmd)
		d.key("ctrl+c")
		if d.m.overlay != overlayDialog || d.m.dialog.kind != c.kind || d.m.dialog.title != "Cancel run?" {
			t.Fatalf("%q: dialog %+v, want kind %d", c.customer, d.m.dialog, c.kind)
		}
		d.key(c.answer...)
		d.drain()
		if got := f.snapshot().cancelled; !reflect.DeepEqual(got, []string{id}) || d.m.status != "cancel requested for run "+id {
			t.Fatalf("%q: cancelled %v status %q", c.customer, got, d.m.status)
		}
	}

	f := newFake()
	d := connected(t, f)
	d.selectCustomer("mike")
	d.key("tab", "right", "right", "enter")
	d.until("the replay", func(m Model) bool { return m.run.stop == nil })
	d.key("ctrl+c")
	if d.m.overlay != overlayNone || d.m.status != "run "+testRuns()[0].ID+" has ended; there is nothing to cancel" {
		t.Fatalf("Ctrl-C on a finished run: overlay %d status %q", d.m.overlay, d.m.status)
	}
}

// A declined, refused or disconnected cancel leaves the run alone and says so.
func TestCancelRefusals(t *testing.T) {
	f := newFake()
	f.cancelErr = &service.Error{Code: service.CodeConflict, Message: "run x is succeeded and not active in this service", Hint: "check the run with run.status"}
	d := connected(t, f)
	d.selectCustomer("bravo")
	d.key("tab", "right", "right", "enter", "ctrl+c", "esc")
	if d.m.status != "not confirmed; run "+testRuns()[2].ID+" continues" || len(f.snapshot().cancelled) != 0 {
		t.Fatalf("declined cancel: status %q cancelled %v", d.m.status, f.snapshot().cancelled)
	}
	d.key("ctrl+c")
	d.typeText("bravo")
	d.key("enter")
	d.drain()
	contains(t, dialogText(d.m), "cannot cancel run", "not active in this service", "next: check the run with run.status")
	d.key("enter")
	d.m.conn = connReconnecting
	d.key("ctrl+c")
	contains(t, dialogText(d.m), "actions are disabled while the service connection is reconnecting")
}

// docs/guides/ui.md §3: unavailable offers the next command and Enter retries; a later answer
// connects. q quits.
func TestUnavailableRetries(t *testing.T) {
	f := newFake()
	f.err = unavailableErr()
	d := newDriver(t, f)
	d.until("unavailable", func(m Model) bool { return m.conn == connUnavailable })
	contains(t, d.frame(), "cause  cannot reach the ktags service", "next   ktags service start", "Enter retries · q quits")
	d.apply(tickMsg{})
	d.drain()
	if n := f.snapshot().refreshes; n != 1 {
		t.Fatalf("%d refreshes after a tick while unavailable; polling must wait for Enter", n)
	}
	f.set(func(f *fakeClient) { f.err = nil })
	d.key("enter")
	d.until("connected", func(m Model) bool { return m.conn == connConnected })
	d.key("q")
	d.until("quit", func(Model) bool { return d.quit })
}

// Keys other than Enter, q, ?, m do nothing while unavailable; ? and m still work.
func TestOfflineKeys(t *testing.T) {
	f := newFake()
	f.err = unavailableErr()
	d := newDriver(t, f)
	d.until("unavailable", func(m Model) bool { return m.conn == connUnavailable })
	d.key(":", "tab")
	if d.m.overlay != overlayNone || d.m.focus != focusList {
		t.Fatal("palette or focus keys acted while unavailable")
	}
	d.key("m")
	if d.m.mouse {
		t.Fatal("m did not toggle the mouse while unavailable")
	}
	d.key("?")
	if d.m.overlay != overlayHelp {
		t.Fatal("? did not open help while unavailable")
	}
	d.key("x", "ctrl+c")
	d.until("quit", func(Model) bool { return d.quit })
}

// A service that cannot be started opens the TUI unavailable with the cause and does not
// refresh until Enter.
func TestStartFailureOpensUnavailable(t *testing.T) {
	f := newFake()
	d := newDriver(t, f, func(o *Options) { o.StartErr = errors.New("launchctl bootstrap failed: exit status 5") })
	d.drain()
	if d.m.conn != connUnavailable || f.snapshot().refreshes != 0 {
		t.Fatalf("state %d after %d refreshes, want unavailable without a refresh", d.m.conn, f.snapshot().refreshes)
	}
	contains(t, d.frame(), "cause  launchctl bootstrap failed: exit status 5")
	d.key("enter")
	d.until("connected", func(m Model) bool { return m.conn == connConnected })
}

// A mismatch drops the data: nothing of a service of another version is rendered.
func TestProtocolMismatchDropsData(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	f.set(func(f *fakeClient) {
		f.err = &service.Error{Code: service.CodeProtocolMismatch, Message: "the service speaks ktags protocol 2", Hint: "upgrade ktags"}
	})
	d.apply(tickMsg{})
	d.until("mismatch", func(m Model) bool { return m.conn == connMismatch })
	frame := d.frame()
	if d.m.haveData || strings.Contains(frame, "mike") || strings.Contains(frame, "▶ 1 run") {
		t.Fatalf("data survived the mismatch:\n%s", frame)
	}
	contains(t, frame, "protocol mismatch", "cause  the service speaks ktags protocol 2", "next   upgrade ktags")
}

// Reconnecting keeps the data with its age, disables the actions in the hint bar, and a
// successful refresh ends it.
func TestReconnectingKeepsDataAndDisablesActions(t *testing.T) {
	f := newFake()
	clock := testNow
	d := connected(t, f, func(o *Options) { o.Now = func() time.Time { return clock } })
	clock = testNow.Add(3 * time.Minute)
	f.set(func(f *fakeClient) { f.err = unavailableErr() })
	d.apply(tickMsg{})
	d.until("reconnecting", func(m Model) bool { return m.conn == connReconnecting })
	contains(t, d.frame(), "service connection lost — reconnecting · data from 3m ago", "Customers (15) · data from 3m ago", "mike")
	for _, h := range d.m.hints() {
		if h.action != nil && !h.off {
			t.Fatalf("hint %s is enabled while reconnecting", h.label)
		}
	}
	d.apply(tickMsg{})
	d.drain()
	if n := f.snapshot().refreshes; n != 3 {
		t.Fatalf("%d refreshes; polling must continue while reconnecting", n)
	}
	f.set(func(f *fakeClient) { f.err = nil })
	d.apply(tickMsg{})
	d.until("connected", func(m Model) bool { return m.conn == connConnected })
	if strings.Contains(d.frame(), "reconnecting") {
		t.Fatal("the banner stayed after the reconnect")
	}
}

// A refresh keeps the selection; a customer that left the inventory hands it to the first.
func TestSelectionSurvivesRefresh(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.selectCustomer("delta")
	d.apply(tickMsg{})
	d.drain()
	if d.m.selected != "delta" {
		t.Fatalf("selection %q after a refresh, want delta", d.m.selected)
	}
	f.set(func(f *fakeClient) { f.fleet.Customers = f.fleet.Customers[:3] })
	d.apply(tickMsg{})
	d.drain()
	if d.m.selected != "zulu" {
		t.Fatalf("selection %q after delta left, want zulu", d.m.selected)
	}
}

// Below 80×24 the frame is one line that asks for a resize; only quitting works.
func TestTooSmall(t *testing.T) {
	d := connected(t, newFake())
	d.apply(tea.WindowSizeMsg{Width: 79, Height: 24})
	if got := strings.TrimSpace(d.frame()); got != "terminal is 79×24; ktags needs at least 80×24 — resize the window" {
		t.Fatalf("frame %q", got)
	}
	d.key(":")
	if d.m.overlay != overlayNone {
		t.Fatal("a key acted below the minimum size")
	}
	d.apply(tea.WindowSizeMsg{Width: 80, Height: 23})
	contains(t, d.frame(), "terminal is 80×23")
	d.apply(tea.WindowSizeMsg{Width: 80, Height: 24})
	contains(t, d.frame(), "Customers (15)")
	d.apply(tea.WindowSizeMsg{Width: 79, Height: 24})
	d.key("q")
	d.until("quit", func(Model) bool { return d.quit })
}

// docs/guides/ui.md §5: a click selects a row or a tab, a double or right click opens the
// menu, the wheel scrolls the panel under the pointer, a hint click runs it, and dialog
// buttons answer.
func TestMouse(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.click("row:mike", tea.MouseLeft)
	if d.m.selected != "mike" || d.m.focus != focusList || d.m.overlay != overlayNone {
		t.Fatalf("click: selected %q focus %d overlay %d", d.m.selected, d.m.focus, d.m.overlay)
	}
	d.click("tab:2", tea.MouseLeft)
	if d.m.focus != focusDetail || d.m.tab != tabRuns {
		t.Fatalf("tab click: focus %d tab %d", d.m.focus, d.m.tab)
	}
	d.click("row:delta", tea.MouseRight)
	if d.m.selected != "delta" || d.m.overlay != overlayMenu {
		t.Fatalf("right click: selected %q overlay %d, want delta's menu", d.m.selected, d.m.overlay)
	}
	d.click("menu:1", tea.MouseLeft)
	if d.m.overlay != overlayDialog || d.m.dialog.kind != dialogConfirm {
		t.Fatalf("menu click: dialog %+v, want the confirmation of fake touch", d.m.dialog)
	}
	d.click("btn:no", tea.MouseLeft)
	if d.m.overlay != overlayNone || len(f.snapshot().started) != 0 {
		t.Fatal("the no button did not decline")
	}
	d.key("enter", "down", "enter")
	d.click("btn:yes", tea.MouseLeft)
	d.drain()
	if len(f.snapshot().started) != 1 {
		t.Fatal("the yes button did not start the run")
	}
	d.key("esc")
	d.wheel("panel:list", tea.MouseWheelUp)
	if d.m.selected != "charlie" || d.m.focus != focusList {
		t.Fatalf("wheel up over the list: selected %q", d.m.selected)
	}
	d.click("row:mike", tea.MouseLeft)
	d.click("row:mike", tea.MouseLeft)
	if d.m.overlay != overlayMenu {
		t.Fatal("a double click did not open the menu")
	}
	d.key("esc")
	d.click("row:mike", tea.MouseLeft)
	d.click("tab:1", tea.MouseLeft)
	d.click("row:mike", tea.MouseLeft)
	if d.m.overlay == overlayMenu {
		t.Fatal("two clicks on a row with another click between them opened the menu")
	}
	d.click("hint:0", tea.MouseLeft)
	if d.m.overlay != overlayPalette {
		t.Fatal("clicking the palette hint did not open the palette")
	}
	d.click("pal:0", tea.MouseLeft)
	d.drain()
	if s := f.snapshot().started; len(s) != 2 || s[1].action != "fake check" || s[1].target.Customer != "mike" {
		t.Fatalf("a palette row click started %+v, want fake check on mike", s)
	}
}

// Clicking the run badge reopens the active run; a double click on a run row opens it.
func TestMouseOpensRuns(t *testing.T) {
	d := connected(t, newFake())
	d.click("badge", tea.MouseLeft)
	if d.m.screen != screenRun || d.m.run.id != testRuns()[2].ID {
		t.Fatalf("badge click: screen %d, want the active run", d.m.screen)
	}
	d.wheel("panel:log", tea.MouseWheelUp)
	d.key("esc")
	d.selectCustomer("mike")
	d.click("tab:2", tea.MouseLeft)
	id := testRuns()[0].ID
	d.click("run:"+id, tea.MouseLeft)
	d.click("run:"+id, tea.MouseLeft)
	if d.m.screen != screenRun || d.m.run.id != id {
		t.Fatalf("double click on a run: screen %d", d.m.screen)
	}
}
