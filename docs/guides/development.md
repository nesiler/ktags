# Development guide — Go, CLI, layout, conventions

Binding for every Go change. Rationale for the big choices is in `docs/STATUS.md` (decisions) and
`docs/adr/`; this file only says how to work within them.

## 1. Layout

```
cmd/ktags/            main only: flags → app.Run; no logic
internal/
  core/…              domain and orchestration, UI-agnostic (never imports bubbletea, lipgloss, cobra)
  cli/                cobra command tree; thin: parse → core call → render
  tui/                Bubble Tea app: model, screens, palette, registry surfaces
  ansible/            subprocess runner, events reader, result.json reader
  inventory/          customer directory contract: schema, strict read, hosts.yml write
  secrets/            encryption boundary (whatever the secrets decision becomes lives here only)
  audit/  lock/  run/ append-only audit, per-customer lock, run directories
  paths/              every filesystem location; the only package that knows about $HOME
  actions/            the action registry (verbs, targets, guards, danger levels) shared by CLI and TUI
ansible/              playbooks, roles, callback plugin, versions file (embedded or shipped — see plan)
spikes/               throwaway; own go.mod; not built by CI
docs/                 status, plan, guides, adr, research, design
```

Rules:

- **One direction of dependency**: `tui` and `cli` import `core` and `actions`; nothing in `core`
  imports `tui`, `cli` or any terminal library. A `core` package must be testable with `go test`
  and no terminal.
- **Every action is registered once** in `actions` and exposed by both the CLI (as a subcommand)
  and the TUI (palette, menu, hints, help). A verb that exists in one but not the other is a bug.
- `paths` is the only place that resolves directories. No other package calls `os.UserHomeDir`
  or reads `KTAGS_*` environment variables.
- No package named `util`, `common`, `helpers`.

## 2. Config, state and logs on the laptop

Small technical decision (advisors consulted, `docs/plan/decisions.md A2`; revisit only through an
ADR): XDG base directories on every platform, overridable for tests and unusual setups. Run
artefacts belong to the customer directory so that history travels with an export; the state dir
holds only what is personal to this laptop.

| What | Default | Override |
|---|---|---|
| Config (tool settings, team members file) | `$XDG_CONFIG_HOME/ktags` → `~/.config/ktags` | `KTAGS_CONFIG_DIR` |
| Customers (one inventory directory each, **including their `runs/` history**, `ansible.md §5`), Ansible content, collections | `$XDG_DATA_HOME/ktags` → `~/.local/share/ktags` | `KTAGS_DATA_DIR` |
| Audit log, locks, ktags's own logs, temporary files (k9s kubeconfig) | `$XDG_STATE_HOME/ktags` → `~/.local/state/ktags` | `KTAGS_STATE_DIR` |
| Everything at once (tests, portable) | — | `KTAGS_HOME` = one root with `config/ data/ state/` |

Files that hold secrets or SSH material are `0600`, their directories `0700`. The tool creates
directories itself with the right mode and refuses to run on a directory with looser permissions.

## 3. CLI conventions

- Noun-verb tree, k8s style: `ktags cluster install acme`, `ktags node add acme …`,
  `ktags secret show acme rke2_token`, `ktags doctor`. `ktags` alone opens the TUI.
- The customer is always an explicit positional argument. There is no "current customer" in the
  CLI. (The TUI has a selection; the CLI does not.)
- Global flags: `--dry-run` (plan only, no lock, audit `dry_run: true`), `--yes` (skips the
  `[y/N]` confirmation outside prod; refused for prod), `--json` (machine output on stdout, human
  text on stderr), `--no-color`, `-v` (Ansible verbosity 1–2; `-vvv` and `ANSIBLE_DEBUG` are
  deliberately unavailable because they defeat `no_log`).
- Exit codes are a contract: `0` success · `1` usage, validation or environment · `2` confirmation
  refused or customer locked · `3` playbook or remote step failed · `4` a check ran and is red
  (health, doctor, verify). Scripts may rely on them.
- Read-only commands (`list`, `info`, `health`, `doctor`, `secret show`) never take the lock and
  never ask for confirmation.
- Output: one summary line first, details after; tables for lists; no ANSI when not a TTY.
  `--json` output has a stable schema documented in the command's help.

## 4. Errors are for the operator

Every error that can reach a person says three things: **what failed**, **why** (the measured
fact, not a guess), and **the next command to run**. Example:

```
cannot reach acme-srv-1 (203.0.113.11:22): connection timed out after 10s
  next: ktags cluster access acme --check    # verifies SSH path, sudo and host key
```

- Wrap with context (`fmt.Errorf("install acme: %w", err)`); never swallow.
- A `Hint` (next command) is part of the error type used at the boundary; the TUI shows it in the
  error dialog, the CLI prints it under the message.
- No stack traces to the terminal. Panics are bugs; recover only in the TUI top level to restore
  the terminal, then re-panic with the log path.
- Never put secret values, full kubeconfigs, tokens or bootstrap passwords in an error, even
  partially. The mask list in `security.md` applies to error text.

## 5. Ansible from Go (summary; contract in `ansible.md`)

- Run `ansible-playbook` as a subprocess with an **allowlisted environment**; never `os.Environ()`.
- Read structure from the events file and `result.json`, never by parsing stdout.
- One run = one run directory under the customer's `runs/` with `extra_vars.json` (no secrets,
  `0600`), `events.jsonl`, `result.json`, `ansible.log`.
- Cancellation kills the process group and is recorded as a failed run (`3`).

## 6. Dependencies

- Standard library first. Allowed without discussion: `charm.land/bubbletea/v2` family
  (bubbletea, lipgloss, bubbles, huh), `lrstanley/bubblezone/v2`, `spf13/cobra`, `goccy/go-yaml`,
  `golang.org/x/{crypto/ssh,term,sync}`, `filippo.io/age`, `sahilm/fuzzy`. Anything else needs a
  line in the PR's "Not done and why / decisions" explaining the alternative rejected.
- No cgo. Cross-compiled darwin/linux, amd64/arm64 binaries must build with `CGO_ENABLED=0`.
- Pin everything; Dependabot groups minor/patch. A major bump is its own `type:chore` task.
- Vendor nothing; `go mod tidy -diff` is clean in CI.

## 7. Code style

- `gofmt`, `go vet`, `golangci-lint` v2 with the repo config are the floor (`make check`).
- Small packages with a one-paragraph `doc.go`. Exported names only when another package needs
  them.
- Context first: any function that does IO or waits takes `ctx context.Context` as the first
  parameter and honours cancellation.
- Time and randomness are injected (`clock`, `rand` in a struct field or parameter) so tests are
  deterministic.
- Tests live next to the code (`_test.go`), table-driven where there are cases, golden files under
  `testdata/`. See `testing.md`.
- Comments explain *why*; the code says *what*. No commented-out code, no TODO without an issue
  number.
- Logging: a structured logger (`log/slog`) to the run's log file; nothing to stderr except the
  operator-facing summary and errors. Secrets never enter a log record (`security.md`).

## 8. Git and PR conventions

- Conventional commit titles: `feat(tui): …`, `fix(ansible): …`, `chore(workflow): …`,
  `docs: …`. Scope = area label. Body says why, not what.
- No AI attribution anywhere (`AGENTS.md`). One logical change per commit; squash on merge.
- Branch names `<type>/<issue>-<slug>`; PR body from the template; `Closes #N`.
- Docs change in the same PR as the behaviour they describe. A user-visible change updates
  `docs/plan/` status only through the maintainer.

## 9. Documentation

- English, plain, present tense. Sentences under ~25 words. No marketing.
- Every operator-facing command has a `--help` text that fits one screen and names the next
  command in the workflow.
- Runbook pages (`docs/runbooks/`, later) are linked from failing checks by name, not by URL, so
  they survive a repository move.
