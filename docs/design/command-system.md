# Command system design (draft, 2026-09-06)

Problem: the set of operations (install, health, backup, node add, secret show, lock, …) will keep
growing. We cannot assign a letter to each, and users must not memorise flags. Everything the UI
offers must therefore be **derived from one registry of actions**, never hard-wired into screens.

## 1. Action registry

```go
type Kind string // "customer", "node", "secret", "" (no target)

type Action struct {
    ID       string   // "cluster.install"
    Verb     string   // what the user types: "install"
    Aliases  []string // "i", "inst"
    Title    string   // "Install / reconcile cluster"
    Help     string   // one line shown in palette and help overlay
    Target   Kind     // which selected object it acts on ("" = none)
    Args     []Arg    // optional flags: {Name:"check", Type:Bool, Help:"dry run"}
    Danger   Level    // None | Confirm | TypeName (prod: type the customer name)
    Long     bool     // runs in the background with a streaming run view
    Guard    func(ctx Context) error // e.g. "locked by ayse: maintenance window"
    Run      func(ctx Context, args Values) tea.Cmd
}
```

Registration is a list in one file. Adding an operation = adding one entry. No screen code changes.

## 2. Surfaces derived from the registry

1. **Command bar** (`:`). A palette, not a plain input:
   - Typing filters actions by fuzzy match on verb, aliases and title. Matches are listed above the
     input with their help line; the best match is pre-selected; `Tab` completes the verb (cycles on
     repeated `Tab`), `↑/↓` moves, `Enter` runs.
   - If the action needs a target and one is already selected in the dashboard (the focused
     customer or node), the palette shows it inline: `install → acme (prod)`. If not, or if the user
     wants another, typing a space after the verb switches the palette into **target search**: it
     lists matching customers/nodes with the same fuzzy filter. `Enter` picks it.
   - After the target, typing `--` offers the action's args with completion (`--check`).
   - Unknown verb: no match message plus the three closest suggestions.
   - `Esc` closes. History with `↑` when the input is empty.
2. **Context menu** on the selected item: `Enter`, `Space` or right-click on a customer/node row
   opens a small menu listing every action whose `Target` matches that kind, with the same help
   lines. So a user who does not know any verb can still do everything by selecting things.
3. **Hint bar** (bottom, spike-01 style): shows the global keys (`:` command, `Enter` actions,
   `Tab` focus, `?` help, `m` mouse, `q` quit) plus up to three most relevant actions for the
   focused item. Clickable. Never shows more than fits on one line; the rest is behind `Enter`.
4. **Help overlay** (`?`): generated from the registry, grouped by target kind, with verbs, aliases,
   args and danger level.

There are **no per-action letter shortcuts** by default. A user may bind their own later
(`keys.yaml`, k9s style); the registry makes that trivial.

## 3. Execution pipeline (same for palette, menu and hint clicks)

resolve target → `Guard` → danger prompt (y/N or type the name) → if `Long`: open run view with
streaming log and task tree, keep the dashboard state, show spinner in the status bar; else: run
inline and toast the result. Every step is one function so the three entry points cannot diverge.

## 4. Dashboard (kept from spike 03, with spike-01 hints)

Left: customers. Right top: nodes of the selected customer. Right bottom: health checks. Top bar:
app name, customer/env, run spinner. Bottom: hint bar + command bar (one line; the palette pops
up above it when open). Mouse on by default, `m` toggles. Colours and layout of spike 03.

## 5. What the mock must prove

- Adding a new action (say `cluster.upgrade`) to the registry makes it appear in palette, context
  menu, hints and help with zero other changes. The mock should include one deliberately "extra"
  action to demonstrate this.
- A user can complete `install` on `acme` three ways: `:inst<Tab> <Enter>`, `Enter` on the acme row
  → menu, and clicking the hint. All three go through the same confirm step (type the name, prod).
- Target search: `:health glo<Enter>` runs health on globex even though acme is selected.
- The palette suggests when the user is lost: empty `:` shows the full list grouped by kind.
