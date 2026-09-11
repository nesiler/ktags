package tui

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/nesiler/ktags/internal/service"
)

// View renders the frame and scans it once at the root for mouse zones.
func (m Model) View() tea.View {
	v := tea.NewView(m.zones.Scan(m.render()))
	v.AltScreen = true
	if m.mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// render lays out the frame: top bar, optional banner, body, hint bar, command bar.
func (m Model) render() string {
	if m.quitting || m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		return fit(fmt.Sprintf("terminal is %d×%d; ktags needs at least %d×%d — resize the window", m.width, m.height, minWidth, minHeight), m.width)
	}
	lines := []string{m.topBar()}
	if b := m.banner(); b != "" {
		lines = append(lines, b)
	}
	lines = append(lines, m.body()...)
	lines = append(lines, m.hintBar(), m.commandBar())
	frame := strings.Join(lines, "\n")
	switch m.overlay {
	case overlayPalette:
		box := m.paletteBox()
		frame = place(frame, box, 0, m.height-1-strings.Count(box, "\n")-2)
	case overlayMenu:
		frame = m.placeMenu(frame)
	case overlayDialog:
		frame = placeCentre(frame, m.dialogBox(), m.width, m.height)
	case overlayHelp:
		frame = placeCentre(frame, m.helpBox(), m.width, m.height)
	}
	return frame
}

func (m Model) connWord() string {
	switch m.conn {
	case connConnected:
		return "connected"
	case connReconnecting:
		return "reconnecting"
	case connUnavailable:
		return "unavailable"
	case connMismatch:
		return "protocol mismatch"
	default:
		return "connecting"
	}
}

func (m Model) topBar() string {
	var conn string
	switch m.conn {
	case connConnected:
		conn = m.th.paint(toneOK, "● service connected")
	case connReconnecting:
		conn = m.th.paint(toneWarn, "◌ service reconnecting")
	case connUnavailable:
		conn = m.th.paint(toneFail, "✗ service unavailable")
	case connMismatch:
		conn = m.th.paint(toneFail, "✗ protocol mismatch")
	default:
		conn = m.th.paint(toneMuted, "○ connecting…")
	}
	parts := []string{bold("ktags " + orDash(m.version)), conn}
	if c := m.current(); c != nil {
		parts = append(parts, c.ID+" ("+m.th.paint(envTone(c.Environment), orDash(c.Environment))+")")
	}
	if active := m.activeRuns(); len(active) > 0 {
		parts = append(parts, m.zones.Mark("badge", m.th.paint(toneAccent, "▶ "+pluralRuns(len(active))+" active")))
	}
	mouse := "mouse off"
	if m.mouse {
		mouse = "mouse on"
	}
	parts = append(parts, m.th.paint(toneMuted, mouse))
	return fit(" "+strings.Join(parts, " │ "), m.width)
}

// banner is the reconnect line: the data stays visible, greyed, with its age.
func (m Model) banner() string {
	if m.conn != connReconnecting {
		return ""
	}
	return fit(m.th.paint(toneWarn, " ◌ service connection lost — reconnecting · data from "+ago(m.now(), m.dataAt)+" · actions disabled"), m.width)
}

func (m Model) bodyHeight() int {
	h := m.height - 3
	if m.banner() != "" {
		h--
	}
	return h
}

// panelRows is the number of content rows inside a body panel.
func (m Model) panelRows() int {
	return max(0, m.bodyHeight()-2)
}

func (m Model) listWidth() int {
	return max(40, min(52, m.width*45/100))
}

func (m Model) body() []string {
	h := m.bodyHeight()
	switch {
	case m.conn == connUnavailable || m.conn == connMismatch:
		return strings.Split(m.offlineBox(h), "\n")
	case m.screen == screenRun && m.run != nil:
		return strings.Split(m.runBox(h), "\n")
	}
	th := m.th
	if m.conn == connReconnecting {
		th = th.greyed()
	}
	lw := m.listWidth()
	left := strings.Split(m.listBox(th, lw, h), "\n")
	right := strings.Split(m.detailBox(th, m.width-lw, h), "\n")
	out := make([]string, h)
	for i := range out {
		out[i] = left[i] + right[i]
	}
	return out
}

// box draws a panel of exactly w×h cells. A focused panel has a heavy border, so focus shows
// without colour. id marks the whole panel for wheel events.
func (m Model) box(id, title string, lines []string, w, h int, focused bool) string {
	tl, tr, bl, br, hz, vt := "┌", "┐", "└", "┘", "─", "│"
	if focused {
		tl, tr, bl, br, hz, vt = "┏", "┓", "┗", "┛", "━", "┃"
	}
	inner := w - 2
	head := hz + " " + title + " "
	if focused {
		head = hz + " " + bold(title) + " "
	}
	head = ansi.Truncate(head, inner, "…")
	out := []string{tl + head + strings.Repeat(hz, max(0, inner-ansi.StringWidth(head))) + tr}
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		out = append(out, vt+fit(line, inner)+vt)
	}
	out = append(out, bl+strings.Repeat(hz, inner)+br)
	// Every line has the same width, so the zone's corners span exactly the panel.
	return m.zones.Mark(id, strings.Join(out, "\n"))
}

func (m Model) listBox(th theme, w, h int) string {
	inner := w - 2
	rows := h - 2
	var lines []string
	title := "Customers"
	switch {
	case m.conn == connConnecting && !m.haveData:
		lines = []string{th.paint(toneMuted, "connecting to the ktags service…")}
	case len(m.customers()) == 0:
		lines = []string{"no customers in the inventory yet", th.paint(toneMuted, "next: ktags customer list")}
	default:
		cs := m.customers()
		title = fmt.Sprintf("Customers (%d)", len(cs))
		if m.conn == connReconnecting {
			title += " · data from " + ago(m.now(), m.dataAt)
		}
		// marker, state, env and activity take 27 cells with their separators.
		idw := inner - 27
		for i := m.listTop; i < len(cs) && i < m.listTop+rows; i++ {
			c := cs[i]
			state, to := stateText(c, m.fleet.Now)
			activity := " "
			if len(c.ActiveRuns) > 0 || c.Checking {
				activity = spinner[m.frame%len(spinner)]
			}
			marker := " "
			if c.ID == m.selected {
				marker = "›"
			}
			row := marker + " " + th.paint(to, fit(state, 13)) + " " + fit(c.ID, idw) + " " + th.paint(envTone(c.Environment), fit(orDash(c.Environment), 7)) + " " + th.paint(toneAccent, activity) + " "
			row = fit(row, inner)
			if c.ID == m.selected && m.focus == focusList {
				row = reverse(ansi.Strip(row))
			}
			lines = append(lines, m.zones.Mark("row:"+c.ID, row))
		}
	}
	return m.box("panel:list", title, lines, w, h, m.focus == focusList)
}

func (m Model) detailBox(th theme, w, h int) string {
	inner := w - 2
	c := m.current()
	if c == nil {
		return m.box("panel:detail", "Detail", nil, w, h, m.focus == focusDetail)
	}
	var tabs []string
	for i, name := range tabNames {
		label := " " + name + " "
		if tab(i) == m.tab {
			label = "[" + name + "]"
			if m.focus == focusDetail {
				label = bold(label)
			}
		}
		tabs = append(tabs, m.zones.Mark("tab:"+strconv.Itoa(i), label))
	}
	lines := []string{strings.Join(tabs, " "), ""}
	switch m.tab {
	case tabNodes:
		lines = append(lines, m.nodeLines(th, c, inner)...)
	case tabHealth:
		lines = append(lines, m.healthLines(th, c, inner)...)
	case tabRuns:
		lines = append(lines, m.runLines(th, c, inner)...)
	}
	title := c.ID + " (" + th.paint(envTone(c.Environment), orDash(c.Environment)) + ")"
	return m.box("panel:detail", title, lines, w, h, m.focus == focusDetail)
}

func (m Model) nodeLines(th theme, c *service.FleetEntry, inner int) []string {
	if c.Problem != "" {
		return problemLines(th, c, inner)
	}
	return []string{
		"cluster   " + orDash(c.Cluster),
		fmt.Sprintf("nodes     %d in the inventory", m.nodes[c.ID]),
		"",
		th.paint(toneMuted, "Per-node role, address, state and last contact"),
		th.paint(toneMuted, "are not reported by the ktags service yet."),
		th.paint(toneMuted, "next: ktags customer list"),
	}
}

func (m Model) healthLines(th theme, c *service.FleetEntry, inner int) []string {
	now := m.fleet.Now
	if c.Problem != "" {
		return problemLines(th, c, inner)
	}
	state, to := stateText(*c, now)
	lines := []string{"health      " + th.paint(to, state)}
	if c.Runbook != "" {
		lines = append(lines, "runbook     "+th.paint(to, c.Runbook))
	}
	if c.MeasuredAt != nil {
		measured := "measured    " + agoPtr(now, c.MeasuredAt)
		if c.Trigger != "" {
			measured += " (" + c.Trigger + ")"
		}
		lines = append(lines, measured)
	}
	if c.IntervalSeconds > 0 {
		lines = append(lines, fmt.Sprintf("interval    every %s", span(secs(c.IntervalSeconds))))
	}
	if c.Stale && c.MeasuredAt != nil {
		lines = append(lines, th.paint(toneMuted, "stale       older than its interval; not current"))
	}
	if c.Missed > 0 {
		lines = append(lines, th.paint(toneWarn, fmt.Sprintf("missed      %d scheduled check(s) could not run", c.Missed)))
	}
	if c.Checking {
		lines = append(lines, th.paint(toneAccent, "checking    a health check is running"))
	}
	conn := "connection  " + c.Connection.State
	if c.Connection.MeasuredAt != nil {
		conn += " · " + agoPtr(now, c.Connection.MeasuredAt)
	}
	lines = append(lines, conn)
	if c.Connection.Detail != "" {
		lines = append(lines, labelled("            ", c.Connection.Detail, inner)...)
	}
	if len(c.Versions) > 0 {
		var vs []string
		for _, k := range slices.Sorted(maps.Keys(c.Versions)) {
			vs = append(vs, k+" "+c.Versions[k])
		}
		lines = append(lines, "versions    "+strings.Join(vs, ", "))
	}
	lines = append(lines, "")
	if len(c.Checks) == 0 {
		return append(lines, th.paint(toneMuted, "no check results yet"))
	}
	lines = append(lines, th.paint(toneMuted, fit("STATUS", 16)+" "+fit("CHECK", 14)+" "+fit("AGE", 8)+" DETAIL"))
	for _, k := range c.Checks {
		text, to := checkText(k)
		// A stale result says so in words; grey alone would carry the state by colour.
		if c.Stale {
			text, to = text+" · stale", toneMuted
		}
		row := th.paint(to, fit(text, 16)) + " " + fit(k.Check, 14) + " " + fit(ago(now, k.MeasuredAt), 8) + " "
		if k.Runbook != "" {
			row += k.Runbook + " "
		}
		row += k.Detail
		lines = append(lines, fit(row, inner))
	}
	return lines
}

func (m Model) runLines(th theme, c *service.FleetEntry, inner int) []string {
	runs := m.customerRuns(c.ID)
	if len(runs) == 0 {
		return []string{th.paint(toneMuted, "no runs for this customer yet"), th.paint(toneMuted, "next: open the palette with :")}
	}
	lines := []string{th.paint(toneMuted, "  "+fit("RESULT", 12)+" "+fit("ACTION", 16)+" "+fit("STARTED", 9)+" DURATION")}
	rows := m.panelRows() - 3
	top := max(0, m.runCursor-rows+1)
	for i := top; i < len(runs) && i < top+rows; i++ {
		r := runs[i]
		text, to := runText(r.Status)
		marker := "  "
		if i == m.runCursor && m.focus == focusDetail {
			marker = "› "
		}
		row := fit(marker+th.paint(to, fit(text, 12))+" "+fit(r.Action, 16)+" "+fit(ago(m.now(), r.StartedAt), 9)+" "+span(runDuration(r, m.now())), inner)
		if i == m.runCursor && m.focus == focusDetail {
			row = reverse(ansi.Strip(row))
		}
		lines = append(lines, m.zones.Mark("run:"+r.ID, row))
	}
	return lines
}

// offlineBox is the service unavailable and protocol mismatch screen: what failed, the cause,
// the next command. A mismatch renders no data.
func (m Model) offlineBox(h int) string {
	p := m.connErr
	title := "ktags service unavailable"
	next := p.hint
	if p.code == service.CodeUnavailable {
		next = "ktags service start"
	}
	if m.conn == connMismatch {
		title = "protocol mismatch"
	}
	inner := m.width - 2
	lines := []string{m.th.paint(toneFail, "✗ "+title), ""}
	lines = append(lines, labelled("cause  ", orDash(p.message), inner)...)
	lines = append(lines, labelled("next   ", orDash(next), inner)...)
	lines = append(lines, "", "Enter retries · q quits")
	return m.box("", title, lines, m.width, h, true)
}

func (m Model) runBox(h int) string {
	r := m.run
	inner := m.width - 2
	text, to := runText(r.info.Status)
	title := fmt.Sprintf("Run %s · %s · %s", r.id, r.info.Action, targetString(r.info.Target))
	lines := []string{m.th.paint(to, text) + " · started " + ago(m.now(), r.info.StartedAt) + " · " + span(runDuration(r.info, m.now()))}
	if r.notice != "" {
		for _, l := range wrap(r.notice, inner) {
			lines = append(lines, m.th.paint(toneWarn, l))
		}
	}
	if r.dropped > 0 {
		lines = append(lines, m.th.paint(toneMuted, fmt.Sprintf("… %d earlier event(s) are in the run record: ktags run watch %s", r.dropped, r.id)))
	}
	var tail []string
	if r.info.Result != nil {
		rt, rto := runText(r.info.Result.Status)
		tail = []string{
			"",
			"result    " + m.th.paint(rto, rt) + "  " + r.info.Result.Summary,
			"duration  " + span(runDuration(r.info, m.now())),
			"finished  " + r.info.Result.FinishedAt.UTC().Format("2006-01-02 15:04:05 UTC"),
			"next      ktags run status " + r.id,
		}
	} else if r.info.Problem != "" {
		tail = []string{"", m.th.paint(toneFail, "problem   "+r.info.Problem), "next      ktags run status " + r.id}
	}
	logRows := max(1, h-2-len(lines)-len(tail))
	if r.offset > 0 {
		logRows--
	}
	end := len(r.lines) - r.offset
	for i := max(0, end-logRows); i < end; i++ {
		e := r.lines[i]
		line := fmt.Sprintf("%4d ", e.ID)
		if e.Step != "" {
			line += m.th.paint(toneAccent, e.Step) + ": "
		}
		lines = append(lines, fit(line+e.Message, inner))
	}
	if len(r.lines) == 0 {
		lines = append(lines, m.th.paint(toneMuted, "waiting for events…"))
	}
	if r.offset > 0 {
		lines = append(lines, m.th.paint(toneWarn, "paused, End to follow"))
	}
	for len(lines) < h-2-len(tail) {
		lines = append(lines, "")
	}
	lines = append(lines, tail...)
	return m.box("panel:log", title, lines, m.width, h, true)
}

// logRows is the log height the run view scrolls by.
func (m Model) logRows() int {
	return max(1, m.bodyHeight()-8)
}

// hint is one entry of the hint bar: a key, or an action of the selected item.
type hint struct {
	key, label string
	press      string
	action     *service.ActionInfo
	off        bool
}

func (m Model) hints() []hint {
	switch {
	case m.overlay == overlayPalette:
		return []hint{{key: "enter", label: "run", press: "enter"}, {key: "tab", label: "complete", press: "tab"}, {key: "↑↓", label: "select"}, {key: "esc", label: "close", press: "esc"}}
	case m.overlay == overlayMenu:
		return []hint{{key: "enter", label: "run", press: "enter"}, {key: "↑↓", label: "select"}, {key: "esc", label: "close", press: "esc"}}
	case m.overlay == overlayDialog:
		return nil
	case m.overlay == overlayHelp:
		return []hint{{key: "any key", label: "close", press: "esc"}}
	case m.conn == connUnavailable || m.conn == connMismatch:
		return []hint{{key: "enter", label: "retry", press: "enter"}, {key: "?", label: "help", press: "?"}, {key: "m", label: "mouse", press: "m"}, {key: "q", label: "quit", press: "q"}}
	case m.screen == screenRun && m.run != nil:
		hs := []hint{{key: "esc", label: "back", press: "esc"}}
		if m.run.info.Status == "running" {
			hs = append(hs, hint{key: "ctrl+c", label: "cancel run", press: "ctrl+c", off: m.conn != connConnected})
		}
		return append(hs, hint{key: "↑↓", label: "scroll"}, hint{key: "end", label: "follow", press: "end"}, hint{key: ":", label: "palette", press: ":"}, hint{key: "?", label: "help", press: "?"}, hint{key: "m", label: "mouse", press: "m"})
	}
	enter := "menu"
	if m.focus == focusDetail && m.tab == tabRuns {
		enter = "open run"
	}
	hs := []hint{{key: ":", label: "palette", press: ":"}, {key: "?", label: "help", press: "?"}, {key: "tab", label: "focus", press: "tab"}}
	if m.focus == focusDetail {
		hs = append(hs, hint{key: "←→", label: "tabs"})
	}
	hs = append(hs, hint{key: "enter", label: enter, press: "enter"}, hint{key: "m", label: "mouse", press: "m"}, hint{key: "q", label: "quit", press: "q"})
	off := m.conn != connConnected
	if len(m.customers()) == 0 {
		if i := slices.IndexFunc(m.actions, func(a service.ActionInfo) bool { return a.ID == "customer add" }); i >= 0 {
			hs = append(hs, hint{key: "▸", label: m.actions[i].ID, action: &m.actions[i], off: off})
		}
		return hs
	}
	for _, a := range m.customerActions() {
		hs = append(hs, hint{key: "▸", label: a.ID, action: &a, off: off})
	}
	return hs
}

func (m Model) hintBar() string {
	var parts []string
	for i, h := range m.hints() {
		text := h.key + " " + h.label
		if h.off {
			text = m.th.paint(toneMuted, text)
		} else {
			text = m.th.paint(toneAccent, h.key) + " " + h.label
		}
		parts = append(parts, m.zones.Mark("hint:"+strconv.Itoa(i), text))
	}
	return fit(" "+strings.Join(parts, "  "), m.width)
}

func (m Model) commandBar() string {
	if m.overlay != overlayPalette {
		return fit(" "+m.status, m.width)
	}
	line := " :" + m.pal.input + "▏"
	q := m.parse(m.pal.input)
	if q.action != nil && len(q.targets) == 0 && q.action.Target != "global" {
		if c := m.current(); c != nil {
			line += m.th.paint(toneMuted, " → "+c.ID+" ("+orDash(c.Environment)+")")
		}
	}
	return fit(line, m.width)
}

func (m Model) paletteBox() string {
	q := m.parse(m.pal.input)
	w := min(m.width, 90)
	inner := w - 2
	var lines []string
	switch {
	case m.pal.err != "":
		lines = append(lines, m.th.paint(toneFail, m.pal.err))
	case q.err != "":
		lines = append(lines, m.th.paint(toneFail, q.err))
	case q.notice != "":
		lines = append(lines, m.th.paint(toneFail, q.notice))
	}
	if q.action != nil {
		lines = append(lines, m.th.paint(toneMuted, "usage: :"+usage(*q.action)+"   cli: ktags action run "+usage(*q.action)))
	}
	maxRows := 10
	group := ""
	start := max(0, m.pal.sel-maxRows+1)
	for i := start; i < len(q.rows) && i < start+maxRows; i++ {
		r := q.rows[i]
		if q.stage == stageVerb && m.pal.input == "" && r.group != group {
			group = r.group
			lines = append(lines, bold(group+" actions"))
		}
		marker := "  "
		if i == m.pal.sel {
			marker = "› "
		}
		row := fit(marker+fit(r.text, 24)+" "+m.th.paint(toneMuted, r.help), inner)
		if i == m.pal.sel {
			row = reverse(ansi.Strip(row))
		}
		lines = append(lines, m.zones.Mark("pal:"+strconv.Itoa(i), row))
	}
	if len(lines) == 0 {
		lines = append(lines, m.th.paint(toneMuted, "no suggestions"))
	}
	return m.box("", "Palette", lines, w, len(lines)+2, true)
}

func (m Model) placeMenu(frame string) string {
	var lines []string
	if len(m.menu.items) == 0 {
		lines = []string{m.th.paint(toneMuted, "no actions for a customer; open the palette with :")}
	}
	for i, a := range m.menu.items {
		marker := "  "
		if i == m.menu.sel {
			marker = "› "
		}
		row := fit(marker+fit(a.ID, 20)+" "+m.th.paint(toneMuted, a.Title), 52)
		if i == m.menu.sel {
			row = reverse(ansi.Strip(row))
		}
		lines = append(lines, m.zones.Mark("menu:"+strconv.Itoa(i), row))
	}
	box := m.box("", m.menu.customer, lines, 54, len(lines)+2, true)
	y := 2 + max(0, m.selectedIndex()-m.listTop) + 1
	y = min(y, m.height-2-len(lines)-2)
	return place(frame, box, 2, y)
}

// wrap breaks s into lines of at most w cells at spaces, so a dialog or the help never cuts
// off a cause or a next command. Only a word wider than a whole line is cut. Continuation
// lines of an indented line keep its indent plus two cells.
func wrap(s string, w int) []string {
	if w <= 0 || ansi.StringWidth(s) <= w {
		return []string{s}
	}
	body := strings.TrimLeft(s, " ")
	indent := s[:len(s)-len(body)]
	hang := indent
	if indent != "" {
		hang += "  "
	}
	if len(hang) >= w {
		indent, hang = "", ""
	}
	var lines []string
	cur, start := indent, true
	for _, word := range strings.Fields(body) {
		room := w - ansi.StringWidth(cur)
		if !start {
			room--
		}
		if ansi.StringWidth(word) > room && !start {
			lines = append(lines, cur)
			cur, start = hang, true
			room = w - ansi.StringWidth(cur)
		}
		for ansi.StringWidth(word) > room {
			lines = append(lines, cur+ansi.Cut(word, 0, room))
			word = ansi.Cut(word, room, ansi.StringWidth(word))
			cur, start = hang, true
			room = w - ansi.StringWidth(cur)
		}
		if word == "" {
			continue
		}
		if !start {
			cur += " "
		}
		cur, start = cur+word, false
	}
	if !start {
		lines = append(lines, cur)
	}
	return lines
}

// labelled wraps text after label; continuation lines are indented under the text.
func labelled(label, text string, w int) []string {
	indent := strings.Repeat(" ", ansi.StringWidth(label))
	lines := wrap(text, max(1, w-len(indent)))
	for i := range lines {
		if i == 0 {
			lines[i] = label + lines[i]
		} else {
			lines[i] = indent + lines[i]
		}
	}
	return lines
}

// problemLines show a refused customer record in full with the next step.
func problemLines(th theme, c *service.FleetEntry, inner int) []string {
	var lines []string
	for _, l := range labelled("! record refused: ", c.Problem, inner) {
		lines = append(lines, th.paint(toneFail, l))
	}
	return append(lines, th.paint(toneMuted, "next: ktags customer list"))
}

func wrapAll(lines []string, w int) []string {
	var out []string
	for _, l := range lines {
		out = append(out, wrap(l, w)...)
	}
	return out
}

func (m Model) dialogBox() string {
	d := m.dialog
	w := min(m.width-4, 72)
	lines := wrapAll(d.lines, w-2)
	lines = append(lines, "")
	switch d.kind {
	case dialogError:
		if d.hint != "" {
			lines = append(lines, wrap("next: "+d.hint, w-2)...)
		}
		lines = append(lines, "", m.zones.Mark("btn:close", m.th.paint(toneAccent, "enter")+" close"))
	case dialogConfirm:
		lines = append(lines, "Continue? [y/N]", "", m.zones.Mark("btn:yes", m.th.paint(toneAccent, "y")+" yes")+"   "+m.zones.Mark("btn:no", m.th.paint(toneAccent, "n")+" no"))
	case dialogTypeName:
		lines = append(lines, fmt.Sprintf("Type %q to continue; --yes never applies here.", d.name), "> "+d.typed+"▏")
		if d.wrong {
			lines = append(lines, m.th.paint(toneFail, "the typed name does not match"))
		}
		lines = append(lines, "", m.th.paint(toneAccent, "enter")+" confirm   "+m.zones.Mark("btn:no", m.th.paint(toneAccent, "esc")+" cancel"))
	}
	return m.box("", d.title, lines, w, len(lines)+2, true)
}

// helpBox shows helpLines wrapped to the box, cut to the screen height.
func (m Model) helpBox() string {
	w := min(m.width-4, 96)
	lines := wrapAll(m.helpLines(), w-2)
	lines = lines[:min(len(lines), m.height-6)]
	return m.box("", "Help", lines, w, len(lines)+2, true)
}

// helpLines are generated from the key table and the action registry, grouped by target
// kind; each action is followed by its CLI form.
func (m Model) helpLines() []string {
	lines := []string{
		bold("keys"),
		":            palette                ?          help",
		"tab          move focus             ←→         switch tabs (detail)",
		"↑↓ pgup/dn   move                   home/end   first/last",
		"enter/space  menu or open run       esc        close / back to the fleet",
		"m            toggle mouse           q          quit; runs continue in the service",
		"ctrl+c       run view: cancel run   elsewhere  same as q",
		"",
	}
	for _, g := range groupOrder {
		var group []string
		for _, a := range m.actions {
			if a.Target == g {
				group = append(group, "  "+fit(a.ID, 22)+" "+a.Title, "      ktags action run "+usage(a))
			}
		}
		if len(group) > 0 {
			lines = append(lines, bold(g+" actions"))
			lines = append(lines, group...)
		}
	}
	if len(m.actions) == 0 {
		lines = append(lines, m.th.paint(toneMuted, "no actions are registered in the ktags service"))
	}
	return lines
}

// place draws box over frame with its top-left cell at x, y.
func place(frame, box string, x, y int) string {
	lines := strings.Split(frame, "\n")
	for i, b := range strings.Split(box, "\n") {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		bw := ansi.StringWidth(b)
		left := fit(ansi.Truncate(lines[row], x, ""), x)
		right := ansi.TruncateLeft(lines[row], x+bw, "")
		lines[row] = left + "\x1b[m" + b + "\x1b[m" + right
	}
	return strings.Join(lines, "\n")
}

func placeCentre(frame, box string, w, h int) string {
	bw := ansi.StringWidth(strings.SplitN(box, "\n", 2)[0])
	bh := strings.Count(box, "\n") + 1
	return place(frame, box, max(0, (w-bw)/2), max(0, (h-bh)/2))
}
