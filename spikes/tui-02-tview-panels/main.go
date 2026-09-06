// Command ktags-spike-tview-panels is a throwaway TUI mock-up for ktags.
//
// Variant: rivo/tview with a lazygit-style panel model. Every panel is always
// visible, exactly one panel has focus (highlighted border + numbered title),
// Tab / Shift-Tab / number keys / mouse clicks move focus, and the bottom bar
// shows the keybindings of the focused panel's context (side panel, main
// panel, popup). Popups are layered on top with tview.Pages.
//
// There is no "run screen": a run streams into the main log panel while the
// side panels stay live, and the play/task tree is a side panel of its own.
package main

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// contextKind is the lazygit idea of "which kind of thing has focus"; it
// decides which keybinding set the bottom bar advertises.
type contextKind int

const (
	ctxSide  contextKind = iota // narrow list panels on the left
	ctxMain                     // wide content panels on the right
	ctxPopup                    // layered dialogs
)

func (c contextKind) String() string {
	switch c {
	case ctxSide:
		return "side"
	case ctxMain:
		return "main"
	default:
		return "popup"
	}
}

type keyHint struct {
	key  string
	desc string
}

// panel is one always-visible region of the dashboard.
type panel struct {
	id    string
	title string
	kind  contextKind
	prim  tview.Primitive
	box   *tview.Box
	keys  []keyHint
}

// theme keeps every colour in one place; backgrounds stay ColorDefault so the
// app inherits the terminal palette (works on dark and light themes).
var (
	colBorder      = tcell.ColorGray
	colBorderFocus = tcell.ColorAqua
	colTitle       = tcell.ColorSilver
	colTitleFocus  = tcell.ColorAqua
	colAccent      = tcell.ColorAqua
	colOK          = tcell.ColorGreen
	colWarn        = tcell.ColorYellow
	colFail        = tcell.ColorRed
	colMuted       = tcell.ColorGray
	colHeader      = tcell.ColorTeal
)

type ui struct {
	app   *tview.Application
	pages *tview.Pages

	// bars
	topBar    *tview.TextView
	keyBar    *tview.TextView
	statusBar *tview.TextView
	cmdBar    *tview.InputField
	bottom    *tview.Pages

	// panels
	customers *tview.List
	tree      *tview.TreeView
	nodes     *tview.Table
	health    *tview.Table
	logView   *tview.TextView
	right     *tview.Flex

	panels []*panel
	focus  int

	// state
	data    []Customer
	sel     int
	mouseOn bool
	cmdOpen bool
	popups  []string
	toast   string
	toastID int

	hints []topHint

	run    *runState
	logBuf *viewBuffer
	af     *addFormData
}

// topHint is a clickable hint in the top bar.
type topHint struct {
	label  string
	x0, x1 int // visible column range inside the top bar
	action func()
}

func main() {
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault
	tview.Styles.ContrastBackgroundColor = tcell.ColorDefault
	tview.Styles.MoreContrastBackgroundColor = tcell.ColorDefault
	tview.Styles.PrimaryTextColor = tcell.ColorDefault
	tview.Styles.BorderColor = colBorder
	tview.Styles.TitleColor = colTitle
	tview.Styles.GraphicsColor = colBorder

	u := newUI()
	if err := u.app.Run(); err != nil {
		panic(err)
	}
}

func newUI() *ui {
	u := &ui{
		app:     tview.NewApplication(),
		pages:   tview.NewPages(),
		data:    fakeCustomers(),
		mouseOn: true,
		logBuf:  newViewBuffer(logBufferLines),
		run:     newRunState(),
	}

	u.buildPanels()
	u.buildBars()

	left := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.customers, 0, 3, true).
		AddItem(u.tree, 0, 4, false)

	u.right = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.nodes, 0, 2, false).
		AddItem(u.health, 0, 3, false).
		AddItem(u.logView, 0, 4, false)

	body := tview.NewFlex().
		AddItem(left, 34, 0, true).
		AddItem(u.right, 0, 1, false)

	dash := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(u.topBar, 1, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(u.keyBar, 1, 0, false).
		AddItem(u.bottom, 1, 0, false)

	u.pages.AddPage("dashboard", dash, true, true)

	u.app.SetRoot(u.pages, true).EnableMouse(true)
	u.app.SetInputCapture(u.globalKeys)
	u.app.SetMouseCapture(u.globalMouse)

	u.renderCustomers()
	u.selectCustomer(0)
	u.focusPanel(0)
	u.startTicker()
	return u
}

// buildPanels creates the five always-visible panels.
func (u *ui) buildPanels() {
	u.customers = tview.NewList().ShowSecondaryText(true).SetHighlightFullLine(true)
	u.customers.SetSelectedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(colAccent))
	u.customers.SetChangedFunc(func(i int, _, _ string, _ rune) { u.selectCustomer(i) })
	u.customers.SetBorder(true)

	u.tree = tview.NewTreeView()
	u.tree.SetBorder(true)
	u.tree.SetGraphicsColor(colMuted)
	u.tree.SetSelectedFunc(func(n *tview.TreeNode) {
		if len(n.GetChildren()) > 0 {
			n.SetExpanded(!n.IsExpanded())
		}
	})
	u.tree.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'c':
			if r := u.tree.GetRoot(); r != nil {
				for _, ch := range r.GetChildren() {
					ch.Collapse()
				}
			}
			return nil
		case 'e':
			if r := u.tree.GetRoot(); r != nil {
				r.ExpandAll()
			}
			return nil
		}
		return ev
	})

	u.nodes = newTable()
	u.nodes.SetSelectionChangedFunc(func(row, _ int) {
		c := u.current()
		if row > 0 && row-1 < len(c.Nodes) {
			u.setStatus(fmt.Sprintf("node %s  %s  %s", c.Nodes[row-1].Name, c.Nodes[row-1].NodeIP, c.Nodes[row-1].Status))
		}
	})

	u.health = newTable()

	u.logView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(false)
	u.logView.SetBorder(true)
	u.logView.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Rune() == 'f' {
			u.run.toggleFollow()
			if u.run.following() {
				u.logView.ScrollToEnd()
			}
			u.setStatus(fmt.Sprintf("log follow %s", onOff(u.run.following())))
			return nil
		}
		return ev
	})

	u.panels = []*panel{
		{id: "customers", title: "Customers", kind: ctxSide, prim: u.customers, box: u.customers.Box, keys: []keyHint{
			{"↑/↓", "select"}, {"enter", "nodes"}, {"i", "install"}, {"h", "health"},
			{"b", "backup"}, {"a", "add"}, {"s", "secrets"},
		}},
		{id: "tasks", title: "Plays / Tasks", kind: ctxSide, prim: u.tree, box: u.tree.Box, keys: []keyHint{
			{"↑/↓", "move"}, {"enter", "fold"}, {"c", "collapse all"}, {"e", "expand all"},
		}},
		{id: "nodes", title: "Nodes", kind: ctxMain, prim: u.nodes, box: u.nodes.Box, keys: []keyHint{
			{"↑/↓", "row"}, {"wheel", "scroll"}, {"h", "health run"},
		}},
		{id: "health", title: "Health", kind: ctxMain, prim: u.health, box: u.health.Box, keys: []keyHint{
			{"↑/↓", "row"}, {"h", "re-run checks"},
		}},
		{id: "log", title: "Run log", kind: ctxMain, prim: u.logView, box: u.logView.Box, keys: []keyHint{
			{"g/G", "top/bottom"}, {"f", "follow"}, {"wheel", "scroll"}, {"ctrl-c", "abort run"},
		}},
	}

	u.customers.SetSelectedFunc(func(int, string, string, rune) { u.focusPanel(2) })
	u.logBuf.Append(fmt.Sprintf("[%s]idle - no run yet. press i (install) or h (health) to start one.[-]", colName(colMuted)))
	u.renderLog(true)
}

func newTable() *tview.Table {
	t := tview.NewTable().SetBorders(false).SetSelectable(true, false).SetFixed(1, 0)
	t.SetSelectedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(colAccent))
	t.SetBorder(true)
	return t
}

// buildBars creates top bar, contextual key bar, status bar and command bar.
func (u *ui) buildBars() {
	u.topBar = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	u.topBar.SetMouseCapture(func(action tview.MouseAction, ev *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action != tview.MouseLeftDown {
			return action, ev
		}
		x, y := ev.Position()
		bx, by, _, _ := u.topBar.GetInnerRect()
		if y != by {
			return action, ev
		}
		col := x - bx
		for _, h := range u.hints {
			if col >= h.x0 && col < h.x1 {
				h.action()
				return action, nil
			}
		}
		return action, ev
	})

	u.keyBar = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	u.statusBar = tview.NewTextView().SetDynamicColors(true).SetWrap(false)

	u.cmdBar = tview.NewInputField().SetLabel(":").SetFieldWidth(0)
	u.cmdBar.SetLabelColor(colAccent)
	u.cmdBar.SetFieldBackgroundColor(tcell.ColorDefault)
	u.cmdBar.SetAutocompleteFunc(func(cur string) []string {
		cur = strings.TrimPrefix(strings.TrimSpace(cur), ":")
		if cur == "" {
			return commandNames()
		}
		var out []string
		for _, c := range commandNames() {
			if strings.HasPrefix(c, strings.ToLower(cur)) && c != cur {
				out = append(out, c)
			}
		}
		return out
	})
	u.cmdBar.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			cmd := strings.TrimPrefix(strings.TrimSpace(u.cmdBar.GetText()), ":")
			u.closeCmd()
			u.runCommand(cmd)
		case tcell.KeyEscape:
			u.closeCmd()
		}
	})

	u.bottom = tview.NewPages()
	u.bottom.AddPage("status", u.statusBar, true, true)
	u.bottom.AddPage("cmd", u.cmdBar, true, false)
}

// ---------------------------------------------------------------- focus model

func (u *ui) focusPanel(i int) {
	if i < 0 || i >= len(u.panels) {
		return
	}
	u.focus = i
	for n, p := range u.panels {
		if n == i {
			p.box.SetBorderColor(colBorderFocus).SetTitleColor(colTitleFocus)
		} else {
			p.box.SetBorderColor(colBorder).SetTitleColor(colTitle)
		}
		p.box.SetTitle(u.panelTitle(n))
	}
	u.app.SetFocus(u.panels[i].prim)
	u.renderKeyBar()
}

// panelTitle numbers every panel like lazygit does, and the log panel also
// advertises its view-buffer state.
func (u *ui) panelTitle(n int) string {
	p := u.panels[n]
	if p.id == "log" {
		return fmt.Sprintf(" %d %s [%s]%d/%d lines  follow:%s[-] ",
			n+1, p.title, colName(colMuted), u.logBuf.len(), logBufferLines, onOff(u.run.following()))
	}
	return fmt.Sprintf(" %d %s ", n+1, p.title)
}

func (u *ui) cycleFocus(delta int) {
	n := len(u.panels)
	u.focusPanel(((u.focus+delta)%n + n) % n)
}

func (u *ui) renderKeyBar() {
	if name := u.topPopup(); name != "" {
		u.keyBar.SetText(fmt.Sprintf("[%s]popup[-] [%s]%s[-]  %s",
			colName(colMuted), colName(colAccent), name, hintString([]keyHint{
				{"tab", "next field"}, {"enter", "confirm"}, {"esc", "close"},
			})))
		return
	}
	p := u.panels[u.focus]
	u.keyBar.SetText(fmt.Sprintf("[%s]%s panel[-] [%s]%s[-]  %s",
		colName(colMuted), p.kind, colName(colAccent), p.title, hintString(p.keys)))
}

func hintString(keys []keyHint) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("[%s]%s[-]:%s", colName(colAccent), k.key, k.desc))
	}
	return strings.Join(parts, "  ")
}

// ---------------------------------------------------------------- global keys

func (u *ui) globalKeys(ev *tcell.EventKey) *tcell.EventKey {
	// Popups and the command bar own the keyboard while they are open.
	if u.topPopup() != "" || u.cmdOpen {
		return ev
	}

	switch ev.Key() {
	case tcell.KeyTab:
		u.cycleFocus(1)
		return nil
	case tcell.KeyBacktab:
		u.cycleFocus(-1)
		return nil
	case tcell.KeyEscape:
		u.focusPanel(0)
		return nil
	case tcell.KeyCtrlC:
		if u.run.active() {
			u.confirmAbort()
			return nil
		}
		u.app.Stop()
		return nil
	case tcell.KeyRune:
		switch r := ev.Rune(); r {
		case ':':
			u.openCmd()
			return nil
		case '?':
			u.showHelp()
			return nil
		case 'm':
			u.toggleMouse()
			return nil
		case 'q':
			u.app.Stop()
			return nil
		case 'h':
			u.runCommand("health")
			return nil
		case 'i':
			u.runCommand("install")
			return nil
		case 'b':
			u.runCommand("backup")
			return nil
		case 'a':
			u.runCommand("add")
			return nil
		case 's':
			u.runCommand("secret")
			return nil
		case '1', '2', '3', '4', '5':
			u.focusPanel(int(r-'1') % len(u.panels))
			return nil
		}
	}
	return ev
}

// globalMouse gives clicks the lazygit behaviour: clicking anywhere inside a
// panel moves focus to it, then the click keeps travelling to the widget so it
// can also select the row underneath.
func (u *ui) globalMouse(ev *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
	if action == tview.MouseLeftDown && u.topPopup() == "" {
		x, y := ev.Position()
		for i, p := range u.panels {
			if p.box.InRect(x, y) {
				if i != u.focus {
					u.focusPanel(i)
				}
				break
			}
		}
	}
	return ev, action
}

func (u *ui) toggleMouse() {
	u.mouseOn = !u.mouseOn
	u.app.EnableMouse(u.mouseOn)
	if u.mouseOn {
		u.setStatus("mouse ON - click selects, wheel scrolls")
	} else {
		u.setStatus("mouse OFF - terminal text selection works again (m to re-enable)")
	}
}

// ------------------------------------------------------------------ selection

func (u *ui) current() Customer { return u.data[u.sel] }

func (u *ui) selectCustomer(i int) {
	if i < 0 || i >= len(u.data) {
		return
	}
	u.sel = i
	// Keep the node table exactly as tall as it needs to be; health and log
	// share what is left, lazygit-style.
	if u.right != nil {
		h := len(u.data[i].Nodes) + 3
		if h > 9 {
			h = 9
		}
		u.right.ResizeItem(u.nodes, h, 0)
	}
	u.renderNodes()
	u.renderHealth()
	u.renderTopBar()
	u.renderStatus()
}

// --------------------------------------------------------------------- status

func (u *ui) setStatus(msg string) {
	u.toast = ""
	u.statusBar.SetText(u.statusLine(msg, colMuted))
}

func (u *ui) renderStatus() { u.statusBar.SetText(u.statusLine("", colMuted)) }

func (u *ui) statusLine(msg string, color tcell.Color) string {
	c := u.current()
	left := fmt.Sprintf("[%s]%s[-] %s", statusColorName(c.HealthStatus), dot(c.HealthStatus), c.Name)
	if c.Lock != "" {
		left += fmt.Sprintf(" [%s] locked[-]", colName(colWarn))
	}
	right := fmt.Sprintf("[%s]mouse:%s  %d customers[-]", colName(colMuted), onOff(u.mouseOn), len(u.data))
	middle := ""
	if s := u.run.statusText(); s != "" {
		middle = "  " + s
	}
	if u.toast != "" {
		middle = fmt.Sprintf("  [%s]%s[-]", colName(colFail), u.toast)
	} else if msg != "" {
		middle = fmt.Sprintf("  [%s]%s[-]", colName(color), msg)
	}
	return left + middle + "   " + right
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
