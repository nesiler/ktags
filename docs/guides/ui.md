# UI guide — the interaction standard for the ktags TUI

Binding for every `internal/tui` change. Built from the four spikes (`spikes/*/NOTES.md`), the
command-system design (`docs/design/command-system.md`) and the maintainer's choices: spike 03's
look, spike 01's bottom hint bar, spike 04's command system. Points still open are marked
**Open:** and are settled by `type:decision` issues, not by the first PR that touches them.

## 1. Principles

1. **Nothing to memorise.** Every action is reachable three ways: `:` palette, context menu on the
   selected item, hint bar. No per-action letter shortcuts. The help overlay (`?`) is generated from
   the action registry, never hand-written.
2. **One pipeline.** Palette, menu and hint click run the same steps: resolve target → guard →
   danger prompt → run. If the three entry points can behave differently, the design is wrong.
3. **The screen never lies.** Every long operation shows progress from the real event stream; a
   stale value is marked stale (age shown), never displayed as current.
4. **Dangerous things are slow on purpose.** Production actions require typing the customer name;
   nothing destructive has a one-key path.
5. **Errors say the next step.** Every error dialog shows what failed, the measured cause and the
   next command (`development.md §4`).
6. **Keyboard complete, mouse welcome.** Everything works without a mouse; with the mouse on,
   everything clickable looks clickable.

## 2. Screens and states

| Screen | Purpose | Enter from | Leave with |
|---|---|---|---|
| Dashboard | customers (left), nodes of the selected customer (right top), health checks (right bottom) | start | `q` quits (confirm if a run is active) |
| Run view | streaming log of one run + play/task tree, summary table at the end | any long action | `Esc` backgrounds the run (dashboard shows spinner + run badge); `Ctrl-C` asks "Abort run?" |
| Form | multi-field input (add customer, add node, export/import) | palette/menu action with args that need a form | `Esc` cancels with confirm if dirty; submit shows a summary → confirm |
| Dialog | confirm, type-the-name, error, secret reveal | pipeline | `Esc`/`n` cancels; `Enter`/`y` confirms |
| Palette | `:` command bar with suggestions above it | `:` anywhere except a form field | `Esc` closes; `Enter` runs |
| Help | generated from the registry, grouped by target kind | `?` | any key |

States a customer row can show: environment badge (`prod` red, `staging` yellow, `test` grey),
health dot (ok / warn / fail / unknown-stale), lock icon with owner, "run in progress" spinner,
maintenance-mode marker. States are computed in `core`, not in the view.

Top bar: app name and version, selected customer / environment, active run indicator, mouse
state. Bottom: hint bar (one line) and the command bar line.

## 3. Keys

Global (work everywhere except inside a text field):

| Key | Meaning |
|---|---|
| `:` | open the palette |
| `?` | help overlay |
| `Tab` / `Shift-Tab` | move focus between panels; focused panel has a highlighted border and title |
| `↑ ↓` `PgUp PgDn` `Home End` | move within the focused panel |
| `Enter` or `Space` | context menu for the selected item |
| `Esc` | close the topmost thing (menu, dialog, palette, run view → background) |
| `m` | toggle mouse (so terminal text selection works); state shown in the top bar |
| `q` | quit from the dashboard (asks if a run is active) |
| `Ctrl-C` | in the run view: "Abort run? [y/N]"; elsewhere same as `q` |

Rules: no other single-letter global keys. Vim-style `j/k` are accepted as aliases for `↑/↓` in
lists (silent, not shown). Keys shown in the hint bar are the only keys that exist.

## 4. Mouse

- On by default; `m` toggles. Click selects rows and focuses the panel; wheel scrolls the panel
  under the pointer; click on a hint runs it; right-click or double-click on a row opens the
  context menu.
- Hit-testing uses bubblezone marks on strings **we** render. Third-party bubbles that render
  themselves are wrapped so their rows are ours (spike 03 lesson: `bubbles/table` and `list` have
  no mouse code, and container-offset arithmetic breaks when the inner viewport scrolls).
- Zones are scanned once at the root per frame. Every panel is padded to a rectangle before
  marking (a short last line made zones dead in spike 03).

## 5. Palette

- Empty `:` lists all actions grouped by target kind (customer, node, global) with help lines.
- Typing filters by fuzzy match on verb, aliases and title; best match pre-selected; `Tab`
  completes and cycles; `↑ ↓` move; `Enter` runs; `Esc` closes.
- Stage is derived from the caret each keystroke (verb → target → args); there is no separate
  state machine (spike 04 lesson). The list and `Enter` consume the same parsed query.
- Target search after a space lists matching customers/nodes; the current selection is the default
  and is shown inline (`install → acme (prod)`).
- `--` offers the action's arguments with completion. Unknown verb → "no such action" plus the
  three closest verbs. History with `↑` on an empty input, persisted per operator.

## 6. Confirmations and danger levels

| Level | Used for | Prompt |
|---|---|---|
| none | read-only actions | runs immediately |
| confirm | changes on non-prod | `[y/N]` with a one-line summary of what will run |
| type-name | any change on `prod`; node remove, decommission, secret rotate on any env | "Type the customer name to continue" — `--yes` never applies |

The prompt shows: customer, environment, action, targets (nodes), `--check` if set, and the lock
state. Locked customer → the guard fails before the prompt with the owner and reason.

## 7. Run view

- Lines come from the structured event stream (play, task, per-host result), pre-styled in
  `Update`, appended to a viewport; auto-scroll only while at the bottom; scrolling up pauses it
  and shows "paused, `End` to follow".
- Left: play/task tree with live ok/changed/failed counts; collapsible. Right: log.
- A generation counter drops messages of a superseded run. `Esc` backgrounds the run; the dashboard
  shows a spinner and the run badge; reopening returns to the same view.
- End of run: the `result.json` summary as a table (checks with ok/warn/fail and detail), the
  duration, the log path, and the next suggested action.
- Never hold more than the visible window plus a bounded backlog in memory; the full log is on disk
  and openable (`o` in the run view opens it with `$PAGER`; the one context-local key allowed).

## 8. Forms

- `huh` forms embedded in the Elm loop; `Esc` reserved by the host (huh only quits on `Ctrl-C`).
- Validation inline per field, on blur; the submit button stays disabled until valid.
- Dynamic fields (jump host appears when access = jump) are their own group with a hide function.
- Repeatable groups (nodes) are a list with "add another"; each entry is edited in a sub-form.
- Password fields masked; a value entered is never echoed back in the summary (`•••••`).
- Submit shows a summary dialog; confirmation follows §6.

## 9. Visual rules

- Adaptive colours: query the terminal background once and pick a light or dark palette. Never a
  light-only or dark-only hard-coded set.
- Semantic colours only: ok (green), warn (yellow), fail (red), prod (red badge), muted (grey),
  accent (one). No decorative colour.
- Minimum terminal 80×24. Below it the app shows a one-line message and waits for a resize.
- Text is truncated with `…` using width-aware functions (`x/ansi`); tables never wrap.
- Timestamps are relative in tables ("2h ago") with the absolute value on hover/detail; ages
  beyond a threshold render as stale (grey + "stale").
- No emoji in the UI; Unicode box drawing and a small set of symbols (● ○ ✓ ✗ ⚠ 🔒 is allowed
  only if the terminal reports wide-character support; otherwise `L`).

## 10. Performance and structure

- `Update` never blocks: IO, SSH, subprocesses and file reads run in `tea.Cmd`s and report back as
  messages. A frame must render in under 16 ms on the 4-customer fake dataset.
- `Update` is split: global messages → mouse → key → dispatch by screen and overlay. Each screen
  swallows what it owns (spike 03 lesson).
- The `Context` handed to actions carries the model pointer for the pipeline only; screens do not
  reach into each other.
- Space key arrives as `"space"` in Bubble Tea v2 — handle it by name (spike 04 lesson).

## 11. Tests (see `testing.md §4`)

- Every screen has a `teatest/v2` golden at 100×30 with colours disabled; goldens are updated only
  with `-update` and reviewed as diffs.
- Every key in §3 and every palette stage has a model-level test (`Update` with a synthetic
  message, assert on the model, not on the frame).
- The pipeline has a test proving that palette, menu and hint reach the same confirmation for the
  same action (spike SPEC §"What the mock must prove").
- The "extra action" test: adding a registry entry in the test makes it appear in palette, menu,
  hints and help with no other change.

## 12. Open points (options, not rules)

- **Open: mouse default.** A: on with `m` toggle (spikes, k9s had to revert once). B: off by
  default, `m` turns it on. Taste decision for the maintainer.
- **Open: dashboard density.** A: three panels always (spike 03). B: customer list + one detail
  panel with tabs (nodes / health / runs). Depends on how many day-2 panels arrive.
- **Open: run history in the TUI.** A: a "Runs" panel per customer with the last N runs and their
  result. B: only the last run summary on the dashboard, history via CLI.
- **Open: theme configuration.** A: none in v1. B: `theme.yaml` with the semantic colours. Not
  before the palette is stable.
