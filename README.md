# ktags

Operate customer Kubernetes (RKE2) clusters from your own laptop: a Go CLI and terminal UI
that drives Ansible over SSH. Status: design and skeleton stage; four local TUI prototypes.

Agents start with `AGENTS.md`. Shared rules live in `docs/workflow.md` and `docs/guides/`;
accepted decisions go in `docs/adr/`. Work is defined by maintainer-approved GitHub issues.
Personal plans, research, drafts and session notes stay local and are intentionally unpublished.

Local service (ADR-0001): `ktags service start|status|stop|run`. `start` does nothing when the
service already runs (macOS: a user launchd agent). `stop` refuses with exit 2 while runs are
active; `stop --cancel-runs` cancels them and records their results. Closing a terminal only
detaches. Checks missed while the laptop slept are reported as missed, never run late.

Development: Go 1.27, `make hooks` once per clone, `make check` before publication.
Temporary scripts belong in `scripts/local/`; private scratch material in `local/`.
Never force-add ignored files. Git hooks check publication paths and known secret signatures.
Previously tracked private documentation is removed from the published tree but remains available
in the maintainer's ignored local context.

Both tools share the task/review/coordinator skills. Each concurrent task gets its own worktree.
Reviewers leave COMMENT evidence; the maintainer approves and squash-merges their own PR.
See `docs/workflow.md` for the exact approval format. Codex lifecycle hooks require trust via
`/hooks`; project defaults do not override the active runtime permission mode.

License: Apache-2.0.
