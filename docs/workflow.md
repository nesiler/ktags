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
| `phase:` | `0`, `1`, `2`, `3` | plan phase from `docs/STATUS.md` |
| `priority:` | `p0`, `p1`, `p2` | from the function catalogue |
| `blocked` | — | waiting on a decision or another issue |

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
- Verdict via `gh pr review --approve` or `--request-changes`, with findings in the standard form
  below. `--request-changes` moves the label back to `stage:dev`.

### merge

- Squash merge by the maintainer once approved and CI is green. Commit title = PR title
  (conventional commit form). The issue closes through `Closes #N`.
- Releases are tags (`v0.x.y`) built by GoReleaser; a release is its own `type:chore` task.

## 3. Evidence standard

- **Claim = evidence.** `file:line`, a command and its output, or a test name. "Done" alone is
  not done.
- **Gates run live.** `make check` output is pasted from this run, not an earlier one.
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
| `.agents/skills/*/SKILL.md` | the two procedures, spec-only frontmatter; `.claude/skills` is a symlink to it |
| `.claude/settings.json` | attribution off, secret paths and pushes to `main` denied, hooks: doctor on start, attribution check before `git commit`, build/vet/test gate on stop (`.claude/hooks/gate.sh`) |
| `.claude/agents/reviewer.md` | fresh-context reviewer subagent |
| `.codex/config.toml` | Codex project defaults (applies once the folder is trusted) |
| `opencode.json` | OpenCode project defaults |
| `.githooks/` | `commit-msg` (attribution), `pre-push` (fmt, vet, quick tests); `make hooks` activates |
| `scripts/guard.sh` | `doctor`, `check-message`, `verify-attribution <range>` |
| `.github/` | CI (`ci-required` is the one status check the `main` ruleset demands), issue forms, PR template, Dependabot |

Repository settings (applied with `gh`, not stored as code): squash-only merges with the PR title as
commit title, branch deleted on merge, ruleset on `main` blocking deletion and force-push and
requiring `ci-required`; the maintainer is the only bypass actor.

Personal settings (`.claude/settings.local.json`, `~/.codex/config.toml`,
`~/.config/opencode/`) are never committed.

## 6. Changing this workflow

Open a `type:chore area:workflow` issue naming the failure or friction, change the files in a
PR, get it reviewed like any other task. Do not fold workflow edits into a feature PR.
