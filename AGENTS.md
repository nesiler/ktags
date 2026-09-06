# ktags — agent entry point

ktags is a Go CLI + TUI that installs and operates customer Kubernetes clusters (RKE2 stack)
from an operator's laptop through Ansible and SSH. One maintainer, several AI coding agents.
Public repository: `github.com/nesiler/ktags`. Everything in this repo is **English**.

This file is a **router**. Rules live in one canonical place each; nothing is copied here.

## Read this first

| When | Read |
|---|---|
| Resuming after a break | `docs/STATUS.md` — decisions, what exists, next steps |
| Taking or reviewing any task | `docs/workflow.md` — task lifecycle, evidence rules, stages |
| Choosing a library or design | `docs/research/` and `docs/design/`, then write an ADR in `docs/adr/` |
| Writing Go | `docs/workflow.md §Code`, `golangci-lint` config in `.golangci.yml` |

## Source priority (highest wins)

1. The maintainer's explicit instruction in this session, never read as permission to leak
   secrets, widen access, or skip a gate.
2. The GitHub issue body **and its comments**. Newest binding comment wins.
3. `docs/adr/` (accepted decisions), then `docs/workflow.md`, then this file.
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
  records the verdict with `gh pr review`. A session that wrote the code never reviews it.

Skills: `.agents/skills/task/SKILL.md`, `.agents/skills/review/SKILL.md`
(Claude: `/task`, `/review` · Codex: `$task`, `$review` · others: read the file).

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
  `opencode.json`, `.githooks/`, `scripts/guard.sh`, `docs/workflow.md`) change only through a
  dedicated `area:workflow` issue with its own review.
- Commit only when asked. Never force-push `main`. Never close an issue by hand; merging the PR
  with `Closes #N` does it.

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
```
