# tui-02-tview-panels

**Libraries:** `github.com/rivo/tview v0.42.0`, `github.com/gdamore/tcell/v2 v2.13.10`. Go 1.27.1.

**LOC (`wc -l *.go`):** commands 90, data 151, main 528, panels 192, popups 472, run 611 — **2044 total**.

Model: lazygit, not k9s. Five panels are always on screen (customers, plays/tasks, nodes,
health, run log); a run streams into the log panel while the side panels stay live.

## Easy

Widgets exist: `Table`, `List`, `TreeView`, `Form`, `Pages`, nested `Flex`. `Flex.ResizeItem`
sizes the node table to its rows. Popups are `Pages.AddPage` over a centred `Flex`. The
per-panel keybar falls out of a small `panel{id, kind, keys}` registry.

## Hard / awkward

* tview has one focus chain and no focused *style*. The lazygit panel model — numbering,
  border/title repaint, Tab order, `contextKind` — is ~60 hand-written lines.
* `Application.SetInputCapture` fires before every widget, so global single-letter commands
  silently shadow widget keys; popups and the command bar need an explicit bail-out
  (`u.topPopup() != "" || u.cmdOpen`). Easiest thing to get wrong.
* **`QueueUpdate` blocks until the event loop runs it** — calling it from the UI goroutine
  deadlocks. Cost one redesign (form rebuilds are synchronous).
* `[gather]` in a log line parses as a colour tag; every bracketed Ansible token needs
  `tview.Escape`.
* `Form` has no insert-at-index, so revealing "jump host" rebuilds the form from a side
  struct; the repeatable node sub-form is clumsy.
* Clickable top-bar hints are manual (column ranges + `Box.SetMouseCapture`); TextView
  regions are not clickable. `Box.SetMouseCapture` and `Application.SetMouseCapture` take
  their two arguments in opposite order.

## Mouse

`EnableMouse(true)`; widgets already do click-select and wheel-scroll. Only addition: an
`Application.SetMouseCapture` that, on `MouseLeftDown`, focuses the panel whose `Box.InRect`
holds the point, then lets the click through. `m` calls `EnableMouse(false)` at runtime and
really frees the mouse.

## Streaming

`TextView` is an `io.Writer`, but one write = one draw. Instead the run goroutine fills a
bounded `viewBuffer` (2000 lines + pending queue); a 30 ms ticker calls `QueueUpdateDraw`,
appends the batch with `Fprintln`, and only `SetText`s when the ring wrapped. It skips the
wake-up when nothing is pending, so an idle dashboard costs zero redraws. No native "at
bottom" check: follow comes from `GetScrollOffset` + inner height + my own line count (wrap
off, so 1 line = 1 row); scrolling up pauses it, `G`/`f` resumes.

## Verdict

Good fit. tview gives the widgets and layering; the panel/focus/context model is yours to
write, but stayed small and explicit. Watch the global input capture, and never call
`QueueUpdate` from the UI thread.
