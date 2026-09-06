#!/usr/bin/env bash
# Claude Code Stop hook: "looks done" must also be "builds, vets and tests green".
# Runs only when Go sources or go.mod changed in the working tree; exit 2 sends the failure back
# to the model so the turn continues until the gate is green. stop_hook_active guards loops.
set -uo pipefail
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0

input="$(cat)"
if command -v jq >/dev/null 2>&1 && printf '%s' "$input" | jq -e '.stop_hook_active == true' >/dev/null 2>&1; then
  exit 0
fi

[[ -f go.mod ]] || exit 0
changed="$(git status --porcelain -- '*.go' go.mod go.sum 2>/dev/null | grep -v '^.. spikes/' || true)"
[[ -n "$changed" ]] || exit 0

out="$( { go build ./... && go vet ./... && go test -count=1 ./... ; } 2>&1 )" && exit 0
echo "Gate failed (go build/vet/test). Fix before finishing:" >&2
echo "$out" | tail -40 >&2
exit 2
