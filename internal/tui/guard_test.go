package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/nesiler/ktags/internal/service"
)

// addRuns records n finished runs of a customer, oldest first.
func addRuns(f *fakeClient, customer string, n int) []string {
	var out []string
	for i := range n {
		id := fmt.Sprintf("20260911T1000%02dZ-%08d", i, 100+i)
		started := testNow.Add(-time.Duration(n-i) * time.Minute)
		f.runs = append(f.runs, service.RunInfo{ID: id, Action: "fake check", Target: service.Target{Kind: "customer", Customer: customer}, Status: "succeeded",
			StartedAt: started, Result: &service.ResultInfo{Status: "succeeded", Summary: "ok", FinishedAt: started.Add(time.Second)}})
		f.final[id] = f.runs[len(f.runs)-1]
		out = append(out, id)
	}
	return out
}

// A protocol error keeps its code, message and hint; a timeout is the service not answering;
// anything else is reported as it came, with the status command as the next step.
func TestDescribe(t *testing.T) {
	cases := []struct {
		err  error
		want problem
	}{
		{&service.Error{Code: service.CodeConflict, Message: "busy", Hint: "wait"}, problem{code: service.CodeConflict, message: "busy", hint: "wait"}},
		{fmt.Errorf("fleet: %w", context.DeadlineExceeded), problem{code: service.CodeUnavailable, message: "the ktags service did not answer within 10s", hint: "ktags service status"}},
		{errors.New("boom"), problem{message: "boom", hint: "ktags service status"}},
	}
	for _, c := range cases {
		if got := describe(c.err); got != c.want {
			t.Errorf("describe(%v) = %+v, want %+v", c.err, got, c.want)
		}
	}
}

// A tick or an Enter while a refresh is in flight does not start a second one.
func TestRefreshesDoNotOverlap(t *testing.T) {
	f := newFake()
	f.block = make(chan struct{})
	t.Cleanup(func() { close(f.block) })
	d := newDriver(t, f)
	d.drain()
	d.apply(tickMsg{})
	d.drain()
	if n := f.snapshot().refreshes; n != 1 {
		t.Fatalf("%d refreshes after a tick during a refresh, want 1", n)
	}
	d.m.conn = connUnavailable
	d.key("enter")
	d.drain()
	if n := f.snapshot().refreshes; n != 1 {
		t.Fatalf("%d refreshes after Enter during a refresh, want 1", n)
	}
}

// A refresh that still lists a finished run as running does not undo its stored result.
func TestStaleRefreshKeepsTheResult(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.selectCustomer("mike")
	d.key("tab", "right", "right", "enter")
	d.until("the replay", func(m Model) bool { return m.run.stop == nil })
	msg, ok := d.m.refresh()().(refreshMsg)
	if !ok {
		t.Fatal("refresh returned no refreshMsg")
	}
	msg.runs[0].Status, msg.runs[0].Result = "running", nil
	d.apply(msg)
	if d.m.run.info.Result == nil || d.m.run.info.Status != "succeeded" {
		t.Fatalf("run view after a stale refresh: %+v, want the stored result kept", d.m.run.info)
	}
	d.key("esc")
	m, cmd := d.m.openRun(msg.runs[0])
	d.m = m
	d.run(cmd)
	d.drain()
	if d.m.run.info.Result == nil {
		t.Fatalf("reopening from a stale list entry lost the stored result: %+v", d.m.run.info)
	}
}

// Enter on an empty Runs tab or with no customer opens nothing.
func TestEnterWithNothingToOpen(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("delta")
	d.key("tab", "right", "right", "enter")
	if d.m.screen != screenFleet || d.m.overlay != overlayNone {
		t.Fatalf("Enter on an empty Runs tab: screen %d overlay %d", d.m.screen, d.m.overlay)
	}
	d = connected(t, emptyFake())
	d.key("down", "end", "enter")
	if d.m.overlay != overlayNone || d.m.selected != "" {
		t.Fatalf("keys without customers: overlay %d selected %q", d.m.overlay, d.m.selected)
	}
}

// Moving on the Health tab leaves the Runs tab cursor alone.
func TestMoveOnHealthTabKeepsRunCursor(t *testing.T) {
	f := newFake()
	addRuns(f, "delta", 3)
	d := connected(t, f)
	d.selectCustomer("delta")
	d.key("tab", "right", "down", "down", "right")
	if d.m.tab != tabRuns || d.m.runCursor != 0 {
		t.Fatalf("after ↓ on Health: tab %d run cursor %d, want Runs at 0", d.m.tab, d.m.runCursor)
	}
}

// ↓ in the menu stops at the last item, and Enter runs that item.
func TestMenuSelectionClamps(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("delta")
	d.key("enter", "down", "down", "down", "down", "down", "enter")
	if d.m.dialog.kind != dialogTypeName || d.m.dialog.start == nil || d.m.dialog.start.action != "fake wipe" {
		t.Fatalf("↓↓↓↓↓ Enter: dialog %+v, want fake wipe, the last item", d.m.dialog)
	}
	d.key("esc", "enter", "up", "up", "down")
	if d.m.menu.sel != 1 {
		t.Fatalf("↑↑↓ from the top gives item %d, want 1", d.m.menu.sel)
	}
}

// The cancel hint of a run is disabled while reconnecting; clicking it does nothing.
func TestDisabledCancelHint(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.selectCustomer("bravo")
	d.key("tab", "right", "right", "enter")
	f.set(func(f *fakeClient) { f.err = unavailableErr() })
	d.m.refreshing = true
	d.run(d.m.refresh())
	d.until("reconnecting", func(m Model) bool { return m.conn == connReconnecting })
	for i, h := range d.m.hints() {
		if h.key == "ctrl+c" {
			if !h.off {
				t.Fatal("the cancel hint is enabled while reconnecting")
			}
			d.click(fmt.Sprintf("hint:%d", i), tea.MouseLeft)
		}
	}
	if d.m.overlay != overlayNone {
		t.Fatalf("a disabled cancel hint opened overlay %d", d.m.overlay)
	}
}

// Long help lines wrap at 80 columns instead of losing the CLI form.
func TestHelpWrapsLongLines(t *testing.T) {
	d := connected(t, newFake())
	d.apply(tea.WindowSizeMsg{Width: 80, Height: 30})
	d.key("?")
	contains(t, d.frame(), "[--verbose]")
}

// A space without text, as some terminals send it, still types a space.
func TestSpaceWithoutText(t *testing.T) {
	d := connected(t, newFake())
	d.key(":")
	d.typeText("fake")
	d.apply(tea.KeyPressMsg{Code: tea.KeySpace})
	if d.m.pal.input != "fake " {
		t.Fatalf("space without text gives %q", d.m.pal.input)
	}
}

// The Health tab names a running check and the connection failure in full; the Runs tab says
// when there are no runs.
func TestDetailTabLines(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("alpha")
	d.key("tab", "right")
	contains(t, d.frame(), "checking    a health check is running")
	d.selectCustomer("golf")
	health := strings.Join(strings.Fields(stripped(strings.Join(d.m.healthLines(d.m.th, d.m.current(), 51), " "))), " ")
	contains(t, health, "connection unreachable · 3h ago lookup golf-srv-1.example.test: no such host", "missed 2 scheduled check(s) could not run", "no check results yet")
	d.selectCustomer("delta")
	d.key("tab", "right")
	contains(t, d.frame(), "no runs for this customer yet")
	d.selectCustomer("zulu")
	d.key("tab", "left")
	contains(t, d.frame(), "[Health]", "! record refused: cannot parse")
}

// A menu opened on the bottom row stays above the hint and command bars.
func TestMenuStaysAboveTheBars(t *testing.T) {
	f := newFake()
	for _, id := range []string{"fake one", "fake two"} {
		f.actions = append(f.actions, service.ActionInfo{ID: id, Title: "Extra", Help: "x", Target: "customer", Effect: "read-only"})
	}
	d := connected(t, f)
	d.apply(tea.WindowSizeMsg{Width: 100, Height: 24})
	d.selectCustomer("delta")
	d.key("enter")
	lines := strings.Split(d.frame(), "\n")
	if !strings.Contains(lines[22], "enter run") {
		t.Fatalf("the hint bar is covered by the menu: %q", lines[22])
	}
}

// A run whose record cannot be read shows the problem; a run without events says it waits.
func TestRunProblemAndWaiting(t *testing.T) {
	f := newFake()
	broken := service.RunInfo{ID: "20260911T115900Z-000000ee", Action: "fake check", Target: service.Target{Kind: "customer", Customer: "delta"}, Status: "unknown", StartedAt: testNow, Problem: "the result file is unreadable"}
	f.final[broken.ID] = broken
	d := connected(t, f)
	m, cmd := d.m.openRun(broken)
	d.m = m
	d.run(cmd)
	d.until("the stream end", func(m Model) bool { return m.run.stop == nil })
	contains(t, d.frame(), "problem   the result file is unreadable", "next      ktags run status "+broken.ID, "waiting for events…")
}

// The help is cut to the screen: the hint and command bars stay visible.
func TestHelpFitsTheScreen(t *testing.T) {
	f := newFake()
	for i := range 30 {
		f.actions = append(f.actions, service.ActionInfo{ID: fmt.Sprintf("extra %02d", i), Title: "Extra", Help: "x", Target: "customer", Effect: "read-only"})
	}
	d := connected(t, f)
	d.apply(tea.WindowSizeMsg{Width: 100, Height: 24})
	d.key("?")
	lines := strings.Split(d.frame(), "\n")
	if len(lines) != 24 || !strings.Contains(lines[22], "any key close") {
		t.Fatalf("help at 100×24: %d lines, hint bar %q", len(lines), lines[len(lines)-2])
	}
}

// Two clicks on a row further apart than the double-click window are two single clicks.
func TestSlowClicksAreNotADoubleClick(t *testing.T) {
	clock := testNow
	d := connected(t, newFake(), func(o *Options) { o.Now = func() time.Time { return clock } })
	d.click("row:mike", tea.MouseLeft)
	clock = clock.Add(time.Second)
	d.click("row:mike", tea.MouseLeft)
	if d.m.overlay == overlayMenu {
		t.Fatal("two clicks a second apart opened the menu")
	}
}

// A menu without customer actions says so and Enter only closes it.
func TestMenuWithoutActions(t *testing.T) {
	f := newFake()
	f.actions = []service.ActionInfo{testActions()[1]}
	d := connected(t, f)
	d.key("enter")
	contains(t, d.frame(), "no actions for a customer; open the palette with :")
	d.key("enter")
	d.drain()
	if d.m.overlay != overlayNone || len(f.snapshot().started) != 0 {
		t.Fatalf("Enter on an empty menu: overlay %d started %v", d.m.overlay, f.snapshot().started)
	}
}

// An environment other than exactly staging or test counts as prod, including another case, a
// space around it or a longer word; a global destructive action is confirmed by typing its ID.
func TestDangerEdges(t *testing.T) {
	f := newFake()
	f.fleet.Customers = append(f.fleet.Customers,
		entry("quebec", "qa", "ok"), entry("romeo", "Staging", "ok"), entry("sierra", "staging ", "ok"),
		entry("tango", "TEST", "ok"), entry("uniform", "testing", "ok"))
	f.actions = append(f.actions, service.ActionInfo{ID: "fake nuke", Title: "Fake nuke", Help: "Wipes the laptop.", Target: "global", Effect: "destructive"})
	for input, name := range map[string]string{
		"fake touch quebec ": "quebec", "fake touch romeo ": "romeo", "fake touch sierra ": "sierra",
		"fake touch tango ": "tango", "fake touch uniform ": "uniform", "fake nuke": "fake nuke",
	} {
		d := connected(t, f)
		d.key(":")
		d.typeText(input)
		d.key("enter")
		if d.m.dialog.kind != dialogTypeName || d.m.dialog.name != name {
			t.Fatalf("%s: dialog %+v, want the typed name %q", input, d.m.dialog, name)
		}
	}
}

// A start that lost the connection does not claim nothing was started: it may have been.
func TestLostStartDoesNotClaimNothingStarted(t *testing.T) {
	f := newFake()
	f.startErr = unavailableErr()
	d := connected(t, f)
	d.key(":")
	d.typeText("fake check delta")
	d.key("enter")
	d.drain()
	text := dialogText(d.m)
	if d.m.dialog.kind != dialogError || strings.Contains(text, "nothing was started") {
		t.Fatalf("lost start dialog:\n%s\nwant an error that does not say nothing was started", text)
	}
	contains(t, text, "cannot reach the ktags service")
}

// A started run a refresh already listed is not listed twice.
func TestStartedRunIsListedOnce(t *testing.T) {
	d := connected(t, newFake())
	info := testRuns()[2]
	d.apply(startedMsg{action: info.Action, info: info})
	n := 0
	for _, r := range d.m.runs {
		if r.ID == info.ID {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("run %s listed %d times", info.ID, n)
	}
}

// A space is typed into the name, so "mi ke" is not "mike".
func TestTypeNameSpace(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	d.key(":")
	d.typeText("fake touch mike")
	d.key("enter")
	d.typeText("mi ke")
	d.key("enter")
	d.drain()
	if d.m.dialog.typed != "mi ke" || !d.m.dialog.wrong || len(f.snapshot().started) != 0 {
		t.Fatalf("typed %q wrong %v started %v", d.m.dialog.typed, d.m.dialog.wrong, f.snapshot().started)
	}
}

// Action IDs and argument names match exactly: another case or a prefix is not the action or
// the argument.
func TestPaletteNamesMatchExactly(t *testing.T) {
	d := connected(t, newFake())
	for _, input := range []string{"FAKE CHECK delta ", "Fake check delta ", "fake chec delta "} {
		if q := d.m.parse(input); q.action != nil {
			t.Fatalf("%q resolved to action %q, want no action", input, q.action.ID)
		}
	}
	for arg, want := range map[string]string{"--Reason": `has no argument "--Reason"`, "--reas": `has no argument "--reas"`} {
		q := d.m.parse("node drain delta srv-1 " + arg + " upgrade ")
		if q.action == nil || !strings.Contains(q.err, want) {
			t.Fatalf("%s: action %v err %q, want %q", arg, q.action, q.err, want)
		}
	}
}

// Palette edges: a given argument is not offered again; after an argument that takes a value
// the next word is that value; a customer already given ends the target rows; keys without
// text and a Tab without suggestions change nothing; Enter with nothing to run says so.
func TestPaletteEdges(t *testing.T) {
	d := connected(t, newFake())
	q := d.m.parse("fake quick --note x --")
	if !reflect.DeepEqual(rowTexts(q.rows), []string{"--count", "--verbose"}) {
		t.Fatalf("-- after --note offers %v", rowTexts(q.rows))
	}
	if q := d.m.parse("node drain --reason "); q.stage != stageArg || len(q.rows) != 0 {
		t.Fatalf("after --reason: stage %d rows %v, want its value", q.stage, rowTexts(q.rows))
	}
	if q := d.m.parse("fake touch mike "); len(q.rows) != 0 {
		t.Fatalf("after the customer: rows %v, want none", rowTexts(q.rows))
	}
	d.key(":")
	d.typeText("fak")
	d.key("down")
	d.apply(tea.KeyPressMsg{Code: tea.KeyF1})
	if d.m.pal.input != "fak" || d.m.pal.sel != 1 {
		t.Fatalf("F1 changed the palette: input %q sel %d", d.m.pal.input, d.m.pal.sel)
	}
	d.key("down", "down", "down", "down", "down", "down")
	if d.m.pal.sel != 3 {
		t.Fatalf("↓ past the last of 4 rows gives %d", d.m.pal.sel)
	}
	d.key("esc", ":")
	d.typeText("fake quick --nope ")
	contains(t, d.frame(), `action "fake quick" has no argument "--nope"`)
	d.key("esc", ":")
	d.typeText("zzz")
	d.key("tab")
	if d.m.pal.input != "zzz" {
		t.Fatalf("Tab without suggestions gives %q", d.m.pal.input)
	}

	f := newFake()
	d = connected(t, f)
	d.key(":")
	d.typeText("fake check del")
	d.key("enter")
	// Each started run is a command on its own goroutine; wait for the first before the second.
	d.until("fake check started", func(Model) bool { return len(f.snapshot().started) == 1 })
	d.key(":")
	d.typeText("node drain delta srv-1 --reason upgrade")
	d.key("enter", "y")
	d.drain()
	started := f.snapshot().started
	if len(started) != 2 || started[0].target.Customer != "delta" || started[1].target != (service.Target{Kind: "node", Customer: "delta", Node: "srv-1"}) {
		t.Fatalf("started %+v, want fake check on delta, then node drain on delta/srv-1", started)
	}

	f = newFake()
	f.actions = nil
	d = connected(t, f)
	d.key(":")
	contains(t, d.frame(), "no suggestions")
	d.key("enter")
	if d.m.overlay != overlayPalette || d.m.pal.err != "type an action, or Esc to close" {
		t.Fatalf("Enter with nothing to run: overlay %d err %q", d.m.overlay, d.m.pal.err)
	}
	contains(t, strings.Join(d.m.helpLines(), "\n"), "no actions are registered in the ktags service")
}

// An action ID typed in full comes first even when the fuzzy score prefers another.
func TestExactVerbFirst(t *testing.T) {
	f := newFake()
	f.actions = []service.ActionInfo{
		{ID: "ab", Title: "a title long enough to lose on length", Help: "x", Target: "global", Effect: "read-only"},
		{ID: "abc", Title: "ab", Help: "x", Target: "global", Effect: "read-only"},
	}
	d := connected(t, f)
	if rows := d.m.parse("ab").rows; len(rows) != 2 || rows[0].text != "ab" {
		t.Fatalf("ab ranks %v, want ab first", rowTexts(rows))
	}
}

// Esc on a finished run returns to the fleet without a detach line.
func TestLeaveFinishedRun(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("mike")
	d.key("tab", "right", "right", "enter")
	d.until("the replay", func(m Model) bool { return m.run.stop == nil })
	d.key("esc")
	if d.m.screen != screenFleet || d.m.status != "" {
		t.Fatalf("Esc on a finished run: screen %d status %q", d.m.screen, d.m.status)
	}
}

// A stream that ends while the run still runs is treated as lost and resumed.
func TestStreamEndingWhileRunningResumes(t *testing.T) {
	f := newFake()
	id := runFixture(f, 3, "succeeded")
	f.final[id] = service.RunInfo{ID: id, Action: "fake touch", Target: service.Target{Kind: "customer", Customer: "delta"}, Status: "running", LastEventID: 3}
	d := openDeltaRun(t, f)
	d.until("the lost stream", func(m Model) bool { return m.run.lost })
	d.apply(tickMsg{})
	d.drain()
	var afters []uint64
	for _, c := range f.snapshot().calls {
		afters = append(afters, c.after)
	}
	if !reflect.DeepEqual(afters, []uint64{0, 3}) {
		t.Fatalf("stream cursors %v, want 0 then 3", afters)
	}
}

// While reconnecting every value is grey: no ok colour is drawn as current.
func TestReconnectingGreysValues(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	okInk := "38;2;63;185;80"
	if !strings.Contains(d.m.render(), okInk) {
		t.Fatal("the connected frame has no ok colour; the test cannot see the difference")
	}
	f.set(func(f *fakeClient) { f.err = unavailableErr() })
	d.apply(tickMsg{})
	d.until("reconnecting", func(m Model) bool { return m.conn == connReconnecting })
	if strings.Contains(d.m.render(), okInk) {
		t.Fatal("a value is drawn in the ok colour while reconnecting")
	}
}

// A disabled action hint does nothing when clicked.
func TestDisabledHintIgnoresClicks(t *testing.T) {
	f := newFake()
	d := connected(t, f)
	f.set(func(f *fakeClient) { f.err = unavailableErr() })
	d.apply(tickMsg{})
	d.until("reconnecting", func(m Model) bool { return m.conn == connReconnecting })
	for i, h := range d.m.hints() {
		if h.action != nil {
			d.click(fmt.Sprintf("hint:%d", i), tea.MouseLeft)
			break
		}
	}
	if d.m.overlay != overlayNone {
		t.Fatalf("a disabled hint opened overlay %d", d.m.overlay)
	}
}

// With no customers the hint bar offers `customer add`, but only when the registry has it.
func TestCustomerAddHint(t *testing.T) {
	f := emptyFake()
	f.actions = append(f.actions, service.ActionInfo{ID: "customer add", Title: "Add a customer", Help: "x", Target: "global", Effect: "mutating"})
	d := connected(t, f)
	found := -1
	for i, h := range d.m.hints() {
		if h.action != nil && h.action.ID == "customer add" {
			found = i
		}
	}
	if found < 0 {
		t.Fatal("no customer add hint on an empty fleet")
	}
	d.click(fmt.Sprintf("hint:%d", found), tea.MouseLeft)
	if d.m.dialog.kind != dialogConfirm || d.m.dialog.start == nil || d.m.dialog.start.action != "customer add" {
		t.Fatalf("customer add hint: dialog %+v", d.m.dialog)
	}
	for _, h := range connected(t, emptyFake()).m.hints() {
		if h.action != nil {
			t.Fatalf("hint %s offered without a registry entry", h.label)
		}
	}
}

// The Runs tab scrolls so the cursor stays visible.
func TestRunsTabScrolls(t *testing.T) {
	f := newFake()
	ids := addRuns(f, "delta", 40)
	d := connected(t, f)
	d.selectCustomer("delta")
	d.key("tab", "right", "right", "end")
	if d.m.runCursor != 39 || !strings.Contains(d.frame(), "› ✓ succeeded") {
		t.Fatalf("End on the Runs tab: cursor %d", d.m.runCursor)
	}
	d.key("enter")
	if d.m.run.id != ids[0] {
		t.Fatalf("the last row opened %s, want the oldest run %s", d.m.run.id, ids[0])
	}
}

// A click outside a menu closes it and does nothing else; a hint click in the palette acts.
func TestMouseOutsideOverlay(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("mike")
	d.key("enter")
	d.click("tab:1", tea.MouseLeft)
	if d.m.overlay != overlayNone || d.m.tab != tabNodes {
		t.Fatalf("click outside the menu: overlay %d tab %d", d.m.overlay, d.m.tab)
	}
	d.key(":")
	d.typeText("fake ch")
	d.click("hint:1", tea.MouseLeft)
	if d.m.overlay != overlayPalette || d.m.pal.input != "fake check " {
		t.Fatalf("the tab hint in the palette: overlay %d input %q, want it completed and open", d.m.overlay, d.m.pal.input)
	}
	d.click("hint:3", tea.MouseLeft)
	if d.m.overlay != overlayNone {
		t.Fatal("the esc hint did not close the palette")
	}
	d.key(":")
	d.click("tab:1", tea.MouseLeft)
	if d.m.overlay != overlayNone || d.m.tab != tabNodes {
		t.Fatalf("click outside the palette: overlay %d tab %d", d.m.overlay, d.m.tab)
	}
	d.key("?")
	d.click("hint:0", tea.MouseLeft)
	if d.m.overlay != overlayNone {
		t.Fatal("a click did not close the help")
	}
	d.selectCustomer("zulu")
	d.key("enter", "enter")
	d.click("btn:close", tea.MouseLeft)
	if d.m.overlay != overlayNone {
		t.Fatal("the close button did not close the refusal")
	}
}

// Clicks on fleet rows act only on the fleet screen: a zone left from an earlier frame does not
// select a customer from the run view.
func TestClicksOnlyActOnTheFleet(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("mike")
	d.m.zones = newZones(t)
	_ = d.m.View()
	deadline := time.Now().Add(5 * time.Second)
	z := d.m.zones.Get("row:delta")
	for z.IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("no row zone")
		}
		<-time.After(time.Millisecond)
		z = d.m.zones.Get("row:delta")
	}
	d.key("tab", "right", "right", "enter")
	d.apply(tea.MouseClickMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseLeft})
	if d.m.screen != screenRun || d.m.selected != "mike" {
		t.Fatalf("a stale row click from the run view: screen %d selected %q", d.m.screen, d.m.selected)
	}
}

// The wheel moves the Runs tab cursor under the pointer and is ignored under an overlay.
func TestWheel(t *testing.T) {
	f := newFake()
	addRuns(f, "delta", 3)
	d := connected(t, f)
	d.selectCustomer("delta")
	d.key("tab", "right", "right", "shift+tab")
	d.wheel("panel:detail", tea.MouseWheelDown)
	if d.m.focus != focusDetail || d.m.runCursor != 1 {
		t.Fatalf("wheel over the detail: focus %d cursor %d", d.m.focus, d.m.runCursor)
	}
	d.key("enter")
	d.key("esc", ":")
	d.wheel("panel:list", tea.MouseWheelUp)
	if d.m.selected != "delta" {
		t.Fatalf("the wheel moved the list under the palette: %q", d.m.selected)
	}
}

// A right click on a run row opens it.
func TestRightClickOpensARun(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("mike")
	d.key("tab", "right", "right")
	d.click("run:"+testRuns()[0].ID, tea.MouseRight)
	if d.m.screen != screenRun || d.m.run.id != testRuns()[0].ID {
		t.Fatalf("right click on a run: screen %d", d.m.screen)
	}
}

// The badge reopens the run last viewed while it is active, else the newest active run; with
// no active run a click on a stale badge does nothing.
func TestBadgePrefersTheViewedRun(t *testing.T) {
	f := newFake()
	older := testRuns()[2]
	newer := service.RunInfo{ID: "20260911T115900Z-000000dd", Action: "fake check", Target: service.Target{Kind: "customer", Customer: "delta"}, Status: "running", StartedAt: testNow}
	f.runs = append(f.runs, newer)
	f.hold[newer.ID] = true
	d := connected(t, f)
	d.click("badge", tea.MouseLeft)
	if d.m.run.id != newer.ID {
		t.Fatalf("badge opened %s, want the newest active run %s", d.m.run.id, newer.ID)
	}
	d.key("esc")
	m, cmd := d.m.openRun(older)
	d.m = m
	d.run(cmd)
	d.key("esc")
	d.click("badge", tea.MouseLeft)
	if d.m.run.id != older.ID {
		t.Fatalf("badge opened %s, want the viewed run %s", d.m.run.id, older.ID)
	}
	d.key("esc")
	d.m.zones = newZones(t)
	_ = d.m.View()
	deadline := time.Now().Add(5 * time.Second)
	z := d.m.zones.Get("badge")
	for z.IsZero() {
		if time.Now().After(deadline) {
			t.Fatal("no badge zone")
		}
		<-time.After(time.Millisecond)
		z = d.m.zones.Get("badge")
	}
	d.m.runs = nil
	d.apply(tea.MouseClickMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseLeft})
	if d.m.screen != screenFleet {
		t.Fatal("a stale badge click without active runs opened a run")
	}
}

// The exit line is printed after the TUI ends, only when there is one.
func TestFinish(t *testing.T) {
	var out bytes.Buffer
	finish(Model{exitLine: "1 run continue in the ktags service"}, &out)
	finish(Model{}, &out)
	finish(nil, &out)
	if out.String() != "ktags: 1 run continue in the ktags service\n" {
		t.Fatalf("exit output %q", out.String())
	}
}

// Ages, state words and symbols: never ok for an unknown state, never an age for the future.
func TestFormat(t *testing.T) {
	for d, want := range map[time.Duration]string{-time.Second: "0s", 45 * time.Second: "45s", 5 * time.Minute: "5m", 3 * time.Hour: "3h", 50 * time.Hour: "2d"} {
		if got := span(d); got != want {
			t.Errorf("span(%s) = %q, want %q", d, got, want)
		}
	}
	if ago(testNow, time.Time{}) != "never" || ago(testNow, testNow.Add(time.Minute)) != "in the future" || agoPtr(testNow, nil) != "never" {
		t.Error("ago of zero, future or nil times")
	}
	if fit("abc", 0) != "" || fit("abc", -2) != "" || fit("abcdef", 4) != "abc…" || fit("ab", 4) != "ab  " {
		t.Error("fit truncates to … and pads to the width")
	}
	for _, c := range []struct {
		e    service.FleetEntry
		want string
		to   tone
	}{
		{entry("x", "", "bogus"), "? bogus", toneFail},
		{entry("x", "", "stale"), "○ stale", toneMuted},
	} {
		if got, to := stateText(c.e, testNow); got != c.want || to != c.to {
			t.Errorf("stateText(%s) = %q %d, want %q %d", c.e.State, got, to, c.want, c.to)
		}
	}
	for _, c := range []struct {
		k    service.CheckInfo
		want string
	}{
		{service.CheckInfo{Severity: "warning", Status: "failed"}, "⚠ warn"},
		{service.CheckInfo{Severity: "warning", Status: "timeout"}, "⚠ timeout"},
		{service.CheckInfo{Severity: "critical", Status: "timeout"}, "✗ timeout"},
	} {
		if got, _ := checkText(c.k); got != c.want {
			t.Errorf("checkText(%+v) = %q, want %q", c.k, got, c.want)
		}
	}
	if got, to := runText("paused"); got != "? paused" || to != toneFail {
		t.Errorf("runText of an unknown status = %q", got)
	}
}
