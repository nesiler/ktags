# UI guide — the interaction standard for the ktags TUI

Binding for every `internal/tui` change. Accepted as ADR-0003. It records the interaction rules
retained from the local prototypes and the maintainer's choices. A change to a rule here needs a
`type:decision` issue, not the first PR that touches it.

## 1. Principles

1. **Nothing to memorise.** Every action is reachable three ways: `:` palette, context menu on the
   selected item, hint bar. No per-action letter shortcuts. The help overlay (`?`) is generated from
   the action registry, never hand-written.
2. **One pipeline.** Palette, menu and hint click run the same steps: resolve target → guard →
   danger prompt → start the run in the service. If the three entry points can behave differently,
   the design is wrong.
3. **The screen never lies.** Every long operation shows progress from the real event stream; a
   stale value is marked stale (age shown), never displayed as current.
4. **Dangerous things are slow on purpose.** Production actions require typing the customer name;
   nothing destructive has a one-key path.
5. **Errors say the next step.** Every error shows what failed, the measured cause and the next
   command (§8, `development.md §4`).
6. **Keyboard complete, mouse welcome.** Everything works without a mouse; with the mouse on,
   everything clickable looks clickable.
7. **The service owns the work.** The TUI is a client of the local service (ADR-0001). Closing it
   never stops a run; reopening it shows the same runs and results.

## 2. Screens

| Screen | Purpose | Enter from | Leave with |
|---|---|---|---|
| Fleet | customer list (left) and a detail panel for the selected customer with tabs **Nodes · Health · Runs** (right) | start | `q` quits the client; running actions continue in the service |
| Run view | streaming log of one run + play/task tree, result table at the end | any long action; Runs tab; run badge | `Esc` returns to the fleet while the run continues; `Ctrl-C` asks "Cancel run?" |
| Form | multi-field input (add customer, add node, export/import) | palette/menu action with args that need a form | `Esc` cancels with confirm if dirty; submit shows a summary → confirm |
| Dialog | confirm, type-the-name, error, secret reveal | pipeline | `Esc`/`n` cancels; `Enter`/`y` confirms |
| Palette | `:` command bar with suggestions above it | `:` anywhere except a form field | `Esc` closes; `Enter` runs |
| Help | generated from the registry, grouped by target kind | `?` | any key |

Detail tabs:

- **Nodes** — nodes of the customer with role, address, state and last contact age.
- **Health** — checks with ok / warn / fail / missed, age and runbook; stale checks greyed.
- **Runs** — the customer's recent runs (action, started, duration, result, who); `Enter` opens the
  run view, which resumes a running stream or shows a finished run's stored result.

New day-2 views (backups, jobs, versions) arrive as further tabs, never as additional fixed panels.

States a customer row can show: environment badge (`prod` red, `staging` yellow, `test` grey),
health symbol (ok / warn / fail / missed / stale / never checked), lock icon with owner, "run in
progress" spinner, maintenance-mode marker. States are computed in `core`, not in the view.

Top bar: app name and version, service connection state, selected customer / environment, active
run badge, mouse state. Bottom: hint bar (one line) and the command bar line.

## 3. Connection, loading, empty and stale states

| State | The screen shows |
|---|---|
| connecting | the fleet frame with "connecting to the ktags service…"; no value is drawn as current |
| connected | normal screens |
| reconnecting | a banner "service connection lost — reconnecting"; data stays visible, greyed, with its age; actions are disabled and greyed in the hint bar |
| service unavailable | an error (§8) with the next command, e.g. `ktags service start`; `Enter` retries, `q` quits |
| protocol mismatch | the upgrade instruction returned by the service; no data is rendered |
| empty | one line saying there are no customers yet; the hint bar offers `customer add` |
| loading a tab | a spinner in the panel title naming what loads; previous content stays, marked stale if old |
| stale | grey value with its age ("stale · 5h"); a check older than its interval is stale |
| missed | a scheduled run that did not happen (laptop asleep, service stopped); as ADR-0001 requires, it is never run late. The count stays with the check: a stale check shows its last result greyed with its age plus the word "missed" (CLI "stale, N missed"), and a check that has since run fresh keeps its current value plus the count (CLI "fresh, N missed"). A missed run is never shown as ok or as completed |

Reconnect keeps the last event cursor of every open run and resumes the stream from it
(ADR-0002): no duplicate and no missing lines. If the cursor is no longer valid, the run view
reloads the run from the start and says so.

## 4. Keys

Global (work everywhere except inside a text field):

| Key | Meaning |
|---|---|
| `:` | open the palette |
| `?` | help overlay |
| `Tab` / `Shift-Tab` | move focus between the list and the detail panel; focused panel has a highlighted border and title |
| `←` / `→` | switch tabs while the detail panel has focus |
| `↑ ↓` `PgUp PgDn` `Home End` | move within the focused panel |
| `Enter` or `Space` | context menu for the selected item |
| `Esc` | close the topmost thing (menu, dialog, palette; run view → fleet) |
| `m` | toggle mouse (so terminal text selection works); state shown in the top bar |
| `q` | quit the client from the fleet screen; if runs are active, the exit line says they continue |
| `Ctrl-C` | in the run view: "Cancel run?"; elsewhere same as `q` |

Rules: no other single-letter global keys. Vim-style `j/k` are accepted as aliases for `↑/↓` in
lists (silent, not shown). Keys shown in the hint bar are the only keys that exist. Cancelling a
run is the registered cancel action and goes through the pipeline, including §7 confirmation.

## 5. Mouse

- On by default; `m` toggles. Click selects rows and focuses the panel; click on a tab title
  switches the tab; wheel scrolls the panel under the pointer; click on a hint runs it;
  right-click or double-click on a row opens the context menu.
- Hit-testing uses bubblezone marks on strings **we** render. Third-party bubbles that render
  themselves are wrapped so their rows are ours (spike 03 lesson: `bubbles/table` and `list` have
  no mouse code, and container-offset arithmetic breaks when the inner viewport scrolls).
- Zones are scanned once at the root per frame. Every panel is padded to a rectangle before
  marking (a short last line made zones dead in spike 03).

## 6. Palette

- Empty `:` lists all actions grouped by target kind (customer, node, global) with help lines.
- Typing filters by fuzzy match on verb, aliases and title; best match pre-selected; `Tab`
  completes and cycles; `↑ ↓` move; `Enter` runs; `Esc` closes.
- Stage is derived from the caret each keystroke (verb → target → args); there is no separate
  state machine (spike 04 lesson). The list and `Enter` consume the same parsed query.
- Target search after a space lists matching customers/nodes; the current selection is the default
  and is shown inline (`install → acme (prod)`).
- `--` offers the action's arguments with completion. Unknown verb → "no such action" plus the
  three closest verbs. History with `↑` on an empty input, persisted per operator.
- Every palette verb is also a CLI subcommand with the same name and arguments.

## 7. Confirmations and danger levels

| Level | Used for | Prompt |
|---|---|---|
| none | read-only actions | runs immediately |
| confirm | changes on non-prod, including cancelling a non-prod run | `[y/N]` with a one-line summary of what will run |
| type-name | any change on `prod`, including cancelling a prod run; node remove, decommission, secret rotate on any env | "Type the customer name to continue" — `--yes` never applies |

The prompt shows: customer, environment, action, targets (nodes), `--check` if set, and the lock
state. Locked customer → the guard fails before the prompt with the owner and reason.

## 8. Errors

Every error states **what failed**, the **measured cause** and the **next command**. It never
shows a stack trace or secret material; details are one action away ("show log").

| Where the error happens | Where it appears |
|---|---|
| a form field is invalid | inline under the field, on blur |
| the action is refused before it starts (guard, lock, confirmation, service refusal) | a dialog; nothing was started |
| the run fails | the run result table: failing check, host, runbook and next action |
| the service connection fails | the connection states of §3, not a dialog per request |

## 9. Run view

- Lines come from the service's event stream (play, task, per-host result), pre-styled in
  `Update`, appended to a viewport; auto-scroll only while at the bottom; scrolling up pauses it
  and shows "paused, `End` to follow".
- Left: play/task tree with live ok/changed/failed counts; collapsible. Right: log.
- A generation counter drops messages of a superseded run. `Esc` returns to the fleet; the top bar
  shows the run badge; reopening from the badge or the Runs tab returns to the same view.
- End of run: the stored result as a table (checks with ok/warn/fail and detail), the duration,
  the log location, and the next suggested action.
- Never hold more than the visible window plus a bounded backlog in memory; the full log stays in
  the run record and is openable (`o` in the run view opens it with `$PAGER`; the one
  context-local key allowed).

## 10. Forms

- `huh` forms embedded in the Elm loop; `Esc` reserved by the host (huh only quits on `Ctrl-C`).
- Validation inline per field, on blur; the submit button stays disabled until valid.
- Dynamic fields (jump host appears when access = jump) are their own group with a hide function.
- Repeatable groups (nodes) are a list with "add another"; each entry is edited in a sub-form.
- Password fields masked; a value entered is never echoed back in the summary (`•••••`).
- Submit shows a summary dialog; confirmation follows §7.

## 11. Visual rules and accessibility

- Adaptive colours: query the terminal background once and pick a light or dark palette. Never a
  light-only or dark-only hard-coded set. No theme configuration in v1.
- Semantic colours only: ok (green), warn (yellow), fail (red), prod (red badge), muted (grey),
  accent (one). No decorative colour.
- Colour never carries a state alone: every state also has a symbol or a word. With `NO_COLOR`
  set the screen is monochrome and still fully usable. Nothing blinks.
- Minimum terminal 80×24. Below it the app shows a one-line message and waits for a resize.
- Text is truncated with `…` using width-aware functions (`x/ansi`); tables never wrap.
- Timestamps are relative in tables ("2h ago") with the absolute value in the detail; ages
  beyond a threshold render as stale (grey + "stale").
- No emoji in the UI; Unicode box drawing and a small set of symbols (● ○ ✓ ✗ ⚠). 🔒 is allowed
  only if the terminal reports wide-character support; otherwise `L`.

## 12. Performance and structure

- `Update` never blocks: service calls and file reads run in `tea.Cmd`s and report back as
  messages. The TUI never runs SSH, Ansible or other subprocesses; the service does (ADR-0001).
  A frame must render in under 16 ms on the 15-customer fixture.
- `Update` is split: global messages → mouse → key → dispatch by screen and overlay. Each screen
  swallows what it owns (spike 03 lesson).
- The `Context` handed to actions carries the model pointer for the pipeline only; screens do not
  reach into each other.
- Space key arrives as `"space"` in Bubble Tea v2 — handle it by name (spike 04 lesson).

## 13. Tests (see `testing.md §1` TUI layer and `§5`; golden pitfalls in `§4`)

- Every screen and every state of §3 has a `teatest/v2` golden at 100×30 with colours disabled;
  goldens are updated only with `-update` and reviewed as diffs.
- Every key in §4 and every palette stage has a model-level test (`Update` with a synthetic
  message, assert on the model, not on the frame).
- The pipeline has a test proving that palette, menu and hint reach the same confirmation for the
  same action.
- The "extra action" test: adding a registry entry in the test makes it appear in palette, menu,
  hints, help and the CLI with no other change.
- A reconnect test: a synthetic service drops the connection mid-run; the run view resumes from
  the cursor without duplicate or missing lines.

## 14. Decided points (ADR-0003)

- Mouse on by default, `m` toggles.
- Fleet list plus one tabbed detail panel; no third fixed panel.
- Run history in the TUI through the Runs tab.
- No theme configuration in v1.
- No further prototype pass; the Phase 0 shell is the proof.
