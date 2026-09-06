package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// popupEntry remembers which primitive to focus again when a layered popup on
// top of it is closed.
type popupEntry struct {
	name  string
	focus tview.Primitive
}

// modal centres a primitive on top of the dashboard.
func modal(p tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(p, height, 0, true).
			AddItem(nil, 0, 1, false), width, 0, true).
		AddItem(nil, 0, 1, false)
}

func (u *ui) topPopup() string {
	if len(u.popups) == 0 {
		return ""
	}
	return u.popups[len(u.popups)-1]
}

func (u *ui) pushPopup(name string, content tview.Primitive, width, height int, focus tview.Primitive) {
	u.popups = append(u.popups, name)
	u.pages.AddPage(name, modal(content, width, height), true, true)
	u.app.SetFocus(focus)
	u.renderKeyBar()
}

func (u *ui) popPopup(name string) {
	u.pages.RemovePage(name)
	for i, n := range u.popups {
		if n == name {
			u.popups = append(u.popups[:i], u.popups[i+1:]...)
			break
		}
	}
	u.focusPanel(u.focus)
	u.renderKeyBar()
}

func popupBox(p interface {
	SetBorder(bool) *tview.Box
}, title string) {
	p.SetBorder(true).SetBorderColor(colBorderFocus).SetTitle(" " + title + " ").SetTitleColor(colTitleFocus)
}

// ---------------------------------------------------------------------- toast

// showToast layers a small, self-dismissing error box in the lower right and
// mirrors the message in the status bar.
func (u *ui) showToast(msg string) {
	u.toastID++
	id := u.toastID
	u.toast = msg

	tv := tview.NewTextView().SetDynamicColors(true).SetWrap(true).
		SetText(fmt.Sprintf("[%s]%s[-]", colName(colFail), msg))
	tv.SetBorder(true).SetBorderColor(colFail).SetTitle(" ! ").SetTitleColor(colFail)

	width := len(msg) + 6
	if width > 60 {
		width = 60
	}
	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(tv, width, 0, false), 4, 0, false).
		AddItem(nil, 3, 0, false)

	keep := u.app.GetFocus()
	u.pages.AddPage("toast", layout, true, true)
	u.app.SetFocus(keep)
	u.renderStatus()

	go func() {
		time.Sleep(3500 * time.Millisecond)
		u.app.QueueUpdateDraw(func() {
			if u.toastID != id {
				return
			}
			f := u.app.GetFocus()
			u.pages.RemovePage("toast")
			u.toast = ""
			u.app.SetFocus(f)
			u.renderStatus()
		})
	}()
}

// ----------------------------------------------------------------------- help

func (u *ui) showHelp() {
	if u.topPopup() == "help" {
		u.popPopup("help")
		return
	}
	var b strings.Builder
	sec := func(s string) { fmt.Fprintf(&b, "\n[%s::b]%s[-:-:-]\n", colName(colAccent), s) }
	row := func(k, d string) { fmt.Fprintf(&b, "  [%s]%-14s[-] %s\n", colName(colHeader), k, d) }

	sec("global")
	row("tab / shift-tab", "move focus to next / previous panel")
	row("1 .. 5", "focus panel by number")
	row("m", "toggle mouse (off = terminal text selection)")
	row(":", "command bar with completion")
	row("? / q / ctrl-c", "help / quit / quit (abort run when one is active)")
	sec("side panels (customers, plays/tasks)")
	row("up / down", "move selection")
	row("enter", "customers: jump to nodes - tasks: fold/unfold")
	row("c / e", "tasks: collapse all / expand all")
	sec("main panels (nodes, health, run log)")
	row("up / down", "move row / scroll")
	row("g / G", "log: top / bottom")
	row("f", "log: toggle auto-follow")
	row("wheel", "scroll any panel under the cursor")
	sec("actions")
	for _, n := range commandNames() {
		row(":"+n, commands[n].desc)
	}
	sec("popups")
	row("esc", "close the top popup")
	row("tab", "next field / button")

	tv := tview.NewTextView().SetDynamicColors(true).SetText(b.String()).SetScrollable(true)
	popupBox(tv, "help")
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Key() == tcell.KeyEscape || ev.Rune() == '?' || ev.Rune() == 'q' {
			u.popPopup("help")
			return nil
		}
		return ev
	})
	u.pushPopup("help", tv, 70, 30, tv)
}

// ------------------------------------------------------------- confirmations

func (u *ui) confirmYesNo(title, text string, onYes func()) {
	tv := tview.NewTextView().SetDynamicColors(true).SetWrap(true).
		SetText(fmt.Sprintf("\n %s\n\n [%s]y[-] confirm    [%s]n / esc[-] cancel", text, colName(colOK), colName(colMuted)))
	popupBox(tv, title)
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Rune() == 'y' || ev.Rune() == 'Y':
			u.popPopup("confirm")
			onYes()
		case ev.Rune() == 'n' || ev.Rune() == 'N' || ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyEnter:
			u.popPopup("confirm")
		}
		return nil
	})
	u.pushPopup("confirm", tv, 62, 8, tv)
}

// confirmTypeName is the k9s-style "type the name to proceed" guard used for
// prod customers.
func (u *ui) confirmTypeName(kind string, c Customer, onYes func()) {
	input := tview.NewInputField().SetLabel("name: ").SetFieldWidth(30)
	input.SetFieldBackgroundColor(tcell.ColorDefault)
	input.SetLabelColor(colAccent)

	info := tview.NewTextView().SetDynamicColors(true).SetWrap(true).SetText(fmt.Sprintf(
		"\n [%s::b]%s[-:-:-] targets the [%s::b]PROD[-:-:-] cluster [%s]%s[-].\n Type the customer name to proceed.\n",
		colName(colAccent), kind, colName(colFail), colName(colAccent), c.Name))

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(info, 5, 0, false).
		AddItem(input, 1, 0, true)
	popupBox(flex, fmt.Sprintf("confirm %s on prod", kind))

	input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			if strings.TrimSpace(input.GetText()) == c.Name {
				u.popPopup("confirm")
				onYes()
				return
			}
			u.popPopup("confirm")
			u.showToast("name did not match - nothing was run")
		case tcell.KeyEscape:
			u.popPopup("confirm")
		}
	})
	u.pushPopup("confirm", flex, 62, 9, input)
}

// confirmAbort is bound to ctrl-c while a run is streaming.
func (u *ui) confirmAbort() {
	u.confirmYesNo("abort", fmt.Sprintf("Abort run [%s]%s[-]? [y/N]", colName(colAccent), u.run.label), func() {
		u.run.abort()
		u.setStatus("abort requested")
	})
}

// --------------------------------------------------------------- secret box

func (u *ui) showSecrets() {
	list := tview.NewList().ShowSecondaryText(false)
	list.SetSelectedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(colAccent))
	for _, s := range fakeSecrets {
		key := s.Key
		val := s.Value
		list.AddItem(key, "", 0, func() {
			u.popPopup("secret")
			u.revealSecret(key, val)
		})
	}
	popupBox(list, "secrets of "+u.current().Name)
	list.SetDoneFunc(func() { u.popPopup("secret") })
	u.pushPopup("secret", list, 50, 6, list)
}

func (u *ui) revealSecret(key, value string) {
	tv := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	popupBox(tv, "secret: "+key)

	done := make(chan struct{})
	var closed bool
	closeBox := func() {
		if closed {
			return
		}
		closed = true
		close(done)
		u.popPopup("reveal")
	}

	render := func(left int) {
		tv.SetText(fmt.Sprintf("\n [%s::b]%s[-:-:-]\n\n [%s]clears in %2ds[-]   [%s]c[-] copy   [%s]any key[-] close",
			colName(colWarn), tview.Escape(value), colName(colMuted), left, colName(colAccent), colName(colMuted)))
	}
	render(15)

	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		if ev.Rune() == 'c' {
			u.setStatus(fmt.Sprintf("copied %s to clipboard (mock)", key))
			return nil
		}
		closeBox()
		return nil
	})
	u.pushPopup("reveal", tv, 62, 8, tv)

	go func() {
		for left := 14; left >= 0; left-- {
			select {
			case <-done:
				return
			case <-time.After(time.Second):
			}
			l := left
			u.app.QueueUpdateDraw(func() {
				if l == 0 {
					tv.SetText(fmt.Sprintf("\n [%s]cleared[-]\n\n [%s]any key[-] close", colName(colMuted), colName(colMuted)))
					return
				}
				render(l)
			})
		}
	}()
}

// ------------------------------------------------------------- add customer

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// addFormData survives the form rebuild that switching access mode triggers.
type addFormData struct {
	name     string
	env      int
	access   int
	jump     string
	rancher  string
	password string

	nodeName string
	nodeRole int
	nodeIP   string
	nodeSSH  string

	nodes    []Node
	building bool
}

var envOptions = []string{"prod", "staging", "test"}
var accessOptions = []string{"direct", "jump"}
var roleOptions = []string{"server", "agent"}

func (u *ui) showAddForm() {
	u.af = &addFormData{}
	u.buildAddForm()
}

func (u *ui) buildAddForm() {
	af := u.af
	af.building = true
	defer func() { af.building = false }()

	form := tview.NewForm()
	form.SetFieldBackgroundColor(tcell.ColorBlack)
	form.SetLabelColor(colAccent)
	form.SetButtonBackgroundColor(tcell.ColorBlack)

	form.AddInputField("customer name", af.name, 32, nil, func(t string) { af.name = t })
	form.AddDropDown("environment", envOptions, af.env, func(_ string, i int) {
		if i >= 0 {
			af.env = i
		}
	})
	form.AddDropDown("access mode", accessOptions, af.access, func(_ string, i int) {
		if i < 0 || af.building || i == af.access {
			return
		}
		af.access = i
		// tview forms have no "insert item at index", so the jump host field is
		// revealed by rebuilding the whole form. The drop-down has already
		// closed its list at this point, so rebuilding here is safe.
		u.popPopup("add")
		u.buildAddForm()
	})
	if af.access == 1 {
		form.AddInputField("jump host", af.jump, 32, nil, func(t string) { af.jump = t })
	}
	form.AddInputField("rancher hostname", af.rancher, 32, nil, func(t string) { af.rancher = t })

	form.AddTextView("nodes", u.nodeSummary(), 40, 3, true, true)
	form.AddInputField("node short name", af.nodeName, 20, nil, func(t string) { af.nodeName = t })
	form.AddDropDown("node role", roleOptions, af.nodeRole, func(_ string, i int) {
		if i >= 0 {
			af.nodeRole = i
		}
	})
	form.AddInputField("node ip", af.nodeIP, 20, nil, func(t string) { af.nodeIP = t })
	form.AddInputField("bootstrap user@host", af.nodeSSH, 28, nil, func(t string) { af.nodeSSH = t })
	form.AddPasswordField("bootstrap password (optional)", af.password, 24, '*', func(t string) { af.password = t })

	form.AddButton("add another node", func() {
		if af.nodeName == "" || af.nodeIP == "" {
			u.showToast("node needs at least a short name and an ip")
			return
		}
		af.nodes = append(af.nodes, Node{
			Name: af.nodeName, Role: roleOptions[af.nodeRole], NodeIP: af.nodeIP,
			AccessIP: af.nodeSSH, RKE2: rke2Version, Status: "Ready",
		})
		af.nodeName, af.nodeIP, af.nodeSSH = "", "", ""
		u.popPopup("add")
		u.buildAddForm()
	})
	form.AddButton("submit", func() { u.submitAddForm() })
	form.AddButton("cancel", func() { u.popPopup("add") })
	form.SetCancelFunc(func() { u.popPopup("add") })

	popupBox(form, "add customer")
	u.pushPopup("add", form, 66, 24, form)
}

func (u *ui) nodeSummary() string {
	af := u.af
	if len(af.nodes) == 0 {
		return "(none yet - fill the node fields and press \"add another node\")"
	}
	rows := make([]string, 0, len(af.nodes))
	for _, n := range af.nodes {
		rows = append(rows, fmt.Sprintf("%s (%s) %s", n.Name, n.Role, n.NodeIP))
	}
	return strings.Join(rows, "\n")
}

func (u *ui) submitAddForm() {
	af := u.af
	if !nameRe.MatchString(af.name) {
		u.showToast("customer name must match ^[a-z][a-z0-9-]{1,30}$")
		return
	}
	for _, c := range u.data {
		if c.Name == af.name {
			u.showToast("customer already exists")
			return
		}
	}
	nodes := af.nodes
	if af.nodeName != "" && af.nodeIP != "" {
		nodes = append(nodes, Node{
			Name: af.nodeName, Role: roleOptions[af.nodeRole], NodeIP: af.nodeIP,
			AccessIP: af.nodeSSH, RKE2: rke2Version, Status: "Ready",
		})
	}
	if len(nodes) == 0 {
		u.showToast("at least one node is required")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "\n [%s]name[-]        %s\n", colName(colMuted), af.name)
	fmt.Fprintf(&b, " [%s]environment[-] %s\n", colName(colMuted), envOptions[af.env])
	fmt.Fprintf(&b, " [%s]access[-]      %s", colName(colMuted), accessOptions[af.access])
	if af.access == 1 {
		fmt.Fprintf(&b, " via %s", af.jump)
	}
	fmt.Fprintf(&b, "\n [%s]rancher[-]     %s\n", colName(colMuted), af.rancher)
	fmt.Fprintf(&b, " [%s]password[-]    %s\n", colName(colMuted), maskOrNone(af.password))
	fmt.Fprintf(&b, " [%s]nodes[-]       %d\n", colName(colMuted), len(nodes))
	for _, n := range nodes {
		fmt.Fprintf(&b, "   - %s (%s) %s\n", tview.Escape(n.Name), n.Role, n.NodeIP)
	}

	tv := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).
		SetText(b.String() + fmt.Sprintf("\n [%s]y[-] create    [%s]n / esc[-] back to form", colName(colOK), colName(colMuted)))
	popupBox(tv, "create this customer?")
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Rune() == 'y' || ev.Rune() == 'Y':
			u.popPopup("addconfirm")
			u.popPopup("add")
			u.appendCustomer(af, nodes)
		case ev.Rune() == 'n' || ev.Rune() == 'N' || ev.Key() == tcell.KeyEscape:
			u.popPopup("addconfirm")
		}
		return nil
	})
	u.pushPopup("addconfirm", tv, 66, 18, tv)
}

func (u *ui) appendCustomer(af *addFormData, nodes []Node) {
	c := Customer{
		Name: af.name, Env: envOptions[af.env], Access: accessOptions[af.access],
		JumpHost: af.jump, Rancher: af.rancher, Nodes: nodes,
		Health: []HealthCheck{
			{"nodes_ready", "warn", "not installed yet"},
			{"node_versions", "warn", "unknown"},
			{"bad_pods", "warn", "unknown"},
			{"helm_releases", "warn", "unknown"},
			{"rancher_ping", "warn", "unknown"},
			{"longhorn_nodes", "warn", "unknown"},
			{"etcd", "warn", "unknown"},
			{"disk", "warn", "unknown"},
			{"last_snapshot", "fail", "never"},
		},
		HealthStatus: "warn", HealthAge: "never",
		BackupStatus: "none", BackupAge: "never",
	}
	u.data = append(u.data, c)
	u.renderCustomers()
	u.customers.SetCurrentItem(len(u.data) - 1)
	u.setStatus(fmt.Sprintf("added customer %s (%s, %d nodes) - in memory only", c.Name, c.Env, len(c.Nodes)))
}

func maskOrNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return strings.Repeat("*", len(s))
}
