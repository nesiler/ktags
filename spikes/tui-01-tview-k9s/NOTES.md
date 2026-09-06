# tui-01 — rivo/tview (k9s-style layout)

**Libraries:** `github.com/rivo/tview v0.42.0`, `github.com/gdamore/tcell/v2 v2.13.10`. Go 1.27.

**Lines of code** (`wc -l *.go`): dashboard 169, data 397, dialogs 286, form 309, main 471,
prompt 179, run 427 — **2238 total** (~1840 without `data.go`).

## Easy

Layout is the strong point: nested `Flex` plus `Pages` gave the whole k9s shell (header, list,
two tables, status line, overlay dialogs) in well under 200 lines, and terminal resizes re-layout
for free. `Table` with `SetFixed(1,0)` + `SetSelectable(true,false)` is exactly the k9s node table.
`Modal`, `Form` (`AddDropDown`, `AddPasswordField`) and `TreeView` are batteries-included.
Streaming is the nicest part: `TextView` implements `io.Writer`, is internally locked, and keeps
its own auto-scroll (`trackEnd`), so `fmt.Fprintf(logView, ...)` from a goroutine just works.

## Hard / awkward

- **No ghost-text input.** `InputField` only offers a drop-down autocomplete, so the fish/k9s inline
  suggestion is a hand-written `Prompt` primitive over `TextView` (own buffer, reverse-video cell as
  the cursor). Idea borrowed from k9s `ui/prompt.go`; code is original.
- **`Form` can only append.** Revealing "jump host" when access = jump means rebuilding the form
  from a draft struct. `Form.SetFocus(i)` only records an index — it must be called *before*
  `Application.SetFocus(form)`.
- **`Pages` routes keys by insertion order**, not by which page is on top: a form left underneath a
  modal kept a stale `hasFocus` and swallowed Enter. Fixed by removing the form page while the
  confirmation modal is up (see `form.go`).
- **`TextView` is only half thread-safe.** `Write` locks, but `GetWrappedLineCount`/`GetScrollOffset`
  do not — calling them from the status bar while the run goroutine wrote raced (caught with
  `-race`). The auto-scroll-paused indicator now tracks scroll gestures itself; `trackEnd` is private
  with no getter.
- `QueueUpdate` blocks until the main loop runs it, so `SetChangedFunc` cannot call it directly;
  a 1-slot channel + pump goroutine coalesces redraws (`App.redraw`).

## Mouse

`app.EnableMouse(true)`; `m` toggles and the state shows in the status bar. `List`/`Table`/`TextView`/
`TreeView` handle click-select and wheel natively. Header hints are clickable via
`SetRegions(true)` + `SetHighlightedFunc`; `TextView` steals focus on mouse-down, so focus is
restored explicitly.

## Verdict

Good fit for a k9s-style multi-panel app: composition, mouse and streaming are solid and the code
stays readable. The cost is imperative wiring — focus, redraws and thread-safety are yours to get
right, and anything beyond the stock widgets (the prompt) must be written by hand.
