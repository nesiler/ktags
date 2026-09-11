# ktags development workflow

The contract every task follows, whoever executes it (maintainer, Claude Code, Codex, OpenCode,
other agents). Kept deliberately short; it grows only when a real failure shows a gap, and each
addition names the failure that caused it.

## 1. Sources of work

- **GitHub issue** — the unit of work. Created from `.github/ISSUE_TEMPLATE/`. Body carries the
  goal and acceptance criteria; comments refine them and are binding (newest wins).
- **Labels** — the state machine and the map to the plan:

| Label group | Values | Meaning |
|---|---|---|
| `stage:` | `dev`, `test`, `review` | where the task is now; exactly one at a time, removed on merge |
| `type:` | `feature`, `bug`, `chore`, `decision`, `spike` | what kind of change |
| `area:` | `core`, `tui`, `ansible`, `cli`, `docs`, `workflow`, `release` | which part |
| `phase:` | `0`, `1`, `2`, `3`, `4` | plan phase from `docs/plan/` (development order, not the cluster lifecycle) |
| `priority:` | `p0`, `p1`, `p2` | from the function catalogue |
| `ready` | — | criteria complete and approved by the maintainer; an agent (or the coordinator) may start it |
| `live` | — | needs real machines; only with the maintainer present, never queued unattended |
| `blocked` | — | waiting on a decision or another issue |

- **Dependencies** are `Depends on #M` lines in the issue body (one per line). A task is eligible
  only when every dependency is closed.
- **Plan** (`docs/plan/phase-*.md`) — phases and task sections (`T-nnn`) with criteria. An issue
  is created from a task section by the maintainer (`scripts/issues.sh create`); the issue then is
  the binding source, the section is local history and is not published.

- **ADR** (`docs/adr/NNNN-title.md`) — an accepted design decision. Created from a
  `type:decision` issue. ADRs are immutable once accepted; a new ADR supersedes an old one.

## 2. Stages

```
issue → stage:dev → stage:test → stage:review → merge (issue closes) → [release]
              ↑                        │
              └── changes requested ───┘
```

### stage:dev — implement (`task` skill)

1. `gh issue view <N> --comments`. Number the criteria `#N-K1 … #N-Kn` and keep the numbering
   through PR and review.
2. Gap analysis before code: for each criterion, what happens today, with `file:line`. Only the
   real delta is implemented.
3. Branch from `main`: `gh issue develop <N> --checkout --name <type>/<N>-<slug>`.
4. Implement; write or extend tests. Each risk named in the plan gets a test.
5. `make check` must be green locally. A new or changed guard/validator gets break-see-red
   evidence.
6. Open a draft PR from the template, body = criteria matrix + evidence + not done and why.
   `Closes #N` in the body. Move label to `stage:test`.

### stage:test — acceptance evidence

- CI (`.github/workflows/ci.yml`) is green on the PR.
- Commands that reach real machines (Hetzner lab, a customer VM, an SSH target) are written into
  the PR by the agent and **executed by the maintainer**, who pastes the output. The agent never
  runs them.
- For TUI work: a `teatest` golden or a recorded run (`vhs`) attached or referenced.
- When the evidence is in the PR, mark the PR ready and move the label to `stage:review`.

### stage:review — independent review (`review` skill)

- Runs in a **fresh session** or through the `reviewer` subagent. A session that saw the
  implementation refuses.
- Re-derives the criteria from the issue, not from the PR text. Re-runs `make check`. Reads the
  diff. For at least one new guard, breaks the code and confirms the test goes red.
- Advisory verdict via `gh pr review --comment`, with first line `Review: ACCEPT <full-head-sha>`
  or `Review: REJECT <full-head-sha>`. REJECT moves the label back to `stage:dev`. Never use native
  approval: both tools may share the PR author's GitHub account.

### merge

- The maintainer approves their own PR by posting `Maintainer: APPROVE <full-head-sha>`
  after an ACCEPT review; `Maintainer: REVOKE <full-head-sha>` withdraws approval. Agents never
  post these comments. A new head needs fresh review and approval; `scripts/pr-state.py` verifies
  the exact head, author and ordering. This records the owner's decision, not a native GitHub approval.
- Squash merge by the maintainer once approved and CI is green. Commit title = PR title
  (conventional commit form). The issue closes through `Closes #N`.
- Releases are tags (`v0.x.y`) built by GoReleaser; a release is its own `type:chore` task.

### unattended runs (`coordinator` skill)

The coordinator (`.agents/skills/coordinator/SKILL.md`) picks `ready` issues in priority order and
starts the `dev` and `review` stages as **separate fresh sessions** in tmux, each in its own git
worktree (`scripts/session.sh spawn`). It verifies a stage from GitHub artefacts only
(`scripts/session.sh verify`: PR not draft, `stage:review` label, `ci-required` green,
an advisory review for the current head). It never implements, reviews, merges, or answers a
child's question. Final approval and merge always belong to the maintainer. Two fix rounds per issue, then `blocked`. Issues
labelled `live` or `type:decision` are never queued. State lives outside the repo
(`~/.ktags-dev/`).
It passes its provider explicitly on every spawn; the runner refuses an ambiguous provider.

## 3. Evidence standard

- **Claim = evidence.** `file:line`, a command and its output, or a test name. "Done" alone is
  not done.
- **Gates run live.** `make check` output is pasted from this run, not an earlier one.
- **Guard inventory.** Before the PR is marked ready, it lists every new or changed refusal,
  validation, exclusivity or error branch in the diff, each with a break-see-red line or a
  checkable `not a guard, because …`. The review compares the list with the diff, and a
  missing guard is High. Cause (#35): reviews of PR #32 and PR #34 found guards without tests
  (`strictInt` sign refusal, exclusive run directory, meta ID check, and five more in round 2),
  and each cost a fix round. Round 3 of #34 then found one false "not a guard" reason and one
  missing row.
- **Green is not proof.** A guard test must be shown to fail when the guarded behaviour is
  broken (break → red → restore → green). Record the break in one line.
- **Not done and why** is mandatory in every PR: out of scope by decision, blocked by
  environment, moved to issue #M (and #M must actually contain it).
- **Findings** (review or otherwise) use three blocks: *Evidence* (`file:line`, output),
  *Impact* (what breaks, or under which condition), *Why existing tests missed it*.

## 4. Code

- Go, module `github.com/nesiler/ktags`. Format with `gofmt` (CI enforces), lint with
  `golangci-lint` v2 (`.golangci.yml`), tests with `-race`.
- Layout: `cmd/ktags` (entry), `internal/` (everything else), `ansible/` (playbooks and roles),
  `spikes/` (throwaway, own modules, not built by CI), `docs/`.
- Errors are for the operator: say what failed and the next command to run.
- No secrets, customer names or real IPs in code, tests, fixtures or docs.

## 5. Tool configuration (all committed)

| File | Purpose |
|---|---|
| `AGENTS.md` | shared instructions (Codex, OpenCode, Gemini CLI, Cursor read it natively) |
| `CLAUDE.md` | `@AGENTS.md` + Claude-only notes |
| `.agents/skills/*/SKILL.md` | `task`, `review`, `coordinator`; spec-only frontmatter; `.claude/skills` is a symlink to it |
| `.claude/settings.json` | attribution off, secret paths and main pushes denied; shared lifecycle hooks |
| `.claude/agents/reviewer.md` | fresh-context reviewer subagent |
| `.codex/config.toml`, `.codex/hooks.json`, `.codex/agents/reviewer.toml` | project defaults, shared hooks and independent reviewer; project/hook trust required |
| `opencode.json` | OpenCode project defaults |
| `.githooks/` | pre-commit publication guard, commit-msg attribution, pre-push history scan + quick gates; `make hooks` activates |
| `scripts/agent-hook.py`, `scripts/repo-policy.py`, `scripts/pr-state.py` | shared lifecycle, publication guard and read-only review/approval verifier |
| `scripts/guard.sh` | `doctor`, `check-message`, `verify-attribution <range>` |
| `scripts/session.sh` | coordinator runner: `spawn`, `wait`, `verify`, `report`, `clean`, usage gate |
| `scripts/labels.sh`, `scripts/issues.sh` | idempotent label set; `check`/`list`/`create` issues from `docs/plan/phase-*.md` |
| `docs/guides/` | binding rule files: development, UI, Ansible, testing, security |
| `.github/` | CI (`ci-required` is the one status check the `main` ruleset demands), issue forms, PR template, Dependabot |

Repository settings (applied with `gh`, not stored as code): squash-only merges with the PR title as
commit title, branch deleted on merge, ruleset on `main` blocking deletion and force-push and
requiring `ci-required`; the maintainer is the only bypass actor.

Personal settings, keys, transcripts, temporary scripts and local plan/research/design/STATUS
are never committed. See `.gitignore`. Optional local context is read from `KTAGS_CONTEXT_ROOT`
in worktrees, never copied into a PR. CI checks shared contracts and workflow tests without
requiring private plan files. Rules do not erase already published history.

Codex hooks need explicit trust through `/hooks` in the CLI. Interactive sessions use the project
`:workspace` permission profile and on-request approvals. Coordinator-spawned Codex children use a
non-interactive profile that grants the active worktree, shared Git metadata, the current run state
directory, GitHub, and required Go module services; denied operations fail instead of prompting.
Coordinator-spawned Claude children run with `--dangerously-skip-permissions` in their own
worktree; the shared PreToolUse hook still applies to them. The runner pre-trusts exactly that
new worktree path in Claude's global config (`projects[<path>].hasTrustDialogAccepted`) and
`clean` revokes it; if the trust screen still appears, `wait` reports the child as `refused`.
Runtime permission overrides win over project defaults. Hooks catch direct secret paths and
prohibited commands; they are not a filesystem sandbox for arbitrary code execution.

## 6. Models

The Claude coordinator runs on `fable`; its fresh child sessions run on `opus` by default.
The Codex coordinator runs on `gpt-6-astra`; its fresh child sessions run on `gpt-5.6-sol` by
default. This applies to both dev and independent review children; separation of context provides
review independence.
Select a runner per stage with `KTAGS_DEV_AGENT` / `KTAGS_REVIEW_AGENT` (`claude` or `codex`),
or `spawn --agent`. Override a child explicitly with `--model`, or set
`KTAGS_CLAUDE_CHILD_MODEL` / `KTAGS_CODEX_CHILD_MODEL` for a coordinator run.
Codex uses provider-enforced quota limits; no percentage preflight is claimed. OpenCode uses
its own defaults; the review stage must still run in a session that did not write the code.

## 7. Changing this workflow

Open a `type:chore area:workflow` issue naming the failure or friction, change the files in a
PR, get it reviewed like any other task. Do not fold workflow edits into a feature PR.

Explicit maintainer requests for local environment preparation may proceed without creating
an issue. This exception does not authorize starting backlog tasks, publishing, or skipping gates.
