---
name: review
description: >
  Independently review a pull request that is in stage:review: re-derive the acceptance
  criteria from the issue, re-run the gates live, read the diff, try to break at least one new
  guard, and record the verdict with gh pr review. Requires a fresh session or the reviewer
  subagent; refuses if this conversation implemented the change. Use for "review PR 14",
  "review #14", "check this PR". Never implements fixes; findings go back to the task.
license: Apache-2.0
metadata:
  argument-hint: "<pr-number>"
---

# review — independent review of one PR

Contract: `docs/workflow.md` §2 (stage:review) and §3 (evidence, findings). This skill writes
only the review itself (`gh pr review`, PR comments, labels). It never implements fixes. Temporary, restored mutation tests in its own clean worktree are the only editing exception.

`$1` is the PR number.

## 0. Refuse gate

Ask: *did this conversation write or edit any of the code in this PR?* If yes or unsure, stop:

```
This session saw the implementation and cannot review it. Run /review $1 in a fresh session or
through the reviewer subagent.
```

## 1. Preconditions

```bash
scripts/guard.sh doctor
gh pr view $1 --json number,title,body,headRefName,baseRefName,labels,isDraft,statusCheckRollup
# Use a dedicated worktree; check cleanliness BEFORE checkout.
git status --porcelain
gh pr checkout $1 --detach
test "$(git rev-parse HEAD)" = "$(gh pr view $1 --json headRefOid --jq .headRefOid)"
scripts/guard.sh verify-attribution origin/main..HEAD
```

Stop with a `BLOCKED` comment if: the PR is a draft, CI is red, the tree is dirty after checkout,
or attribution is found.

## 2. Re-derive the criteria

Read the issue yourself before the PR body:

```bash
gh issue view <N> --comments      # N from "Closes #N" in the PR body
```

Number the criteria `#N-K1…` on your own, then compare with the PR's matrix. Differences are
findings. Copying the PR's matrix is not a review.

## 3. Verify the diff and the "already met" claims

```bash
git diff origin/main...HEAD --stat
git diff origin/main...HEAD
```

For every criterion the PR marks "already met", open the code at the cited `file:line` and
confirm. For every "to build" row, find the code and the test that protects it.

## 4. Run the gates live

```bash
make check
```

Do not trust the numbers pasted in the PR; record your own. A mismatch is a finding.

## 5. Check the guard inventory against the diff

Walk the diff yourself and list every new or changed refusal, validation, exclusivity or error
branch before you read the PR's `Guard inventory` table. Then compare the two lists:

- A guard in the diff with no row in the table is a **High** finding.
- A row without a break-see-red line or a `not a guard, because …` reason is a **High** finding.
- Check at least one `not a guard` reason against the code. A reason that is false (for
  example, the branch is reachable) is a **High** finding.

## 6. Try to break a guard

Pick at least one new or changed guard, validator or safety check. Prefer a different mutation
from the implementation evidence. If there is no such guard, record not applicable and why.
Use only your dedicated, initially clean worktree; never mutate the maintainer's checkout. Break it, run the relevant test, expect red, restore with
`git checkout -- <file>`, expect green. If the test stays green, that is a High finding: the
test does not protect the behaviour. Confirm the tree is clean afterwards:

```bash
git status --porcelain     # must be empty
```

## 7. Check the not-done section and scope

- Every "not done" row has a reason and, if moved, an issue number that really contains it
  (`gh issue view <M>`).
- Nothing in the diff is outside the issue's scope, except regressions the change caused.
- No secrets, real hostnames, customer names, or attribution strings in the diff or the PR.

## 8. Findings

Each finding uses the three-block form with a severity tag in the title:

```
### [High] <title>
**Evidence:** file:line / command output
**Impact:** what breaks, or under which condition
**Why existing tests missed it:** ...
```

Severity: `Blocker` (wrong or unsafe behaviour, secret, data loss), `High` (criterion unmet or
unprotected), `Medium` (works but fragile or unclear), `Low` (style, naming, docs).

## 9. Verdict

- `ACCEPT` — no Blocker/High findings, all criteria evidenced, gates green here.
- `REJECT` — otherwise; move the issue back to `stage:dev` after publishing the findings.
- `BLOCKED` — cannot be reviewed (draft, red CI, environment); explain the blocker.

Use `gh pr review $1 --comment --body-file <review>` for ACCEPT and REJECT, even when the
reviewer and PR author share a GitHub login. Never use `--approve` or `--request-changes`.
The exact first line is `Review: ACCEPT <full-head-sha>` or `Review: REJECT <full-head-sha>`.
Read the head from GitHub immediately before publishing; it must match the tested checkout.
If it changed, rerun review for that head instead of attaching stale evidence.
Follow with the criteria matrix, gate output, mutation evidence, and findings by severity.

This advisory verdict is not maintainer approval. Only the maintainer posts
`Maintainer: APPROVE <full-head-sha>` after reviewing the evidence, or
`Maintainer: REVOKE <full-head-sha>` to withdraw it. Agents never post either line.
The maintainer then squash-merges their own PR with CI green. A new head needs a fresh review
and approval. `scripts/pr-state.py --pr N` verifies the recorded state without writing anything.

## Never in this session

Implement a fix. Leave mutation changes behind. Merge. Close the issue. Submit maintainer approval.
Review a PR whose implementation context you inherited.
