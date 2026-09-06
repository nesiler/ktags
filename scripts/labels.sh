#!/usr/bin/env bash
# Create or update the ktags label set on GitHub. Idempotent. Needs gh with repo scope.
set -euo pipefail
REPO="${1:-nesiler/ktags}"

label() { gh label create "$1" --repo "$REPO" --color "$2" --description "$3" --force >/dev/null && echo "  $1"; }

echo "labels on $REPO"
label "stage:dev"      "1d76db" "being implemented"
label "stage:test"     "fbca04" "acceptance evidence being collected"
label "stage:review"   "5319e7" "independent review"
label "blocked"        "b60205" "waiting on a decision or another issue"

label "type:feature"   "0e8a16" "new behaviour"
label "type:bug"       "d73a4a" "wrong behaviour, evidence attached"
label "type:chore"     "c5def5" "tooling, workflow, release, maintenance"
label "type:decision"  "e99695" "design decision ending in an ADR"
label "type:spike"     "f9d0c4" "throwaway exploration"

label "area:core"      "006b75" "inventory, ansible runner, secrets, audit, lock"
label "area:tui"       "006b75" "Bubble Tea UI, palette, screens"
label "area:cli"       "006b75" "commands and flags"
label "area:ansible"   "006b75" "playbooks and roles"
label "area:docs"      "006b75" "documentation and ADRs"
label "area:workflow"  "006b75" "AGENTS.md, skills, hooks, CI, tool configs"
label "area:release"   "006b75" "GoReleaser, Homebrew tap, versioning"

label "phase:0"        "bfd4f2" "pre-flight / first contact"
label "phase:1"        "bfd4f2" "build and bring-up"
label "phase:2"        "bfd4f2" "operate (day-2)"
label "phase:3"        "bfd4f2" "decommission"

label "priority:p0"    "b60205" "must exist in v1"
label "priority:p1"    "d93f0b" "soon after v1"
label "priority:p2"    "fef2c0" "later"
