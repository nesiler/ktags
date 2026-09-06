package main

import (
	"fmt"
	"sort"
	"strings"
)

// command is one entry of the ":" command bar.
type command struct {
	name string
	desc string
	run  func(u *ui)
}

var commands = map[string]command{
	"health":  {"health", "run health checks on the selected customer", func(u *ui) { u.guardedRun("health") }},
	"install": {"install", "install / converge the selected cluster", func(u *ui) { u.guardedRun("install") }},
	"backup":  {"backup", "etcd + volume snapshot", func(u *ui) { u.guardedRun("backup") }},
	"add":     {"add", "add a new customer", func(u *ui) { u.showAddForm() }},
	"secret":  {"secret", "reveal a secret for 15 seconds", func(u *ui) { u.showSecrets() }},
	"quit":    {"quit", "leave ktags", func(u *ui) { u.app.Stop() }},
}

func commandNames() []string {
	out := make([]string, 0, len(commands))
	for k := range commands {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (u *ui) openCmd() {
	u.cmdOpen = true
	u.cmdBar.SetText("")
	u.bottom.SwitchToPage("cmd")
	u.app.SetFocus(u.cmdBar)
	u.keyBar.SetText(fmt.Sprintf("[%s]command bar[-]  %s", colName(colMuted), hintString([]keyHint{
		{"tab", "complete"}, {"enter", "run"}, {"esc", "cancel"},
	})) + fmt.Sprintf("   [%s]%s[-]", colName(colMuted), strings.Join(commandNames(), " ")))
}

func (u *ui) closeCmd() {
	u.cmdOpen = false
	u.bottom.SwitchToPage("status")
	u.focusPanel(u.focus)
}

func (u *ui) runCommand(name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return
	}
	if name == "q" {
		name = "quit"
	}
	c, ok := commands[name]
	if !ok {
		u.showToast(fmt.Sprintf("unknown command %q - try one of: %s", name, strings.Join(commandNames(), ", ")))
		return
	}
	c.run(u)
}

// guardedRun applies the lock check and the prod confirmation rules before a
// run is started.
func (u *ui) guardedRun(kind string) {
	c := u.current()
	if kind != "health" && c.Lock != "" {
		u.showToast(fmt.Sprintf("locked by %s", c.Lock))
		return
	}
	start := func() {
		u.startRun(kind, c)
		u.focusPanel(4)
		u.setStatus(fmt.Sprintf("started %s on %s", kind, c.Name))
	}
	if kind == "health" {
		start()
		return
	}
	if c.Env == "prod" {
		u.confirmTypeName(kind, c, start)
		return
	}
	u.confirmYesNo(strings.ToUpper(kind[:1])+kind[1:],
		fmt.Sprintf("Run [%s::b]%s[-:-:-] on [%s]%s[-] (%s)?", colName(colAccent), kind, colName(colAccent), c.Name, c.Env),
		start)
}
