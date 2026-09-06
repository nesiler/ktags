# tui-03-bubbletea

**Versions** (Go 1.27.1): bubbletea v2.0.9, lipgloss v2.0.6, bubbles v2.2.1,
huh v2.0.3 on the new `charm.land/...` paths (huh publishes *only* there),
bubblezone/v2 v2.0.0, `x/ansi` for width-safe truncation.

**LOC** (`wc -l *.go`): 2856 — main 706, runview 451, form 379, data 343,
view 273, styles 245, dialogs 237, mouse 222.

**Easy.** `JoinHorizontal/JoinVertical` over a hand-rolled `box()` built the whole
three-region dashboard. v2's per-frame `tea.View` struct is a real win: `AltScreen`
and `MouseMode` are fields on the returned view, so the `m` toggle needs no program
option. `tea.RequestBackgroundColor` + `BackgroundColorMsg.IsDark()` replaces
AdaptiveColor.

**Streaming.** No `io.Writer` anywhere. A goroutine walks a fake event script and
calls `prog.Send(runLineMsg{gen, ev})`; `Update` appends a pre-styled line to
`viewport.SetContentLines`, auto-scrolling only while `AtBottom()`. A generation
counter drops a superseded run's messages. Backgrounding is free: Esc just changes
`m.screen` while the goroutine keeps sending.

**Mouse / bubblezone.** ~220 lines plus marker calls in the views. bubblezone only
helps where *you* build the string: per-row customer zones work because the list
`ItemDelegate` writes the row itself. `bubbles/table` and `viewport` render
themselves, so you can only mark the container and convert clicks with
`ZoneInfo.Pos()` minus a header offset — which breaks if the inner viewport scrolls.
Two traps cost real time: `InBounds` is a box test that fails when `StartX > EndX`,
exactly what a multi-line block with a short last line produces (every panel zone was
silently dead until I padded blocks square); and `zone.Scan` must run once, at the
root — scanning twice clears the table. Zones are also recorded asynchronously, so the
first click on a brand-new zone can miss.

**Six screens in one Elm loop.** Workable, but only after splitting `Update` into
global messages → `handleMouse`/`handleKey` → dispatch on `screen` plus `overlay` and
a command-bar flag. The cost: every keystroke crosses several `switch`es and each
screen must swallow what it owns.

**huh.** Embeds, but not cleanly. `Form.Update` returns `huh.Model` (a v1-style
`View() string`), so every call needs `upd.(*huh.Form)`. Navigation happens through
internal messages returned as *commands* — forget to run them and nothing advances.
huh quits only on ctrl+c, so the host must reserve `esc` first. No repeatable groups:
"add another node" is a fresh `Form` per node behind a confirm, and the dynamic jump
host needs its own group with `WithHideFunc`. No mouse support.

**Verdict.** Good fit for k9s-style work: layout, theming and streaming are pleasant.
The price is that mouse hit-testing is a hand-maintained bolt-on, and every
third-party bubble is a black box for both clicks and keys.
