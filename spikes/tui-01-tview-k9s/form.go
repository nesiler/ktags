package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

var nameRe = regexp.MustCompile(CustomerNameRe)

// draft is the in-progress customer of the :add form.
type draft struct {
	name, env, access, jump, rancher, password string
	nodes                                      []Node

	nodeName, nodeRole, nodeIP, nodeBoot string
}

type addForm struct {
	a *App
	d draft

	page      *tview.Flex
	main      *tview.Form
	nodeForm  *tview.Form
	nodeTable *tview.Table
	hint      *tview.TextView

	panels   []tview.Primitive
	panelIdx int
	building bool
}

func (a *App) showAddForm() {
	f := &addForm{
		a: a,
		d: draft{env: "prod", access: "direct", nodeRole: "server"},
	}

	f.main = tview.NewForm()
	frame(f.main.Box, "Customer")
	f.main.SetBorderColor(borderFocused)

	f.nodeForm = tview.NewForm()
	frame(f.nodeForm.Box, "Node")

	f.nodeTable = newTable()
	frame(f.nodeTable.Box, "Nodes [0]")

	f.hint = tview.NewTextView().SetDynamicColors(true)
	f.hint.SetBorderPadding(0, 0, 1, 1)
	f.hint.SetText(fmt.Sprintf(
		"[%s::b]Add customer[-::-]  [%s]Tab moves inside a panel · Ctrl-N / Ctrl-P jump between panels · click works too · Esc cancels[-]",
		colAccent, colDim))

	f.buildMain()
	f.buildNodeForm()
	f.renderNodes()

	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(f.nodeForm, 13, 0, false).
		AddItem(f.nodeTable, 0, 1, false)

	body := tview.NewFlex().
		AddItem(f.main, 0, 1, true).
		AddItem(right, 0, 1, false)

	f.page = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(f.hint, 1, 0, false).
		AddItem(body, 0, 1, true)

	f.panels = []tview.Primitive{f.main, f.nodeForm, f.nodeTable}
	f.page.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlN:
			f.focusPanel(f.panelIdx + 1)
			return nil
		case tcell.KeyCtrlP:
			f.focusPanel(f.panelIdx - 1)
			return nil
		}
		return ev
	})

	a.showFullPage("form", f.page)
	f.focusPanel(0)
}

func (f *addForm) focusPanel(i int) {
	n := len(f.panels)
	f.panelIdx = (i + n) % n
	for j, p := range f.panels {
		if b, ok := p.(interface{ SetBorderColor(tcell.Color) *tview.Box }); ok {
			if j == f.panelIdx {
				b.SetBorderColor(borderFocused)
			} else {
				b.SetBorderColor(borderIdle)
			}
		}
	}
	f.a.app.SetFocus(f.panels[f.panelIdx])
}

func (f *addForm) cancel() {
	f.a.pages.RemovePage("form")
	f.a.focusCurrent()
	f.a.flash("[%s]add cancelled[-]", colDim)
}

// buildMain (re)builds the customer form. tview's Form can only append items, so
// revealing the "jump host" field means rebuilding the form from the draft.
func (f *addForm) buildMain() {
	f.building = true
	defer func() { f.building = false }()

	f.main.Clear(true)
	f.main.AddInputField("Customer name", f.d.name, 26, acceptCustomerRune, func(t string) { f.d.name = t })
	f.main.AddDropDown("Environment", []string{"prod", "staging", "test"}, indexOf([]string{"prod", "staging", "test"}, f.d.env),
		func(opt string, _ int) { f.d.env = opt })
	f.main.AddDropDown("Access mode", []string{"direct", "jump"}, indexOf([]string{"direct", "jump"}, f.d.access),
		func(opt string, _ int) {
			if f.building || opt == f.d.access {
				return
			}
			f.d.access = opt
			// Rebuild on the next draw so we do not tear the form down from
			// inside its own event handler.
			go f.a.app.QueueUpdateDraw(func() {
				f.buildMain()
				// Form.SetFocus only records the index: it has to be set
				// before the application focuses the form.
				f.main.SetFocus(2)
				f.a.app.SetFocus(f.main)
			})
		})
	if f.d.access == "jump" {
		f.main.AddInputField("Jump host", f.d.jump, 26, nil, func(t string) { f.d.jump = t })
	}
	f.main.AddInputField("Rancher hostname", f.d.rancher, 26, nil, func(t string) { f.d.rancher = t })
	f.main.AddPasswordField("Bootstrap password", f.d.password, 26, '*', func(t string) { f.d.password = t })
	f.main.AddTextView("", "optional — leave empty to use your ssh agent", 40, 2, true, false)
	f.main.AddButton("Submit", f.submit)
	f.main.AddButton("Cancel", f.cancel)
	f.main.SetCancelFunc(f.cancel)
}

func (f *addForm) buildNodeForm() {
	roles := []string{"server", "agent"}
	f.nodeForm.AddInputField("Short name", "", 22, nil, func(t string) { f.d.nodeName = t })
	f.nodeForm.AddDropDown("Role", roles, 0, func(opt string, _ int) { f.d.nodeRole = opt })
	f.nodeForm.AddInputField("Node IP", "", 22, nil, func(t string) { f.d.nodeIP = t })
	f.nodeForm.AddInputField("Bootstrap user@host", "", 22, nil, func(t string) { f.d.nodeBoot = t })
	f.nodeForm.AddButton("Add another node", f.addNode)
	f.nodeForm.SetCancelFunc(f.cancel)
}

func (f *addForm) addNode() {
	if strings.TrimSpace(f.d.nodeName) == "" {
		f.a.flashErr("node short name is required")
		return
	}
	access := f.d.nodeBoot
	if i := strings.Index(access, "@"); i >= 0 {
		access = access[i+1:]
	}
	if access == "" {
		access = fmt.Sprintf("203.0.113.%d", 60+len(f.d.nodes))
	}
	f.d.nodes = append(f.d.nodes, Node{
		Name:     strings.TrimSpace(f.d.nodeName),
		Role:     f.d.nodeRole,
		NodeIP:   orDefault(f.d.nodeIP, fmt.Sprintf("10.10.9.%d", 11+len(f.d.nodes))),
		AccessIP: access,
		RKE2:     RKE2Version,
		Status:   "Ready",
		Longhorn: f.d.nodeRole == "server",
	})
	f.d.nodeName, f.d.nodeIP, f.d.nodeBoot = "", "", ""
	f.nodeForm.GetFormItem(0).(*tview.InputField).SetText("")
	f.nodeForm.GetFormItem(2).(*tview.InputField).SetText("")
	f.nodeForm.GetFormItem(3).(*tview.InputField).SetText("")
	f.nodeForm.SetFocus(0)
	f.renderNodes()
	f.a.flash("[%s]node added (%d total)[-]", colOK, len(f.d.nodes))
}

func (f *addForm) renderNodes() {
	t := f.nodeTable
	t.Clear()
	for i, h := range []string{"NAME", "ROLE", "NODE IP", "BOOTSTRAP"} {
		t.SetCell(0, i, headerCell(h))
	}
	for r, n := range f.d.nodes {
		t.SetCell(r+1, 0, cell(n.Name))
		t.SetCell(r+1, 1, cell(n.Role))
		t.SetCell(r+1, 2, cell(n.NodeIP))
		t.SetCell(r+1, 3, cell("["+colDim+"]"+n.AccessIP+"[-]").SetExpansion(1))
	}
	t.SetTitle(fmt.Sprintf(" Nodes [%d] ", len(f.d.nodes)))
}

func (f *addForm) submit() {
	switch {
	case !nameRe.MatchString(f.d.name):
		f.a.flashErr("customer name %q does not match %s", f.d.name, CustomerNameRe)
		return
	case f.nameTaken():
		f.a.flashErr("customer %q already exists", f.d.name)
		return
	case f.d.access == "jump" && strings.TrimSpace(f.d.jump) == "":
		f.a.flashErr("jump access needs a jump host")
		return
	case len(f.d.nodes) == 0:
		f.a.flashErr("add at least one node")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Create customer [%s::b]%s[-::-]?\n\n", colAccent, f.d.name)
	fmt.Fprintf(&b, "environment : %s\n", f.d.env)
	access := f.d.access
	if f.d.access == "jump" {
		access += " via " + f.d.jump
	}
	fmt.Fprintf(&b, "access      : %s\n", access)
	fmt.Fprintf(&b, "rancher     : %s\n", orDefault(f.d.rancher, "-"))
	fmt.Fprintf(&b, "password    : %s\n", maskOrNone(f.d.password))
	fmt.Fprintf(&b, "nodes       : %d\n", len(f.d.nodes))
	for _, n := range f.d.nodes {
		fmt.Fprintf(&b, "  · %s (%s) %s\n", n.Name, n.Role, n.NodeIP)
	}

	m := tview.NewModal().
		SetText(b.String()).
		AddButtons([]string{"Create", "Back"}).
		SetDoneFunc(func(_ int, label string) {
			f.a.pages.RemovePage("summary")
			if label != "Create" {
				f.a.showFullPage("form", f.page)
				f.main.SetFocus(f.main.GetFormItemCount())
				f.a.app.SetFocus(f.main)
				return
			}
			c := NewCustomer(f.d.name, f.d.env, f.d.access, f.d.jump, f.d.rancher, f.d.nodes)
			f.a.customers = append(f.a.customers, c)
			f.a.sel = len(f.a.customers) - 1
			f.a.reloadCustomers()
			f.a.selectCustomer(f.a.sel)
			f.a.focusCurrent()
			f.a.flash("[%s]customer %s created (in memory)[-]", colOK, c.Name)
		})
	// tview.Pages routes keys to the first page whose HasFocus() is true, in
	// insertion order — not to the page on top. A form left below the modal can
	// therefore keep swallowing keys, so the form page is taken out while the
	// confirmation is up and put back if the user goes "Back".
	f.a.pages.RemovePage("form")
	f.a.pages.AddPage("summary", m, true, true)
	f.a.app.SetFocus(m)
}

func (f *addForm) nameTaken() bool {
	for i := range f.a.customers {
		if f.a.customers[i].Name == f.d.name {
			return true
		}
	}
	return false
}

// acceptCustomerRune keeps the input field inside the allowed character set;
// the full pattern is still checked on submit.
func acceptCustomerRune(text string, last rune) bool {
	if len(text) > 31 {
		return false
	}
	switch {
	case last >= 'a' && last <= 'z':
		return true
	case last >= '0' && last <= '9', last == '-':
		return len(text) > 1
	}
	return false
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func maskOrNone(v string) string {
	if v == "" {
		return "(none)"
	}
	return strings.Repeat("*", len(v))
}
