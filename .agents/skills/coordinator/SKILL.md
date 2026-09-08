---
name: coordinator
description: >
  Runs the ktags backlog unattended, one issue at a time: picks the next `ready` issue, starts a
  fresh `task` session, then a fresh `review` session, each in tmux inside its own worktree, and
  verifies every stage from GitHub artefacts (PR, labels, checks, head-bound advisory review), never from the
  child's summary. Writes no code, no reviews, no merges. Use when the maintainer says "run the
  backlog", "work through the ready issues", "start the coordinator", or hands the queue over for
  the night.
license: Apache-2.0
metadata:
  argument-hint: "[issue...] [--dry-run] [--max-issues N]"
---

# coordinator — queue and session manager

Mechanical tool: `scripts/session.sh`. Contract: `docs/workflow.md` §2 and §7.
This skill is the only meta role. The `task` and `review` skills do the work; this one decides the
**order** and keeps the **sessions separate**. Fresh session per stage is what makes a review
independent; chaining stages inside this conversation would destroy that.

## 0. Boundaries — read first

The coordinator never does the work itself:

- Never runs `/task` or `/review` in this session. It spawns them.
- Never edits, commits, pushes, merges or closes anything. May label/comment `blocked` after a
  failed round. Never posts maintainer approval or revocation. `--auto-merge` is not supported.
- Never answers a child's technical or product question on the maintainer's behalf.
- Never touches the maintainer's checkout. Children run in their own worktrees under
  `~/.ktags-dev/worktrees/`; the coordinator's own state is `~/.ktags-dev/runs/`.
- Never queues an issue labelled `live` (needs real machines, the maintainer must be present).

## 1. Preconditions

```bash
scripts/guard.sh doctor
scripts/session.sh doctor --agent <provider>
scripts/session.sh status --json          # [] expected; anything `awaiting: true` → do not spawn
git -C . status --porcelain               # informational: the coordinator does not need a clean tree
```

Any red line: report it and stop. A session still `awaiting` means a previous run is mid-stage;
resume from §6 for that issue instead of starting another.

## 2. Queue

```bash
gh issue list --state open --label ready --limit 100 \
  --json number,title,labels,body,createdAt
```

Eligible: label `ready`, not `blocked`, not `live`, not `type:decision` (decisions are the
maintainer's), and every `Depends on #M` line in the body points to a **closed** issue
(`gh issue view M --json state`). Explicit arguments (`/coordinator 12 15`) restrict the queue to
those numbers but the same eligibility applies.

Order (first difference wins): issue already mid-stage (has an open PR or a run dir) → `priority:p0`
before `p1` before `p2` → lower `phase:N` → `type:bug` before `feature` before `chore` → fewer
acceptance criteria (count `K` lines) → lower number.

Print the queue before starting; `--dry-run` stops here.

## 3. Usage gate — before every spawn

Choose `<provider>` from the coordinator that is running this skill (`claude` in Claude Code,
`codex` in Codex) and pass it explicitly to every `doctor`, `usage` and `spawn` command. Never
infer it from whichever executable happens to be installed.

`scripts/session.sh usage --agent <provider> --json` → `ok` continue · `hold` (any window ≥ 90 %) wait until the
`resets` time and re-measure · `unknown` stop and report. For Codex, `provider` means native
quota enforcement with no percentage preflight; it does not mean unused quota. `spawn` runs the same gate itself. A
running child is never interrupted for usage reasons.

## 4. Stage `dev`

Write the prompt to `~/.ktags-dev/runs/prompt-<N>-dev.md` (state dir, not the repo), then:

```bash
scripts/session.sh spawn --agent <provider> --issue N --stage dev --prompt-file ~/.ktags-dev/runs/prompt-N-dev.md --json
```

Prompt (short; the rules live in the skill, do not copy them):

```
Read .agents/skills/task/SKILL.md and implement issue N.

Read the issue and all comments first. Follow the task skill to the end: PR ready, evidence in the
PR, label stage:review.
Decision protocol: a technical fork with more than one defensible answer → pick the option that
matches an ADR or docs/guides; if none applies, stop and report blocked with the question. Product
questions → report blocked. Never guess.
Live systems: if the issue needs a real machine, stop and report blocked — this run is unattended.
When done, run exactly one of:
  scripts/session.sh report --status ready_for_review --summary "<one line>" --artifact "<PR url>"
  scripts/session.sh report --status blocked --summary "<what blocks and the question>"
Then do nothing else. Do not start the review; the coordinator does.
```

Round 2 and later: add the review URL and "address every finding; re-derive the criteria from the
issue, not from the review", spawn with `--round 2`.

Select providers with `KTAGS_DEV_AGENT` and `KTAGS_REVIEW_AGENT`, or `spawn --agent`.
Fresh Claude children default to `opus`; fresh Codex children default to `gpt-5.6-sol`.
Override with `KTAGS_CLAUDE_CHILD_MODEL`, `KTAGS_CODEX_CHILD_MODEL`, or `spawn --model`.

## 5. Stage `review`

Only after §6 says `dev` is done:

```bash
scripts/session.sh spawn --agent <provider> --issue N --stage review --pr P --prompt-file ~/.ktags-dev/runs/prompt-N-review.md --json
```

```
Read .agents/skills/review/SKILL.md and independently review PR P.

Fresh session: you have not seen the implementation. Follow the review skill to the end and record
the head-bound advisory verdict with `gh pr review --comment`. Then run exactly one of:
  scripts/session.sh report --status accept --summary "<one line>"
  scripts/session.sh report --status reject --summary "<n findings, highest severity>"
  scripts/session.sh report --status blocked --summary "<why>"
Do nothing else afterwards.
```

Review uses the same provider-specific child default while remaining a fresh session with no
implementation history.

## 6. Wait, then verify from artefacts

```bash
scripts/session.sh wait ktags-N-dev --timeout 7200     # 0 result · 61 dead · 62 timeout
scripts/session.sh verify --issue N --stage dev --json # 0 done · 70 not done · 71 blocked
```

`result.json` is a wake-up signal, not evidence. `verify` re-derives the state:

| Stage | Done when |
|---|---|
| `dev` | PR with head `<type>/N-*` exists, is not a draft, issue carries `stage:review`, `ci-required` is `success` |
| `review` | current-head COMMENT review is ACCEPT or REJECT, PR open/ready, `ci-required` green |

If the child reported `ready_for_review` but `verify` says not done: wait once more (CI may still
be running, poll every 60 s up to 20 min), then treat as failed round. Idle child (`pane_idle_seconds`
> 1800 and no result): read `session.sh logs`; if it is waiting on a question, the answer is the
maintainer's — log it and move on to the next issue, leave the session alive.

## 7. Transitions

| Measured | Action |
|---|---|
| `dev` done | spawn `review` |
| review `accept` | leave the PR for maintainer approval and squash merge; report reviewed-awaiting-maintainer, then pick an issue whose dependencies are closed |
| review `reject` | round + 1 of `dev` with the review URL; at most **2** fix rounds per issue, then stop that issue and label it `blocked` with a comment pointing at the review |
| child `blocked` / `refused` | label `blocked`, write the question into the report, next issue |
| `dead` without result | keep the run dir, write the last 40 log lines into the report, next issue |
| usage `hold` | wait for the reset time, then continue where it stopped |

Dependents of an unmerged reviewed PR are not eligible until the maintainer merges and closes
the issue. This is what keeps the overnight run branch-independent.

After a stage is verified done, `scripts/session.sh clean --issue N` when the issue is finished
(merged or given up); logs are archived, not deleted.

## 8. Report

Append to `~/.ktags-dev/reports/<YYYY-MM-DD>.md` on every transition and print the same block:

```
queue    : #12 (p0, phase 1) → #15 (p0) → #18 (p1)
active   : #12 · stage review · ktags-12-review · remote: <url>
measured : dev done (PR #31, ci-required success) · review pending
usage    : session 37% · week 70%  (threshold 90%)
next     : ACCEPT → maintainer approves and merges · REJECT → #12 dev r2
```

The morning summary lists: merged / reviewed-awaiting-maintainer / blocked with question / failed with
log pointer, and the PR URLs.

## Stop conditions — do not continue

- `guard.sh doctor` or `session.sh doctor` red; usage gate `unknown`.
- A session is `awaiting` that this run did not start.
- The queue is empty or `--max-issues` reached.
- Anything that needs a maintainer decision (product question, live machine, third failed round).
