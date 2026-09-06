# tui-04-bubbletea-palette

Spike 03's stack unchanged: Go 1.27.1, bubbletea v2.0.9, lipgloss v2.0.6,
bubbles v2.2.1, huh v2.0.3, bubblezone/v2 v2.0.0, plus `sahilm/fuzzy` v0.1.3
(already in spike 03's `go.sum`, as a huh dependency).

**LOC** 4282 (`wc -l *.go`) vs spike 03's 2856: the command system cost
**~1400 lines** — `actions.go` 598, `palette.go` 644, ~180 across the pipeline,
hint bar, help overlay and menu clicks.

**What the registry cost.** The 13 entries are 200 lines of struct literal —
`cluster.upgrade` is 18 of them, and adding it touched no other file: it
appeared in the palette, the customer menu, the hint bar and the help overlay by
itself. The expensive part is not the registry, it is the *surfaces*: fuzzy
ranking and "did you mean" 173 lines, palette parse and keys 338, palette and
menu rendering ~230. Paid once, and mostly for things spike 03 lacked.

The pipeline is 99 lines (`exec → resolve → Guard → confirm → fire`) replacing
spike 03's 30-line `action(name)` switch. Worth it: the type-the-name prompt,
the lock guard and `--check` reach the run view identically from `:install`,
from Enter on the row and from a hint click, because there is one path.

**Palette state machine.** There is none, deliberately. `parse()` re-reads the
buffer every keystroke and every frame and derives one of three stages —
verb / target / args — from the token under the caret. The only stored state is
the buffer, the selected row, the history and a Tab "stem" (needed because
completing the buffer destroys the query you were cycling). Rendering and Enter
consume the same `palQuery`, so the list cannot show one thing and run another.

**Rough edges.**
- The design says `Run(ctx, args) tea.Cmd`, but a `tea.Cmd` cannot mutate the
  model, so `Context` carries a `*model`. It works (Update holds `m` by value,
  the pipeline passes `&m`) but it is a back door out of the Elm loop.
- A package-level `var registry = []*Action{…}` is an initialisation cycle; the
  slice has to be filled in `init()`.
- Bubble Tea reports the space bar as `"space"`, not `" "` — spike 03 has the
  same latent bug in its run view.
- Hint relevance is "first three whose Guard passes". Fine here (the locked
  cluster offers unlock), but real priority wants a field.
- The pop-up caps at eight rows, so empty `:` shows `customer` and you scroll
  for `node` and `global`.
