# ktags — agent entry point

ktags is a Go CLI + TUI that installs and operates customer Kubernetes clusters (RKE2 stack)
from an operator's laptop through Ansible and SSH. One maintainer, several AI coding agents.
Public repository: `github.com/nesiler/ktags`. Everything in this repo is **English**.

This file is a **router**. Rules live in one canonical place each; nothing is copied here.

## Read this first

| When | Read |
|---|---|
| Resuming after a break | `README.md`, open issue + comments; `docs/STATUS.md` when available locally |
| Taking or reviewing any task | `docs/workflow.md` — task lifecycle, evidence rules, stages |
| Choosing a library or design | `docs/adr/`; local research/design notes when available |
| Writing Go, Ansible, UI, tests | `docs/guides/` — one short rule file per topic; they bind |
| Picking the next piece of work | Maintainer-approved GitHub issues; `docs/plan/` is a local unpublished backlog |
| Running the backlog unattended | `.agents/skills/coordinator/SKILL.md` and `scripts/session.sh` |

## Source priority (highest wins)

1. The maintainer's explicit instruction in this session, never read as permission to leak
   secrets, widen access, or skip a gate.
2. The GitHub issue body **and its comments**. Newest binding comment wins.
3. `docs/adr/` (accepted decisions), then `docs/guides/`, then `docs/workflow.md`, then this file.
4. Code and **measured** behaviour: `file:line`, command output. A claim that "the code already
   does this" is not accepted until re-measured.
5. Plan and review artefacts (PR descriptions, review comments): they record what was done, they
   do not override 1–4.
6. Personal agent memory and past chats: hints only, never evidence.

## Task lifecycle (short form; contract in `docs/workflow.md`)

Every task is a GitHub issue and moves through three stages, tracked with `stage:*` labels:

```
issue (criteria K1..Kn) → stage:dev → stage:test → stage:review → merged (issue closes)
```

- **dev** — the `task` skill: read issue + comments, number the criteria `#N-K1…`, do a
  `file:line` gap analysis before writing code, implement on a branch, write tests, run the
  gates, open a PR from the template.
- **test** — acceptance evidence. Gates run live; commands that touch real machines are written
  by the agent and **run by the maintainer**, who pastes the result into the PR.
- **review** — the `review` skill in a **fresh session or the `reviewer` subagent**. It re-derives
  the criteria from the issue, re-runs the gates, tries to break at least one new guard, and
  records an advisory COMMENT verdict tied to the tested head with `gh pr review --comment`. A session that wrote the code never reviews it.

Skills: `.agents/skills/task/SKILL.md`, `.agents/skills/review/SKILL.md`,
`.agents/skills/coordinator/SKILL.md` (Claude: `/task`, `/review`, `/coordinator` · Codex: `$task`,
`$review` · others: read the file). The coordinator only sequences fresh `task` and `review`
sessions and verifies each stage from GitHub; it never implements or reviews itself.

## Evidence rules

- "Done" needs `file:line` or command output. A criterion without evidence counts as not done.
- Gates are run live, never quoted from an earlier run: `make check` (fmt, vet, lint, test -race).
- A new or changed guard, validator or safety check needs break-see-red evidence: break the
  code, watch the test fail, restore, watch it pass.
- Scope is the issue. Findings outside it become new issues, not extra commits. Exception:
  regressions your own change caused.
- Not done and why is the most valuable part of a PR description.

## Hard rules

- **No AI attribution anywhere**: no `Co-Authored-By`, `Generated with`, `Claude-Session`,
  robot emoji, or tool names in commit messages, PR bodies, branch names or docs.
  Enforced by `.githooks/commit-msg`, a Claude hook, and CI.
- **No secrets in the repo or in output**: no keys, tokens, passwords, kubeconfigs, customer
  hostnames. `.env*`, `*.key`, `*.age`, `secrets/` are denied to agents.
- **Live systems**: commands against real servers are proposed by the agent and executed by the
  maintainer. Never touch machines you did not create in this task.
- **Workflow files** (`AGENTS.md`, `CLAUDE.md`, `.agents/`, `.claude/`, `.codex/`,
  `opencode.json`, `.githooks/`, `scripts/`, `docs/workflow.md`,
  `docs/guides/`) change only through a dedicated `area:workflow` issue with its own review, unless the maintainer explicitly requests local environment preparation without an issue.
- Commit only when asked. Never force-push `main`. Never close an issue by hand; merging the PR
  with `Closes #N` does it. Final approval and squash merge belong to the maintainer, including
  their own PRs. Agents never post `Maintainer: APPROVE` or `Maintainer: REVOKE` comments.

## Decisions

Technical decisions with more than one defensible answer are settled by consultation, not by
guessing: the maintainer runs a multi-model consultation (personal skill) or decides directly,
and the outcome is written as an ADR (`docs/adr/`) or as a binding issue comment. Product
decisions (what ktags should do for whom) always go to the maintainer.

## Local commands

```bash
make hooks      # once per clone: activate .githooks
make check      # fmt + vet + lint + test -race (the gate)
make guard      # doctor: tools, hooks active, gh auth, no attribution in HEAD
scripts/session.sh doctor   # coordinator prerequisites (tmux, gh auth, state dirs)
```

## Local material and parallel work

- Personal Markdown is ignored by default. Only README, these entry points, workflow/guides,
  accepted ADRs, shared skills/reviewer definitions and the PR template are published.
- Plans, research, design drafts and STATUS are local context, not GitHub deliverables. Never
  force-add them. In a runner worktree, `KTAGS_CONTEXT_ROOT` points to the maintainer's checkout
  for optional local documentation; read only the named documentation there, never edit it.
  On another machine use the issue and shared contracts; do not invent missing local context.
- Put temporary scripts in `scripts/local/`, other scratch work in `local/` or `scratch/`.
  Session transcripts and usage/auth state remain outside Git. Ignore rules do not remove history.
- Every concurrent implementation or review gets its own worktree. Never share a mutable
  checkout. For independent review use a fresh session; a subagent must not inherit implementation
  history. Both tools have a project `reviewer` definition. Shared skills remain canonical.
- Session publication checks reject ignored paths and known credential signatures without
  printing values. They complement, rather than replace, secret hygiene and runtime permissions.
