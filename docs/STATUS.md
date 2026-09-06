# ktags — project status and hand-off (7 September 2026, updated after workflow setup)

Read this first when resuming work. It summarises what was decided, what exists, and what comes next.

## What ktags is

A single Go binary each operator runs on their own laptop (macOS first, Linux later) to install and
operate customer Kubernetes clusters (RKE2 + Cilium + cert-manager + Rancher + Longhorn on Ubuntu 24.04)
through Ansible playbooks and SSH. Full-screen TUI by default, classic CLI for automation. No central
server, no shared git: each operator holds the configs of the customers they are responsible for and
hands them over with encrypted export/import. It replaces the earlier `ops-platform` prototype
(`~/dev/projects/ansible/ops-platform`, kept as reference), whose Ansible roles and Go core are largely
reusable.

## Decisions taken (to be formalised as ADRs)

1. **Language Go**; existing ~9k lines of ops-platform Go are the starting material.
2. **TUI: Bubble Tea v2 stack** (`charm.land/bubbletea/v2`, lipgloss v2, bubbles v2, huh v2,
   `lrstanley/bubblezone/v2`). Chosen after four spikes; the user preferred spike 03's look plus
   spike 01's bottom hint bar; spike 04 proved the command system. tview was the runner-up.
3. **Command system**: one action registry drives the `:` palette (fuzzy search, Tab completion,
   target search, arg completion, "did you mean"), the context menu on any selected item, the hint
   bar and the help overlay. No per-action letter shortcuts. One execution pipeline for all entry
   points (resolve target → guard → danger prompt → run). Design: `docs/design/command-system.md`.
4. **No bastion, no shared git.** Configs live per operator; sharing = age passphrase-encrypted
   export/import (a hand-over, not a sync). Locks and audit are per operator.
5. **Access modes**: `direct` is the default; `jump` and `tailscale` are options. A secondary access
   door (Tailscale/jump) may be set up only with explicit customer consent.
6. **Ansible stays**, run as a subprocess with the existing structured-events callback; ktags installs
   and pins `ansible-core` itself via `uv tool install`, never via Homebrew's ansible formula.
7. **Distribution**: own Homebrew tap, GoReleaser `homebrew_casks`, cask depends on `age` (and `sops`
   only if kept). k9s is a suggested, not required, dependency.
8. **Secrets**: proposal to drop sops and encrypt directly with age to the operator's key (unblocks
   `age-plugin-se` / Touch ID). Decision pending.
9. **k9s bridge** (Phase 1): fetch `rke2.yaml` over SSH, open an SSH tunnel to 6443, run
   `k9s --kubeconfig <tmp>` via `tea.ExecProcess`, clean up and audit on exit. ktags never
   re-implements cluster-internal browsing.
10. **Backups are not manual**: second node becomes the backup peer automatically; scheduled
    backups and health run on the node/in-cluster and are verified from the laptop.
11. **English everywhere** (code, comments, commits, docs). **No AI co-author signatures in commits.**
12. Project management: phased plan, ADRs, `docs/` as the source of truth. Working system set up
    on 2026-09-07: `AGENTS.md` router, `docs/workflow.md` contract (issue → stage:dev → stage:test
    → stage:review → merge), skills `task` and `review` in `.agents/skills/`, `reviewer` subagent,
    guard script + git hooks + CI, tool configs for Claude Code, Codex and OpenCode. Public repo
    `github.com/nesiler/ktags`, Apache-2.0.

## What exists in this repo

- `docs/research/2026-09-06/` — language/distribution, TUI cluster tools, TUI libraries (sourced).
- `docs/research/2026-09-07/operational-functions.md` — full day-0/1/2/decommission function
  catalogue with priorities; the backlog seed.
- `docs/design/command-system.md` — action registry + palette design.
- `spikes/tui-01..04` — throwaway mocks; `tui-04-bubbletea-palette` is the reference for the real UI.
  Run any with `cd spikes/<name> && go run .`.

## Lessons from the ops-platform live test (2026-09-06)

The whole flow (add → access → install → node add → health → backup) passed first time in direct
mode, but a new operator would have hit four walls before the first install: forced tailscale/jump
mode, sops key not found (macOS path), ssh-agent invisible to the Ansible subprocess (allowlist),
and a half-committed inventory after a failed ping. Hence: a `doctor` pre-flight, human error
messages with the next command, atomic access step, and interactive-by-default commands are P0.

## Open questions

- Drop sops in favour of plain age? (recommended yes)
- Before building the real UI: write a short interaction standard (screens, states, keys, error and
  confirmation conventions) and possibly one more prototype pass on the real data model, so the
  many day-2 functions land in a consistent frame.

## Next steps (in order)

1. ~~Working system~~ done 2026-09-07 (see decision 12 and `docs/workflow.md`). Research behind it:
   `docs/research/2026-09-07/agentic-dev-workflow.md`.
2. Write the interaction standard and the "first day" scenario (10 lines) → ADR-001..n.
3. Phase plan from the function catalogue: Phase 1 = P0 items ops-platform already has + doctor,
   palette UI, direct mode, export/import, k9s bridge, scheduled backups, host firewall.
4. Port the ops-platform core (inventory, ansible runner, audit, lock, secrets) into `internal/`,
   then the UI from spike 04.
