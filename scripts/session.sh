#!/usr/bin/env bash
# ktags session runner — the only mechanical tool of the `coordinator` skill.
#
# Starts one lifecycle stage (dev or review) of one issue as a fresh selected-agent session in
# tmux, inside its own git worktree (Remote Control for Claude); measures its state; derives stage
# completion from GitHub artefacts (PR, labels, checks, reviews), never from the child's claim.
#
#   session.sh doctor  [--agent claude|codex]             tools, gh auth, run dir writable
#   session.sh usage   [--agent claude|codex] [--threshold 90] [--json]   quota preflight
#   session.sh spawn   --issue N --stage dev|review --prompt-file F
#                      [--agent claude|codex] [--pr P] [--round R] [--model M] [--effort E]
#                      [--no-worktree] [--skip-usage-gate] [--json]
#   session.sh status  [key...] [--issue N] [--json]
#   session.sh wait    <key> [--timeout s] [--interval 20]     (0 result · 61 dead · 62 timeout)
#   session.sh verify  --issue N --stage dev|review [--json]   (0 done · 70 not done · 71 blocked)
#   session.sh report  --status S [--summary ..] [--artifact ..] [--next ..]   (child calls this)
#   session.sh logs    <key> [-n 60] | send <key> <text> | stop <key>
#   session.sh clean   --issue N [--force] [--purge] [--json]
#
# State lives outside the repository: $KTAGS_RUN_DIR (default ~/.ktags-dev/runs) and
# $KTAGS_WORKTREE_DIR (default ~/.ktags-dev/worktrees). Nothing here writes into the checkout.
# Exit codes: 0 ok · 10 usage · 20 environment · 60 safety refusal · 61/62 wait · 70/71 verify · 75/76 usage gate
# Works with macOS bash 3.2.
set -euo pipefail

ROOT="${KTAGS_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
RUN_ROOT="${KTAGS_RUN_DIR:-$HOME/.ktags-dev/runs}"
WT_ROOT="${KTAGS_WORKTREE_DIR:-$HOME/.ktags-dev/worktrees}"
CLAUDE_BIN="${KTAGS_CLAUDE_BIN:-}"
REPO="${KTAGS_REPO:-nesiler/ktags}"
DEFAULT_AGENT="${KTAGS_AGENT:-}"

die() { echo "session.sh: $1" >&2; exit "${2:-10}"; }
now_iso() { date -u +%Y-%m-%dT%H:%M:%SZ; }
epoch() { date -u +%s; }
mtime() { stat -f %m "$1" 2>/dev/null || stat -c %Y "$1" 2>/dev/null || echo 0; }
need() { command -v "$1" >/dev/null 2>&1 || die "$1 not found" 20; }

resolve_claude() {
  if [[ -n "$CLAUDE_BIN" ]]; then echo "$CLAUDE_BIN"; return; fi
  local c
  c="$(command -v claude 2>/dev/null || true)"
  [[ -n "$c" && -x "$c" ]] || c="$HOME/.local/bin/claude"
  [[ -x "$c" ]] || die "claude CLI not found (set KTAGS_CLAUDE_BIN)" 20
  echo "$c"
}


resolve_agent() {
  case "$1" in
    claude) resolve_claude ;;
    codex) command -v codex || die "codex CLI not found" 20 ;;
    *) die "--agent must be claude or codex" 10 ;;
  esac
}

default_child_model() {
  case "$1" in
    claude) echo "${KTAGS_CLAUDE_CHILD_MODEL:-${KTAGS_DEV_MODEL:-opus}}" ;;
    codex) echo "${KTAGS_CODEX_CHILD_MODEL:-gpt-5.6-sol}" ;;
    *) die "--agent must be claude or codex" 10 ;;
  esac
}

launch_command() {
  local agent="$1" bin="$2" model="$3" effort="$4" rc="$5" opts=""
  [[ -n "$model" ]] && opts="--model $(printf '%q' "$model")"
  case "$agent" in
    claude)
      [[ -n "$effort" ]] && opts="$opts --effort $(printf '%q' "$effort")"
      printf '%s --remote-control %s %s\n' "$(printf '%q' "$bin")" "$(printf '%q' "$rc")" "$opts"
      ;;
    codex)
      [[ -n "$effort" ]] && opts="$opts -c $(printf '%q' "model_reasoning_effort=\"$effort\"")"
      printf '%s --sandbox workspace-write --ask-for-approval on-request %s\n' "$(printf '%q' "$bin")" "$opts"
      ;;
    *) die "unsupported provider" 10 ;;
  esac
}

# key: ktags-<issue>-<stage>[-r<N>]   remote-control name: #<issue>-<Stage>[-rN]
session_key() {
  local k="ktags-$1-$2"
  [[ "${3:-1}" -gt 1 ]] && k="$k-r$3"
  echo "$k"
}
rc_name() {
  local p
  case "$2" in dev) p="Dev" ;; review) p="Review" ;; *) p="$2" ;; esac
  local n="#$1-$p"
  [[ "${3:-1}" -gt 1 ]] && n="$n-r$3"
  echo "$n"
}
run_dir() { echo "$RUN_ROOT/$1"; }

# Another session that is alive and has not reported yet blocks a new spawn: one stage at a time.
awaiting_sessions() {
  local d k
  for d in "$RUN_ROOT"/ktags-*; do
    [[ -d "$d" ]] || continue
    k="$(basename "$d")"
    [[ "$k" == "${1:-}" ]] && continue
    if tmux has-session -t "=$k" 2>/dev/null && [[ ! -f "$d/result.json" ]]; then echo "$k"; fi
  done
}

# ── doctor ───────────────────────────────────────────────────────────────────
cmd_doctor() {
  local rc=0 agent="$DEFAULT_AGENT"
  if [[ "${1:-}" == "--agent" ]]; then agent="${2:?agent}"; fi
  [[ "$agent" =~ ^(claude|codex)$ ]] || die "select the coordinator provider with --agent claude|codex or KTAGS_AGENT" 10
  for t in tmux jq gh git python3; do
    if command -v "$t" >/dev/null 2>&1; then echo "ok    $t"; else echo "FAIL  $t missing"; rc=20; fi
  done
  local c
  if c="$(resolve_agent "$agent" 2>/dev/null)"; then echo "ok    $agent ($c)"; else echo "FAIL  $agent CLI not found"; rc=20; fi
  if gh auth status >/dev/null 2>&1; then echo "ok    gh auth"; else echo "FAIL  gh not authenticated"; rc=20; fi
  if git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then echo "ok    repo $ROOT"; else echo "FAIL  not a git repo: $ROOT"; rc=20; fi
  if mkdir -p "$RUN_ROOT" "$WT_ROOT" 2>/dev/null; then echo "ok    state dirs $RUN_ROOT, $WT_ROOT"; else echo "FAIL  cannot create state dirs"; rc=20; fi
  local busy
  busy="$(awaiting_sessions)"
  if [[ -n "$busy" ]]; then echo "note  sessions still awaiting: $busy"; else echo "ok    no session awaiting"; fi
  return $rc
}

# ── usage gate ───────────────────────────────────────────────────────────────
cmd_usage() {
  local threshold="${KTAGS_USAGE_THRESHOLD:-90}" json=0 agent="$DEFAULT_AGENT"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --agent) agent="$2"; shift 2 ;;
      --threshold) threshold="$2"; shift 2 ;;
      --json) json=1; shift ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  [[ "$agent" =~ ^(claude|codex)$ ]] || die "select the coordinator provider with --agent claude|codex or KTAGS_AGENT" 10
  need jq
  resolve_agent "$agent" >/dev/null
  if [[ "$agent" == codex ]]; then
    # No stable CLI percentage API: never fabricate a percentage or disable native limits.
    if [[ "$json" == 1 ]]; then
      jq -n '{gate:"provider",reason:"Native quota enforcement; no percentage preflight available",max_percent:null}'
    else
      echo "usage gate: provider — native quota enforcement; percentage preflight unavailable"
    fi
    return 0
  fi
  local claude_bin raw
  claude_bin="$(resolve_claude)"
  raw="$("$claude_bin" -p "/usage" 2>&1 || true)"
  local rows="" line label pct resets
  while IFS= read -r line; do
    if [[ "$line" =~ ^(Current[^:]*):[[:space:]]*([0-9]+)%\ used ]]; then
      label="${BASH_REMATCH[1]}"; pct="${BASH_REMATCH[2]}"; resets=""
      [[ "$line" =~ resets\ (.*)$ ]] && resets="${BASH_REMATCH[1]}"
      rows="$rows$(jq -nc --arg w "$label" --argjson p "$pct" --arg r "$resets" '{window:$w,percent:$p,resets:$r}')"$'\n'
    fi
  done <<<"$raw"
  local parsed="[]" count max=-1
  [[ -n "$rows" ]] && parsed="$(printf '%s' "$rows" | jq -s .)"
  count="$(echo "$parsed" | jq 'length')"
  [[ "$count" -gt 0 ]] && max="$(echo "$parsed" | jq 'map(.percent) | max')"
  local gate reason
  if [[ "$count" -eq 0 ]]; then gate="unknown"; reason="/usage output could not be parsed (fail closed)"
  elif [[ "$max" -ge "$threshold" ]]; then gate="hold"; reason="highest window ${max}% >= threshold ${threshold}%"
  else gate="ok"; reason="highest window ${max}% < threshold ${threshold}%"; fi
  if [[ "$json" == 1 ]]; then
    jq -n --argjson w "$parsed" --argjson m "$max" --argjson th "$threshold" --arg g "$gate" --arg r "$reason" --arg t "$(now_iso)" \
      '{gate:$g,reason:$r,max_percent:(if $m<0 then null else $m end),threshold:$th,windows:$w,measured_at:$t}'
  else
    echo "usage gate: $gate — $reason"
    echo "$parsed" | jq -r '.[] | "  \(.window): \(.percent)%  (resets \(.resets))"'
  fi
  case "$gate" in ok) return 0 ;; hold) return 75 ;; *) return 76 ;; esac
}

# ── spawn ────────────────────────────────────────────────────────────────────
cmd_spawn() {
  local issue="" stage="" pr="" round=1 prompt_file="" model="" effort="" json=0
  local agent="" use_worktree=1 skip_usage=0 threshold="${KTAGS_USAGE_THRESHOLD:-90}"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --issue) issue="$2"; shift 2 ;;
      --stage) stage="$2"; shift 2 ;;
      --agent) agent="$2"; shift 2 ;;
      --pr) pr="$2"; shift 2 ;;
      --round) round="$2"; shift 2 ;;
      --prompt-file) prompt_file="$2"; shift 2 ;;
      --model) model="$2"; shift 2 ;;
      --effort) effort="$2"; shift 2 ;;
      --threshold) threshold="$2"; shift 2 ;;
      --no-worktree) use_worktree=0; shift ;;
      --skip-usage-gate) skip_usage=1; shift ;;
      --json) json=1; shift ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  [[ "$issue" =~ ^[0-9]+$ ]] || die "--issue must be a number"
  [[ "$stage" =~ ^(dev|review)$ ]] || die "--stage must be dev or review"
  [[ "$round" =~ ^[0-9]+$ ]] || die "--round must be a number"
  [[ -z "$pr" || "$pr" =~ ^[0-9]+$ ]] || die "--pr must be a number"
  [[ -n "$prompt_file" && -r "$prompt_file" ]] || die "--prompt-file not readable: $prompt_file"
  [[ -z "$effort" || "$effort" =~ ^(low|medium|high|xhigh|max|ultra)$ ]] || die "--effort must be low|medium|high|xhigh|max|ultra"
  if [[ -z "$agent" ]]; then
    case "$stage" in dev) agent="${KTAGS_DEV_AGENT:-$DEFAULT_AGENT}" ;; review) agent="${KTAGS_REVIEW_AGENT:-$DEFAULT_AGENT}" ;; esac
  fi
  [[ "$agent" =~ ^(claude|codex)$ ]] || die "select the child provider with --agent claude|codex, KTAGS_DEV_AGENT/KTAGS_REVIEW_AGENT, or KTAGS_AGENT" 10
  if [[ -z "$model" ]]; then
    model="$(default_child_model "$agent")"
  fi
  need tmux; need jq; need git
  local agent_bin key rc dir
  agent_bin="$(resolve_agent "$agent")"
  key="$(session_key "$issue" "$stage" "$round")"
  rc="$(rc_name "$issue" "$stage" "$round")"
  dir="$(run_dir "$key")"
  mkdir -p "$RUN_ROOT" "$WT_ROOT"

  # Critical section: check-then-act guarded by a lock directory (atomic mkdir).
  local lock="$RUN_ROOT/.spawn.lock"
  local waited=0
  until mkdir "$lock" 2>/dev/null; do
    sleep 1; waited=$((waited + 1))
    [[ $waited -lt 60 ]] || die "spawn lock held for 60s: $lock (remove it if no spawn is running)" 60
  done
  trap 'rmdir "$lock" 2>/dev/null || true' EXIT

  tmux has-session -t "=$key" 2>/dev/null && die "'$key' is already running; use status" 60
  [[ -f "$dir/result.json" ]] && die "'$key' already reported; a new attempt needs --round $((round + 1))" 60
  local busy
  busy="$(awaiting_sessions "$key")"
  [[ -z "$busy" ]] && true || die "another session is still awaiting: $busy — one stage at a time" 60

  local workdir="$ROOT"
  if [[ "$use_worktree" == 1 ]]; then
    workdir="$WT_ROOT/$key"
    [[ -e "$workdir" ]] && die "worktree path exists: $workdir (clean first)" 60
    git -C "$ROOT" fetch -q origin main || die "git fetch origin main failed" 20
    git -C "$ROOT" diff --quiet origin/main -- AGENTS.md CLAUDE.md .agents .claude .codex scripts .githooks Makefile .gitignore docs/workflow.md docs/guides \
      || die "local workflow differs from origin/main; publish the prepared workflow before spawning" 60
    git -C "$ROOT" worktree add --detach -q "$workdir" origin/main || die "git worktree add failed" 20
  else
    local st
    st="$(git -C "$ROOT" status --porcelain 2>/dev/null)" || die "git status failed; cannot measure cleanliness" 60
    [[ -z "$st" ]] || die "working tree is dirty and --no-worktree given; the child would inherit and clobber it" 60
  fi

  if [[ "$skip_usage" == 0 ]]; then
    local ucode=0
    cmd_usage --agent "$agent" --threshold "$threshold" >&2 || ucode=$?
    if [[ "$ucode" -ne 0 ]]; then
      [[ "$use_worktree" == 1 ]] && git -C "$ROOT" worktree remove --force "$workdir" 2>/dev/null || true
      echo "session.sh: usage gate closed — '$key' NOT started" >&2
      exit "$ucode"
    fi
  fi

  mkdir -p "$dir"
  cp "$prompt_file" "$dir/prompt.md"
  : >"$dir/pane.log"
  jq -n --arg s "$key" --argjson i "$issue" --arg st "$stage" --arg pr "$pr" --argjson r "$round" --arg rc "$rc" \
    --arg agent "$agent" --arg cwd "$workdir" --arg t "$(now_iso)" --argjson e "$(epoch)" --arg model "$model" --arg effort "$effort" \
    --arg sha "$(git -C "$ROOT" rev-parse origin/main 2>/dev/null || git -C "$ROOT" rev-parse HEAD)" \
    '{session:$s,issue:$i,stage:$st,pr:(if $pr=="" then null else ($pr|tonumber) end),round:$r,rc_name:$rc,
      agent:$agent,cwd:$cwd,started_at:$t,started_epoch:$e,base_sha:$sha,model:$model,effort:(if $effort=="" then null else $effort end)}' \
    >"$dir/meta.json"

  local launch
  launch="$(launch_command "$agent" "$agent_bin" "$model" "$effort" "$rc")"
  cat >"$dir/run.sh" <<RUNNER
#!/usr/bin/env bash
cd $(printf '%q' "$workdir")
export KTAGS_RUN_DIR=$(printf '%q' "$RUN_ROOT")
export KTAGS_SESSION=$(printf '%q' "$key")
export KTAGS_ROOT=$(printf '%q' "$ROOT")
export KTAGS_CONTEXT_ROOT=$(printf '%q' "$ROOT")
set +e
$launch \\
  "\$(cat $(printf '%q' "$dir/prompt.md"))"
code=\$?
printf '{"exit_code": %d, "exited_at": "%s"}\n' "\$code" "\$(date -u +%Y-%m-%dT%H:%M:%SZ)" > $(printf '%q' "$dir/exit.json")
RUNNER
  chmod +x "$dir/run.sh"
  tmux new-session -d -s "$key" -c "$workdir" "$dir/run.sh"
  tmux set-window-option -t "${key}:" remain-on-exit on >/dev/null
  tmux pipe-pane -o -t "${key}:" "cat >> $(printf '%q' "$dir/pane.log")"
  rmdir "$lock" 2>/dev/null || true
  trap - EXIT

  sleep 2
  if ! tmux has-session -t "=$key" 2>/dev/null; then
    echo "session.sh: '$key' died within 2s — last output:" >&2
    tail -n 20 "$dir/pane.log" >&2 || true
    exit 20
  fi
  if [[ "$json" == 1 ]]; then
    jq -n --arg s "$key" --arg rc "$rc" --arg d "$dir" --arg w "$workdir" '{spawned:true,session:$s,rc_name:$rc,run_dir:$d,workdir:$w}'
  else
    echo "spawned  $key  (agent: $agent, model: ${model:-inherited})"
    echo "workdir  $workdir"
    echo "run dir  $dir"
  fi
}

# ── status / wait ────────────────────────────────────────────────────────────
one_status() {
  local key="$1" dir alive="dead" now pane_age=null pane_bytes=0 result=null exitj=null url=""
  dir="$(run_dir "$key")"
  [[ -d "$dir" ]] || die "unknown session: $key"
  tmux has-session -t "=$key" 2>/dev/null && alive="alive"
  now="$(epoch)"
  if [[ -f "$dir/pane.log" ]]; then
    pane_bytes="$(wc -c <"$dir/pane.log" | tr -d ' ')"
    pane_age=$((now - $(mtime "$dir/pane.log")))
  fi
  [[ -f "$dir/result.json" ]] && result="$(jq -c . "$dir/result.json" 2>/dev/null || echo '{"malformed":true}')"
  [[ -f "$dir/exit.json" ]] && exitj="$(jq -c . "$dir/exit.json" 2>/dev/null || echo null)"
  url="$(grep -o 'https://claude.ai/code/session_[A-Za-z0-9]*' "$dir/pane.log" 2>/dev/null | tail -1 || true)"
  jq -n --slurpfile meta "$dir/meta.json" --arg alive "$alive" --argjson result "$result" --argjson exit "$exitj" \
    --arg url "$url" --argjson pb "$pane_bytes" --argjson pa "$pane_age" \
    --argjson age "$((now - $(jq -r .started_epoch "$dir/meta.json")))" \
    '$meta[0] + {tmux:$alive,age_seconds:$age,pane_bytes:$pb,pane_idle_seconds:$pa,result:$result,exit:$exit,
                 remote_url:(if $url=="" then null else $url end),awaiting:($alive=="alive" and $result==null)}'
}

cmd_status() {
  local json=0 issue="" keys="" k d
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --json) json=1; shift ;;
      --issue) issue="$2"; shift 2 ;;
      *) keys="$keys $1"; shift ;;
    esac
  done
  need jq
  if [[ -z "$keys" ]]; then
    for d in "$RUN_ROOT"/ktags-${issue:+$issue-}*; do [[ -d "$d" ]] && keys="$keys $(basename "$d")"; done
  fi
  if [[ -z "${keys// /}" ]]; then [[ "$json" == 1 ]] && echo "[]" || echo "(no sessions)"; return 0; fi
  local out=""
  for k in $keys; do out="$out$(one_status "$k")"$'\n'; done
  local arr
  arr="$(printf '%s' "$out" | jq -s .)"
  if [[ "$json" == 1 ]]; then echo "$arr"; else
    echo "$arr" | jq -r '.[] | "\(.session)  tmux=\(.tmux)  age=\(.age_seconds)s  idle=\(.pane_idle_seconds)s  result=\(if .result then .result.status else "-" end)"'
  fi
}

cmd_wait() {
  local key="" timeout=0 interval=20
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --timeout) timeout="$2"; shift 2 ;;
      --interval) interval="$2"; shift 2 ;;
      -*) die "unknown argument: $1" ;;
      *) key="$1"; shift ;;
    esac
  done
  [[ -n "$key" ]] || die "usage: session.sh wait <key> [--timeout s] [--interval s]"
  need jq
  local dir started outcome=""
  dir="$(run_dir "$key")"
  [[ -d "$dir" ]] || die "unknown session: $key"
  started="$(epoch)"
  while :; do
    if [[ -f "$dir/result.json" ]]; then outcome="result"; break; fi
    if ! tmux has-session -t "=$key" 2>/dev/null; then
      # result.json first, liveness second: a child that reports and exits within ms must count as result.
      if [[ -f "$dir/result.json" ]]; then outcome="result"; else outcome="dead_no_result"; fi
      break
    fi
    if [[ "$timeout" -gt 0 && $(($(epoch) - started)) -ge "$timeout" ]]; then outcome="timeout"; break; fi
    sleep "$interval"
  done
  local result=null
  [[ -f "$dir/result.json" ]] && result="$(jq -c . "$dir/result.json" 2>/dev/null || echo '{"malformed":true}')"
  jq -cn --arg o "$outcome" --arg s "$key" --argjson r "$result" --argjson w "$(($(epoch) - started))" --arg t "$(now_iso)" \
    '{outcome:$o,session:$s,result:$r,waited_seconds:$w,measured_at:$t}'
  case "$outcome" in result) return 0 ;; dead_no_result) return 61 ;; *) return 62 ;; esac
}

# ── verify: derive stage completion from GitHub, not from the child ─────────
issue_labels() { gh issue view "$1" --repo "$REPO" --json labels --jq '[.labels[].name]'; }

find_pr() {
  # PR whose head branch follows the contract <type>/<issue>-<slug>; newest first.
  gh pr list --repo "$REPO" --state all --limit 50 --json number,headRefName,state,isDraft,url \
    | jq -c --arg n "$1" '[.[] | select(.headRefName | test("^[a-z]+/" + $n + "-"))] | sort_by(.number) | reverse | .[0] // empty'
}

cmd_verify() {
  local issue="" stage="" json=0
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --issue) issue="$2"; shift 2 ;;
      --stage) stage="$2"; shift 2 ;;
      --json) json=1; shift ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  [[ "$issue" =~ ^[0-9]+$ ]] || die "--issue must be a number"
  [[ "$stage" =~ ^(dev|review)$ ]] || die "--stage must be dev or review"
  need gh; need jq
  local approval=false labels pr prnum="" reasons="[]" done=false verdict="none" ci="none" prstate="" draft=true
  labels="$(issue_labels "$issue")" || die "gh issue view failed for #$issue" 20
  pr="$(find_pr "$issue")"
  if [[ -n "$pr" ]]; then
    prnum="$(echo "$pr" | jq -r .number)"
    prstate="$(echo "$pr" | jq -r .state)"
    draft="$(echo "$pr" | jq -r .isDraft)"
    local checks
    checks="$(gh pr checks "$prnum" --repo "$REPO" --json name,state 2>/dev/null)" || true
    echo "$checks" | jq -e 'type == "array"' >/dev/null 2>&1 || checks='[]'
    ci="$(echo "$checks" | jq -r '[.[] | select(.name=="ci-required")] | if length==0 then "none" else (.[0].state|ascii_downcase) end')"
    local review_state
    review_state="$(KTAGS_REPO="$REPO" python3 "$ROOT/scripts/pr-state.py" --pr "$prnum")" || die "cannot verify review evidence" 20
    verdict="$(echo "$review_state" | jq -r .verdict)"
    approval="$(echo "$review_state" | jq -r .maintainer_approved)"
  fi
  add() { reasons="$(echo "$reasons" | jq -c --arg r "$1" '. + [$r]')"; }
  case "$stage" in
    dev)
      [[ -n "$prnum" ]] || add "no PR with head branch <type>/$issue-* found"
      [[ "$prstate" == "OPEN" || "$prstate" == "MERGED" ]] || { [[ -n "$prnum" ]] && add "PR #$prnum is $prstate"; }
      [[ "$draft" == "false" ]] || { [[ -n "$prnum" ]] && add "PR #$prnum is still a draft"; }
      echo "$labels" | jq -e 'index("stage:review")' >/dev/null || add "issue lacks label stage:review (has: $(echo "$labels" | jq -c .))"
      [[ "$ci" == "success" ]] || add "ci-required is '$ci' (need success)"
      [[ "$(echo "$reasons" | jq length)" -eq 0 ]] && done=true
      ;;
    review)
      [[ -n "$prnum" ]] || add "no PR found for #$issue"
      case "$verdict" in
        accept|reject)
          [[ "$prstate" == OPEN && "$draft" == false && "$ci" == success ]] && done=true \
            || add "review requires an open ready PR with ci-required success"
          ;;
        *) add "advisory verdict is '$verdict' (need accept or reject for this head)" ;;
      esac
      ;;
  esac
  local blocked=false
  echo "$labels" | jq -e 'index("blocked")' >/dev/null && blocked=true
  local out
  out="$(jq -n --argjson i "$issue" --arg st "$stage" --argjson d "$done" --arg v "$verdict" --arg ci "$ci" \
    --arg pr "$prnum" --arg prs "$prstate" --argjson draft "$draft" --argjson labels "$labels" --argjson reasons "$reasons" \
    --argjson approved "$approval" --argjson blocked "$blocked" --arg t "$(now_iso)" \
    '{issue:$i,stage:$st,done:$d,verdict:$v,ci:$ci,pr:(if $pr=="" then null else ($pr|tonumber) end),pr_state:$prs,
      draft:$draft,labels:$labels,maintainer_approved:$approved,blocked:$blocked,reasons:$reasons,measured_at:$t}')"
  if [[ "$json" == 1 ]]; then echo "$out"; else
    echo "$out" | jq -r '"verify #\(.issue) \(.stage): done=\(.done) verdict=\(.verdict) ci=\(.ci) pr=\(.pr // "-") blocked=\(.blocked)" , (.reasons[] | "  - " + .)'
  fi
  if [[ "$blocked" == true ]]; then return 71; fi
  [[ "$done" == true ]] && return 0 || return 70
}

# ── report (called by the child session) ─────────────────────────────────────
cmd_report() {
  local key="${KTAGS_SESSION:-}" status="" summary="" artifact="" next=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --session) key="$2"; shift 2 ;;
      --status) status="$2"; shift 2 ;;
      --summary) summary="$2"; shift 2 ;;
      --artifact) artifact="$2"; shift 2 ;;
      --next) next="$2"; shift 2 ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  [[ -n "$key" ]] || die "--session missing and KTAGS_SESSION empty"
  [[ "$status" =~ ^(ready_for_review|accept|reject|blocked|refused|done)$ ]] || die "--status: ready_for_review|accept|reject|blocked|refused|done"
  need jq
  local dir tmp
  dir="$(run_dir "$key")"
  [[ -d "$dir" ]] || die "unknown session: $key"
  tmp="$dir/.result.json.tmp"
  if ! jq -n --arg s "$key" --arg st "$status" --arg sum "$summary" --arg a "$artifact" --arg n "$next" --arg t "$(now_iso)" \
    '{session:$s,status:$st,summary:$sum,artifact:(if $a=="" then null else $a end),next:(if $n=="" then null else $n end),reported_at:$t}' >"$tmp"; then
    rm -f "$tmp"; die "could not write result.json"
  fi
  mv -f "$tmp" "$dir/result.json"   # atomic: a waiter sees nothing or the whole file
  echo "reported $key -> $status"
}

# ── logs / send / stop / clean ───────────────────────────────────────────────
cmd_logs() {
  local key="" n=60
  while [[ $# -gt 0 ]]; do
    case "$1" in -n) n="$2"; shift 2 ;; *) key="$1"; shift ;; esac
  done
  [[ -n "$key" ]] || die "usage: session.sh logs <key> [-n lines]"
  local dir; dir="$(run_dir "$key")"
  if tmux has-session -t "=$key" 2>/dev/null; then tmux capture-pane -p -t "${key}:" | tail -n "$n"
  elif [[ -f "$dir/pane.log" ]]; then tail -n "$n" "$dir/pane.log"
  else die "no log for $key"; fi
}

cmd_send() {
  local key="${1:-}"; shift || true
  [[ -n "$key" && $# -gt 0 ]] || die "usage: session.sh send <key> <text>"
  tmux has-session -t "=$key" 2>/dev/null || die "session not running: $key"
  tmux send-keys -t "${key}:" -l -- "$*"; sleep 0.3; tmux send-keys -t "${key}:" Enter
  echo "sent -> $key"
}

cmd_stop() {
  local key="${1:-}"
  [[ -n "$key" ]] || die "usage: session.sh stop <key>"
  tmux kill-session -t "=$key" 2>/dev/null && echo "stopped $key" || echo "already stopped: $key"
}

cmd_clean() {
  local issue="" force=0 purge=0 json=0 d k keys="" busy=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --issue) issue="$2"; shift 2 ;;
      --force) force=1; shift ;;
      --purge) purge=1; shift ;;
      --json) json=1; shift ;;
      *) die "unknown argument: $1" ;;
    esac
  done
  [[ "$issue" =~ ^[0-9]+$ ]] || die "--issue must be a number"
  for d in "$RUN_ROOT"/ktags-"$issue"-*; do [[ -d "$d" ]] && keys="$keys $(basename "$d")"; done
  [[ -n "${keys// /}" ]] || { echo "nothing to clean for #$issue"; return 0; }
  if [[ "$force" == 0 ]]; then
    for k in $keys; do
      tmux has-session -t "=$k" 2>/dev/null && [[ ! -f "$(run_dir "$k")/result.json" ]] && busy="$busy $k"
    done
    [[ -z "${busy// /}" ]] || die "still running without a result:$busy (use --force deliberately)" 60
  fi
  local archive="$RUN_ROOT/archive" stamp cleaned=""
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  mkdir -p "$archive"
  for k in $keys; do
    tmux kill-session -t "=$k" 2>/dev/null || true
    if [[ -d "$WT_ROOT/$k" ]]; then git -C "$ROOT" worktree remove --force "$WT_ROOT/$k" 2>/dev/null || rm -rf "$WT_ROOT/$k"; fi
    if [[ "$purge" == 1 ]]; then rm -rf "$(run_dir "$k")"; else mv "$(run_dir "$k")" "$archive/$stamp-$k"; fi
    cleaned="$cleaned $k"
  done
  git -C "$ROOT" worktree prune 2>/dev/null || true
  if [[ "$json" == 1 ]]; then
    printf '%s\n' $cleaned | jq -R . | jq -s --argjson i "$issue" --arg a "$archive" --argjson p "$purge" '{issue:$i,cleaned:.,archived_to:(if $p==1 then null else $a end)}'
  else
    echo "cleaned:$cleaned"; [[ "$purge" == 1 ]] && echo "run dirs deleted" || echo "run dirs archived under $archive"
  fi
}

usage() { sed -n '2,22p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

main() {
  local cmd="${1:-}"; shift || true
  case "$cmd" in
    doctor) cmd_doctor "$@" ;;
    usage) cmd_usage "$@" ;;
    spawn) cmd_spawn "$@" ;;
    status) cmd_status "$@" ;;
    wait) cmd_wait "$@" ;;
    verify) cmd_verify "$@" ;;
    report) cmd_report "$@" ;;
    logs) cmd_logs "$@" ;;
    send) cmd_send "$@" ;;
    stop) cmd_stop "$@" ;;
    clean) cmd_clean "$@" ;;
    -h|--help|help|"") usage ;;
    *) usage; exit 10 ;;
  esac
}
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then main "$@"; fi
