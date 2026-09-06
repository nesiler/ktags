# TUI libraries — market research (6 September 2026)

Method: figures pulled live from GitHub / npm / crates.io / PyPI / Go proxy APIs on the date above.
Reddit was unreachable from the research environment; HN, Lobsters and GitHub issues were used instead.

## Health

| Library | ★ | Last commit | Last release | License | 90d/30d commits | Bus factor |
|---|---|---|---|---|---|---|
| [Bubble Tea](https://github.com/charmbracelet/bubbletea) | 44,819 | 2026-08-19 | v2.0.9 | MIT | 15/11 | ~3 (Charm Inc.) |
| Bubbles / Lip Gloss / Huh | 8.9k/11.8k/7.2k | 2026-09-06 | v2.2.1 / v2.0.6 / v2.0.3 | MIT | active | ~3 |
| [tview](https://github.com/rivo/tview) | 14,084 | 2026-08-11 | v0.42.0 (**Aug 2025**) | MIT | 17/5 | **1** (rivo) |
| [gocui](https://github.com/jroimartin/gocui) | 10,596 | **2025-05-01** | — | BSD-3 | 0/0 | dead |
| [ratatui](https://github.com/ratatui/ratatui) | 22,515 | 2026-09-04 | 0.30.2 | MIT | 100+/43 | ~4 |
| [cursive](https://github.com/gyscos/cursive) | 4,845 | 2026-09-06 | 0.21.1 (**Aug 2024**) | MIT | 24/9 | 1 |
| [Ink](https://github.com/vadimdemedes/ink) | 39,822 | 2026-08-25 | v7.1.1 | MIT | 13/3 | 2 (now sindresorhus) |
| [OpenTUI](https://github.com/anomalyco/opentui) | 13,250 | 2026-09-06 | v0.5.10 | MIT | 100+/100+ | SST/Anomaly |
| [blessed](https://github.com/chjj/blessed) | 11,886 | 2024-03-22 | npm 0.1.81 (**2015**) | MIT-ish | 0/0 | dead |
| [Textual](https://github.com/Textualize/textual) | 37,156 | **2026-07-11** | v8.2.8 (Jun 26) | MIT | 21/**0** | **1** (McGugan) |

## Capability matrix

| | Mouse: widgets ready? | Password / forms | Streaming output | Tests | Single binary |
|---|---|---|---|---|---|
| **Bubble Tea v2** | Partly. `MouseClickMsg/WheelMsg`, `View.MouseMode`; `viewport` handles wheel but **`bubbles/table` and `list` have no mouse code** → [bubblezone](https://github.com/lrstanley/bubblezone) (911★) | Best: huh v2 `EchoModePassword`, Input/Select/MultiSelect/Confirm/FilePicker + `.Validate()` + accessible mode | `Program.Send()` from a goroutine | `teatest/v2`, "experimental, no backwards-compat promise" | GoReleaser `homebrew_casks` |
| **tview** | **Yes.** `Primitive` requires `MouseHandler()`; Table/List/Form handle click + 4-way scroll themselves | `Form.AddPasswordField()`, `SetMaskCharacter()`. **No multi-select, no form `Validate()`** | **`TextView` is an `io.Writer`** → `cmd.Stdout = textView` | none official; `tcell.SimulationScreen` | same |
| **ratatui** | No. Raw `MouseEvent`; hit-test with `Rect::contains` yourself | **No input/button/form in core.** 3rd party: `tui-input` 0.15.4, `ratatui-textarea` 0.9.2, `tuirealm` 4.1 | own tokio loop | `TestBackend` + insta | cargo-dist |
| **Ink v7** | **No, and will not come**: maintainer "I have no plans to add mouse support to Ink" ([#632](https://github.com/vadimdemedes/ink/issues/632)) | `@inkjs/ui` PasswordInput + MultiSelect, last release May 2024 | native | `ink-testing-library` (2024) | `bun build --compile` |
| **OpenTUI** | Yes, hit-test in native Zig, z-index aware | **No password masking** | native | `createTestRenderer` + `mockMouse` | **Bun + FFI required**, pre-1.0 |
| **Textual v8** | Yes, default on, `capture_mouse()` for drag | Richest: `Input(password=True)`, `MaskedInput`, `DataTable`, `SelectionList`, `RichLog`, validators | asyncio worker + `RichLog` | best: `pilot.click("#selector")` + snapshot tests | **weak**: PyInstaller / `uvx` |

## What is Claude Code written in?

**Ink (React), not OpenTUI**, now a heavily modified fork: Ink README lists Claude Code first
([source](https://github.com/vadimdemedes/ink#whos-using-ink)); Pragmatic Engineer, Sep 2025: "UI is
written in React, using the Ink framework", layout via Yoga, bundled with Bun
([source](https://newsletter.pragmaticengineer.com/p/how-claude-code-is-built)); the local 2.1.263
binary contains `ink-root`, `ink-box`, `ink-text` node names and 31 `yoga` strings, zero `opentui`;
Anthropic's bcherny: "We started by using Ink, and at this point it's our own framework"
([HN, Nov 2025](https://news.ycombinator.com/item?id=45902653)). OpenTUI belongs to SST/Anomaly
(Dax Raad), used by `opencode`.

## Known complaints

- **Charm / Bubble Tea** — [HN, Mar 2026](https://news.ycombinator.com/item?id=47268662): "an
  MVC-architecture cribbed from Elm. It completely takes over and rips apart my CLI structure";
  commercialisation worries (Gradient $6M round, [OpenCode→Crush](https://news.ycombinator.com/item?id=44741894)).
  Mouse-specific: "I haven't been able to simultaneously support both mouse wheel scrolling and the
  ability to select text" ([HN](https://news.ycombinator.com/item?id=47269609)) — a terminal
  limitation that applies to every full-screen TUI. v2 migration friction
  ([Lobsters](https://lobste.rs/s/1to8sq/charm_v2_major_releases_for_bubble_tea_lip)); open v2
  regressions [#1614](https://github.com/charmbracelet/bubbletea/issues/1614),
  [#1646](https://github.com/charmbracelet/bubbletea/issues/1646),
  [#1571](https://github.com/charmbracelet/bubbletea/issues/1571).
- **tview** — maintainer rivo, June 2026: "I should apologize for my absence… I've just been very very
  busy"; users drifting to the undocumented `ayn2op/tview` fork ([#1124](https://github.com/rivo/tview/issues/1124));
  95 open issues; no `ScrollView` primitive ([#1104](https://github.com/rivo/tview/issues/1104)).
- **ratatui** — former lead maintainer joshka: "If you're designing a new library, don't make the same
  mistakes that Ratatui makes… Ratatui handles just the output portion of an app"
  ([Lobsters](https://lobste.rs/s/rmga0q/bubbletea_rs_rust_implementation)); widget version-sync pain
  ([HN](https://news.ycombinator.com/item?id=45835465)).
- **Ink** — flicker / scrollback clearing visible through Claude Code
  ([HN](https://news.ycombinator.com/item?id=46853395), [#935](https://github.com/vadimdemedes/ink/issues/935),
  [#990](https://github.com/vadimdemedes/ink/issues/990)); no scrolling primitive since 2019
  ([#222](https://github.com/vadimdemedes/ink/issues/222)).
- **OpenTUI** — `@opentui/core` loads Zig via `bun:ffi`, does not run on Node.
- **Textual** — company wound down (confirmed, [7 May 2025](https://textual.textualize.io/blog/2025/05/07/the-future-of-textualize/));
  99 of the last 100 commits by one person, ~8 weeks without commits, 358 open issues, maintainer's
  focus moved to [Toad](https://willmcgugan.github.io/announcing-toad/); "hijacks the mouse… breaks
  text selection and copy/paste" ([HN](https://news.ycombinator.com/item?id=45211816)).

## Recommendation from this report

**1st: Go + Bubble Tea v2 + Bubbles v2 + Huh v2 + bubblezone.** Company-backed, weekly commits,
Charm runs its own 27.9k★ product Crush on `bubbletea/v2 v2.0.9`. Huh v2 is the best form layer.
Streaming via `Program.Send()`, tests via `teatest/v2`, Homebrew via GoReleaser. Cost: table/list
mouse hit-testing via bubblezone (about a day), absorbing v2 churn, Elm architecture dominating the CLI.

**2nd: Go + tview (+ tcell).** Fastest time-to-value: Table/List click and scroll work out of the box,
`AddPasswordField`, and `TextView` as `io.Writer` makes wiring a long Ansible run one line. Field proof:
**k9s is built on tview**. Second only because of maintainer absence, 13 months without a tagged
release, community fragmenting into forks. Still worth a **2-day spike first**; MIT and the
`derailed/tview` fork keep the risk manageable.

**Avoid:** blessed/neo-blessed (dead), gocui upstream (16 months idle), cursive (single maintainer,
2024 release), Ink (no mouse, ever), OpenTUI (no password masking, pre-1.0, Bun-locked), ratatui
(wrong layer unless Rust is required), Textual (bus factor 1 + Python single-binary story).

## Synthesis (decision taken after reading all three reports)

Requirements weighted: mouse, forms, streaming Ansible output, multi-panel dashboard, maintenance.
Decision: **Go + tview, validated by spikes against a Bubble Tea v2 variant.** tview's ready-made mouse
handling, `io.Writer` text view and k9s precedent outweigh its maintenance risk, which MIT licensing,
the k9s fork and a UI-agnostic core mitigate. If the spike hits a wall, Bubble Tea v2 is the fallback.
