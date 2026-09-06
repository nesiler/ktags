---
name: review
description: >
  Independently review a pull request that is in stage:review: re-derive the acceptance
  criteria from the issue, re-run the gates live, read the diff, try to break at least one new
  guard, and record the verdict with gh pr review. Requires a fresh session or the reviewer
  subagent; refuses if this conversation implemented the change. Use for "review PR 14",
  "review #14", "check this PR". Never edits source; findings go back to the task.
license: Apache-2.0
metadata:
  argument-hint: "<pr-number>"
---

# review — independent review of one PR

Contract: `docs/workflow.md` §2 (stage:review) and §3 (evidence, findings). This skill writes
only the review itself (`gh pr review`, PR comments, labels). It never edits source or tests.

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
gh pr checkout $1
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

## 5. Try to break a guard

Pick at least one new or changed guard, validator or safety check that the PR did **not**
already document breaking. Break it, run the relevant test, expect red, restore with
`git checkout -- <file>`, expect green. If the test stays green, that is a High finding: the
test does not protect the behaviour. Confirm the tree is clean afterwards:

```bash
git status --porcelain     # must be empty
```

## 6. Check the not-done section and scope

- Every "not done" row has a reason and, if moved, an issue number that really contains it
  (`gh issue view <M>`).
- Nothing in the diff is outside the issue's scope, except regressions the change caused.
- No secrets, real hostnames, customer names, or attribution strings in the diff or the PR.

## 7. Findings

Each finding uses the three-block form with a severity tag in the title:

```
### [High] <title>
**Evidence:** file:line / command output
**Impact:** what breaks, or under which condition
**Why existing tests missed it:** ...
```

Severity: `Blocker` (wrong or unsafe behaviour, secret, data loss), `High` (criterion unmet or
unprotected), `Medium` (works but fragile or unclear), `Low` (style, naming, docs).

## 8. Verdict

- `ACCEPT` — no Blocker/High findings, all criteria evidenced, gates green here.
  `gh pr review $1 --approve --body-file <review>`; keep `stage:review` for the maintainer to merge.
- `REJECT` — otherwise. `gh pr review $1 --request-changes --body-file <review>` and
  `gh issue edit <N> --remove-label stage:review --add-label stage:dev`.
- `BLOCKED` — cannot be reviewed (draft, red CI, environment). A PR comment explaining why.

The review body starts with the verdict, then the criteria matrix with your own evidence, your
gate output, the break-see-red line(s), then findings by severity.

## Never in this session

Fix a finding. Edit source or tests. Merge. Close the issue. Approve a PR you implemented.
