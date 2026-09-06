package main

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// buildDashboard creates the three dashboard panels: the customer list on the
// left, the node table and the health table on the right.
func (a *App) buildDashboard() {
	a.custList = tview.NewList().
		ShowSecondaryText(true).
		SetHighlightFullLine(true).
		SetWrapAround(true).
		SetUseStyleTags(true, true)
	a.custList.SetSelectedStyle(tcell.StyleDefault.
		Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite).Bold(true))
	a.custList.SetChangedFunc(func(i int, _, _ string, _ rune) { a.selectCustomer(i) })
	a.custList.SetSelectedFunc(func(i int, _, _ string, _ rune) { a.selectCustomer(i) })
	frame(a.custList.Box, "Customers")

	a.nodeTable = newTable()
	a.nodeTable.SetSelectedFunc(func(row, _ int) {
		c := a.current()
		if row >= 1 && row <= len(c.Nodes) {
			n := c.Nodes[row-1]
			a.flash("node [%s]%s[-] · %s · %s · ssh %s", colAccent, n.Name, n.Role, n.Status, n.AccessIP)
		}
	})
	frame(a.nodeTable.Box, "Nodes")

	a.healthTable = newTable()
	a.healthTable.SetSelectedFunc(func(row, _ int) {
		c := a.current()
		if row >= 1 && row <= len(c.Health) {
			h := c.Health[row-1]
			a.flash("%s: [%s]%s[-] – %s", h.Name, statusColor(h.Status), h.Status, h.Detail)
		}
	})
	frame(a.healthTable.Box, "Health")

	a.reloadCustomers()
}

func (a *App) dashboardBody() *tview.Flex {
	right := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.nodeTable, 0, 2, false).
		AddItem(a.healthTable, 0, 3, false)
	return tview.NewFlex().
		AddItem(a.custList, 32, 0, true).
		AddItem(right, 0, 1, false)
}

func newTable() *tview.Table {
	t := tview.NewTable().
		SetBorders(false).
		SetFixed(1, 0).
		SetSelectable(true, false)
	t.SetSelectedStyle(tcell.StyleDefault.
		Background(tcell.ColorDarkBlue).Foreground(tcell.ColorWhite).Bold(true))
	return t
}

func headerCell(text string) *tview.TableCell {
	return tview.NewTableCell(text).
		SetTextColor(tcell.ColorDodgerBlue).
		SetAttributes(tcell.AttrBold).
		SetSelectable(false).
		SetAlign(tview.AlignLeft)
}

func cell(text string) *tview.TableCell {
	return tview.NewTableCell(text).SetAlign(tview.AlignLeft)
}

// reloadCustomers rebuilds the left list (used at start-up and after :add).
func (a *App) reloadCustomers() {
	keep := a.sel
	a.custList.Clear()
	for i := range a.customers {
		c := &a.customers[i]
		lock := ""
		if c.Locked() {
			lock = " [" + colFail + "]\U0001F512[-]"
		}
		main := fmt.Sprintf("[%s]●[-] [::b]%s[::-]%s", statusColor(c.HealthState), c.Name, lock)
		secondary := fmt.Sprintf("  %s [%s]· %d node(s) · %s[-]",
			envTag(c.Environment), colDim, len(c.Nodes), c.Access)
		a.custList.AddItem(main, secondary, 0, nil)
	}
	if keep >= len(a.customers) {
		keep = len(a.customers) - 1
	}
	a.custList.SetCurrentItem(keep)
}

// selectCustomer moves the selection and refreshes both right-hand tables.
func (a *App) selectCustomer(i int) {
	if i < 0 || i >= len(a.customers) {
		return
	}
	a.sel = i
	c := a.current()
	a.renderContext()
	a.renderNodes(c)
	a.renderHealth(c)
	a.custList.SetTitle(fmt.Sprintf(" Customers [%d] ", len(a.customers)))
}

func (a *App) renderNodes(c *Customer) {
	t := a.nodeTable
	t.Clear()
	for i, h := range []string{"NAME", "ROLE", "NODE IP", "ACCESS IP", "RKE2 VERSION", "STATUS", "LONGHORN"} {
		t.SetCell(0, i, headerCell(h))
	}
	for r, n := range c.Nodes {
		status := "[" + colOK + "]Ready[-]"
		if n.Status != "Ready" {
			status = "[" + colFail + "::b]NotReady[-::-]"
		}
		longhorn := "[" + colDim + "]no[-]"
		if n.Longhorn {
			longhorn = "yes"
		}
		role := n.Role
		if role == "server" {
			role = "[" + colAccent + "]server[-]"
		}
		t.SetCell(r+1, 0, cell(n.Name))
		t.SetCell(r+1, 1, cell(role))
		t.SetCell(r+1, 2, cell(n.NodeIP))
		t.SetCell(r+1, 3, cell(n.AccessIP))
		t.SetCell(r+1, 4, cell(n.RKE2))
		t.SetCell(r+1, 5, cell(status))
		t.SetCell(r+1, 6, cell(longhorn).SetExpansion(1))
	}
	t.SetTitle(fmt.Sprintf(" Nodes · %s [%d] ", c.Name, len(c.Nodes)))
	t.Select(1, 0)
	t.ScrollToBeginning()
}

func (a *App) renderHealth(c *Customer) {
	fillHealthTable(a.healthTable, c.Health)
	a.healthTable.SetTitle(fmt.Sprintf(" Health · %s (%s) ", c.Name, c.LastHealth))
}

// fillHealthTable is shared by the dashboard health panel and the run summary.
func fillHealthTable(t *tview.Table, checks []HealthCheck) {
	t.Clear()
	for i, h := range []string{"CHECK", "STATUS", "DETAIL"} {
		t.SetCell(0, i, headerCell(h))
	}
	for r, h := range checks {
		mark := map[string]string{StatusOK: "✓", StatusWarn: "▲", StatusFail: "✗"}[h.Status]
		t.SetCell(r+1, 0, cell(h.Name))
		t.SetCell(r+1, 1, cell(fmt.Sprintf("[%s::b]%s %s[-::-]", statusColor(h.Status), mark, h.Status)))
		t.SetCell(r+1, 2, cell("["+colDim+"]"+h.Detail+"[-]").SetExpansion(1))
	}
	t.Select(1, 0)
	t.ScrollToBeginning()
}

func (a *App) showDashboard() {
	a.bodyPages.SwitchToPage("dashboard")
	a.setFocusRing("dashboard")
	a.renderStatus()
}
