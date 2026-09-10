# ADR 0003: Operator interaction standard

## Context

ktags has two clients, a CLI and a full-screen TUI, over one local service (ADR-0001) that owns
actions, scheduling and durable runs (ADR-0002). Four local prototypes settled most interaction
rules, and `docs/guides/ui.md` records them. Four points were open: mouse default, dashboard
density, run history in the TUI and theme configuration. The local service also changed what
leaving the TUI means, so reconnect behaviour and connection states needed rules.

## Decision

`docs/guides/ui.md` is the binding interaction standard. Its main rules are:

1. **Navigation frame.** One fleet screen: a customer list on the left and one detail panel for
   the selected customer with tabs (Nodes, Health, Runs). New day-2 views arrive as tabs, never as
   additional fixed panels. Run view, forms, dialogs, palette and help are the only other screens.
2. **Actions.** Every action comes from the one action registry and is reachable from the `:`
   palette, the context menu of the selected item and the hint bar. The same registry generates
   help and the CLI subcommands. There are no per-action letter shortcuts. All entry points use
   the same pipeline: resolve target, check guards, confirm by danger level, then start the run
   in the service.
3. **Confirmations.** There are three danger levels. Read-only actions run immediately. Changes
   outside production ask `[y/N]` with a one-line summary. Any change on production, and node
   removal, decommission and secret rotation anywhere, require typing the customer name.
   `--yes` never replaces a typed name.
4. **Runs and reconnect.** Leaving the run view or quitting the TUI detaches from the run; the run
   continues in the service. Cancellation is an explicit, confirmed action. Reopening a run from
   the Runs tab or the run badge resumes the event stream from the client's cursor. A finished
   run shows its stored result.
5. **Errors.** Every error states what failed, the measured cause and the next command. Field
   errors appear inline, refusals before a run appear in a dialog, and failures during a run
   appear in the run result with the failing check and runbook. Service connection problems are
   screen states, not repeated dialogs.
6. **States.** Connecting, reconnecting, service unavailable, protocol mismatch, empty, loading
   and stale each have a defined presentation. A stale value always shows its age and is never
   drawn as current. A check missed while the laptop slept is shown as missed.
7. **Mouse.** Mouse support is on by default; `m` toggles it so that terminal text selection
   works. The top bar shows the mouse state.
8. **Accessibility baseline.** Colour never carries a state alone; every state also has a symbol
   or word. `NO_COLOR` gives a monochrome screen that stays fully usable. The minimum terminal size
   is 80×24.
9. **Theme.** No theme configuration in v1. The palette adapts to a light or dark terminal
   background.

A further prototype pass is not needed. The Phase 0 TUI shell is built on synthetic adapters
against this standard.

### Daily scenario

1. The operator opens `ktags`. The fleet list loads from the service; one customer shows a red
   health dot and another a grey "stale · 5h" marker because the laptop was asleep.
2. They select the red customer. The Health tab shows the failing check, its age and the runbook.
3. They open the palette, type `health`, and confirm the target shown inline. The check starts in
   the service and the run view streams its events.
4. They press `Esc` to return to the fleet and quit the TUI. The service keeps running the check.
5. Later they open `ktags` again. The run badge and the Runs tab show the same run. Opening it
   resumes the stream or shows the stored result table with the next suggested action.

## Consequences

The TUI never runs SSH, Ansible or other subprocesses; it renders service data and sends
actions. Tests need goldens for the connection and staleness states as well as the normal screens.
Quitting no longer needs a confirmation for active runs, but cancellation does. Adding a tab is a
UI change under this ADR; adding a fixed panel or a per-action key needs a new ADR.

## Alternatives considered

- Three fixed panels (customers, nodes, health), as in prototype 03: dense, but it does not fit
  80×24 once runs and scheduled jobs arrive.
- Mouse off by default: keeps terminal selection, but hides the clickable hint bar and rows.
- Last-run summary only, with history through the CLI: simpler, but ADR-0001 and ADR-0002 already
  promise that a run can be reopened after leaving.
- A `theme.yaml` in v1: premature before the palette is stable.
