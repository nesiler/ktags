#!/usr/bin/env bash
# Plan tooling: read the task sections in docs/plan/phase-*.md, check them, create GitHub issues.
#
#   scripts/issues.sh list                     every task with phase, priority, type, live, mapped issue
#   scripts/issues.sh check                    lint: ids unique, metadata valid, deps exist and point backwards
#   scripts/issues.sh show T-021               print one task section
#   scripts/issues.sh create [--phase N] [T-id...] [--dry-run]
#                                              create or update issues; mapping kept in docs/plan/issues.map
#
# Task section format (docs/plan/README.md §5):
#   ### T-021 · Title
#   `feature` · `area:core` · `p0` · live: no · depends: T-020, T-022 · ready: no
#   ...body until the next ### or ## heading...
# The issue body is the section body plus `Depends on #N` lines resolved through the map, so create
# dependencies first (the script orders by phase, then id; dependencies only point backwards).
# Needs python3 (parsing) and gh (create). Works with macOS bash 3.2.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="${KTAGS_REPO:-nesiler/ktags}"
PLAN="$ROOT/docs/plan"
MAP="$PLAN/issues.map"

# All parsing in one python helper; emits TSV or a body.
py() { python3 - "$@" <<'PY'
import sys, re, glob, json, os
plan = sys.argv[1]; mode = sys.argv[2]; args = sys.argv[3:]
TYPES = {"feature","chore","decision","spike","bug"}
AREAS = {"core","tui","cli","ansible","docs","workflow","release"}
PRIOS = {"p0","p1","p2"}
tasks = []
errors = []
for f in sorted(glob.glob(os.path.join(plan, "phase-*.md"))):
    phase_num = re.search(r"phase-(\d+)", os.path.basename(f)).group(1)
    text = open(f, encoding="utf-8").read()
    parts = re.split(r"(?m)^(?=###? )", text)
    for part in parts:
        m = re.match(r"### (T-\d{3}) · (.+)\n", part)
        if not m: continue
        tid, title = m.group(1), m.group(2).strip()
        lines = part.split("\n")
        meta = lines[1] if len(lines) > 1 else ""
        mm = re.match(r"`(\w+)` · `area:(\w+)` · `(p\d)` · live: (yes|no) · depends: ([^·]*)· ready: (yes|no)", meta)
        if not mm:
            errors.append(f"{f}: {tid} metadata line malformed: {meta!r}"); continue
        typ, area, prio, live, deps, ready = mm.groups()
        deps = [d.strip() for d in deps.replace("—","").split(",") if d.strip()]
        body = "\n".join(lines[2:]).strip()
        if typ not in TYPES: errors.append(f"{tid}: bad type {typ}")
        if area not in AREAS: errors.append(f"{tid}: bad area {area}")
        if prio not in PRIOS: errors.append(f"{tid}: bad priority {prio}")
        if not re.search(r"\*\*Criteria\.\*\*", body): errors.append(f"{tid}: no Criteria block")
        if not re.search(r"- K1 ", body): errors.append(f"{tid}: no K1 criterion")
        tasks.append(dict(id=tid, title=title, type=typ, area=area, prio=prio, live=live=="yes",
                          ready=ready=="yes", deps=deps, phase=int(phase_num), body=body, file=os.path.basename(f)))
ids = [t["id"] for t in tasks]
dupes = {i for i in ids if ids.count(i) > 1}
for d in dupes: errors.append(f"duplicate id {d}")
byid = {t["id"]: t for t in tasks}
def num(tid): return int(tid[2:])
for t in tasks:
    if int(t["id"][2]) != t["phase"]:
        errors.append(f"{t['id']}: id prefix does not match phase file {t['file']}")
    for d in t["deps"]:
        if d not in byid: errors.append(f"{t['id']}: depends on unknown {d}")
        elif num(d) >= num(t["id"]): errors.append(f"{t['id']}: dependency {d} does not point backwards")
tasks.sort(key=lambda t: (t["phase"], num(t["id"])))
if mode == "check":
    for e in errors: print("ERROR", e)
    print(f"{len(tasks)} tasks, {len(errors)} errors")
    sys.exit(1 if errors else 0)
if mode == "list":
    for t in tasks:
        print("\x1f".join([t["id"], str(t["phase"]), t["prio"], t["type"], t["area"], "yes" if t["live"] else "no",
                           "yes" if t["ready"] else "no", ",".join(t["deps"]) or "-", t["title"]]))
elif mode == "show":
    t = byid.get(args[0])
    if not t: sys.exit(f"unknown task {args[0]}")
    print(f"### {t['id']} · {t['title']}\n{t['type']} · area:{t['area']} · {t['prio']} · live={t['live']} · depends={t['deps']}\n\n{t['body']}")
elif mode == "json":
    print(json.dumps(tasks))
PY
}

mapped() { [[ -f "$MAP" ]] && awk -v id="$1" '$1==id {print $2}' "$MAP" || true; }

cmd_list() {
  printf '%-6s %-2s %-3s %-9s %-9s %-5s %-6s %-7s %s\n' ID PH PRI TYPE AREA LIVE READY ISSUE TITLE
  py "$PLAN" list | while IFS=$'\x1f' read -r id ph prio typ area live ready deps title; do
    m="$(mapped "$id")"
    printf '%-6s %-2s %-3s %-9s %-9s %-5s %-6s %-7s %s\n' "$id" "$ph" "$prio" "$typ" "$area" "$live" "$ready" "${m:+#$m}" "$title"
  done
}

cmd_create() {
  local phase="" want="" dry=0 line
  while [[ $# -gt 0 ]]; do
    case "$1" in --phase) phase="$2"; shift 2 ;; --dry-run) dry=1; shift ;; *) want="$want $1"; shift ;; esac
  done
  command -v gh >/dev/null || { echo "gh not found" >&2; exit 20; }
  py "$PLAN" check >/dev/null || { echo "plan check failed; run: scripts/issues.sh check" >&2; exit 1; }
  py "$PLAN" json | python3 -c '
import sys, json
for t in json.load(sys.stdin):
    print(json.dumps(t))' | while IFS= read -r line; do
    local id ph typ area prio live ready deps title body labels depline="" d dn existing num
    id="$(echo "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')"
    ph="$(echo "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin)["phase"])')"
    [[ -n "$phase" && "$ph" != "$phase" ]] && continue
    [[ -n "${want// /}" ]] && ! grep -qw -- "$id" <<<"$want" && continue
    typ="$(echo "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin)["type"])')"
    area="$(echo "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin)["area"])')"
    prio="$(echo "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin)["prio"])')"
    live="$(echo "$line" | python3 -c 'import sys,json;print("yes" if json.load(sys.stdin)["live"] else "no")')"
    ready="$(echo "$line" | python3 -c 'import sys,json;print("yes" if json.load(sys.stdin)["ready"] else "no")')"
    deps="$(echo "$line" | python3 -c 'import sys,json;print(",".join(json.load(sys.stdin)["deps"]))')"
    title="$(echo "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin)["title"])')"
    body="$(echo "$line" | python3 -c 'import sys,json;print(json.load(sys.stdin)["body"])')"
    labels="type:$typ,area:$area,phase:$ph,priority:$prio"
    [[ "$live" == yes ]] && labels="$labels,live"
    [[ "$ready" == yes ]] && labels="$labels,ready"
    for d in ${deps//,/ }; do
      dn="$(mapped "$d")"
      if [[ -z "$dn" ]]; then
        if [[ "$dry" == 1 ]]; then depline="$depline"$'\n'"Depends on $d (no issue yet)"; continue; fi
        echo "!! $id depends on $d which has no issue yet — create it first" >&2; exit 1
      fi
      depline="$depline"$'\n'"Depends on #$dn"
    done
    body="$body"$'\n'"$depline"$'\n\n'"_Plan: \`docs/plan/\` $id._"
    existing="$(mapped "$id")"
    if [[ "$dry" == 1 ]]; then
      echo "== $id → ${existing:+#}${existing:-new}: $title  [$labels]"; printf '%s\n' "$body" | sed 's/^/   /' | head -30; echo; continue
    fi
    if [[ -n "$existing" ]]; then
      gh issue edit "$existing" --repo "$REPO" --title "$title" --body "$body" >/dev/null
      for l in ${labels//,/ }; do gh issue edit "$existing" --repo "$REPO" --add-label "$l" >/dev/null; done
      echo "updated  $id → #$existing"
    else
      num="$(gh issue create --repo "$REPO" --title "$title" --body "$body" --label "$labels" | grep -oE '[0-9]+$')"
      printf '%s %s\n' "$id" "$num" >>"$MAP"
      echo "created  $id → #$num"
    fi
  done
}

case "${1:-list}" in
  list) cmd_list ;;
  check) py "$PLAN" check ;;
  show) py "$PLAN" show "${2:?task id}" ;;
  create) shift; cmd_create "$@" ;;
  *) sed -n '2,16p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
