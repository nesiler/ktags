# TUI spike specification

Purpose: compare terminal UI libraries for **ktags**, a local (per-operator laptop) CLI + full-screen
TUI that installs and operates customer Kubernetes (RKE2) clusters through Ansible. These spikes are
**throwaway mock-ups**: no real SSH, no Ansible, no files written. Fake data only. What we are judging
is how each library handles layout, mouse, forms, streaming output and code size.

Every spike implements the same screens with the same fake data so they can be compared fairly.
Language: Go 1.27. Code, comments, commit messages and docs in English.

## Fake data (identical in every spike)

Customers (a "customer" is one isolated cluster):

| name    | environment | access | nodes                                  | last health | last backup | lock |
|---------|-------------|--------|----------------------------------------|-------------|-------------|------|
| acme    | prod        | direct | acme-srv-1 (server), acme-agt-1 (agent) | ok, 2h ago  | ok, 6h ago  | -    |
| globex  | staging     | jump   | globex-srv-1 (server)                  | WARN (disk 87%), 1d ago | none | -    |
| initech | test        | direct | initech-srv-1, initech-srv-2, initech-srv-3 (servers), initech-agt-1 (agent) | ok, 10m ago | ok, 1h ago | locked by ayse: "maintenance window" |
| umbrella| prod        | direct | umbrella-srv-1 (server)                | FAIL (1 node NotReady), 30m ago | ok, 12h ago | - |

Node table columns: NAME, ROLE, NODE IP, ACCESS IP, RKE2 VERSION, STATUS (Ready/NotReady), LONGHORN (yes/no).
Use plausible IPs (10.10.x.y for node IP, 203.0.113.x for access IP), RKE2 version `v1.35.7+rke2r1`.

Health checks for the selected customer (name, status ok/warn/fail, detail):
nodes_ready, node_versions, bad_pods, helm_releases, rancher_ping, longhorn_nodes, etcd, disk, last_snapshot.

## Screens (all required)

1. **Dashboard** — full-screen, three regions:
   - Left: customer list (name, env badge, health colour dot, lock icon). Arrow keys and **mouse click**
     select; selection updates the right side.
   - Right top: node table of the selected customer. Rows clickable.
   - Right bottom: health check table of the selected customer.
   - Top bar: app name, current customer/env, key hints. Bottom bar: status line + command bar.
   - Command bar opened with `:` (k9s style). Accept `:health`, `:install`, `:backup`, `:add`, `:secret`,
     `:quit` with inline suggestions as the user types. Same actions also reachable by single-key
     shortcuts shown in the top bar (h, i, b, a, s, q) and by mouse-clicking the hints.
2. **Run view** — triggered by `:install` or `:health`. Replaces/overlays the right side with a
   streaming log panel. A goroutine emits fake Ansible-style lines every 50-150 ms for ~8 seconds:
   `PLAY [..]`, `TASK [role : name]`, `ok:`/`changed:`/`skipping:` lines (colour by result), then a
   PLAY RECAP and a final summary table like the health table. Requirements: panel auto-scrolls while
   at bottom, user can scroll up with wheel/keys and auto-scroll pauses, `Esc` returns to dashboard,
   the run keeps going in background with a spinner/progress in the status bar; `Ctrl-C` on the run
   view asks "Abort run? [y/N]".
   Also show a left-side **play/task tree** (ansible-navigator style): plays as parents, tasks as
   children with ok/changed/failed counts updating live. Collapsible.
3. **Add customer form** — `:add` opens a form (modal or full page): customer name (validated
   `^[a-z][a-z0-9-]{1,30}$`), environment (select: prod/staging/test), access mode (select:
   direct/jump; jump reveals a "jump host" field), Rancher hostname, then a repeatable node
   sub-form (short name, role select, node IP, bootstrap user@host) with "add another node", and a
   **password field** (masked) for "bootstrap password (optional)". Submit shows a confirmation
   dialog summarising the input; on confirm append the customer to the in-memory list.
4. **Secret reveal** — `:secret` opens a small dialog listing secret keys (rke2_token,
   rancher_admin_password); selecting one shows the fake value in a box with a 15 s countdown, then
   the box clears itself. `c` copies to clipboard (mock: just say "copied"), any key closes early.
5. **Confirmation for prod** — any of install/backup on a `prod` customer asks the user to type
   the customer name to proceed (k9s-style prompt). Non-prod asks y/N.
6. **Lock indicator** — locked customer shows lock icon; `:install` on it shows an error toast
   "locked by ayse: maintenance window" and does nothing.

## Interaction rules

- Mouse ON by default: click to select rows/items, wheel to scroll every panel, click on hints/buttons.
  Provide a key (`m`) to toggle mouse off (so terminal text selection works) and show the state in the
  status bar.
- Keyboard: Tab cycles panel focus; the focused panel has a highlighted border/title. `?` shows a help
  overlay. `q`/`Ctrl-C` on the dashboard quits.
- Resizing the terminal must re-layout without glitches.
- Colours: sensible defaults on dark terminals; do not hard-code a light-only palette.

## Deliverables per spike

Directory `spikes/tui-<nn>-<name>/` with its own `go.mod` (module `ktags-spike-<name>`), `main.go`
and as many files as you like. Must pass `go build ./...` and `go vet ./...`. Do not add tests.
Add `NOTES.md` (English, ≤ 400 words): library versions, lines of code (`wc -l *.go`), what was
easy, what was hard or impossible, mouse behaviour observed in code, how streaming output is wired
(io.Writer? channel? Program.Send?), rough verdict for a k9s-style multi-panel app.
Do not create git commits.
