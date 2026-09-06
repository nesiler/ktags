package main

// Add-customer form, built with huh v2.
//
// huh is a Bubble Tea program in its own right, so embedding it means:
//   - huh.Form.Update returns huh.Model (the v1-style interface: View() string),
//     not tea.Model, so every call needs a type assertion back to *huh.Form;
//   - the host app must forward *all* messages while the form is up, and stop
//     interpreting its own hotkeys;
//   - huh has no "repeatable group", so the node sub-form is driven by
//     rebuilding a fresh Form per node and looping on an "add another?" confirm.

import (
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

type formStage int

const (
	stageMain formStage = iota
	stageNode
	stageSummary
)

type addForm struct {
	th   *theme
	w, h int

	stage formStage
	form  *huh.Form

	name     string
	env      string
	access   string
	jump     string
	rancher  string
	password string

	nodeName string
	nodeRole string
	nodeIP   string
	nodeSSH  string

	addAnother bool
	create     bool

	nodes []Node
}

func newAddForm(th *theme, w, h int) *addForm {
	f := &addForm{th: th, w: w, h: h, env: "prod", access: "direct", nodeRole: "server"}
	f.form = f.buildMain()
	f.resize(w, h)
	return f
}

func (f *addForm) resize(w, h int) {
	f.w, f.h = w, h
	inner := w - f.sideW() - 6
	if inner < 30 {
		inner = 30
	}
	if f.form != nil {
		f.form = f.form.WithWidth(inner).WithHeight(h - 4)
	}
}

func (f *addForm) sideW() int {
	s := f.w / 3
	if s < 26 {
		s = 26
	}
	if s > 40 {
		s = 40
	}
	return s
}

func (f *addForm) buildMain() *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("name").
				Title("Customer name").
				Description("lowercase, digits and dashes: ^[a-z][a-z0-9-]{1,30}$").
				Placeholder("acme").
				Value(&f.name).
				Validate(func(s string) error {
					if !nameRe.MatchString(s) {
						return fmt.Errorf("must match ^[a-z][a-z0-9-]{1,30}$")
					}
					return nil
				}),
			huh.NewSelect[string]().
				Key("env").
				Title("Environment").
				Options(huh.NewOptions("prod", "staging", "test")...).
				Value(&f.env),
			huh.NewSelect[string]().
				Key("access").
				Title("Access mode").
				Options(huh.NewOptions("direct", "jump")...).
				Value(&f.access),
		).Title("New customer"),

		// Dynamic field: huh hides/reveals per group, so the jump host lives in
		// its own group guarded by a hide func that reads the previous answer.
		huh.NewGroup(
			huh.NewInput().
				Key("jump").
				Title("Jump host").
				Description("bastion the cluster is reached through").
				Placeholder("jump.example.com").
				Value(&f.jump).
				Validate(huh.ValidateNotEmpty()),
		).Title("Jump host").WithHideFunc(func() bool { return f.access != "jump" }),

		huh.NewGroup(
			huh.NewInput().
				Key("rancher").
				Title("Rancher hostname").
				Placeholder("rancher.acme.example").
				Value(&f.rancher).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Key("password").
				Title("Bootstrap password (optional)").
				Description("masked; never stored by this spike").
				EchoMode(huh.EchoModePassword).
				Value(&f.password),
		).Title("Cluster access"),
	).WithShowHelp(true)
}

func (f *addForm) buildNode() *huh.Form {
	n := len(f.nodes) + 1
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Key("nodeName").
				Title("Short name").
				Placeholder(f.name+"-srv-"+fmt.Sprint(n)).
				Value(&f.nodeName).
				Validate(huh.ValidateNotEmpty()),
			huh.NewSelect[string]().
				Key("nodeRole").
				Title("Role").
				Options(huh.NewOptions("server", "agent")...).
				Value(&f.nodeRole),
			huh.NewInput().
				Key("nodeIP").
				Title("Node IP").
				Placeholder("10.10.9.11").
				Value(&f.nodeIP).
				Validate(huh.ValidateNotEmpty()),
			huh.NewInput().
				Key("nodeSSH").
				Title("Bootstrap user@host").
				Placeholder("root@203.0.113.90").
				Value(&f.nodeSSH).
				Validate(huh.ValidateNotEmpty()),
			huh.NewConfirm().
				Key("more").
				Title("Add another node?").
				Affirmative("Add another").
				Negative("Done").
				Value(&f.addAnother),
		).Title(fmt.Sprintf("Node %d", n)),
	).WithShowHelp(true)
}

func (f *addForm) buildSummary() *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Confirm new customer").
				Description(f.summary()),
			huh.NewConfirm().
				Key("create").
				Title("Create this customer?").
				Affirmative("Create").
				Negative("Discard").
				Value(&f.create),
		),
	).WithShowHelp(true)
}

func (f *addForm) resetNodeFields() {
	f.nodeName, f.nodeIP, f.nodeSSH = "", "", ""
	f.nodeRole = "server"
	f.addAnother = false
}

func (f *addForm) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "name      %s\n", f.name)
	fmt.Fprintf(&b, "env       %s\n", f.env)
	fmt.Fprintf(&b, "access    %s\n", f.access)
	if f.access == "jump" {
		fmt.Fprintf(&b, "jump host %s\n", f.jump)
	}
	fmt.Fprintf(&b, "rancher   %s\n", f.rancher)
	pw := "(not set)"
	if f.password != "" {
		pw = strings.Repeat("•", len(f.password))
	}
	fmt.Fprintf(&b, "password  %s\n", pw)
	fmt.Fprintf(&b, "nodes     %d\n", len(f.nodes))
	for _, n := range f.nodes {
		fmt.Fprintf(&b, "  %-16s %-7s %s\n", n.Name, n.Role, n.NodeIP)
	}
	return b.String()
}

func (f *addForm) customer() Customer {
	c := Customer{
		Name:       f.name,
		Env:        f.env,
		Access:     f.access,
		JumpHost:   f.jump,
		Rancher:    f.rancher,
		Nodes:      f.nodes,
		Health:     "warn",
		HealthNote: "never checked",
		HealthAge:  "never",
		Backup:     "none",
	}
	c.Checks = seedChecks(c)
	return c
}

// ---------------------------------------------------------------------------
// Wiring into the host model
// ---------------------------------------------------------------------------

func (m model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	f := m.form

	// huh only aborts on ctrl+c, and it swallows everything else, so the host
	// app has to reserve esc for itself before handing the message over.
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		m.screen = screenDashboard
		m.form = nil
		m.flash("add customer cancelled", "info")
		return m, m.expireToast()
	}

	upd, cmd := f.form.Update(msg)
	if hf, ok := upd.(*huh.Form); ok {
		f.form = hf
	}

	switch f.form.State {
	case huh.StateAborted:
		m.screen = screenDashboard
		m.form = nil
		m.flash("add customer cancelled", "info")
		return m, m.expireToast()
	case huh.StateCompleted:
		return m.advanceForm()
	}
	return m, cmd
}

func (m model) advanceForm() (tea.Model, tea.Cmd) {
	f := m.form

	switch f.stage {
	case stageMain:
		f.stage = stageNode
		f.resetNodeFields()
		f.form = f.buildNode()

	case stageNode:
		f.nodes = append(f.nodes, Node{
			Name:     f.nodeName,
			Role:     f.nodeRole,
			NodeIP:   f.nodeIP,
			AccessIP: accessIPFrom(f.nodeSSH),
			Version:  rke2Version,
			Status:   "Ready",
		})
		if f.addAnother {
			f.resetNodeFields()
			f.form = f.buildNode()
		} else {
			f.stage = stageSummary
			f.form = f.buildSummary()
		}

	case stageSummary:
		m.screen = screenDashboard
		if f.create {
			c := f.customer()
			m.customers = append(m.customers, c)
			m.form = nil
			m.sel = len(m.customers) - 1
			m.syncCustomers()
			m.flash("customer "+c.Name+" added", "ok")
			return m, m.expireToast()
		}
		m.form = nil
		m.flash("add customer discarded", "info")
		return m, m.expireToast()
	}

	f.resize(m.contentW(), m.contentH())
	return m, f.form.Init()
}

func accessIPFrom(userHost string) string {
	if i := strings.Index(userHost, "@"); i >= 0 {
		return userHost[i+1:]
	}
	if userHost == "" {
		return "-"
	}
	return userHost
}

func (m model) renderFormScreen() string {
	f := m.form
	ch := m.contentH()
	sw := f.sideW()
	fw := m.w - sw

	stageLbl := map[formStage]string{
		stageMain:    "1/3 customer",
		stageNode:    "2/3 nodes",
		stageSummary: "3/3 confirm",
	}[f.stage]

	formBox := m.th.box("add customer · "+stageLbl, fitLines(f.form.View(), ch-2), fw, ch, true)

	var side strings.Builder
	side.WriteString(m.th.faint.Render(" collected so far") + "\n\n")
	side.WriteString(kv(m.th, "name", f.name))
	side.WriteString(kv(m.th, "env", f.env))
	side.WriteString(kv(m.th, "access", f.access))
	if f.access == "jump" {
		side.WriteString(kv(m.th, "jump", f.jump))
	}
	side.WriteString(kv(m.th, "rancher", f.rancher))
	pw := ""
	if f.password != "" {
		pw = strings.Repeat("•", len(f.password))
	}
	side.WriteString(kv(m.th, "password", pw))
	side.WriteString("\n" + m.th.faint.Render(" nodes") + "\n")
	if len(f.nodes) == 0 {
		side.WriteString(m.th.faint.Render("  (none yet)") + "\n")
	}
	for _, n := range f.nodes {
		side.WriteString(fmt.Sprintf("  %s %s\n",
			lipgloss.NewStyle().Foreground(m.th.accent).Render(n.Name),
			m.th.faint.Render(n.Role+" "+n.NodeIP)))
	}
	side.WriteString("\n" + m.th.faint.Render(" esc cancels the whole form"))

	sideBox := m.th.box("summary", fitLines(side.String(), ch-2), sw, ch, false)
	return lipgloss.JoinHorizontal(lipgloss.Top, formBox, sideBox)
}

func kv(th *theme, k, v string) string {
	if v == "" {
		v = th.faint.Render("—")
	}
	return fmt.Sprintf("  %s %s\n", th.faint.Render(padTo(k, 9)), v)
}
