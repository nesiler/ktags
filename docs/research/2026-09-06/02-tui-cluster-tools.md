# Terminal (TUI) infrastructure and cluster tools — survey (6 September 2026)

All figures pulled from the GitHub API / source files on the date above.

## 1. Comparison

| Project | ★ | Last release / push | Lang | TUI library | License | Mouse |
|---|---|---|---|---|---|---|
| [k9s](https://github.com/derailed/k9s) | 34.5k | v0.51.0 (2026-06-06) / push 2026-09-06 | Go | `derailed/tview` v0.8.5 + `derailed/tcell` v2 (forks) | Apache-2.0 | opt-in (`enableMouse`, default **false**) |
| [lazygit](https://github.com/jesseduffield/lazygit) | 82.1k | v0.65.0 (2026-09-05) | Go | `jesseduffield/gocui` (fork) + `gdamore/tcell/v3` | MIT | yes, **default true** |
| [lazydocker](https://github.com/jesseduffield/lazydocker) | 52.7k | v0.25.2 (2026-04-19) | Go | gocui | MIT | yes |
| [ansible-navigator](https://github.com/ansible/ansible-navigator) | 548 | v26.8.0 (2026-08-12) | Python | bare `curses` (own `ui_framework`) | Apache-2.0 | no |
| [kubetui](https://github.com/sarub0b0/kubetui) | 393 | v1.14.0 (2026-06-05) | Rust | ratatui 0.30 | MIT | partial |
| [kdash](https://github.com/kdash-rs/kdash) | 2.5k | v2.1.1 (2026-07-22) | Rust | ratatui 0.30 + crossterm 0.29 | MIT | limited |
| [oxker](https://github.com/mrjackwills/oxker) | 1.8k | push 2026-08-22 | Rust | ratatui 0.30 | MIT | yes |
| [gum](https://github.com/charmbracelet/gum) | 24.3k | v2.0.0 (2026-08-20) | Go | Bubble Tea | MIT | — |
| [huh](https://github.com/charmbracelet/huh) | 7.2k | v2.0.3 | Go | Bubble Tea | MIT | yes |
| [sshs](https://github.com/quantumsheep/sshs) | 1.6k | 4.8.0 (2026-08-28) | Rust | ratatui + tui-input | MIT | yes |
| [wander](https://github.com/robinovitch61/wander) (Nomad) | 480 | v1.1.0 (2024-02), **dead** | Go | Bubble Tea 0.24 | MIT | limited |
| [toolong](https://github.com/Textualize/toolong) | 3.9k | v1.4.0 (2024-03), **dead** | Python | Textual | MIT | yes |
| [posting](https://github.com/darrenburns/posting) | 12.4k | 2.10.0 (2026-03) | Python | Textual | Apache-2.0 | yes |
| [harlequin](https://github.com/tconbeer/harlequin) | 6.4k | v2.13.0 (2026-09-02) | Python | Textual | MIT | yes |
| [moulti](https://github.com/xavierog/moulti) | 166 | v1.34.1 (2025-08) | Python | Textual | MIT | yes |
| [awx-tui](https://github.com/ansible-community/awx-tui) | 32 | 2026.5.0 | Python | Textual | NOASSERTION | yes |
| [ansi-tui](https://github.com/3A2DEV/ansi-tui) | 6 | v0.1.0 (2026-04) | TS | Ink | MIT | — |

Libraries: [tview](https://github.com/rivo/tview) 14.1k MIT · [Bubble Tea](https://github.com/charmbracelet/bubbletea) 44.8k MIT (v2.0.9) · [ratatui](https://github.com/ratatui/ratatui) 22.5k MIT · [Textual](https://github.com/Textualize/textual) 37.2k MIT · gocui fork BSD-3-Clause.

## 2. Architecture notes

### k9s — table + `:` command bar
`go.mod` proves the tview/tcell forks. Clean layering: `internal/dao/` (data access), `internal/model/`
(state), `internal/render/` (row rendering), `internal/ui/` (widgets), `internal/view/` (screens).
`model/stack.go` + `ui/crumbs.go`: navigation stack + breadcrumbs (Escape goes back for free).
`ui/prompt.go` + `model/fish_buff.go`: fish-shell style inline suggestions (`Suggester` interface).
`ui/table.go`: `ColorerFunc` + `DecorateFunc` for row colouring, sorting and filtering in one widget.
Plugin system: `plugins.yaml` with shortcuts, `$NAME/$NAMESPACE/$CONTEXT` substitution, typed inputs,
background execution and confirmation dialogs. Mouse was enabled by default in v0.21.10 and
**reverted** in v0.22.0 because of side effects (`change_logs/release_v0.22.0.md`).

### lazygit — panel focus model + streaming long output
`pkg/gui/types/context.go`: `ContextKind` enum (`SIDE_CONTEXT`, `MAIN_CONTEXT`, `PERSISTENT_POPUP`,
`TEMPORARY_POPUP`, `EXTRAS_CONTEXT`, `GLOBAL_CONTEXT`, `DISPLAY_CONTEXT`); each context carries its own
keybindings; "window" and "view" are separate concepts.
`pkg/tasks/tasks.go` — **`ViewBufferManager`**: the most valuable piece for us. Only one command at a
time, read only enough output to fill the panel, read more as the user scrolls; `THROTTLE_TIME = 30ms`,
`COMMAND_START_THRESHOLD = 10ms`, `stopCurrentTask()` blocks until the old task stops. `Cmd` interface
(`Wait/String/Terminate`) with PTY implementations (`pkg/gui/pty.go`). Mouse default on; config
comment warns that text selection then needs the Option key on macOS.

### ansible-navigator — play/task tree (the only serious Ansible TUI)
Bare curses with its own `ui_framework`. Two modes: `interactive` and `--mode stdout`. `:` commands.
Streaming (`runner/command_async.py`): `ansible_runner.run_command_async(..., event_handler)` in a
thread; each event is deep-copied into a `queue.Queue`; the UI thread drains it. **It does not parse
stdout; it consumes structured job events.** `actions/run.py`: play menu columns `__play_name`,
`__task_count`, `__progress`, `__ok`, `__failed`, `__unreachable`, `__changed`; `RESULT_TO_COLOR`
regex table. Drill-down playbook → play → task → task result JSON. Runs inside an Execution
Environment (podman/docker) by default; `--execution-environment false` disables.

### Others
kdash/kubetui/oxker: ratatui immediate-mode, every frame redrawn; not portable to Go but layout ideas
(kdash single-screen overview + tabs) are. gum/huh: script helpers, not a TUI; `huh` can be imported
directly for pre-run forms. moulti: CLI-fed TUI with **collapsible step blocks**, self-described as
Ansible-friendly. Headlamp (7.2k) and Rancher have **no TUI equivalent**; k9s is the closest.
Gap: `gh search repos "ansible tui"` finds nothing beyond ansible-navigator above 33 stars.
**The Ansible + cluster-fleet TUI niche is effectively empty.**

## 3. Conclusions

### Closest relatives
1. **k9s** — same problem space, same language, Apache-2.0; table + `:` bar + navigation stack maps
   directly onto customer → nodes → run log.
2. **lazygit** — same language, MIT, the most mature "stream a long-running command into a panel"
   solution (`ViewBufferManager`).
3. **ansible-navigator** — the only real Ansible TUI; Python (not reusable as code) but the play/task
   data model and event-queue architecture are copyable design.

### Reusable pieces
| Piece | Source | License | Use |
|---|---|---|---|
| `ViewBufferManager` + throttling + `stopCurrentTask` | lazygit `pkg/tasks/tasks.go` | MIT | stream playbook output |
| `Cmd` interface + `pty.go` | lazygit | MIT | run `ansible-playbook` in a PTY, keep colours |
| `ContextKind` panel/popup focus model | lazygit `pkg/gui/types/context.go` | MIT | layout + per-context keys |
| `Suggester` + fish-buff command bar | k9s `internal/ui/prompt.go`, `model/fish_buff.go` | Apache-2.0 | `:` command palette |
| `ColorerFunc`/`DecorateFunc` table, `stack.go`/`crumbs.go` | k9s | Apache-2.0 | tables, breadcrumb navigation |
| `plugins.yaml` schema | k9s | Apache-2.0 | team runbook shortcuts |
| play/task column model + colour regex table | ansible-navigator `actions/run.py` | Apache-2.0 (design) | play/task tree |
| event handler → queue → UI thread | ansible-navigator `runner/command_async.py` | Apache-2.0 (design) | Go: callback plugin → channel |
| `huh` forms | charmbracelet | MIT | pre-run parameter/confirm forms |
| collapsible step blocks | moulti | MIT (idea) | one collapsible block per task |

No GPL in the list. Apache-2.0 copies require NOTICE + change statements. `awx-tui` license is
NOASSERTION: do not copy.

### Patterns to avoid
- Mouse on by default without a toggle: k9s enabled it in v0.21.10 and reverted in v0.22.0.
- Parsing stdout with regexes: even ansible-navigator moved to structured events.
- Holding all output in memory: lazygit's comment is explicit; read windowed/lazy.
- Bubble Tea for a multi-panel, constantly streaming k9s-style screen: wander (Bubble Tea, Nomad) is
  dead since 2024; the Elm loop excels at single-flow forms. Hybrid: **tview main TUI + huh forms**.
- Writing your own curses/widget framework (ansible-navigator's `ui_framework` is the cost example).
- Porting Rust ratatui code: immediate-mode does not map onto tview's retained mode; take layout only.
- Mandatory container/EE: navigator's podman default is its most complained-about friction.
