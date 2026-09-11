---
name: task
description: >
  Implement one GitHub issue end to end through the dev and test stages: read the issue and
  its comments, number the acceptance criteria, do a file:line gap analysis before writing
  code, implement on an issue branch, add tests, run the gates, open a PR from the template
  with evidence, and hand the PR to review. Use for "implement #12", "do issue 12", "pick up
  #12", "fix #12". Never reviews or merges its own work.
license: Apache-2.0
metadata:
  argument-hint: "<issue-number>"
---

# task — implement one issue

Contract: `docs/workflow.md` §2 (stage:dev, stage:test) and §3 (evidence). This skill may change
source, tests, docs and the PR it opens. It does not approve, merge, or close anything.

`$1` is the issue number. If none was given, ask for it; do not guess.

## 0. Preconditions

```bash
scripts/guard.sh doctor
git status --porcelain            # maintainer's own changes? protect them, or stop
git rev-parse --abbrev-ref HEAD   # expect main, or the issue branch when resuming
```

Stop and report if `doctor` is red or if the tree holds changes you cannot attribute.

## 1. Read the issue, comments included

```bash
gh issue view $1 --comments
```

Comments are as binding as the body; the newest binding comment wins. If a comment reverses an
earlier decision, say so in the PR.

Label and dependency check:

- `blocked` → stop and say what it waits for.
- `live` (needs a real machine) → the acceptance run needs the maintainer present. In an
  unattended session (`KTAGS_SESSION` is set) stop and report `blocked`; otherwise continue and
  put the machine commands in the PR for the maintainer.
- Every `Depends on #M` line in the body → `gh issue view M --json state` must be `CLOSED`;
  otherwise stop and name the open dependency.
- Read the binding guides for the areas you touch: `docs/guides/` (development, ui, ansible,
  testing, security). A rule there is a criterion even when the issue does not repeat it.

## 2. Number the criteria

Write the acceptance criteria as `#$1-K1 … #$1-Kn`. Keep the numbering in the PR and in review.
If a criterion is a product question (who should see what, which field is mandatory), stop and
ask the maintainer; do not answer it yourself.

## 3. Gap analysis before code

For every criterion: what happens today, with `file:line`. Classify:

| State | Meaning |
|---|---|
| already met | evidence `file:line`; nothing to build |
| to build | the real delta |
| not applicable | out of scope by comment / decision; name the comment |

Post the matrix as an issue comment titled `Plan`. Add a **risk → test** table: each risk that
could go wrong in this change and the test that will catch it.

Add the label: `gh issue edit $1 --add-label stage:dev`.

## 4. Branch

Use a dedicated worktree for each implementation; never change another session's checkout.
Follow the repository branch form below (no tool-name prefix).

```bash
git fetch origin main
gh issue develop $1 --checkout --base main --name <type>/$1-<slug>
```

`<type>` is `feat`, `fix`, `chore`, `docs` or `spike`.

## 5. Implement

Only the "to build" rows. Follow `docs/workflow.md §4`. Scope creep goes to a new issue with the
three-block finding form, not into this branch. Regressions you cause are in scope.

Design decisions with more than one defensible answer: check `docs/adr/` first; if nothing
covers it, ask the maintainer before implementation. Do not silently choose.

## 6. Tests and gates

- Every risk in the plan table has a test.
- `make check` green. Paste the tail of the output into the PR.
- New or changed guard/validator: break the code, run the test, see red, restore, see green.
  Record it in the PR as one line per guard: `broke X in file:line → test Y red → restored`.
- **Guard inventory.** Before `gh pr ready`, walk `git diff origin/main...HEAD` hunk by hunk
  and list **every** new or changed refusal, validation, exclusivity (`O_EXCL`, `Mkdir`
  instead of `MkdirAll`, locks, "at most one") or error branch. That covers the guard the
  issue names and every guard you added along the way. Each row in the PR's `Guard inventory`
  table carries either a break-see-red line or `not a guard, because …`. The reason must
  be one the reviewer can check against the code: "unreachable" names the caller that
  prevents it, and "pure passthrough" names the tested caller. A reason that turns out to be
  false counts as a missing guard.

## 7. Commit and open the PR

Commit and publication require maintainer authorization for this task. If not already given,
finish and verify the local changes first, then ask. Unattended runs without commit/publication
authorization report blocked; a `ready` label alone does not grant it.

Commits: conventional form `type(scope): summary` in English, no attribution of any kind.
`scripts/guard.sh check-message` runs from the commit-msg hook; if hooks are inactive, run
`make hooks` first.

```bash
git push -u origin HEAD
gh pr create --draft --fill --body-file <filled template>
gh issue edit $1 --remove-label stage:dev --add-label stage:test
```

The PR body follows `.github/PULL_REQUEST_TEMPLATE.md`: criteria matrix with evidence, risk →
test table, gate output, the guard inventory, **not done and why**, and `Closes #$1`.

## 8. stage:test — acceptance evidence

- Wait for CI on the PR; fix red.
- If the change touches real machines or SSH targets, write the exact commands the maintainer
  should run into a PR comment titled `Acceptance run` and stop. The maintainer runs them and
  pastes the output. You never run them.
- When evidence is complete, including a guard inventory that covers the whole diff (§6):
  `gh pr ready` and
  `gh issue edit $1 --remove-label stage:test --add-label stage:review`.

## Stop conditions

Do not say "ready" or "done" when any of these holds: a criterion has no evidence; `make check`
was not run in this session; a guard has no break-see-red line;
the PR lacks a complete guard inventory (§6); the PR lacks the not-done section; the tree has
uncommitted changes.

## Never in this session

Review or approve this PR. Merge. Close the issue. Edit `main` directly. Run commands against
real servers. Change workflow files without an `area:workflow` issue.
