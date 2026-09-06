# Agentic development workflow for `ktags` — state of the art, September 2026

Research date: **2026-09-07**. Target: `ktags`, an open-source Go CLI+TUI for operating
Kubernetes clusters, GitHub-hosted, **one human maintainer + AI coding agents**
(Claude Code, OpenAI Codex CLI, OpenCode; ideally Gemini CLI, Cursor, Copilot CLI).

Goal: a workflow that is **robust (mechanical gates, not trust)** and **tool-agnostic**.

Reference design being evaluated (from a previous project):

- `AGENTS.md` as single entry point, `CLAUDE.md` containing only `@AGENTS.md`
- skills in `.agents/skills/<name>/SKILL.md`
- a custom guard script with schema-validated handoff metadata between phases
  (issue → independent review in a fresh session → delivery)
- husky hooks
- GitLab labels as a state machine
- "no AI attribution in commits" rule
- a coordinator that spawns phases as separate tmux sessions

> Every claim below carries a URL. Items I could not verify are marked
> **[UNVERIFIED]**. Note: the web-search budget for this session was exhausted partway
> through; the later sections lean more heavily on direct fetches of primary docs.

**Ground truth from the maintainer's machine, 2026-09-07** (`--version` output, so these
are hard facts rather than doc claims):

```
Claude Code    2.1.263
codex-cli      0.153.4
opencode       1.18.20
gh             2.100.0 (2026-09-03)
go             1.27.1 darwin/arm64
golangci-lint  2.13.2  (built 2026-08-27, go1.27.0)
lefthook       not installed
```

---

## TL;DR — is the reference design still state of the art?

**Mostly yes in its principles, mostly no in its implementation.** Six of its eight elements
have been absorbed into the tools themselves since it was written.

**Still correct, keep as-is:**

- `AGENTS.md` as the single entry with `CLAUDE.md` = `@AGENTS.md`. Claude Code **still** does
  not read `AGENTS.md` natively (Anthropic's docs say so explicitly), and its own prescribed
  workaround is exactly this shim.
- Skills in `.agents/skills/`. This has gone from a convention to **the convergence point**:
  Codex's primary project location, read by OpenCode, and *preferred over its own directory*
  by Gemini CLI. Claude Code is the only holdout — bridged by a committed symlink (verified
  working).
- Independent review in a fresh session, and "no AI attribution in commits".

**Now redundant or replaceable by native features:**

- The **schema-validated handoff guard script** → `claude -p --output-format json
  --json-schema` and `codex exec --output-schema`.
- The **fresh-session reviewer machinery** → a plain `.claude/agents/reviewer.md` (already
  starts with no history) or `codex review --base main`.
- The **tmux coordinator** → shipped natively as agent teams (`teammateMode: "tmux"`) but
  experimental, and every measured 2026 result argues against multi-agent orchestration at
  solo scale (3–10× tokens; single-agent matches multi-agent at equal budget).
- **husky** → no release since 2024-11-18; use lefthook as a `go.mod` tool.
- **Labels as a state machine** → issue types + a Projects v2 `Status` field + sub-issues.
- Bespoke agent sandboxing → client-enforced worktree isolation that *cannot be disabled*.

**The one thing to change on the evidence, not just for convenience:** the reference design
optimises the *instruction* layer. The 2026 measurements say that layer barely works — repo
overviews in context files *"are not helpful"* while adding **>20% inference cost**
([arXiv:2602.11988](https://arxiv.org/abs/2602.11988)), instruction adherence decays with
count, and the only *measured* wins come from deterministic gates and **cross-model** review.
Cut `AGENTS.md` to ~100 lines, and spend the saved effort on `ci-required`, a Stop hook, and a
different model reviewing the diff.

---

## 1. The AGENTS.md standard

### Status as of September 2026

`AGENTS.md` is the de-facto cross-tool instruction file: "a README for agents", plain
Markdown with no mandatory fields
([agents.md](https://agents.md/), fetched 2026-09-07).

The site's own list of agents with **native** support is:

> "Codex from OpenAI, Jules from Google, Factory, Aider, goose, opencode, Zed, Warp,
> VS Code, Devin from Cognition, Autopilot & Coded Agents from UiPath, Junie from
> JetBrains, Amp, Cursor, RooCode, Gemini CLI from Google, Kilo Code, Phoenix, Semgrep,
> Coding agent from GitHub Copilot, Ona, Windsurf from Cognition, Augment Code"
> — [agents.md](https://agents.md/), fetched 2026-09-07

**Claude Code is conspicuously absent from that list.** Secondary sources report the
format was placed under the Linux Foundation's Agentic AI Foundation in December 2025
with backing from OpenAI, Anthropic, Google and AWS, and that 28+ tools and 60k+ public
repos carry the file
([buildbetter.ai, 2026](https://blog.buildbetter.ai/agents-md-complete-guide-for-engineering-teams-in-2026/);
[codersera, 2026](https://codersera.com/blog/agents-md-complete-guide-2026/)).
The Linux-Foundation governance claim is secondary-source only — **[UNVERIFIED]** against
a primary LF announcement.

### Does Claude Code read AGENTS.md natively? No.

This is settled by Anthropic's own docs, which have an explicit `AGENTS.md` section:

> "Claude Code reads `CLAUDE.md`, not `AGENTS.md`. If your repository already uses
> `AGENTS.md` for other coding agents, create a `CLAUDE.md` that imports it so both
> tools read the same instructions without duplicating them."
> — [code.claude.com/docs/en/memory](https://code.claude.com/docs/en/memory), fetched 2026-09-07

The two documented options:

```markdown
<!-- CLAUDE.md -->
@AGENTS.md

## Claude Code
Use plan mode for changes under `src/billing/`.
```

or, when no Claude-specific content is needed:

```bash
ln -s AGENTS.md CLAUDE.md
```

Anthropic notes the symlink needs Administrator privileges or Developer Mode on Windows,
so the `@AGENTS.md` import is the portable choice. Two adjacent features exist:

- `/init` with `CLAUDE_CODE_NEW_INIT=1` reads `AGENTS.md`, `.cursor/rules/`,
  `.github/copilot-instructions.md`, `.devin/rules/`, `.windsurf/rules/`, `.clinerules`
  and folds relevant parts into the generated `CLAUDE.md`.
- `/import` (**requires Claude Code v2.1.213+**) appends a *one-time copy* of another
  agent's instruction files to `CLAUDE.md` and carries over MCP servers, commands,
  subagents and skills.
  ([memory docs](https://code.claude.com/docs/en/memory))

Both are one-shot copies, not live reads. **So `@AGENTS.md` in `CLAUDE.md` remains the
correct answer in September 2026.** Community trackers agree the upstream issue is still
open with no roadmap signal
([gist tracking issue #6235, 2026](https://gist.github.com/yurukusa/d36197848911f025add142abefcde685)).

### Nesting rules

| Tool | Rule | Source |
|---|---|---|
| Generic spec | Nearest `AGENTS.md` to the edited file wins; root file is the default | [agents.md](https://agents.md/) |
| Codex | Global `~/.codex/AGENTS.override.md` → `~/.codex/AGENTS.md` → every level from git root down to cwd (override files first). Concatenated root-downward; **closer files override earlier guidance because they appear later in the prompt** | [learn.chatgpt.com/docs/agent-configuration/agents-md](https://learn.chatgpt.com/docs/agent-configuration/agents-md.md) |
| Claude Code | `CLAUDE.md` + `CLAUDE.local.md` loaded from cwd and every ancestor at launch, concatenated root→cwd (closest read last). Subdirectory files load **on demand** when Claude reads files there | [memory docs](https://code.claude.com/docs/en/memory) |
| OpenCode | `instructions` array of paths/globs in `opencode.json`, plus native AGENTS.md | [opencode.ai/docs/config](https://opencode.ai/docs/config/) |

### Size guidance — concrete numbers

- **Codex**: hard cap of **32 KiB** by default, tunable via `project_doc_max_bytes`;
  the docs suggest splitting across nested directories when you hit the cap.
  ([Codex AGENTS.md docs](https://learn.chatgpt.com/docs/agent-configuration/agents-md.md))
- **Claude Code**: *"target under 200 lines per CLAUDE.md file. Longer files consume more
  context and reduce adherence."* A file over **4 MiB** is skipped entirely. Splitting via
  `@path` imports helps organisation but **does not reduce context**, because imports are
  expanded at launch.
  ([memory docs](https://code.claude.com/docs/en/memory))
- Claude Code's `/doctor` (**v2.1.206+**) actively proposes trims to a checked-in
  `CLAUDE.md`: it cuts what Claude can derive from the codebase (directory layouts,
  dependency lists, architecture overviews) and keeps pitfalls, rationale and
  conventions that differ from tool defaults. That is a good editorial rule of thumb.

### The genuinely new thing: `.claude/rules/` with path scoping

Claude Code now supports `.claude/rules/*.md`, discovered recursively, each optionally
carrying `paths:` frontmatter with glob patterns. Rules **without** `paths` load at launch
at the same priority as `.claude/CLAUDE.md`; rules **with** `paths` load only when Claude
reads a matching file. `.claude/rules/` supports symlinks (including to a shared directory).
Project rules are skipped when `project` is excluded from `--setting-sources`.
([memory docs](https://code.claude.com/docs/en/memory))

This is the native, zero-context-cost answer to "my AGENTS.md is getting long" — but it is
**Claude-Code-only**. There is no cross-tool equivalent; Codex's equivalent is nested
`AGENTS.md` files, OpenCode's is the `instructions` glob list.

### What this means for ktags

Keep `AGENTS.md` as the single tool-agnostic entry point and keep it **under ~200 lines /
well under 32 KiB**; make `CLAUDE.md` a two-line file (`@AGENTS.md` plus any Claude-only
notes). Push everything path-specific or procedural out of `AGENTS.md` into skills and
`docs/`, and mirror the Claude-only path-scoped bits into `.claude/rules/` only if the
duplication earns its keep. The reference design's `AGENTS.md`-as-single-entry choice is
**still correct and still the only portable option**.

---

## 2. Agent Skills (`SKILL.md`)

### The spec

The Agent Skills format was developed by Anthropic, released as an open standard, and is
now documented at [agentskills.io](https://agentskills.io/) with a public repo at
[github.com/agentskills/agentskills](https://github.com/agentskills/agentskills).
Secondary sources date the public spec release to **2025-12-18** and say it is stewarded
through the Agentic AI Foundation
([thepromptindex, 2026](https://www.thepromptindex.com/how-to-use-ai-agent-skills-the-complete-guide.html))
— the exact date and the foundation stewardship are **[UNVERIFIED]** against a primary
announcement. The spec page itself carries **no version number**.

A skill is a directory with a required `SKILL.md` plus optional `scripts/`, `references/`,
`assets/`.

### Portable frontmatter (the whole spec surface)

| Field | Required | Constraints |
|---|---|---|
| `name` | Yes | 1–64 chars, lowercase `a-z0-9` and `-`, no leading/trailing/consecutive hyphens, **must match the parent directory name** |
| `description` | Yes | 1–1024 chars; say what it does *and* when to use it |
| `license` | No | License name or bundled file reference |
| `compatibility` | No | ≤500 chars; environment requirements |
| `metadata` | No | string→string map for client-specific extras |
| `allowed-tools` | No | space-separated pre-approved tools — **experimental, support varies** |

— [agentskills.io/specification](https://agentskills.io/specification), fetched 2026-09-07

Progressive-disclosure budget from the same page: metadata **~100 tokens** loaded at
startup for every skill; the `SKILL.md` body **< 5000 tokens recommended**; keep the main
`SKILL.md` **under 500 lines** and push detail into `references/`. Validation tooling:
`skills-ref validate ./my-skill`
([skills-ref](https://github.com/agentskills/agentskills/tree/main/skills-ref)).

### Directory discovery — where the tools actually look

This is the crux of "is there convergence on a shared directory". The answer as of
2026-09-07 is: **`.agents/skills/` is the converged cross-tool location — Codex scans it as
its *primary* project location, OpenCode reads it, Gemini CLI reads it and gives it
*precedence over its own* `.gemini/skills/` — and Claude Code is the sole holdout.**

| Tool | Project locations | Global locations | Source |
|---|---|---|---|
| **Codex** | `.agents/skills` — scanned in *every* directory from cwd up to the repo root | `$HOME/.agents/skills`, `/etc/codex/skills`, plus OpenAI-bundled | [learn.chatgpt.com/docs/build-skills](https://learn.chatgpt.com/docs/build-skills) |
| **OpenCode** | `.opencode/skills/`, `.claude/skills/`, **`.agents/skills/`** (traverses upward to the git worktree root) | `~/.config/opencode/skills/`, `~/.claude/skills/`, `~/.agents/skills/` | [opencode.ai/docs/skills](https://opencode.ai/docs/skills/) |
| **Claude Code** | **`.claude/skills/<name>/SKILL.md` only** — plus nested `.claude/skills/` in subdirs, plugin `skills/`, and `--add-dir` skills | `~/.claude/skills/`, enterprise managed dir | [code.claude.com/docs/en/skills](https://code.claude.com/docs/en/skills) |
| **Gemini CLI** | `.gemini/skills/` **or `.agents/skills/`** — *"within the same tier (user or workspace), the `.agents/skills/` alias takes precedence over the `.gemini/skills/` directory"* | `~/.gemini/skills/` or `~/.agents/skills/` | [geminicli.com/docs/cli/skills](https://geminicli.com/docs/cli/skills/) |
| Cursor | [cursor.com/docs/context/skills](https://cursor.com/docs/context/skills) | — | [agentskills.io](https://agentskills.io/) |
| GitHub Copilot / VS Code | [docs.github.com/.../about-agent-skills](https://docs.github.com/en/copilot/concepts/agents/about-agent-skills), [code.visualstudio.com/docs/copilot/customization/agent-skills](https://code.visualstudio.com/docs/copilot/customization/agent-skills) | — | [agentskills.io](https://agentskills.io/) |

A targeted re-read of the Claude Code skills page confirms `.agents/` appears **nowhere**
in it, while the page does explicitly say:

> "Claude Code skills follow the [Agent Skills](https://agentskills.io) open standard,
> which works across multiple AI tools. Claude Code extends the standard with additional
> features like invocation control, subagent execution, and dynamic context injection."
> — [code.claude.com/docs/en/skills](https://code.claude.com/docs/en/skills), fetched 2026-09-07

Some third-party guides claim Codex also reads `.codex/skills/` (project) and
`~/.codex/skills/` (global)
([ITECS, 2026](https://itecsonline.com/post/codex-cli-agent-skills-guide-install-usage-cross-platform-resources-2026)).
The official Codex docs list only `.agents/skills`, `$HOME/.agents/skills`,
`/etc/codex/skills` and bundled skills — treat `.codex/skills` as **[UNVERIFIED]**.

### Portable vs tool-specific frontmatter

Claude Code accepts a much larger frontmatter surface than the spec, and enforces the
distinction at packaging time:

- **Portable (spec) fields**: `name`, `description`, `license`, `compatibility`,
  `metadata`, `allowed-tools`
- **Claude-Code-only fields**: `when_to_use`, `disable-model-invocation`, `user-invocable`,
  `disallowed-tools`, `model`, `effort`, `context: fork`, `agent`, `background`,
  `arguments`, `argument-hint`, `paths`, `shell`, `hooks`

Uploading a skill with a non-spec field to claude.ai / the Skills API fails hard:

> `Unexpected key(s) in SKILL.md frontmatter: disable-model-invocation. Allowed properties
> are: allowed-tools, compatibility, description, license, metadata, name`
> — [code.claude.com/docs/en/skills](https://code.claude.com/docs/en/skills)

OpenCode requires `name` + `description` and accepts `license`, `compatibility`,
`metadata` — i.e. the spec exactly
([opencode.ai/docs/skills](https://opencode.ai/docs/skills/)).

### Skill sprawl has a measurable cost — and Claude Code now surfaces it

- Skill **descriptions** are sent to Claude **every turn**, not just at startup, capped at
  **1% of the context window** (`skillListingBudgetFraction`, default `0.01`) with a
  per-description cap of **1,536 characters** (`skillListingMaxDescChars`). Over budget,
  *"Claude Code keeps every skill's name but drops the descriptions of the least-used
  skills, so Claude can still invoke those skills but is less likely to choose one on its
  own"* ([settings reference](https://code.claude.com/docs/en/settings-reference#skilllistingbudgetfraction)).
  Skill sprawl therefore degrades *silently*: your newest skill stops being auto-selected
  and nothing tells you why.
- **`/skill-doctor`** (added in **v2.1.252**, surfaced in the changelog for **v2.1.261,
  2026-09-04**) *"shows unused loaded skills and their context cost for pruning"*.
  ([skills docs](https://code.claude.com/docs/en/skills);
  [changelog](https://code.claude.com/docs/en/changelog))

That is a strong signal that "too many skills" is a real, vendor-acknowledged failure mode.

### What this means for ktags

Author skills **once** in `.agents/skills/<name>/SKILL.md` using **only spec frontmatter**
— that gets you Codex, OpenCode and Gemini CLI for free and is the future-proof location.
For Claude Code, commit a symlink `.claude/skills -> ../.agents/skills`.

> **Verified experimentally on 2026-09-07** against locally installed **Claude Code
> v2.1.263**: a scratch git repo with `.agents/skills/zzz-canary-skill/SKILL.md` and
> `ln -s ../.agents/skills .claude/skills` — `claude -p` discovered and invoked the skill
> and returned the canary string. The Claude Code docs document symlink support only for
> `.claude/rules/`, but it works for `.claude/skills/` too. Note that a symlink checked
> into git is a one-line blob, so this costs nothing and needs no sync step. (Windows
> contributors will need Developer Mode; the fallback is a `make sync-skills` copy target.)

Keep the skill count small (start with 2–4) and run `/skill-doctor` periodically to see
what the descriptions are costing.

---

## 3. Claude Code project configuration in 2026

Current version at time of research: **v2.1.263 (2026-09-06)**
([changelog](https://code.claude.com/docs/en/changelog)).

### Settings files and precedence

Highest to lowest ([settings docs](https://code.claude.com/docs/en/settings)):

1. **Managed settings** (`managed-settings.json`, MDM, or the claude.ai console) — org
2. `--settings` on the command line — this session
3. `.claude/settings.local.json` — you, this project
4. `.claude/settings.json` — **everyone in the project (commit this)**
5. `~/.claude/settings.json` — you, every project

Team-repo guidance, verbatim:

> "Commit `.claude/settings.json` so everyone who clones the repository gets the same
> permissions, hooks, telemetry, and plugins. Each teammate can still override it for
> themselves in their own `.claude/settings.local.json`."
> — [settings docs](https://code.claude.com/docs/en/settings)

`.claude/settings.local.json` is added to your **global** git excludes the first time
Claude Code writes it; if you create it by hand, gitignore it yourself.

### Workspace trust — the part that bites

From a committed `.claude/settings.json`, these keys **wait for the folder to be trusted**:
`permissions.allow`, `permissions.additionalDirectories`, `extraKnownMarketplaces`, and
most `env` values. **`deny` and `ask` rules apply immediately.**
([settings docs](https://code.claude.com/docs/en/settings))

Practical consequence: a committed deny-list is a real gate from first clone; a committed
allow-list is a convenience that only kicks in after trust.

Deny-rule syntax and its honest limits
([settings reference](https://code.claude.com/docs/en/settings-reference#permissions-deny)):

```json
{ "permissions": { "deny": [
  "Read(./.env)", "Read(./.env.*)", "Read(./secrets/**)", "Bash(curl *)"
] } }
```

> "Read and Edit deny rules apply to Claude's built-in file tools, to file commands Claude
> Code recognizes in Bash, such as `cat`, `head`, `tail`, and `sed`, and to the targets of
> Bash redirections such as `> file` and `< file`; **they don't apply to arbitrary
> subprocesses**, so for OS-level enforcement [enable the sandbox]."

Tool names accept globs, so `"*"` denies every tool and `"mcp__*"` every MCP tool. This key
replaces the deprecated `ignorePatterns`.

`permissions.defaultMode` accepts `"default"` (reads only), `"acceptEdits"`, `"plan"`
(reads and plans but blocks edits until you approve a plan), `"auto"`, `"bypassPermissions"`
— but **`auto` and `bypassPermissions` deliberately do not take effect from project or
local settings** (since v2.1.257), so a committed `"defaultMode": "plan"` is a safe,
enforceable repo-level default that a cloned repo cannot use to escalate anyone's
permissions ([settings reference](https://code.claude.com/docs/en/settings-reference#permissions-defaultmode)). Permission-rule patterns use a trailing ` *` for
prefix matching, and **the space matters**: `Bash(git diff *)` matches any `git diff …`,
while `Bash(git diff*)` would also match `git diff-index`
([headless docs](https://code.claude.com/docs/en/headless)).

### Hooks — the full 2026 event list

([hooks reference](https://code.claude.com/docs/en/hooks), fetched 2026-09-07)

| Event | Fires |
|---|---|
| `SessionStart` / `SessionEnd` | session begins/resumes; session terminates |
| `UserPromptSubmit` / `UserPromptExpansion` | prompt submitted; typed command expands |
| `PreToolUse` | before a tool call — **can block** |
| `PostToolUse` / `PostToolUseFailure` / `PostToolBatch` | after success / failure / parallel batch |
| `PermissionRequest` / `PermissionDenied` | permission decision needed / auto-mode denial |
| `Stop` / `StopFailure` | Claude finishes responding / turn ends on API error |
| `SubagentStart` / `SubagentStop` | subagent spawned / finished |
| `TaskCreated` / `TaskCompleted` / `TeammateIdle` | agent-team task + teammate lifecycle |
| `Notification` / `MessageDisplay` | notification sent; assistant text displayed |
| `InstructionsLoaded` | a `CLAUDE.md` or `.claude/rules/*.md` file is loaded |
| `ConfigChange` / `CwdChanged` / `DirectoryAdded` / `FileChanged` | environment changes |
| `WorktreeCreate` / `WorktreeRemove` | replace git worktree logic (non-git VCS) |
| `PreCompact` / `PostCompact` | around context compaction |
| `PreModelSwitch` / `PostModelSwitch` | added **v2.1.251 (2026-08-28)**; can block/confirm a model switch |
| `Setup` | `--init-only` / maintenance mode |
| `Elicitation` / `ElicitationResult` | MCP server requests user input |

Config shape and semantics:

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash",
      "hooks": [{
        "type": "command",
        "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/check.sh",
        "if": "Bash(git commit *)",
        "timeout": 600
      }]
    }]
  }
}
```

- `type` can be `command`, `http`, `mcp_tool`, `prompt`, or `agent`.
- `if` narrows a tool event with permission-rule syntax (`Bash(git *)`, `Edit(*.ts)`).
- **Exit codes**: `0` = success (JSON on stdout may carry a decision); **`2` = blocking
  error** (blocks even if the JSON says allow; message from stderr); anything else =
  non-blocking error, the action proceeds.
- JSON output: `hookSpecificOutput.permissionDecision` (`allow`/`deny`) +
  `permissionDecisionReason`, `continue` (Stop/SubagentStop), `retry`, plus universal
  `systemMessage`, `additionalContext`, `updatedInput`.
- `${CLAUDE_PROJECT_DIR}` stays at the project root even inside a worktree; read the
  `cwd` field of the hook's stdin JSON for the worktree path.

The memory docs are blunt about the hooks-vs-instructions boundary:

> "Claude treats them as context, not enforced configuration. To block an action
> regardless of what Claude decides, use a PreToolUse hook instead."
> — [memory docs](https://code.claude.com/docs/en/memory)

### Subagents: `.claude/agents/*.md`

([sub-agents docs](https://code.claude.com/docs/en/sub-agents))

Locations by priority: managed settings > `--agents` JSON flag > `.claude/agents/`
(project, **commit for team use**) > `~/.claude/agents/` > plugin `agents/`. Both project
and user dirs are scanned recursively.

Only `name` and `description` are required. Notable optional fields:

| Field | Values / effect |
|---|---|
| `tools` / `disallowedTools` | allowlist / denylist |
| `model` | `sonnet`, `opus`, `haiku`, `fable`, `inherit`, or a full id like `claude-opus-5` |
| `permissionMode` | `default`, `acceptEdits`, `auto`, `dontAsk`, `bypassPermissions`, `plan` |
| `maxTurns` | cap agentic turns |
| `skills` | preload skills into the subagent's context |
| `mcpServers` | scope MCP servers to this subagent |
| `hooks` | `PreToolUse` / `PostToolUse` / `Stop` for this subagent |
| `memory` | `user` / `project` / `local` persistent memory |
| `isolation: worktree` | **run this subagent in its own git worktree** |
| `effort` | `low`…`max` |
| `background` | run in background |

Model resolution order: per-invocation `model` param → frontmatter `model` →
`CLAUDE_CODE_SUBAGENT_MODEL` env → main conversation's model.

**Crucially for the "independent review in a fresh session" pattern**: a non-fork subagent
gets its own system prompt (not Claude Code's default), the full `CLAUDE.md` hierarchy, a
git-status snapshot, and its preloaded skills — but **no conversation history, no auto
memory from the main conversation**. A `fork` subagent, by contrast, inherits everything.
That means an ordinary `.claude/agents/reviewer.md` subagent *already is* a fresh-context
reviewer.

### Plugins and marketplaces

([plugins docs](https://code.claude.com/docs/en/plugins))

A plugin is a directory with an optional `.claude-plugin/plugin.json` manifest plus, at the
**plugin root** (never inside `.claude-plugin/`): `skills/`, `commands/`, `agents/`,
`hooks/hooks.json`, `.mcp.json`, `.lsp.json`, `monitors/monitors.json`, `bin/`,
`settings.json`.

- Test locally with `claude --plugin-dir ./my-plugin` (also accepts a `.zip`) or
  `--plugin-url <zip url>`; `/reload-plugins` picks up edits.
- `claude plugin validate ./my-plugin` (`--strict` treats warnings as errors);
  `claude plugin validate --json` added in **v2.1.259 (2026-09-02)**.
- Standalone `.claude/` is recommended for project-specific work; convert to a plugin only
  when you want to share/version it. Plugin skills are namespaced `/plugin-name:skill-name`.
- A `.lsp.json` shipping `gopls` is genuinely useful for a Go repo:
  ```json
  { "go": { "command": "gopls", "args": ["serve"], "extensionToLanguage": { ".go": "go" } } }
  ```
  The docs recommend installing the official language LSP plugins rather than authoring
  your own where one exists.

### Headless / CI mode

([headless docs](https://code.claude.com/docs/en/headless))

The big 2026 additions are `--bare` and `--json-schema`.

- **`--bare`**: skips auto-discovery of hooks, skills, custom commands, subagents,
  plugins, MCP servers, auto memory and `CLAUDE.md`. *"Bare mode is the recommended mode
  for scripted and SDK calls, and will become the default for `-p` in a future release."*
  It never reads OAuth credentials or the keychain, so set `ANTHROPIC_API_KEY`.
- **Security note that matters for a public repo**: *without* `--bare`, a `-p` session
  runs the hooks in a project's `.claude/settings.json` and connects the servers in its
  `.mcp.json` **even in a folder you've never trusted**, with no trust dialog and no
  per-server approval prompt. Anyone running `claude -p` inside a cloned untrusted repo is
  exposed. Use `--bare` in CI and for any drive-by clone.
- `--output-format text | json | stream-json`; `json` includes `result`, `session_id`,
  `total_cost_usd` and a per-model cost breakdown.
- **`--json-schema '<JSON Schema>'`** with `--output-format json` puts validated
  structured output in the `structured_output` field. Invalid schemas hard-fail with
  `Error: --json-schema is not a valid JSON Schema` (silently ignored before v2.1.205).
- `--append-system-prompt` / `--append-system-prompt-file`; `--system-prompt` to replace.
  `--append-subagent-system-prompt-file` added **v2.1.261 (2026-09-04)**.
- `--permission-mode auto | dontAsk | acceptEdits | plan`; for `-p` the *starting* mode is
  Manual on every plan, so pass one explicitly. `dontAsk` is described as "useful for
  locked-down CI runs".
- **`--permission-prompts none`** (**requires v2.1.259+**) denies anything that would
  prompt, for unattended hosts.
- `--allowedTools "Bash(git diff *),Read,Edit"` uses permission-rule syntax; the space
  before `*` matters.
- Exit code 0 on success, non-zero on failure, **143** on SIGTERM.
- CI gate hooks: the `system/init` stream event exposes `plugin_errors` and
  `mcp_server_errors` arrays — *"a CI gate can fail on a non-empty array"* (requires
  v2.1.219+).

**This is the single most important finding for the reference design.** The custom
"guard script with schema-validated handoff metadata between phases" is now a native
feature in both major CLIs: `claude -p --output-format json --json-schema …` and
`codex exec --output-schema …` (see §4). A phase can be made to *emit* a schema-validated
handoff object without any bespoke validation code.

### Worktree isolation

([worktrees docs](https://code.claude.com/docs/en/worktrees))

- `claude --worktree <name>` / `-w` creates `.claude/worktrees/<name>/` on branch
  `worktree-<name>`. Add `.claude/worktrees/` to `.gitignore`.
- `--worktree "#1234"` (or a PR/MR URL) branches from that pull request, checked out at
  `.claude/worktrees/pr-<number>` (PR-URL/MR-URL forms require **v2.1.233+**).
- `worktree.baseRef`: `"fresh"` (default, branch from the remote default branch) or
  `"head"` (branch from local HEAD).
- `.worktreeinclude` (gitignore syntax) copies gitignored files such as `.env` into every
  new worktree.
- Enforcement is **mechanical, not advisory**: while isolated, Claude Code blocks edits
  targeting the main checkout, blocks commands whose cwd resolves to the main checkout,
  blocks git redirects (`git -C`, `--git-dir`, `GIT_DIR`, `GIT_WORK_TREE`, `cd` then git),
  and blocks commands whose shape it can't verify. *"You can't turn this check off."*
  The same enforcement covers every subagent spawned from the isolated session.
- `isolation: worktree` in a subagent's frontmatter makes that isolation permanent.

### Native "no AI attribution in commits" — `attribution`

The reference design's hand-written rule now has a first-class setting.
([settings reference](https://code.claude.com/docs/en/settings-reference#attribution),
fetched 2026-09-07)

| Key | Type | Default |
|---|---|---|
| `attribution.commit` | string | unset → `Co-Authored-By: <name> <noreply@anthropic.com>` where `<name>` is the session's active model, e.g. `Claude Sonnet 5` |
| `attribution.pr` | string | unset → `🤖 Generated with [Claude Code](https://claude.com/claude-code)` |
| `attribution.sessionUrl` | boolean | `true` → appends a `Claude-Session` trailer on commits from cloud / Remote Control sessions |

*"Set it to an empty string to hide commit attribution."* So the complete opt-out, in
committed `.claude/settings.json`:

```json
{ "attribution": { "commit": "", "pr": "", "sessionUrl": false } }
```

`includeCoAuthoredBy` is **deprecated** in favour of `attribution`. Note this changes what
Claude Code *constructs*; a model can still type a trailer into a commit message it writes
by hand, and Codex/OpenCode have their own defaults — so a `commit-msg` hook remains the
mechanical gate (see §6).

### Memory files

Covered in §1. Worth restating: **auto memory** is on by default, stores per-repo notes in
`~/.claude/projects/<project>/memory/` with a `MEMORY.md` index whose **first 200 lines or
25 KB** load every session; disable per-project with `{"autoMemoryEnabled": false}` or
`CLAUDE_CODE_DISABLE_AUTO_MEMORY=1`. It is machine-local and never shared.
([memory docs](https://code.claude.com/docs/en/memory))

### MCP configuration

([mcp docs](https://code.claude.com/docs/en/mcp))

`.mcp.json` at the project root, `{"mcpServers": {...}}`, **checked into version control**.
Approval keys in `.claude/settings.json`: `enableAllProjectMcpServers` (true = approve all),
`enabledMcpjsonServers` (allowlist by name), `disabledMcpjsonServers` (takes precedence).
`claude mcp reset-project-choices` resets. Note the `-p` caveat above: in non-interactive
runs project servers load **without** prompting. Also: in a folder whose trust dialog you
haven't accepted, `enabledMcpjsonServers` is honoured from *user*, *managed* and
`--settings` sources but **ignored in the shared project file** — so committing it doesn't
silently auto-approve servers for a stranger who clones the repo
([settings reference](https://code.claude.com/docs/en/settings-reference#enabledmcpjsonservers)).

### Agent teams — the native tmux coordinator

([agent-teams docs](https://code.claude.com/docs/en/agent-teams))

**Experimental and disabled by default**; enable with
`CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS=1` in `settings.json` `env` or the environment.
A lead session spawns teammates that are full independent Claude Code sessions with their
own context windows, a shared task list, and a file-based mailbox at
`~/.claude/teams/{team}/inboxes/{agent}.json`.

- Display modes via `teammateMode` / `--teammate-mode`: `"in-process"` (default since
  v2.1.179), `"tmux"`, `"iterm2"` (v2.1.186+), `"auto"`. **Split panes require tmux or
  iTerm2 + the `it2` CLI** — i.e. Anthropic shipped the tmux coordinator natively.
- Quality gates: `TeammateIdle`, `TaskCreated`, `TaskCompleted` hooks — **exit code 2
  prevents the transition and sends feedback**, which is exactly the mechanical
  phase-gate primitive the reference design hand-rolled.
- Teammates are **not spawned in `-p` / headless mode**.
- Documented limitations: no session resumption with in-process teammates, task status
  lags, one team per session, no nested teams, lead is fixed, permissions set at spawn.
- Token cost scales linearly with teammates; docs recommend **3–5**.

Lighter alternatives the docs point at first: **subagents** (within one session) and
**cross-session messaging** ([docs](https://code.claude.com/docs/en/cross-session-messaging)).

### What this means for ktags

Commit `.claude/settings.json` with a **deny-list** (which applies before trust) and hooks;
leave `.claude/settings.local.json` to the human. Express the reviewer phase as a plain
`.claude/agents/reviewer.md` subagent — it already gets a fresh context. Use
`--worktree` / `isolation: worktree` instead of hand-rolled sandboxing. Use `-p --bare
--output-format json --json-schema` for any scripted phase. Treat agent teams as
interesting but **experimental**: do not build the workflow on them yet.

---

## 4. Codex CLI and OpenCode project configuration

### Codex CLI

**Config file locations and precedence** — Codex genuinely supports a repo-level config:

1. `~/.codex/config.toml` — personal defaults
2. **`.codex/config.toml` — project overrides, loaded only for *trusted* projects**
3. `~/.codex/<profile>.config.toml` — profiles
4. `/etc/codex/config.toml` — system (Unix)

> "Codex reads configuration details from more than one location. Your personal defaults
> live in `~/.codex/config.toml`, and you can add project overrides with
> `.codex/config.toml` files."
> — [learn.chatgpt.com/docs/config-file/config-basic](https://learn.chatgpt.com/docs/config-file/config-basic)

Two important properties reported by secondary sources and consistent with the docs:

- A repo-level `.codex/config.toml` **cannot override provider, auth, notification,
  telemetry, or profile-selection keys** — a deliberate security boundary so that cloning
  a repo cannot redirect your prompts and API key.
- **Project config is ignored until the project is trusted** — the usual reason a
  committed `.codex/config.toml` "does nothing".
  ([majesticlabs, 2026-07](https://majesticlabs.dev/blog/202607/codex-cli-configuration-guide);
  [inventivehq](https://inventivehq.com/knowledge-base/openai/where-configuration-files-are-stored))
  The exact key-exclusion list is **[UNVERIFIED]** against the primary reference page.

**Key TOML options** ([config-basic](https://learn.chatgpt.com/docs/config-file/config-basic),
[config-reference](https://learn.chatgpt.com/docs/config-file/config-reference.md)):

| Key | Purpose |
|---|---|
| `model` | default model (e.g. `"gpt-5.6"`) |
| `approval_policy` | `"on-request"`, `"never"`, `"untrusted"` |
| `sandbox_mode` | filesystem/network access level |
| `model_reasoning_effort` | e.g. `"high"` |
| `personality` | `"friendly"`, `"pragmatic"`, `"none"` |
| `web_search` | `"cached"`, `"indexed"`, `"live"`, `"disabled"` |
| `log_dir` | log location |
| `project_doc_max_bytes` | AGENTS.md size cap (default 32 KiB) |
| `[permissions.<name>]` | custom permission profiles |
| `[features]` | feature flags |
| `[shell_environment_policy]` | env-var filtering |
| `[mcp_servers.<name>]` | MCP servers (TOML tables, **not** `.mcp.json`) |

**AGENTS.md handling**: covered in §1 — `~/.codex/AGENTS.override.md`, `~/.codex/AGENTS.md`,
then git-root-downward with override files taking precedence at each level; 32 KiB default cap.
Custom filenames (e.g. `TEAM_GUIDE.md`) can be configured in `config.toml`.

**Skills**: `.agents/skills` scanned from cwid up to the repo root, then
`$HOME/.agents/skills`, `/etc/codex/skills`, then bundled. Invocation is explicit
(`/skills` or `$skill` in Codex, `@skill` in ChatGPT) or implicit by description match.
Optional presentation/tool-dependency metadata lives in `agents/openai.yaml`.
([build-skills](https://learn.chatgpt.com/docs/build-skills))

**Headless `codex exec`** ([non-interactive-mode](https://learn.chatgpt.com/docs/non-interactive-mode.md)):

| Flag | Effect |
|---|---|
| `--json` | JSON Lines event stream on stdout |
| **`--output-schema <path>`** | enforce a structured JSON response matching a schema |
| `-o <path>` | write final message to a file (still prints to stdout) |
| `--sandbox workspace-write` | allow edits (**replaces the deprecated `--full-auto`**) |
| `--sandbox danger-full-access` | broad access, controlled environments only |
| `--skip-git-repo-check` | bypass the git-repo requirement |
| `--ignore-user-config` | skip `$CODEX_HOME/config.toml` — the Codex analogue of `--bare` |
| `--ignore-rules` | skip execpolicy rule files |
| `--ephemeral` | don't persist session rollout files |
| `resume [<SESSION_ID>]` | continue a previous run |

**`codex review` — a native independent-reviewer command.** Verified locally against
**codex-cli 0.153.4** on 2026-09-07 (`codex review --help`):

```
Run a code review non-interactively
Usage: codex review [OPTIONS] [PROMPT]
  [PROMPT]            Custom review instructions. If `-` is used, read from stdin
  --uncommitted       Review staged, unstaged, and untracked changes
  --base <BRANCH>     Review changes against the given base branch
  --commit <SHA>      Review the changes introduced by a commit
  --title <TITLE>     Optional commit title to display in the review summary
```

This is a **fresh, non-interactive review process** with no memory of the session that
wrote the code — i.e. the reference design's "independent review in a fresh session" is
now a single shipped command. Other verified subcommands on the same build: `exec`,
`sandbox` (run commands inside a Codex-provided sandbox), `plugin`, `doctor`, `fork`,
`apply` (apply the agent's latest diff as `git apply` to the local tree), `cloud`.

Auth in CI via `CODEX_API_KEY`; the docs recommend the **Codex GitHub Action** for CI to
avoid credential exposure. Sandboxing and approvals are documented separately at
[sandboxing](https://learn.chatgpt.com/docs/sandboxing.md) and
[agent-approvals-security](https://learn.chatgpt.com/docs/agent-approvals-security.md);
there is also an **auto-review** feature
([sandboxing/auto-review](https://learn.chatgpt.com/docs/sandboxing/auto-review.md)).

### OpenCode

([opencode.ai/docs/config](https://opencode.ai/docs/config/))

- Files: **`opencode.json` / `opencode.jsonc`** at the project root; global at
  `~/.config/opencode/opencode.json`; custom path via `OPENCODE_CONFIG`.
- Schema: `"$schema": "https://opencode.ai/config.json"` — a real JSON Schema you can
  validate in CI.
- Top-level keys: `model`, `provider`, `small_model`, `agent`, `default_agent`,
  `permission`, `tools`, `mcp`, `instructions`, `formatter`, `lsp`, `plugin`, `share`,
  `autoupdate`, `snapshot`, `server`, `shell`, `attachment`.
- **`instructions`**: an array of paths and glob patterns to instruction files, e.g.
  `["CONTRIBUTING.md", "docs/guidelines.md", ".cursor/rules/*.md"]`. This is OpenCode's
  progressive-disclosure lever and its AGENTS.md-adjacent mechanism.
- **`permission`**: `"ask" | "allow" | "deny"` per capability (`edit`, `bash`, `webfetch`).
  Default is permissive: *"opencode allows all operations without requiring explicit
  approval"* — so a committed `opencode.json` with `{"permission": {"bash": "ask"}}` is a
  real hardening step.
- **Agents**: either inline under the `agent` key (with `description`, `model`, `prompt`,
  `tools`) or as markdown files in `.opencode/agents/` (project) or
  `~/.config/opencode/agents/` (global).
- Skills: `.opencode/skills/`, `.claude/skills/`, `.agents/skills/` (see §2).

### Is there a shared MCP config format?

**Partly, and not at the file level.** The `{"mcpServers": {...}}` *object shape* is the de
facto standard — Claude Code's `.mcp.json`, Claude Desktop's `claude_desktop_config.json`,
Cursor and other MCP clients all use it, and the Claude Code docs say configurations "can
be copied between Claude Code and other MCP clients (with minor adjustments for
transport-specific fields)" ([mcp docs](https://code.claude.com/docs/en/mcp)). But:

- **Claude Code**: `.mcp.json` at the repo root — a distinct file.
- **Codex**: `[mcp_servers.<name>]` tables **inside `config.toml`** — different file, different format.
- **OpenCode**: an `mcp` key **inside `opencode.json`** — different file, different shape.

So `.mcp.json` is *a* shared format but **not a universal one**. Plan on writing the MCP
config once per tool, or generating all three from one source.

### What this means for ktags

`ktags` should commit three small tool configs — `.claude/settings.json`,
`.codex/config.toml`, `opencode.json` — each doing the same two jobs: **deny dangerous
operations** and **point at the same skills/instructions**. Both Codex and Claude Code
ignore repo-level *permissive* config until the folder is trusted, which is the right
security default for an OSS repo that strangers clone. Use `codex exec --output-schema`
and `claude -p --json-schema` as the portable phase-handoff mechanism.

## 5. GitHub-native workflow for a solo dev + agents

*Claims verified against primary sources; unverified items flagged inline.*

### 5.0 The single most important structural finding

**Issue types and issue fields are organization-scoped. They do not exist on
personal-account repositories.** The REST API is `GET/POST /orgs/{org}/issue-types` —
org-only by construction ([REST: issue types](https://docs.github.com/en/rest/orgs/issue-types)).
The GA announcement covers "all GitHub organizations on Free, Team, Enterprise"
([changelog, 2026-07-02](https://github.blog/changelog/2026-07-02-issue-fields-are-now-generally-available/)).
Community confirms personal repos are excluded even on paid personal plans
([community #175785](https://github.com/orgs/community/discussions/175785)), and
transferring a repo *out* of an org strips all issue types from its issues
([transferring a repository](https://docs.github.com/en/repositories/creating-and-managing-repositories/transferring-a-repository)).

Free organizations cost nothing. Three capabilities flip on the moment `ktags` lives in
one: issue types, issue fields, and org-level default labels
([managing labels](https://docs.github.com/en/issues/using-labels-and-milestones-to-track-work/managing-labels)).
**Create a free org for `ktags` before anything else in this document.** Tradeoff: the
*"Required reviewers"* ruleset rule and merge queue also require org ownership, but org
rulesets themselves need Team/GHEC.

### 5.1 Issues in 2026: types, sub-issues, labels

| Feature | Status | Date | Limits |
|---|---|---|---|
| Sub-issues | **GA** | [2025-04-09](https://github.blog/changelog/2025-04-09-evolving-github-issues-and-projects/) | **100 per parent, 8 nesting levels**, cross-repo ([docs](https://docs.github.com/en/issues/tracking-your-work-with-issues/using-issues/adding-sub-issues)) |
| Issue types | **GA**, org-only | 2025-04-09 | Defaults Bug / Feature / Task; custom types with name, description, color, `is_enabled` |
| Issue fields | **GA**, org-only | [2026-07-02](https://github.blog/changelog/2026-07-02-issue-fields-are-now-generally-available/) | Defaults Priority, Effort, Start date, Target date; 40,000+ orgs adopted since May preview |
| Multi-select fields | **GA** | [2026-08-07](https://github.blog/changelog/2026-08-07-connecting-issues-and-multi-select-field-support/) | Works across issues *and* projects |
| "Relates to" relationship | **Public preview** | 2026-08-07 | Neutral link, no blocking semantics |
| Issue dependencies (`blocked by`/`blocking`) | Shipped | by [2026-06-10](https://github.blog/changelog/2026-06-10-manage-sub-issues-types-and-dependencies-from-github-cli/) | ⚠️ Docs page 404s; verified via CLI flags + changelog |
| Label archiving + suggested labels | **GA** | [2026-08-27](https://github.blog/changelog/2026-08-27-label-archiving-is-generally-available/) | Archive preserves history on existing issues |
| Advanced search for Projects | **GA** | [2026-07-16](https://github.blog/changelog/2026-07-16-advanced-search-for-projects-is-generally-available/) | `AND`/`OR` in any project filter bar; new `reviews:` filter |

New repos ship **10 default labels** (accessibility was added): accessibility, bug,
documentation, duplicate, enhancement, good first issue, help wanted, invalid, question,
wontfix.

**Is "labels as a state machine" still sensible? Largely no**, and 2026 made the case
decisively. The classic `status/todo → status/in-progress → status/review` ladder existed
because labels were the only queryable per-issue axis. Three things replaced it:

1. **A Projects v2 single-select `Status` field is a real state machine** — mutually
   exclusive by construction, whereas nothing stops an issue carrying `status/todo` *and*
   `status/done` simultaneously. Projects also ships built-in automation for it (§5.2).
2. **Issue types replace the `kind/*` label family** with a single-valued, org-consistent,
   searchable field — and unlike labels it is filterable via `gh issue list --type`.
3. **Sub-issues replace `epic`/`tracked-by` label conventions** with real hierarchy and a
   progress rollup.

What labels remain good at: **orthogonal, multi-valued, cheap tags** — `good first issue`,
`help wanted` (both have special GitHub UI treatment), `area/tui`, `area/k8s-client`,
`breaking-change`. GitHub's own best-practices page nudges the same way, framing custom
fields as the escape from being "limited to the built-in metadata (assignee, milestone,
labels, etc.)"
([best practices for projects](https://docs.github.com/en/issues/planning-and-tracking-with-projects/learning-about-projects/best-practices-for-projects)).
Net: **type = issue type, state = Projects status field, everything else = labels.**

### 5.2 Projects v2: fields, automation, and the `gh project` CLI

- **Limits:** 50,000 items per project (up from 1,200, 2025-04-09); **50 fields per
  project** including built-in metadata
  ([about projects](https://docs.github.com/en/issues/planning-and-tracking-with-projects/learning-about-projects/about-projects)).
  Projects can be user- *or* org-owned — unlike issue types.
- **Built-in automations** ([docs](https://docs.github.com/en/issues/planning-and-tracking-with-projects/automating-your-project/using-the-built-in-automations)):
  two are on by default — close an issue/PR → Status = Done; merge a PR → Status = Done.
  Plus auto-archive and auto-add.
- **Auto-add is plan-limited and this bites a solo dev**: **GitHub Free = 1 auto-add
  workflow per project**, Pro/Team = 5, Enterprise = 20
  ([adding items automatically](https://docs.github.com/en/issues/planning-and-tracking-with-projects/automating-your-project/adding-items-automatically)).
  It filters on `is`, `label`, `reason`, `assignee`, `no`, with negation. Critically, **it
  only catches items created or updated after you enable it** — no backfill.
- **The GraphQL-only limitation is real.** The key mutations are `addProjectV2ItemById` and
  `updateProjectV2ItemFieldValue`. The hard blocker: **"`GITHUB_TOKEN` is scoped to the
  repository level and cannot access projects"**
  ([automating projects using Actions](https://docs.github.com/en/issues/planning-and-tracking-with-projects/automating-your-project/automating-projects-using-actions)).
  Org projects need a GitHub App; user projects need a PAT with `project` + `repo`.
  **There is no REST API for Projects v2.**
- **`gh project` covers the practical surface** (verified against gh 2.100.0): `create,
  list, view, edit, close, copy, delete, mark-template, link, unlink, field-create,
  field-list, field-delete, item-add, item-create, item-list, item-edit, item-archive,
  item-delete`. Minimum token scope `project` (`gh auth refresh -s project`).
- **`gh project item-edit` now takes human-readable names**, which is the difference
  between usable and not for agent scripting:
  ```bash
  gh project item-edit 1 --owner ktags-dev \
    --url https://github.com/ktags-dev/ktags/issues/23 \
    --field "Status" --value "In Progress"
  ```
  **Caveat: for non-draft issues, only one field can be updated per invocation.**

### 5.3 Issue forms, config.yml, and PR templates

Top-level keys for `.github/ISSUE_TEMPLATE/*.yml`
([syntax for issue forms](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-issue-forms)):
`name` (required, unique), `description` (required), `body` (required), plus optional
`title`, `labels`, `assignees`, **`type`**, `projects`.

**`type:` is supported in issue forms**, and applies an org-level issue type automatically.
This is the cleanest wiring available: a `bug.yml` form that stamps `type: Bug` means every
inbound bug is correctly typed with zero triage, and your agents can then run
`gh issue list --type Bug --json number,title,body`. It only works if `ktags` is in an org.

Body element types: `markdown`, `input`, `textarea`, `dropdown` (supports `multiple: true`),
`checkboxes`, and **`upload`** — a dedicated file-upload input added
[2026-03-05](https://github.blog/changelog/2026-03-05-hierarchy-view-improvements-and-file-uploads-in-issue-forms/),
which for a TUI project is genuinely useful (demand a terminal screenshot or a `--debug`
log up front). All support `validations: {required: true}`.

`config.yml`: `blank_issues_enabled` (boolean) and `contact_links[]` with `name`, `url`,
`about`. Template names must exceed 3 characters, and files sort alphanumerically — use
`1-`, `2-` prefixes, remembering `11-bug.yml` sorts between `1-feature.yml` and
`2-support.yml`
([configuring issue templates](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/configuring-issue-templates-for-your-repository)).

**PR templates** ([docs](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/creating-a-pull-request-template-for-your-repository)):
single template at `pull_request_template.md`, `docs/pull_request_template.md`, or
`.github/pull_request_template.md`. **Multiple templates** go in a
`PULL_REQUEST_TEMPLATE/` subdirectory, selected via the `template` query parameter. Note
the asymmetry: multiple PR templates are **not** offered in a chooser UI the way issue
templates are — they require a hand-crafted URL, near-useless interactively but fine for
agents. `gh pr create --template <file>` selects one directly.

### 5.4 Rulesets vs branch protection for a solo maintainer

**Classic branch protection is not deprecated and has no announced sunset** — it is
feature-frozen and de-emphasised. But there is now an official migration path:
**Settings → Branches → Convert to ruleset**
([changelog, 2026-08-11](https://github.blog/changelog/2026-08-11-automatically-migrate-branch-protection-rules-to-repository-rulesets/);
[docs](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/converting-branch-protections-to-rulesets)),
available on free public repos.

**Can a solo maintainer require PRs and status checks on a free public repo? Yes.** The
authoritative gating string is: *"Rulesets are available in public repositories with GitHub
Free."* ⚠️ **Documentation trap:** the rendered
[About rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/about-rulesets)
page says "for customers on GitHub Team and GitHub Enterprise plans" — that clause applies
to the *organization-level/multi-repo* case, and read alone it wrongly implies free public
repos are excluded.

**Free on a public repo:** require PR before merging (approvals 0–10, dismiss stale
approvals, Code Owners review, require approval of most recent reviewable push,
conversation resolution, **allowed merge methods**), require status checks (incl.
up-to-date branches and **integration/app-ID pinning**), require signed commits, require
linear history, block force pushes, restrict deletions/creations/updates, require
deployments, require code scanning results, ruleset import/export as JSON, Rule Insights.

**Not available to you:** ⚠️ **metadata restrictions — commit message pattern, branch name
pattern, author email pattern — are GHEC-only.** This is the biggest free-tier gap: **you
cannot enforce Conventional Commits at the platform layer**; it must be a CI check (§5.6).
Also unavailable: required workflows, merge queue rule, "Evaluate" enforcement mode, push
rulesets (Team + private only), and — because user-owned repos have no teams — the
"Required reviewers" rule.

**The solo self-approval trap.** You cannot approve your own PR. So
`required_approving_review_count >= 1` is a permanent self-lock for a solo maintainer. The
live feature request
([community #197170, 2026-05-28](https://github.com/orgs/community/discussions/197170)) is
open and unanswered. ⚠️ **[UNVERIFIED]** as a quotable docs sentence — it is documented only
implicitly, and is well-established behaviour rather than a citable line.

**The fix is approvals = 0.** GitHub's own docs bless it: *"The pull request doesn't
necessarily have to be approved, but it must be opened."* You keep the PR gate, the diff,
and the CI gate, and lose only a signature you couldn't have provided anyway.

⚠️ **A rule that will deadlock you if you ignore it:** *"Require an additional approval for
unattributed Copilot pull requests"* is **on by default in every new and existing ruleset**
(public preview, free tier). With approvals = 1, a Copilot-authored PR demands **two**
approvals from write-access humans — instant deadlock for a solo dev. It has no effect when
approvals = 0.

**Bypass actors.** Use **"For pull requests only"**, not "Always": it forbids direct pushes
to `main` even for you, but lets you force-merge a PR when CI is broken at 2am, leaving a
trail. Bypasses are logged in **Rule Insights** (free tier) and exposed via the
[rule-suites REST API](https://docs.github.com/en/rest/repos/rule-suites).
⚠️ **Rulesets cannot be defined as code in `.github/`** — no such file exists. The
workaround is export-to-JSON, commit `ruleset.json`, apply with
`gh api -X POST repos/:owner/ktags/rulesets --input ruleset.json`. `gh ruleset` is
**read-only** (`check`, `list`, `view`) — verified locally on gh 2.100.0.

**"Require review from Copilot"** is two pieces. (a) A ruleset option *"Automatically
request Copilot code review"*
([changelog, 2025-09-10](https://github.blog/changelog/2025-09-10-copilot-code-review-independent-repository-rule-for-automatic-reviews/)).
(b) **New and directly relevant: Copilot can now submit approving reviews that count toward
required approvals**
([changelog, 2026-09-01](https://github.blog/changelog/2026-09-01-copilot-code-review-can-now-approve-pull-requests/))
— *"Copilot can submit an approval that counts toward the repository's required-approvals
rule."* Off by default; admin-enabled at repo/org/enterprise level with file-path scoping;
approval is dismissed when new commits land. **This is the first genuine answer to the
solo-maintainer approval problem** — but it is **public preview** and requires a paid
Copilot seat. ⚠️ Whether it can approve a PR you authored yourself is **[UNVERIFIED]**.

**Merge queue is doubly unavailable**: it needs an **organization-owned** repo, and the
ruleset rule is GHEC-only. Irrelevant for a solo dev anyway.

### 5.5 Required status checks: making unchecked merges structurally impossible

Checks become required **by name string**. From
[troubleshooting rules](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/troubleshooting-rules):
*"Required status checks do not take workflow, matrix, or event trigger types into
account."* A check must have run in the last 7 days to appear in the picker.

**The skipped-check trap, straight from GitHub's docs**
([troubleshooting required status checks](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks)):

- A **workflow** skipped by `paths`/`branches` filtering or `[skip ci]` → checks stay
  **Pending and block merging forever**. GitHub's own advice: *"Avoid requiring workflows
  that can be skipped."*
- A **job** skipped by an `if:` conditional → reports **Success** and does *not* block.

That asymmetry — workflow-level skips are fatal, job-level skips are safe — is what
everyone trips over.

**The correct pattern:** require exactly one aggregator job, e.g. `ci-required`, that runs
unconditionally on `pull_request` with `if: always()` and `needs: [build, test, lint]`,
failing unless every dependency is `success` or `skipped`. One stable required name, no
path-filter deadlock, no matrix-name churn.

**Pin required checks to the GitHub Actions app.** The docs allow selecting an expected
source app with `statuses:write`: *"If the status is set by any other person or
integration, merging won't be allowed."* Without this, **any actor with write access —
including a leaked agent token — can `POST` a green commit status with the right name and
satisfy your gate.** This is the difference between a real control and theatre.

**What actually stops an agent merging:**

1. **Rulesets bind GitHub Apps and `GITHUB_TOKEN`** unless explicitly listed as bypass
   actors. Confirmed inversely by the Copilot docs: *"If you have configured a ruleset…
   that isn't compatible… access to the agent will be blocked… you can add Copilot as a
   bypass actor."* **So list nobody but yourself.**
2. **Actions cannot approve PRs by default** — *"when you create a new repository in your
   personal account, workflows are not allowed to create or approve pull requests"*
   ([Actions settings](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository#preventing-github-actions-from-creating-or-approving-pull-requests)).
   Leave that toggle **off**.
3. **`permissions:` minimalism.** `permissions: contents: read` at workflow top level;
   escalate per job. 2026 added new scopes: `artifact-metadata`, `code-quality`,
   `attestations`
   ([workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)).
   `permissions: {}` disables everything.
4. **`gh pr merge --auto --squash` is the right agent verb.** The agent opens a PR and
   *arms* auto-merge; GitHub merges only once checks pass. The agent never makes the merge
   decision. `gh pr merge --admin` exists as the human override.
5. **The weakest link is your PAT.** Give agents a fine-grained PAT scoped to `ktags` with
   `contents: read` + `pull-requests: write` and no admin. If you list "repository admin"
   as a bypass actor while an agent holds admin-capable credentials, the entire ruleset is
   decorative.

⚠️ **Unresolved risk:** [community #162623](https://github.com/orgs/community/discussions/162623)
(2025-06-12) reports auto-merge silently never firing when protection comes from rulesets
rather than classic branch protection, quoting GitHub support calling it "a known
limitation." Community-sourced, **[UNVERIFIED]** on any GitHub-owned page. **Test `--auto`
on `ktags` before depending on it**; fallback is a workflow calling the merge API on
`check_suite: completed`.

### 5.6 Conventional commits, release-please, and GoReleaser

**Conventional Commits is still v1.0.0** ([spec](https://www.conventionalcommits.org/en/v1.0.0/))
— frozen for years, no 2026 developments. Since platform-level commit-message rules are
GHEC-only (§5.4), enforce it in two cheap layers: set **squash-merge default commit message
= "Pull request title"**
([docs](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/configuring-commit-squashing-for-pull-requests))
so the PR title is the only string that matters, then lint that title with
**`amannn/action-semantic-pull-request` v6.1.1 (2025-08-22)**. Skip commitlint (v21.2.2,
2026-08-13) — it drags Node into a Go repo for marginal gain.

**Current versions, all atom-feed verified:**

| Tool | Version | Date |
|---|---|---|
| `googleapis/release-please` (CLI) | **v17.11.2** | 2026-08-24 |
| `googleapis/release-please-action` | **v5.0.0** | 2026-04-22 |
| `goreleaser/goreleaser` | **v2.18.1** | 2026-09-05 |
| `goreleaser/goreleaser-action` | **v7.2.3** | 2026-06-29 |
| `caarlos0/svu` | v3.4.1 | 2026-05-01 |
| `git-cliff` | v2.14.1 | 2026-09-01 |
| `actions/attest-build-provenance` | v4.2.2 | 2026-08-06 |
| `sigstore/cosign-installer` | v4.1.2 | 2026-05-07 |
| `anchore/sbom-action` | v0.24.2 | 2026-08-28 |

**The release-please GitHub App is dead.** Sunset **2025-08-14** with roughly two weeks'
notice, announced only via a permissions-change notification; the tracking issue
([#2569](https://github.com/googleapis/release-please/issues/2569)) is still open. **The
replacement is the GitHub Action.** Separately,
`google-github-actions/release-please-action` is **archived** — the live repo is
`googleapis/release-please-action`. ⚠️ That action's README still shows `@v4` while the
released tag is v5.0.0; trust the releases page. v5's only breaking change is Node 24
runtime.

**Go support:** `release-type: go`. It maintains `CHANGELOG.md` and optionally a version
file, but the `VersionGo` updater matches the literal pattern `const Version = "x.y.z"`.
⚠️ **`go-yoshi` is not a documented public release type** — it is Google-internal for
`google-cloud-go` monorepos; do not use it. `include-v-in-tag` defaults to `true`, giving
the `v1.2.3` tags Go modules require. **Skip `version-file` for `ktags`** — GoReleaser
injects the version via `-ldflags -X main.version=`, so a checked-in constant is duplicated
state. Note release-please has **no handling for the `/v2` module-path suffix**; a v2.0.0
bump requires hand-editing `go.mod` and imports.

**GoReleaser is still v2 — no v3 exists or is announced.** v2.18 (2026-08-23) added Actions
job summaries and **preflight checks** (`fail_on_error: true` validates tokens before
building), both OSS. Config essentials: `version: 2`, `gomod: {proxy: true}`,
`archives: formats: [tar.gz]` (plural — singular `format` is deprecated), `sboms:` (OSS,
syft), `signs:` (cosign). ⚠️ **`brews:` is deprecated → use `homebrew_casks:`**, and
**cosign v3 changed the idiom** to a single `--bundle` producing one `.sigstore.json`
instead of the old `.pem`+`.sig` pair
([goreleaser blog, 2025-11-05](https://goreleaser.com/blog/cosign-v3/)). Also note
**immutable releases** ([2026-04-26](https://goreleaser.com/blog/immutable-releases/)): a
failed release cannot be re-run over the same tag — cut a new patch. **Pro ($15/mo) buys
nothing `ktags` needs.**

**The combination.** They solve disjoint problems: release-please decides the version,
writes `CHANGELOG.md`, opens a release PR and creates the tag; GoReleaser takes a tag and
builds/signs/publishes. GoReleaser's own `changelog:` only shapes the GitHub release body —
it cannot compute a version. ⚠️ **[UNVERIFIED]**: no canonical 2026 blog post or reference
repo wiring the two together was found. The composition is sound and each half is
documented, but it is a pattern you assemble, not one you copy.

**⚠️ The critical gotcha:** release-please's tag push uses the default `GITHUB_TOKEN`, which
**does not trigger downstream workflows**. A separate `on: push: tags` GoReleaser workflow
will never fire. Fix by chaining jobs in one workflow (`needs:` +
`if: needs.release-please.outputs.release_created == 'true'`) rather than issuing a PAT.

**Recommendation:** start with **`svu` + GoReleaser** (`git tag $(svu next)`) while `ktags`
is young; graduate to **release-please + GoReleaser** once you have users. The decisive
argument for release-please is the **release PR as a human gate** — you see the computed
version and rendered changelog before anything is tagged, which matters precisely because
there is no second maintainer to catch a mistake.

**Supply chain.** 🔴 **`slsa-framework/slsa-github-generator` is no longer maintained** —
its README says so outright, latest release v2.1.0 (Feb 2025), and it redirects users to
GitHub artifact attestations. **Do not wire it into a new 2026 project.** Use
`actions/attest-build-provenance@v4` (or `actions/attest@v4`, which the repo now recommends
for new implementations) with
`permissions: {contents: write, id-token: write, attestations: write}`. Attest
`dist/checksums.txt` — every artifact is listed in it. Separately, `go install` gives you
free provenance via `proxy.golang.org` + `sum.golang.org`; the corollary is that **a
published tag is immutable — you cannot retract a bad `v1.2.3`, only ship `v1.2.4`.**

### 5.7 CODEOWNERS on a solo repo

**Verdict: near-useless, and mildly harmful.** CODEOWNERS' entire function is
auto-requesting review from *other people*
([about code owners](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners)).
Code owners must have write permission. GitHub does not request review from the PR author,
so a solo repo where you own every path generates exactly zero review requests. Worse,
combining it with a *"Require review from Code Owners"* rule reproduces the self-approval
deadlock from §5.4.

Two narrow reasons to add one anyway: (a) it is **forward-looking documentation** — the day
a second contributor appears, ownership is already declared; (b) `*` → `@you` makes
outside-contributor PRs auto-request you, which is real value on a public repo. Keep it a
two-line file, and **do not** enable the Code Owners ruleset rule. Limit: 3 MB, beyond
which the file silently does not load.

### 5.8 `gh` CLI as the agent substrate

**Current: gh 2.100.0, released 2026-09-03** — verified locally. Its release notes include
*"improving command help output when invoked by coding agents"*, which tells you where
GitHub thinks the CLI is heading.

The **2026-06-10 release (v2.94.0)** turned `gh` into a complete Issues-2.0 client
([changelog](https://github.blog/changelog/2026-06-10-manage-sub-issues-types-and-dependencies-from-github-cli/)).
Verified against the local install:

- `gh issue create`: `--type`, `--parent`, `--blocked-by`, `--blocking`, `--project`,
  `--template`, and **`--assignee "@copilot"`**
- `gh issue edit`: `--type`, `--remove-type`, `--parent`, `--remove-parent`,
  `--add-sub-issue`, `--remove-sub-issue`, `--add-blocked-by`/`--add-blocking` (+ removes)
- `gh issue list`: `--type` filter
- **JSON fields for automation**: `issueType`, `parent`, `subIssues`, `subIssuesSummary`,
  `blockedBy`, `blocking`, `projectItems`, `closedByPullRequestsReferences`

**No `gh sub-issue` extension is needed** — third-party extensions like
`yahsan2/gh-sub-issue` are now obsolete, as are `gh-issue-type` extensions.

**`gh issue develop` is the local-first primitive**, and it now has a
**`--worktree <path>`** flag: issue → linked branch → worktree in one command, no cloud
agent involved:

```bash
gh issue develop 123 --checkout --worktree /path/to/wt-feature
```

This is the single most important command for a local-first agent workflow — each agent
gets an isolated tree tied to a GitHub-tracked branch.

Also relevant: `gh api graphql` (the only route to Projects v2), `gh ruleset
check/list/view` (read-only), `gh label clone`, and **`--attach` for media** on
`issue create/edit/comment` and `pr create/edit/comment` (gh v2.99.0,
[2026-09-01](https://github.blog/changelog/2026-09-01-github-cli-media-in-issues-pull-requests-and-comments/);
10 MB images, 50 files/command) — genuinely useful for a TUI project where agents attach
terminal captures.

**GitHub MCP server** ([github/github-mcp-server](https://github.com/github/github-mcp-server))
is the alternative agent surface: remote endpoint at `https://api.githubcopilot.com/mcp/`
with OAuth, 20+ toolsets (`issues`, `pull_requests`, `actions`, `projects`, `labels`, …), a
**`--read-only` flag** and per-tool selection via `--toolsets`/`GITHUB_TOOLSETS`. For a
local-first flow, `gh` in Bash is simpler and more auditable; MCP earns its place if you
want read-only enforcement at the protocol layer.

### 5.9 Agents on GitHub, and whether they fight a local-first flow

**GitHub Copilot cloud agent** (renamed from "coding agent" ~[2026-04-01](https://github.blog/changelog/2026-04-01-research-plan-and-code-with-copilot-cloud-agent/)),
GA [2025-09-25](https://github.blog/changelog/2025-09-25-copilot-coding-agent-is-now-generally-available/).
Triggered by assigning an issue to Copilot (`gh issue create --assignee "@copilot"`),
`@copilot` PR comments, or the Agents panel. **Billing moved from premium requests to
GitHub AI Credits on 2026-06-01** (1 credit = $0.01); Pro $10 → 1,500 credits, Pro+ $39 →
7,000, Max $100 → 20,000 ([plans](https://docs.github.com/en/copilot/get-started/plans)).
⚠️ Source conflict: the [April announcement](https://github.blog/news-insights/company-news/github-copilot-is-moving-to-usage-based-billing/)
says Pro includes "$10 in AI credits" while docs now say 1,500. **Actions minutes are free
on public repos**, so the runner half costs nothing. **Conflicts with local-first:
strongly.** No local handoff whatsoever — work exists only as a `copilot/` branch on
GitHub. ⚠️ And **Copilot Automations are explicitly unavailable on public repositories**.

**Claude Code GitHub Action** (`anthropics/claude-code-action`). Use **`@v1`** — a moving
major tag, last repointed 2026-09-06 alongside v1.0.217; the v1.0 GA object is dated
2025-08-26. Install via `/install-github-app` or the
[claude app](https://github.com/apps/claude). Auth: `ANTHROPIC_API_KEY`, or
`CLAUDE_CODE_OAUTH_TOKEN` (bills your **subscription**, not API credits), or **OIDC
workload identity federation with no long-lived secret**, or Bedrock/Vertex/Foundry. Two
modes: no `prompt` → interactive, waits for `@claude`; with `prompt` → automation on any
event. Canonical permissions: `contents: write, pull-requests: write, issues: write,
id-token: write, actions: read` — `id-token: write` is required for the action's own GitHub
App auth, not just cloud providers. **Built-in injection defence: the triggering user must
have write access, and bot actors are rejected unless allowlisted.** ⚠️ The stock GitHub App
grants broad scopes (Actions/Checks/Contents/Discussions/Issues/PRs/Hooks/Workflows RW); a
custom app with only Contents/Issues/PRs is tighter but breaks Code Review.

**OpenAI Codex.** ⚠️ Docs moved: `developers.openai.com/codex/*` now 308-redirects to
**`learn.chatgpt.com/docs/*`**. GitHub triggering is `@codex review` on a PR, or automatic
reviews on every PR; requires push or admin permission
([Codex in GitHub](https://learn.chatgpt.com/docs/third-party/github)). Code Review is
**GA**; Security Review is **research preview**. Configured entirely through **`AGENTS.md`**,
including a `## Code Review Rules` section. Plans: Plus/Pro/Business/Enterprise with
per-5-hour message windows; **cloud and local CLI draw from the same allowance**. **Best
local handoff of the three:** `codex cloud` lets you browse chats, submit work, and **apply
results to your local repo from the terminal**.

**Google Jules.** ⚠️ **Status genuinely ambiguous** — [jules.google/docs](https://jules.google/docs)
still says "experimental," while secondary 2026 write-ups claim GA at I/O 2026; no primary
confirmation found. **No documented GitHub-native "assign an issue to Jules" trigger.**
Since 2026-02-19 it auto-detects and fixes failing GitHub Actions checks. Limits: Free 15
tasks/24h; Pro 100/day; Ultra 300/day.

**Others, briefly:** **Cursor Bugbot** — GA PR reviewer, `bugbot run` comment, usage-based
billing since May 2026. **Gemini Code Assist review** — `/gemini` in PR comments; notably
**excludes `.github/workflows`** to avoid writing insecure configs (docs updated
2026-08-26). ⚠️ Devin, Amazon Q/Kiro, Sourcegraph Amp, Charlie and Factory Droid could not
be verified for 2026 GitHub status.

**Hardening.** GitHub's canonical page is
[Secure use reference](https://docs.github.com/en/actions/reference/security/secure-use):
default `GITHUB_TOKEN` to read-only and escalate per job; **`pull_request_target` workflows
"must not explicitly check out untrusted code"**; **pin third-party actions to full commit
SHAs** — "currently the only way to use an action as an immutable release"; pass untrusted
issue/PR text through **intermediate environment variables**, never interpolated into
`run:`. Prompt-injection guidance:
[Copilot cloud agent: risks and mitigations](https://docs.github.com/en/copilot/concepts/agents/cloud-agent/risks-and-mitigations)
— hidden characters filtered from user input (HTML comments in issues are stripped), only
write-access users can trigger agents, the agent can only push to a single `copilot/`
branch, and **workflows do not run on agent PRs until a write-access user clicks "Approve
and run workflows."** Third-party corroboration: CSA's *Comment and Control* research
([Apr 2026](https://labs.cloudsecurityalliance.org/research/csa-research-note-comment-control-github-prompt-injection-20/),
[Aug 2026](https://labs.cloudsecurityalliance.org/research/csa-research-note-ai-coding-agent-ci-prompt-injection-202608/))
documents AI review agents in Actions exfiltrating CI secrets via injected issue text.
**GitHub Agentic Workflows**
([public preview, 2026-06-11](https://github.blog/changelog/2026-06-11-github-agentic-workflows-is-now-in-public-preview/))
— Markdown workflows compiled to Actions YAML, read-only by default, sandboxed behind an
Agent Workflow Firewall — is worth watching but too new to build on.

**The local-first verdict:** the "write the code" half of every cloud agent duplicates what
you already run locally, at the cost of a paid plan. The half that *isn't* duplicated is
**review** and **async triage**. Note a public-repo asymmetry that cuts against cloud
review: **secrets are withheld from fork-PR runs**, so a `claude-code-action` review
workflow silently will not run on outside contributions — exactly the PRs a solo maintainer
most wants reviewed.

### What this means for ktags

Move `ktags` into a **free GitHub organization** first — it unlocks issue types, issue
fields and org default labels, and transferring *out* of an org later destroys issue types.
**Retire "labels as a state machine"**: type → issue type (stamped by `type:` in the issue
form), state → a Projects v2 single-select `Status` field, hierarchy → sub-issues, labels →
orthogonal tags only. One ruleset on `main` with **approvals = 0** (the only
non-deadlocking config for a solo maintainer, and it also neutralises the Copilot
extra-approval rule that ships on by default), one **aggregator status check pinned to the
GitHub Actions app**, block force pushes, linear history, squash-only. Yourself as the only
bypass actor, "for pull requests only". `gh issue develop <n> --checkout --worktree <path>`
is the spine of the local-first loop. `svu` + GoReleaser v2 now, release-please v5 later.

### Flagged as unverified in this section

Solo self-approval is behaviour-established but has no quotable docs sentence. No canonical
reference implementation of release-please + GoReleaser together. Jules' GA status is
contradictory. Issue-dependencies docs page 404s. Copilot Pro's exact AI-credit allowance
conflicts between the April blog and current docs. Whether Copilot approvals work on
self-authored PRs is unstated. The auto-merge-with-rulesets defect is community-sourced
only. Max issue types per org is undocumented.

---

## 6. Mechanical guardrails for verifying agent-written Go code

*All release versions/dates below were verified against `gh release list` / the GitHub REST
API / `proxy.golang.org`, because page summarisers repeatedly mis-reported years on GitHub
release pages. Current Go: **1.27.1 (2026-09-01)** and **1.26.8**; Go 1.25 is out of support
([go.dev/doc/devel/release](https://go.dev/doc/devel/release)).*

### 6.1 Git hooks manager: lefthook vs husky vs pre-commit

| Tool | Latest | Runtime | Notes |
|---|---|---|---|
| **lefthook** | **v2.1.12, 2026-08-28** ([releases](https://github.com/evilmartians/lefthook/releases)) | single Go binary, 8.8k stars, pushed 2026-08-31 | Installable as a **`go.mod` tool**: `go get -tool github.com/evilmartians/lefthook/v2` ([docs](https://github.com/evilmartians/lefthook/blob/master/docs/installation/go.md)). Config `lefthook.yml` (also TOML/JSON/JSONC, `lefthook-local.yml` merge). Hooks are keys (`pre-commit`, `commit-msg`) with `jobs:`, `glob`, `run`, `{staged_files}`, `{1}`, `stage_fixed: true`, `parallel: true`. `lefthook install`, `lefthook validate`; skip with `LEFTHOOK=0 git commit`. |
| husky | v9.1.7, **2024-11-18** ([releases](https://github.com/typicode/husky/releases)) | Node/npm | **No release in ~22 months; no v10 exists** (npm `latest` = 9.1.7). Pulling a Node toolchain into a Go repo solely for hooks is not justified in 2026. |
| pre-commit | v4.6.2, 2026-08-10 ([releases](https://github.com/pre-commit/pre-commit/releases)) | Python | Actively maintained; has `language: golang` and "will bootstrap `go` if it is not present" ([pre-commit.com](https://pre-commit.com/)). Fine if you already carry Python; otherwise a second interpreter for the same job. |

**Verdict for ktags: lefthook**, pinned via the Go 1.24+ `tool` directive so
`go tool lefthook install` works with no extra toolchain — and note the reference design's
**husky is the weakest link, since husky has not shipped in nearly two years.**

**Bypass is inherent to git**: `git commit --no-verify` "Bypass the pre-commit and commit-msg
hooks" ([git-commit docs](https://git-scm.com/docs/git-commit)), and lefthook adds
`LEFTHOOK=0`. **The standard answer is that hooks are convenience, CI is the gate**: run the
identical commands in CI (`go tool lefthook run pre-commit --all-files`, or the same `make`
targets) and make those jobs required status checks.

```yaml
# lefthook.yml
pre-commit:
  parallel: true
  jobs:
    - name: fmt
      run: go tool golangci-lint fmt {staged_files}
      glob: "*.go"
      stage_fixed: true
    - name: lint
      run: go tool golangci-lint run --new-from-rev=HEAD
    - name: tidy
      run: go mod tidy -diff
    - name: test
      run: go test ./...
commit-msg:
  jobs:
    - name: no-ai-trailers
      run: scripts/commit-msg-guard.sh {1}
```

### 6.2 Blocking AI attribution trailers in `commit-msg`

**What the agents actually write (verified from official docs/source):**

- **Claude Code**: default commit trailer `Co-Authored-By: <model name> <noreply@anthropic.com>`
  (e.g. `Claude Sonnet 5`, or bare `Claude` / `Claude Code` when the model can't be resolved);
  PR line `🤖 Generated with [Claude Code](https://claude.com/claude-code)`; plus a
  **`Claude-Session:` trailer** on cloud/Remote Control commits. Controlled by
  `attribution.commit` / `attribution.pr` / `attribution.sessionUrl`; `includeCoAuthoredBy`
  deprecated since v2.0.62
  ([settings reference](https://code.claude.com/docs/en/settings-reference#attribution)).
- **OpenAI Codex** hardcodes `Co-authored-by: Codex <noreply@openai.com>` in
  `codex-rs/ext/git-attribution/src/world_state.rs` ("Commit messages must end with
  `Co-authored-by: Codex <noreply@openai.com>`") and has **no documented opt-out** — open
  issue [#14051](https://github.com/openai/codex/issues/14051).

**Why a hook and not just the setting:** as of Aug–Sep 2026 there are multiple **open**
Claude Code bugs where attribution is added despite the setting —
[#79909](https://github.com/anthropics/claude-code/issues/79909),
[#89164](https://github.com/anthropics/claude-code/issues/89164) (VS Code/SDK never inject
attribution settings), [#4287](https://github.com/anthropics/claude-code/issues/4287),
[#91829](https://github.com/anthropics/claude-code/issues/91829),
[#92169](https://github.com/anthropics/claude-code/issues/92169),
[#92386](https://github.com/anthropics/claude-code/issues/92386). Setting-level control is
unreliable; the hook is the deterministic layer, and CI should re-scan
`git log origin/main..HEAD` so `--no-verify` can't leak one through.

**Linters — a negative finding: no off-the-shelf rule exists.**

- `commitlint` (Node) **v21.2.2, 2026-08-13** — has `trailer-exists` and `signed-off-by`
  (require) but **no "trailer-forbidden"** rule
  ([rules reference](https://github.com/conventional-changelog/commitlint/blob/master/docs/reference/rules.md));
  you'd write a plugin. Go re-implementation `conventionalcommit/commitlint` v0.12.0,
  2026-02-22, still pre-release.
- `siderolabs/conform` (Go) v0.1.0-alpha.31, 2026-01-12 — header/DCO/GPG/conventional/
  spellcheck policies, **no trailer policy** ([README](https://github.com/siderolabs/conform)).
- `crate-ci/committed` (Rust) v1.1.11, 2026-02-25 — subject/line-length/imperative/fixup,
  **no trailer policy**.
- `gitlint` (Python) 0.19.1, **2023-03-10** — stale.
- `git interpret-trailers` has `--if-exists=replace`, `--trim-empty`, `--parse`, but **no
  remove-by-key** ([docs](https://git-scm.com/docs/git-interpret-trailers)).

So the practical implementation is a five-line `grep -Ei`:

```sh
#!/usr/bin/env sh
# scripts/commit-msg-guard.sh — exit 1 blocks the commit
if grep -Eiq '^(Co-Authored-By|Co-authored-by):.*(claude|codex|copilot|noreply@anthropic\.com|noreply@openai\.com)|^Claude-Session:|Generated with \[?Claude Code|🤖 Generated with' "$1"; then
  echo "commit-msg: AI attribution trailer found; remove it (repo policy)" >&2
  exit 1
fi
```

**The 2025–2026 debate.** The Linux kernel went the *opposite* way from "strip": disclosure
is **required**, authorship is **forbidden**. Sasha Levin's RFC (2025-07-25,
[LWN 1031473](https://lwn.net/Articles/1031473/)) proposed
`Co-developed-by: Claude claude-opus-4-…`; the merged
`Documentation/process/coding-assistants.rst` (committed 2025-12-23, in mainline April 2026)
instead uses `Assisted-by: LLM [TOOL1] [TOOL2]` and states **"AI agents MUST NOT add
Signed-off-by tags. Only humans can legally certify the Developer Certificate of Origin"**
([docs.kernel.org](https://docs.kernel.org/process/coding-assistants.html)). Claude Code issue
[#36105](https://github.com/anthropics/claude-code/issues/36105) asks for `Assisted-by`
instead of `Co-authored-by`. Both camps agree on the substance: **an AI is not a co-author
with legal standing.** ktags' existing "no attribution" rule is a stricter variant; the same
hook could alternatively *rewrite* to `Assisted-by:` if you ever want kernel-style disclosure.

### 6.3 golangci-lint v2

- **v2.13.2, 2026-08-27** ([releases](https://github.com/golangci/golangci-lint/releases));
  v2 launched 2025-03-23. JSON schema embedded in the binary since 2026-03-22
  ([changelog](https://golangci-lint.run/docs/product/changelog/)). *Verified locally: this
  is the installed version, and `golangci-lint config verify` validates a `version: "2"` file
  silently.*
- **Schema** ([configuration/file](https://golangci-lint.run/docs/configuration/file/)):
  top-level `version: "2"`, `run`, `linters` (`default: standard|all|none|fast`, `enable`,
  `disable`, `settings`, `exclusions` with `generated: lax|strict`, `paths`, `rules`),
  **`formatters`** (`enable`, `settings`, `exclusions`), `issues`, `output`, `severity`.
- **Migration** ([guide](https://golangci-lint.run/docs/product/migration-guide/)):
  `golangci-lint migrate` (drops comments, backs up); `linters-settings` splits into
  `linters.settings` / `formatters.settings`; `disable-all` → `default: none`;
  `skip-dirs`/`exclude-rules` → `linters.exclusions.paths/rules`; **`stylecheck` + `gosimple`
  merged into `staticcheck`**; 13 dead linters removed (`deadcode`, `exportloopref`, `golint`,
  `tenv`…); aliases dropped (`gas`, `gomnd`, `megacheck`). `gci`, `gofmt`, `gofumpt`,
  `goimports` moved from linters to **formatters**; new **`golangci-lint fmt`** command
  (*verified present locally*).
- **Defaults**: errcheck, govet, ineffassign, staticcheck, unused.
- **Recommended set for a CLI/TUI** (from the maintained "golden config", targets v2.7.1,
  updated 2026-09-05, [gist](https://gist.github.com/maratori/47a4d00457a92aa426dbd48a18776322),
  pruned for a solo repo): defaults + `errorlint`, `gocritic`, `gosec`, `revive`, `misspell`,
  `nilerr`, `noctx`, `copyloopvar`, `testifylint`, `usetesting`, `perfsprint`, `exhaustive`
  (k8s enums), **`forbidigo`** (ban `fmt.Print*`/`log.Print*` in TUI packages — stray stdout
  corrupts a Bubble Tea screen), `depguard`, `bodyclose`. **Skip** `wrapcheck`, `varnamelen`,
  `exhaustruct` — noise on agent-written code. `formatters.enable: [gofumpt, gci]`.
- **Action**: `golangci/golangci-lint-action` **v9.3.0, 2026-06-29**; v7.0.0 (2025-03-24) was
  the first v2-only major; v9.0.0 (2025-11-07) moved to node24. Use `with: version: v2.13` and
  `only-new-issues: true`. Its README recommends lint in a job separate from `go test`.

### 6.4 Formatters

- **gofumpt v0.11.0, 2026-07-27** — "fork of `gofmt` as of Go 1.27.0, requires Go 1.26 or
  later", strict superset ("running `gofmt` after `gofumpt` should produce no changes"),
  sponsored, active ([README](https://github.com/mvdan/gofumpt)). **Still maintained, and it
  ships as a golangci-lint v2 formatter** — verified locally in `golangci-lint formatters`.
- **gci v0.14.0, 2026-02-28** — deterministic import grouping (std / third-party / local),
  also a v2 formatter. **goimports** lives in `golang.org/x/tools` (no separate release
  cadence) and is redundant once `gci` handles groups and `gofumpt` handles formatting.
- Run all through `golangci-lint fmt` — one binary, one config — rather than three installs.

### 6.5 Tests, race detector, coverage

- **gotestsum v1.13.0, 2025-09-11**; last commit 2026-04-15; not archived — *maintained but
  slow, so pin it* ([releases](https://github.com/gotestyourself/gotestsum/releases)).
  `--junitfile report.xml`, `--format testname|testdox|pkgname`, `--jsonfile`.
  **`--rerun-fails` is a flake-hider**; `--rerun-fails-abort-on-data-race` exists because
  rerun + `-race` is a known foot-gun. (Homebrew also has 1.13.0.)
- **`go test -race`**: "memory usage may increase by 5-10x and execution time by 2-20x"
  ([race detector doc](https://go.dev/doc/articles/race_detector)). Run as a **separate CI
  job** without coverage; TUI goroutine/timer code is exactly where it pays. Go 1.27 caveats:
  `go test` now runs the `stdversion` vet check by default and `go test -json` gained an
  `OutputType` field ([go1.27](https://go.dev/doc/go1.27)) — re-verify gotestsum after
  upgrading.
- **Coverage floor**: `vladopajic/go-test-coverage` **v2.19.0, 2026-07-28**;
  `.testcoverage.yml` with `threshold: {file, package, total}`, `override` regex,
  `exclude.paths`, `// coverage-ignore`, and a badge pushed to an orphan branch with no
  third-party account. `codecov/codecov-action` **v7.0.0, 2026-06-07** — but its README and
  docs still show `@v5` ⚠️. For a solo repo, go-test-coverage is the hard gate; Codecov is
  optional UI.
- **Whole-binary coverage**: `go build -cover` + `GOCOVERDIR=dir ./ktags …` +
  `go tool covdata textfmt/percent/merge`, since **Go 1.20**
  ([go.dev/doc/build-cover](https://go.dev/doc/build-cover)). This is how you get honest
  coverage for the TUI path (drive the real binary under a PTY), then merge with the unit
  profile before thresholding. Go 1.26 adds `T.ArtifactDir()` / `go test -artifacts` for
  golden-file outputs ([go1.26](https://go.dev/doc/go1.26)) — useful for TUI golden frames.

### 6.6 Mutation testing in Go

| Tool | State |
|---|---|
| `go-gremlins/gremlins` | **v0.6.0, 2025-12-06** (prior v0.5.0 2023-12-19); last commit 2026-03-30; not archived; 403 stars. README: designed for "smallish Go modules", big modules "can take hours". Gates: `--threshold-efficacy`, `--threshold-mcover` (default 0 = off). Docs site partially 404. |
| `zimmski/go-mutesting` | **Dead**: v1.2 2021-06-10, default branch untouched since 2021; `avito-tech/go-mutesting` fork pushed 2026-01-12. |
| `gtramontina/ooze` | Pushed **2026-08-31**, cross-OS CI, but **zero GitHub releases** (tags to v0.3.1); runs *inside* `go test` via `ooze.Release(t)`. |
| Newer (2025–26) | `sivchari/gomu` (44 stars, pushed 2026-07-10); `szhekpisov/gomutants` 0.5.0 (7 stars, Go 1.26+, `--changed-since <ref>`, exit 10/11 thresholds, gremlins-compatible JSON — perf claims **self-reported**); `fchimpan/mutest` (5 stars, comparison/equality operators only). |

**Context:** Thoughtworks Technology Radar **Vol. 34 (April 2026)** puts **"Mutation testing"
in Trial** precisely because of agents: coverage "can mask logically hollow tests or generated
code that has never been meaningfully asserted… mutation testing acts as a reinforcement layer
for catching 'perpetually green' tests" — but it names Stryker, Pitest and cargo-mutants and
**no Go tool** ([radar](https://www.thoughtworks.com/radar/techniques)).

**Verdict:** valuable signal for agent-written tests, immature Go ecosystem. **Not a PR gate.**
Run gremlins (or gomutants `--changed-since`) as a `workflow_dispatch`/weekly job scoped to
pure-logic packages (inventory parsing, filtering, the action registry state machine), not TUI
rendering; look at the report before adding a threshold.

### 6.7 Supply chain

- **govulncheck**: `golang.org/x/vuln` **v1.7.0, 2026-08-13** per
  [proxy.golang.org](https://proxy.golang.org/golang.org/x/vuln/@latest) — the
  [GitHub releases page](https://github.com/golang/vuln/releases) misleadingly stops at
  v1.1.4 (mirror only). Symbol-level reachability, not manifest matching
  ([go.dev/doc/security/vuln](https://go.dev/doc/security/vuln/)); `-mode binary` scans a
  released `ktags` binary; formats `text|json|sarif|openvex` — ⚠️ **json/sarif exit 0 even
  with findings.** `golang/govulncheck-action` **v1.1.0, 2026-07-10** (README still says
  "experimental") — simpler to run
  `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` yourself.
- **Module integrity**: `go mod verify` only checks the **local module cache** against
  recorded hashes ([go.dev/ref/mod](https://go.dev/ref/mod#go-mod-verify)); the real
  protection is a committed `go.sum` + sum.golang.org. **`go mod tidy -diff` (Go 1.23)** fails
  non-zero if go.mod/go.sum are untidy without mutating the tree
  ([go1.23](https://go.dev/doc/go1.23)) — replaces the `git diff --exit-code` folk pattern.
  Set **`GOTOOLCHAIN=local`** in CI so an agent- or bot-authored `go.mod` bump can't silently
  download a different toolchain ([go.dev/doc/toolchain](https://go.dev/doc/toolchain)).
- **Dependency bots**: Dependabot `gomod` + `github-actions` with `groups` (incl.
  `applies-to: security-updates`)
  ([options reference](https://docs.github.com/en/code-security/dependabot/working-with-dependabot/dependabot-options-reference)).
  Renovate **44.65.5, 2026-09-05**, `gomod` manager with `gomodUpdateImportPaths` for `/v2`
  majors. For one module on GitHub, **Dependabot is enough**; switch to Renovate for
  `go.work`/multi-module. Only automerge behind required checks.
- **Provenance/signing — the 2026 change**: `slsa-framework/slsa-github-generator` (v2.1.0,
  2025-02-24) added a **"no longer actively maintained"** notice on **2026-08-07** pointing to
  GitHub artifact attestations ([README](https://github.com/slsa-framework/slsa-github-generator)).
  Use **`actions/attest@v4`** (v4.2.2, 2026-08-04; `attest-build-provenance` v4.2.2 is now a
  wrapper) with `permissions: id-token: write, attestations: write, artifact-metadata: write`
  and `subject-checksums: dist/checksums.txt` ([actions/attest](https://github.com/actions/attest)).
  SLSA spec is **v1.2 (2025-11-24)**; v1.1 marked Retired. **cosign v3.1.3, 2026-08-06** —
  keyless `cosign sign-blob --bundle x.sigstore.json --yes`.
  **goreleaser v2.18.1, 2026-09-05** with built-in Syft SBOMs and cosign `signs:` on
  `artifacts: checksum`; `goreleaser/goreleaser-action` **v7.2.3, 2026-06-29**.
- **`actions/setup-go` v7.0.0, 2026-07-16** (node24, ESM); **`cache: true` is the default**,
  caching `GOMODCACHE` and `GOCACHE`, keyed on `go.mod` (since v6.3.0) — **no separate
  `actions/cache` step needed**
  ([advanced-usage](https://github.com/actions/setup-go/blob/main/docs/advanced-usage.md)).
  **`actions/checkout` v7.0.1, 2026-07-20**; v7.0.0 blocks fork checkout under
  `pull_request_target`/`workflow_run`. SHA-pin actions in release workflows.

### 6.8 The "independent reviewer agent" pattern — what is actually evidenced

**Vendor guidance.** Anthropic's April 2025 best-practices essay is the canonical source:
"Have one Claude write code; use another Claude to verify… Run `/clear` or start a second
Claude in another terminal"
([Wayback 2025-07-15](http://web.archive.org/web/20250715133307/https://www.anthropic.com/engineering/claude-code-best-practices)).
It now redirects to a more careful 2026 rewrite that ranks verification by hardness:
prompt → `/goal` → **Stop hook as a deterministic gate** ("blocks the turn from ending until
it passes. Claude Code overrides the hook and ends the turn after 8 consecutive blocks") →
"**By a second opinion**: a verification subagent… has a fresh model try to refute the result,
so the agent doing the work isn't the one grading it" — and ships its own caveat:
"**A reviewer prompted to find gaps will usually report some, even when the work is sound**"
([best practices](https://code.claude.com/docs/en/best-practices)).

Anthropic's production **Code Review** uses parallel specialised agents plus "a verification
step [that] checks candidates against actual code behavior to filter out false positives", and
deliberately **never blocks** ("always completes with a neutral conclusion") — you gate on its
severity JSON yourself ([docs](https://code.claude.com/docs/en/code-review)). Anthropic's only
numbers: PRs with substantive review comments **16% → 54%**, ~1/3 of past incident bugs would
have been caught ([SDLC post, 2026-07-21](https://claude.com/blog/how-anthropic-secures-its-ai-native-software-development-lifecycle))
— vendor data. OpenAI (2025-09-15): "we always recommend using Codex as an additional
reviewer—not a replacement for human reviews"
([announcement](https://openai.com/index/introducing-upgrades-to-codex/)); its docs add "Code
review rules guide Codex; they don't replace tests, branch protections, or required approvals"
([docs](https://learn.chatgpt.com/docs/third-party/github)). GitHub Copilot: "Supplement
Copilot's feedback with a human review"
([docs](https://docs.github.com/en/copilot/concepts/code-review/code-review)).
**No vendor publishes precision/recall** (Anthropic, OpenAI, GitHub, Google, Greptile,
CodeRabbit).

**Independent evidence (arXiv, 2025–2026):**

- **Fresh session helps, modestly, and it's one small study.** Cross-Context Review,
  [arXiv:2603.12123](https://arxiv.org/abs/2603.12123) (2026-03-12): fresh-session F1
  **28.6%** vs same-session self-review **24.6%** (p=0.008); a *context-aware* subagent did
  **worse** (23.8%) — **context separation, not "subagent", is the active ingredient.**
  n=30 synthetic artifacts, single author, unreplicated; all conditions missed ~70% of
  injected errors.
- **Cross-model beats same-model, asymmetrically.**
  [arXiv:2607.21656](https://arxiv.org/abs/2607.21656) (2026-07-22): Claude reviewing Codex
  **71.6% → 89.7%**; Codex reviewing Claude **91.4% → 82.8%** (a weaker reviewer talked a
  stronger writer out of correct code); Claude self-review 91.4% → 91.4% (**no effect**).
  Mechanism is *error decorrelation* ([arXiv:2607.10139](https://arxiv.org/abs/2607.10139)).
- **Shipped AI review has low precision.** CR-Bench
  [arXiv:2603.11078](https://arxiv.org/abs/2603.11078): **3.5–5.1% precision**; Reflexion
  ("look harder") raised recall but cut SNR 5.11 → 1.95. SWE-PRBench
  [arXiv:2603.26130](https://arxiv.org/abs/2603.26130): models catch **15–31%** of
  human-flagged issues and get *worse* with more context. CodeRabbit in the wild
  [arXiv:2607.03316](https://arxiv.org/abs/2607.03316): 31k comments, **56.3% rejected**.
- **Self-review failure modes.** [arXiv:2605.21537](https://arxiv.org/abs/2605.21537) — 31.7%
  of semantic breakages silently endorsed by the model that produced them, some while
  "explicitly articulating the very… distinction that broke their output"; DeepMind
  [arXiv:2310.01798](https://arxiv.org/abs/2310.01798) — LLMs cannot self-correct without
  external feedback; recursive self-gating enters a "rubber-stamp regime"
  [arXiv:2606.28438](https://arxiv.org/abs/2606.28438).
- **Sycophancy / self-preference / anchoring.** Anthropic's own
  [arXiv:2310.13548](https://arxiv.org/abs/2310.13548); self-preference
  [arXiv:2404.13076](https://arxiv.org/abs/2404.13076) — but 2026 work finds the bias is driven
  largely by **labels** ("this is yours") rather than recognition
  [arXiv:2608.18091](https://arxiv.org/abs/2608.18091), and is weak on verifiable tasks
  [arXiv:2606.20093](https://arxiv.org/abs/2606.20093) → **don't tell the reviewer whose code
  it is; hand it only the diff and the acceptance criteria.** Prior context anchors judges even
  as metadata and blocks 48% of corrections; CoT and "ignore it" instructions don't fix it
  [arXiv:2608.25869](https://arxiv.org/abs/2608.25869). Multi-agent debate **amplifies** shared
  bias [arXiv:2608.02827](https://arxiv.org/abs/2608.02827) — more agents of one model is not
  more independence. Code-smell verdicts flip up to 72% under prompt framing
  [arXiv:2607.10411](https://arxiv.org/abs/2607.10411).

**Practitioners.** Simon Willison: "The one thing you absolutely cannot outsource to the
machine is testing that the code actually works… If you haven't seen it run, it's not a working
system" ([2025-03-11](https://simonwillison.net/2025/Mar/11/using-llms-for-code/)); "Don't file
pull requests with code you haven't reviewed yourself… The initial review pass is your
responsibility, not something you should farm out"
([anti-patterns, 2026-03-04](https://simonwillison.net/guides/agentic-engineering-patterns/anti-patterns/))
— he does **not** endorse AI-reviews-AI as the primary net. Kief Morris (Thoughtworks,
2026-03-04) frames the bottleneck: "Agents can generate code faster than humans can manually
inspect it."

**Synthesis:** the reviewer agent earns its keep in this order — (a) a **different model** at
least as strong as the writer, that can *execute* the code; (b) the deterministic checks it
runs (tests/lint/build); (c) context separation, real but modest. **A fresh session of the
*same* model that can't run anything is the weakest form — and it is the one most people mean
by "independent reviewer".** Treat its output as a candidate list, never as an approval.

### 6.9 Deterministic gates over trust

- Anthropic, current docs: "Claude stops when the work looks done. Without a check it can run,
  'looks done' is the only signal available, and you become the verification loop… Give Claude
  something that produces a pass or fail, and the loop closes on its own"
  ([best practices](https://code.claude.com/docs/en/best-practices)). Mechanisms: **Stop hook**
  (blocks turn end until a script passes), **PreToolUse hook** exit 2 (denies a Bash call
  unconditionally — "even a JSON `permissionDecision` of 'allow' can't override it")
  ([hooks](https://code.claude.com/docs/en/hooks)); OS-enforced Bash **sandbox** (Seatbelt on
  macOS, bubblewrap on Linux) with filesystem/network allowlists
  ([sandboxing](https://code.claude.com/docs/en/sandboxing)); **`claude --worktree <name>`** so
  parallel agents cannot collide ([worktrees](https://code.claude.com/docs/en/worktrees)).
- Thoughtworks Radar **Vol. 34, April 2026**: **"Feedback sensors for coding agents" — Trial**:
  "deterministic quality gates such as compilers, linters, structural tests and test suites…
  wired into agentic workflows so that failures trigger timely self-correction… these sensors
  should run during the coding session and report clean results **before a commit is made**";
  **"Sandboxed execution for coding agents" — Trial**: "a sensible default rather than an
  optional enhancement"; **"Codebase cognitive debt" — Caution**, referencing **"Complacency
  with AI-generated code" — Hold (Nov 2025)**
  ([radar](https://www.thoughtworks.com/radar/techniques)).
- "LLM-as-a-Judge Is Not an Oracle: Why Self-Improving Agents Need Deterministic Guardrails"
  [arXiv:2609.02246](https://arxiv.org/abs/2609.02246) (2026-09-02): eleven judge failure modes
  (one: a 100% pass rate hiding 68% true capability); prescription — **acceptance checks
  outrank judges**.
- Armin Ronacher on why Go suits this: "In Go, tests run straightforwardly and incrementally,
  significantly enhancing the agentic workflow"; "Tools need to be fast"
  ([2025-06-12](https://lucumr.pocoo.org/2025/6/12/agentic-coding/)). Willison: "Eyeballing
  every line of code has never been the most effective way to validate a change"
  ([2026-08-22](https://simonwillison.net/2026/Aug/22/more-than-just-code-review/)).
- **The operational rule that follows: prompts and AGENTS.md are suggestions; CI status checks
  and branch protection are the contract.** Anything the agent must not do (attribution
  trailers, `--no-verify`, dependency bumps without tidy, untested TUI paths) needs a check
  that fails red, re-run server-side.

### Concrete guardrail stack for ktags (Sept 2026)

1. **Go 1.27.1** in `go.mod`; `GOTOOLCHAIN=local` and `GOFLAGS=-mod=readonly` in CI;
   `actions/checkout@v7` + `actions/setup-go@v7` (`go-version-file: go.mod`, default caching,
   **no** `actions/cache`).
2. **lefthook v2.1.12** as a `go.mod` tool (`go get -tool github.com/evilmartians/lefthook/v2`);
   `pre-commit`: `golangci-lint fmt` + `run`, `go mod tidy -diff`, `go test ./...`;
   `commit-msg`: the AI-trailer guard. CI re-runs every hook command **and** greps
   `git log origin/main..HEAD` for trailers.
3. **golangci-lint v2.13.2** (`version: "2"`, `linters.default: standard` + the CLI/TUI set,
   `formatters: [gofumpt v0.11.0, gci v0.14.0]`) via `golangci/golangci-lint-action@v9`
   (`version: v2.13`, `only-new-issues: true`) in its own job.
4. **Tests**: `gotestsum v1.13.0` (`--format testname --junitfile`), **no `--rerun-fails`**;
   a separate `go test -race ./...` job with a raised `-timeout`.
5. **Coverage floor**: `go test -coverprofile -covermode=atomic -coverpkg=./...` merged with a
   `go build -cover` + `GOCOVERDIR` run of the real binary under a PTY; gate with
   `vladopajic/go-test-coverage@v2` (v2.19.0; start `total: 70`, `package: 60`; badge to an
   orphan branch). Codecov optional.
6. **Mutation**: `gremlins v0.6.0` as a weekly/`workflow_dispatch` job on logic packages only;
   report, no threshold initially.
7. **Supply chain**: `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` (v1.7.0, **text**
   format so it exits non-zero) on push + weekly; Dependabot `gomod` + `github-actions` with
   grouped minor/patch; SHA-pinned actions in release workflows.
8. **Release**: `goreleaser v2.18.1` via `goreleaser-action@v7`, Syft SBOM, cosign v3.1.3
   keyless `sign-blob --bundle` on the checksums file, **`actions/attest@v4`** with
   `subject-checksums` — **not** slsa-github-generator. Verify with `gh attestation verify`.
9. **Agent-side**: `attribution: {commit:"", pr:"", sessionUrl:false}` in
   `.claude/settings.json` *and* the hook (settings are demonstrably unreliable); Bash sandbox
   on; one `claude --worktree` per task; a `Stop` hook running
   `go build ./... && go vet ./... && go test ./...`; and a **`PreToolUse` hook denying the
   escape hatches** — `git commit --no-verify`, `git push --force`, and `LEFTHOOK=0` — since
   those are exactly how an agent routes around every client-side guard. Note the ceiling:
   **Claude Code overrides a Stop hook after 8 consecutive blocks**, so the Stop hook is a
   nudge toward green, not an absolute gate; the absolute gate is `ci-required`.
10. **Review**: agent-written PRs get (a) all of the above as required checks, (b) an
    **unlabelled, diff-only** review by a **different model** (the cross-model asymmetry
    evidence lines up exactly with the existing project rule "opus implements / fable
    reviews"), told to report only correctness and requirements gaps, and (c) the maintainer's
    own read. **AI review never approves or merges.**

### Flagged as unverified in this section

gremlins' numeric exit codes for threshold failures (gremlins.dev CI docs 404).
gomutants/mutest/gomu performance claims are self-reported; all three are <50 stars and
<15 months old. Codecov's intended current tag: releases say v7.0.0, README/docs still `@v5`.
GitHub Copilot code review's original 2025 GA date. SLSA v1.2 changelog contents;
`cosign verify-blob` exact identity flags. **The "fresh session" benefit rests on a single
unreplicated n=30 study; no study measures fresh-session review on *real* AI-written PRs.**

---

## 7. Planning artefacts: ADRs, specs, plan files

*Every version/date below was checked against the GitHub API or the vendor's own docs
unless marked **[UNVERIFIED]**.*

### 7.1 ADRs — the format survived, the tooling did not

| Thing | Latest release | Repo activity | Verdict |
|---|---|---|---|
| [MADR](https://github.com/adr/madr) | **4.0.0, 2024-09-17** | last push **2026-08-28**, 2,452★ | Alive, but no new version in ~2 years. The template is done. |
| [`adr-tools` (npryce)](https://github.com/npryce/adr-tools) | **3.0.0, 2018-07-25** | last code push **2024-04-25**, 5,670★, not archived | Effectively **unmaintained**. Do not build on it. |
| [`log4brains`](https://github.com/thomvaill/log4brains) | **v1.1.0, 2024-12-17** | dormant ~21 months, 1,582★ | A static-site generator for ADRs — pure overhead for a solo repo. |
| Nygard format (2011) | n/a | — | Still the substrate most rewrites emit. |

The [official ADR tooling index](https://adr.github.io/adr-tooling/) still lists
`adr-manager`, `pyadr`, `dotnet-adr`, `adr-log` — all pinned to **MADR 2.1.2**, two major
versions behind the template, and none of them AI/agent-aware.

Directory convention has no single winner: `docs/adr/` (Nygard/`adr-tools`' default is
actually `doc/adr/`) vs `docs/decisions/` (MADR 4.x's own preference). MADR 4.0 dropped
numeric prefixes as a hard requirement.

**Are ADRs useful as agent context in 2026?** The argument is loud; the evidence is thin.

- [Actual AI, "ADRs for Coding Agents: Architectural Context, Optimized" (2026-06-23)](https://www.actual.ai/blog/agent-optimized-adrs)
  argues ADRs must be **rewritten for a machine reader**: MUST/MUST NOT normative
  language, `applies_to:` file globs so only relevant records load, stable IDs
  (`R-IMG-001`), a *verifiable check* per rule (a grep or lint command), and ~200 lines
  max per file. It suggests `doc/adr/` or `.claude/rules/`. **No metrics or experiments** —
  the case rests on AWS/Microsoft/Spotify precedent.
- [Codex Knowledge Base, "Architecture Decision Records with Codex CLI" (2026-04-28)](https://codex.danielvaughan.com/2026/04/28/codex-cli-architecture-decision-records-adr-automated-governance/)
  documents agents *generating* ADRs from diffs and *checking* new code against them.
- The recurring practical pattern is an **ADR clause in `AGENTS.md`**: "check `docs/adr/`
  before any architectural choice; write a `proposed` ADR before implementing". That is
  the whole mechanism — no agent auto-loads ADRs.
- **[UNVERIFIED]**: no benchmark, A/B test or dataset measuring whether ADRs in context
  improve agent output. All claims are anecdotal or vendor-blog.

ADRs are cheap, tool-agnostic, and the only artefact here that **ages well** — a decision
from 2026 is still true in 2028; a task list is garbage in a week.

### 7.2 GitHub Spec Kit

[`github/spec-kit`](https://github.com/github/spec-kit), created **2025-08-21**,
**133,716★**, 319 open issues, last push **2026-09-04**. **v1.0.0 shipped 2026-08-21**;
current **v1.0.4, 2026-09-02**. Aggressive release cadence.

Commands: `/speckit.constitution`, `/speckit.specify`, `/speckit.plan`, `/speckit.tasks`,
`/speckit.implement`, `/speckit.converge`, plus optional `/speckit.clarify`,
`/speckit.analyze`, `/speckit.checklist`.

Artefacts, per [`spec-driven.md`](https://github.com/github/spec-kit/blob/main/spec-driven.md):
a feature branch gets `specs/<NNN-branch-name>/` containing `spec.md`, `plan.md`,
`research.md`, `data-model.md`, `contracts/`, `quickstart.md`, `tasks.md`. The constitution
lives under `.specify/`, which also holds `templates/`, `templates/overrides/`, `presets/`,
`extensions/`, `memory/` and platform scripts, with a documented 4-level override precedence.

**Tool-agnosticism**: good on the *content* side — everything is plain Markdown in the
repo. But `specify init` also writes **agent-specific command dirs**: `.claude/commands/`,
`.codex/prompts/`, `.opencode/command/`, `.github/prompts/`. The site claims **"38
integrations … Switch freely between agents with a single command"** plus a `generic`
escape hatch ([spec-kit docs](https://github.github.io/spec-kit/), updated 2026-08-21).
So: agnostic artefacts, per-agent shims.

**Criticism** — the strongest is
[Scott Logic, "Putting Spec Kit Through Its Paces: Radical Idea or Reinvented Waterfall?" (2025-11-26)](https://blog.scottlogic.com/2025/11/26/putting-spec-kit-through-its-paces-radical-idea-or-reinvented-waterfall.html):
the same feature took **3.5 hours via Spec Kit vs 23 minutes via iterative prompting
(~9×)**; one plan phase emitted **>2,000 lines of Markdown** including a 406-line research
doc they judged duplicative; *"a lot of time spent reviewing markdown or waiting for the
agent to churn out more markdown … no qualitative benefit to justify the overhead."*

### 7.3 Kiro (AWS)

Announced **2025-07-14**, **GA 2026-05-07**
([AWS May 2026 roundup](https://www.usage.ai/blogs/aws/monthly-updates/aws-may-2026/));
>250k developers in preview. Amazon Q Developer is being retired in its favour
([AWS end-of-support announcement](https://aws.amazon.com/blogs/devops/amazon-q-developer-end-of-support-announcement/)).

Specs produce three files per feature — **`requirements.md`** (user stories + acceptance
criteria in **EARS** notation), **`design.md`**, **`tasks.md`**
([Kiro specs docs](https://kiro.dev/docs/specs/)). Credit-metered pricing.

**Steering** ([docs](https://kiro.dev/docs/steering/)): `.kiro/steering/` (or
`~/.kiro/steering/`), defaults `product.md`, `tech.md`, `structure.md`, "included in every
interaction by default." YAML front-matter inclusion modes: `always`, `fileMatch` (glob),
`manual` (`#filename`), `auto`. The docs explicitly say these are markdown, part of your
codebase, and should be version controlled. Kiro also natively reads **`AGENTS.md`**.

**Portability**: the *files* are plain Markdown and survive outside Kiro. What does **not**
port is the machinery — task-by-task execution, `#file` reference syntax, front-matter
inclusion modes, the three-phase gate. EARS is a documented public notation.
Net: **low lock-in on artefacts, high lock-in on workflow.**

### 7.4 OpenSpec (Fission-AI)

[`Fission-AI/OpenSpec`](https://github.com/Fission-AI/OpenSpec), created **2025-08-05**,
**67,449★**, last push **2026-09-04**. npm `@fission-ai/openspec`, latest **v1.12.0,
2026-09-03** (roughly weekly minors). Node ≥20.19.0, MIT.

Layout: `openspec/` with `specs/` (requirements + scenarios, plain Markdown) and
`changes/<change>/` holding `proposal.md`, `design.md`, `tasks.md`, plus `archive/`.
Workflow `/opsx:explore → /opsx:propose → /opsx:apply → /opsx:archive`. Ships as **plain
Markdown skills + slash commands, no MCP, no API keys**; the README notes tools spell the
commands differently (`/opsx-propose` in Cursor/Copilot, `$openspec-propose` in Codex,
`@opsx-propose` in Amazon Q).

Positioning: **brownfield/delta-oriented** — you describe the *change* against existing
specs rather than re-specifying the world. That is the meaningful design difference vs
Spec Kit and why it's the common recommendation for existing codebases.

### 7.5 BMAD-METHOD

[`bmad-code-org/BMAD-METHOD`](https://github.com/bmad-code-org/BMAD-METHOD), created
**2025-04-13**, **52,732★**, last push **2026-09-06**. Current **v6.12.0, 2026-09-04** —
so **v6 is the live line**, not v5. It simulates an agile *team* of agent personas
(BMad Method core, BMad Builder, Creative Intelligence Suite, BMad Test Architect, BMad
Loop, BMad Game Dev Studio) and installs as skills into Claude Code / Codex plugins.

v6.12.0's notes read as a direct answer to the "too heavy" complaint: *"Build decides how
much ceremony a change needs after investigating it, not before. Simple changes now get a
two-section spec and finish in one session."* Breaking changes in that release:
`persistent_facts` ships empty, `{diff_output}`→`{diff_file}`, `project-context.md` must be
re-added manually, `llms.txt` publication removed.

Criticism is consistent across 2026 comparisons
([DEV](https://dev.to/willtorber/spec-kit-vs-bmad-vs-openspec-choosing-an-sdd-framework-in-2026-d3j),
[Reenbit](https://reenbit.com/bmad-vs-spec-kit-vs-openspec-choosing-your-spec-driven-ai-framework/),
[ToolTwist](https://tooltwist.com/insights/spec-driven-frameworks-cxo-guide)):
weeks-to-months learning curve vs a day for Spec Kit, high token cost, overkill for small
tasks, *"for a two-person startup it's a trap."* A Reenbit comparison reports **5.5 hours
(BMAD) vs 12 minutes (OpenSpec)** for a CRM dashboard — **[UNVERIFIED]**, vendor blog, no
methodology. **[UNVERIFIED]**: the `bmad/` folder layout and the "scale-adaptive levels
0–4" model could not be confirmed from primary docs.

### 7.6 Plan mode across the four agents

| Agent | Enter | Persists to a file? |
|---|---|---|
| **Claude Code** | `Shift+Tab` cycle (`default → acceptEdits → plan`), `/plan` prefix for one prompt, or `claude --permission-mode plan`; `defaultMode: "plan"` in `.claude/settings.json` | **No.** The plan is a conversation artefact surfaced by `ExitPlanMode`. `Ctrl+G` opens it in `$EDITOR`. With `showClearContextOnPlanAccept` there's an option that **approves and clears the planning context** — the built-in nod to compaction. ([docs](https://code.claude.com/docs/en/permission-modes)) |
| **Codex CLI** | `/plan` or `Shift+Tab`; read-only until you approve | **No file.** The `update_plan` tool is an in-session TODO, and it was made **opt-in** (`tools.update_plan.enabled` defaults to `false`) in a PR merged **2026-08-31** ([openai/codex#41744](https://github.com/openai/codex/pull/41744)). Signal: OpenAI is de-emphasising the ephemeral plan tool. |
| **OpenCode** | `Tab` switches between the two built-in primary agents **Build** and **Plan**; Plan sets edits/patches/bash to `ask` ([docs](https://opencode.ai/docs/agents/)) | **No — explicitly does not write files.** |
| **Cursor** | `Shift+Tab`, `/plan`, or `--mode=plan` | **Yes, optionally.** Produces a `*.plan.md` in a dedicated Plan editor; **"Save to workspace"** writes it into **`.cursor/plans/`** ([blog](https://cursor.com/blog/plan-mode), [docs](https://cursor.com/docs/agent/plan-mode)). Default storage is the home dir — **[UNVERIFIED]** whether `.cursor/plans/` is the exact committed path in current builds. |

**The gap:** three of four leave the plan in a context window that dies. That gap is
exactly what `docs/plans/*.md` fills.

### 7.7 `docs/plans/*.md` — plan-file-as-handoff

This is the best-evidenced pattern in the whole list, and the least tool-specific.

- **HumanLayer, [ACE-FCA](https://github.com/humanlayer/advanced-context-engineering-for-coding-agents/blob/main/ace-fca.md)** /
  [blog](https://www.humanlayer.dev/blog/advanced-context-engineering) — "Advanced Context
  Engineering for Coding Agents": the **research → plan → implement** loop with **frequent
  intentional compaction**, keeping context utilisation in the **40–60%** band and
  distilling each phase into a structured artefact that is the *only* input to the next.
  Files live in a `thoughts/` tree (`thoughts/shared/research/`, `thoughts/shared/plans/`),
  driven by `.claude/commands/research_codebase.md`, `create_plan.md`, `implement_plan.md`.
  After each verified phase, status is **compacted back into the original plan file**.
  Claims: a bug fixed in a 300k-LOC Rust codebase (BAML) with the first PR approved
  overnight; 35k LOC of cancellation + WASM support in 7 hours.
- **OpenAI Cookbook, ["Using PLANS.md for multi-hour problem solving"](https://developers.openai.com/cookbook/articles/codex_exec_plans)** —
  "ExecPlans": a single self-contained Markdown doc (purpose, progress checklist with
  timestamps, *Surprises & Discoveries*, *Decision Log*, concrete steps with expected
  outputs, validation). Kept at repo root or `.agent/`, referenced from `AGENTS.md`. The
  hard rule: the plan must be **fully self-contained** so a fresh session needs *"only the
  ExecPlan and no other work."* Claims a near-identical PLANS.md let Codex work **>7 hours
  from a single prompt**. **[UNVERIFIED]**: no publication date on the page.
- **Hartley Brody, ["Markdown is the new source code" (2026-03-09)](https://blog.hartleybrody.com/markdown-research-planning/)** —
  deliberately *rejects* chat-based plan mode in favour of files: research →
  `.scratch/research/{SYSTEM}.md`, plan → `.scratch/plan/{FEATURE}.md`, human annotates
  with `-->` comment markers, then a **fresh session** implements from the finalised plan
  and updates it in place; the plan is pasted into the PR inside a `<details>` block.
  Ships `.agent-skills` (`/research`, `/plan-create`, `/plan-implement`) against the
  agentskills.io spec.
- **[`OthmanAdi/planning-with-files`](https://github.com/OthmanAdi/planning-with-files)** —
  **26,665★**, last push 2026-09-05, **v3.16.1**. Writes `task_plan.md` / `findings.md` /
  `progress.md` (or `.planning/YYYY-MM-DD-slug/` for parallel work), re-injects the plan
  every turn via hooks, and flushes state on a pre-compaction hook so `/clear` and
  auto-compaction don't lose the goal. Installs across **60+ agents** via the Agent Skills
  standard (`npx skills add …`). Self-reported 96.7% pass rate on its own 706-test suite
  and 3/3 blind A/B wins — **[UNVERIFIED]**, self-benchmarked.
- Naming convention seen repeatedly in the wild: `docs/plans/YYYY-MM-DD-slug.md`,
  superseded plans moved to `docs/archive/`, an optional `docs/plans/index.md`. No
  standards body behind it.

### 7.8 Tool-agnostic vs locked-in

| Artefact / system | Files in repo | Portable across CC / Codex / OpenCode? | Lock-in surface | Maintenance cost |
|---|---|---|---|---|
| **ADRs (MADR 4.0)** in `docs/adr/` | ✅ plain `.md` | ✅ fully — any agent reads them if `AGENTS.md` says to | none | low, append-only |
| **`docs/plans/*.md`** (hand-rolled) | ✅ plain `.md` | ✅ fully | none | low, disposable |
| **`AGENTS.md`** | ✅ | ✅ native in Codex, OpenCode, Kiro, Cursor; Claude Code via `CLAUDE.md` import | trivial | low |
| **OpenSpec** | ✅ `openspec/specs/`, `openspec/changes/` | ✅ artefacts; slash-command names differ per tool | thin CLI (`npx @fission-ai/openspec`) | medium |
| **Spec Kit** | ✅ `specs/NNN-*/`, `.specify/` | ✅ artefacts; ⚠️ writes `.claude/commands/`, `.codex/prompts/`, `.opencode/command/` shims | `specify` CLI + templates + 38 per-agent shims | medium-high (2,000-line plan phases) |
| **Kiro specs/steering** | ✅ `.kiro/specs/`, `.kiro/steering/` | ⚠️ files yes; inclusion modes, `#file` refs, phase gating are Kiro-only | Kiro IDE/CLI, credit-metered AWS account | medium |
| **BMAD v6** | ✅ but many | ⚠️ installed as per-tool plugins/skills | whole persona framework, own CLI, breaking changes between minors | **high** |
| **Plan mode (CC / Codex / OpenCode)** | ❌ nothing persists | n/a — dies with the session | none | zero |
| **Cursor `.cursor/plans/*.plan.md`** | ✅ if you "Save to workspace" | ✅ readable Markdown; ⚠️ Plan-editor UI is Cursor-only | dir name only | low |

### 7.9 Does spec-driven actually improve output? 2026 evidence

- **The only peer-reviewable positive result found is small.**
  [Taghavi & Bhavani, *Spec Kit Agents: Context-Grounded Agentic Workflows*, arXiv:2604.05278 (2026-04-07)](https://arxiv.org/abs/2604.05278):
  adding read-only context-grounding/validation hooks across Specify/Plan/Tasks/Implement
  gave **+0.15 on a 1–5 LLM-as-judge composite (~3%)** and **58.2% Pass@1 on SWE-bench Lite
  (+1.7pp over baseline)**, over 128 runs / 32 features / 5 repos, Wilcoxon p<0.05. Note
  the comparison is *Spec Kit with grounding vs Spec Kit without* — **not spec-driven vs
  plain prompting.**
- **The negative result is bigger and more practical.** Scott Logic's 3.5h vs 23min (§7.2)
  is the number people cite. Their follow-up,
  ["The Specification Renaissance?" (2025-12-15)](https://blog.scottlogic.com/2025/12/15/the-specification-renaissance-skills-and-mindset-for-spec-driven-development.html),
  softens it toward *specification skill* mattering more than *specification tooling*.
- [Farrag, *The Productivity-Reliability Paradox*, arXiv:2605.01160 (2026-05-01)](https://arxiv.org/pdf/2605.01160)
  frames the tension: controlled studies show **20–56% productivity gains on well-scoped
  tasks**, while field data shows **98% more PRs but 91% longer review times with flat
  delivery metrics**. Its thesis — *"specification discipline, not model capability, is the
  binding constraint"* — is argued, not measured.
- Recurring practitioner complaint ([Martinelli](https://martinelli.ch/why-spec-driven-development-tools-fail-in-the-enterprise/)
  and the comparison posts): **spec rot** — specs go stale within months, at which point
  they actively mislead agents.
- **Honest summary**: there is *no* 2026 benchmark showing a full spec pipeline beats
  plan-file + iterative prompting on quality. There *is* replicated evidence that
  plan/context artefacts help long-horizon runs, and measured evidence that heavyweight
  pipelines cost 5–10× the wall-clock.

### What this means for ktags

Keep two artefact families and no framework: **`docs/adr/NNNN-slug.md`** in MADR 4.0
format (hand-written, agent-shaped: MUST/MUST NOT, a `Consequences` section, and a
verification command where one exists) and **`docs/plans/YYYY-MM-DD-slug.md`** for anything
bigger than a two-file change. Plan mode does not persist in Claude Code, Codex or
OpenCode, so the plan file is the only thing that survives `/clear`, compaction, a crash,
or a switch between agents. Steal ACE-FCA's one free trick: **compact status back into the
plan file after each verified phase, and start the implement step in a fresh session.**
Archive or delete finished plans. Skip Spec Kit, Kiro and BMAD; if you later want more
structure, evaluate **OpenSpec** first — it's plain Markdown, delta-oriented, and the
cheapest to walk away from.

---

## 8. Community sentiment 2025–2026: what works vs what is over-engineered

**How to read this section.** Almost everything the community "knows" about agent workflows
is anecdote. The small number of *measured* results (RCTs, benchmarks, repo telemetry) are
consistently more pessimistic than self-report, and they mostly point the same direction:
**less scaffolding, tighter verification, shorter context**. Claims are tagged
MEASURED / SELF-REPORTED / ANECDOTAL.

### 8.1 How big should AGENTS.md / CLAUDE.md be?

**Vendor guidance is now explicitly numeric and it is small.** Anthropic's memory docs:
*"**Size**: target under 200 lines per CLAUDE.md file. Longer files consume more context and
reduce adherence."* ([memory docs](https://code.claude.com/docs/en/memory), live 2026-09-07).
The best-practices page is blunter: *"Keep it concise. For each line, ask: 'Would removing
this cause Claude to make mistakes?' If not, cut it. **Bloated CLAUDE.md files cause Claude
to ignore your actual instructions!**"* and names "The over-specified CLAUDE.md" as a
failure pattern — *"If your CLAUDE.md is too long, Claude ignores half of it because
important rules get lost in the noise."*
([best practices](https://code.claude.com/docs/en/best-practices)).

**The "progressive disclosure via linked docs" argument is half-wrong, at least in Claude
Code.** The docs are explicit: *"Splitting into `@path` imports helps organization but
**doesn't reduce context**, since imported files load at launch."* Only *skills* and
*path-scoped rules* (`.claude/rules/` with `paths:`) actually defer loading. Simon Willison
confirmed the mechanism empirically: *"I've sniffed Claude Code's HTTP traffic and confirmed
that the CLAUDE.md file content (and AGENTS.md if it is @-referenced) is automatically
included in the system prompt"* ([HN, 2025-11-02](https://news.ycombinator.com/item?id=45619537)).
**So `@AGENTS.md` buys tool portability, not a token saving.**

**Practitioner numbers, all ANECDOTAL:**

- HumanLayer, 2025-11-25: *"**< 300 lines is best, and shorter is even better**"*; their own
  file is *"less than sixty lines"*. Also: *"Claude will ignore the contents of your
  CLAUDE.md if it decides that it is not relevant to its current task."* and *"Never send an
  LLM to do a linter's job."*
  ([humanlayer.dev](https://www.humanlayer.dev/blog/writing-a-good-claude-md);
  HN 748 pts, [46098838](https://news.ycombinator.com/item?id=46098838))
- HN `nico`: *"The more information you have in the file that's not universally applicable to
  the tasks you have it working on, the more likely it is that Claude will ignore your
  instructions."* Counter-report from `sothatsit`: *"Claude rarely actually reads the other
  documentation files I point it to"*, and `Sammi`: *"I don't trust any agent to follow
  document references consistently."* — the strongest community evidence **against** the
  pointer/index pattern.
- `kajman`, 2026-04-28: *"The goal is a small file, so just ruthlessly cut anything that
  doesn't have high value across the entire project."* `chickensong` on `/init`-style
  generation: *"Every attempt I've made to quickly get an LLM to one-shot an AGENTS file has
  been too verbose in all the wrong areas."*
  ([HN 47938417](https://news.ycombinator.com/item?id=47938417), 142 pts)
- **Simon Willison's own position is near-nihilist**, 2026-04-28: *"Most of my projects are
  without an AGENTS.md/CLAUDE.md at the moment. I've found that if the project itself is in
  good shape — clear docs, comprehensive tests — you don't need to tell the coding agent
  much."*

**MEASURED — and this is the finding to lead with.** Gloaguen, Mündler, Müller, Raychev &
Vechev (ETH Zürich / LogicStar), *"Evaluating AGENTS.md: Are Repository-Level Context Files
Helpful for Coding Agents?"*, [arXiv:2602.11988](https://arxiv.org/abs/2602.11988),
v1 2026-02-12, v2 2026-06-23. 138 tasks / 12 repos with developer-committed context files,
plus 300 SWE-bench Lite tasks, 4 coding agents. Headline: *"providing context files does not
generally improve task success rates, while **increasing inference cost by over 20% on
average**."* LLM-generated files: **−0.5% to −2%**. Developer-written: **+2.4%, not
statistically significant**. Crucially: *"**repository overviews**, although popular and
recommended by model providers, **are not helpful**"* — while *"instructions in the context
files are well followed."* Their recommendation: *"Human-written context files should only
include instructions required for coding agents that are **not already present in the
README** (e.g., specific conventions or non-functional requirements)."*
HN reaction ([47034087](https://news.ycombinator.com/item?id=47034087)) split between
`prodigycorp` (*"4% improvement is massive!"*) and `imiric`: *"**Many of the practices in
this field are mostly based on feelings and wishful thinking, rather than any demonstrable
benefit.**"*

**MEASURED, counter-evidence for a well-built index.** Vercel (Jude Gao), 2026-01-27:
baseline no-docs **53%**, skill default **53%**, skill with explicit instructions **79%**,
*"A compressed 8KB docs index embedded directly in `AGENTS.md` achieved a **100% pass
rate**"* — after compressing ~40KB → 8KB.
([vercel.com/blog](https://vercel.com/blog/agents-md-outperforms-skills-in-our-agent-evals);
HN 524 pts, [46809708](https://news.ycombinator.com/item?id=46809708)). Caveat raised
in-thread by `alex_metacraft`: the comparison conflates "always-in-context, high token
count" with "lazy-loaded with a decision point."

**MEASURED, adherence vs instruction count.** *IFScale*
([arXiv:2507.11538](https://arxiv.org/abs/2507.11538), 2025-07-15): 500 keyword
instructions, 20 models — **the best frontier model reaches only 68% accuracy at 500
instructions**, with a measured **bias toward earlier instructions** (rules at the bottom of
a long file die first). *"When Instructions Multiply"*
([arXiv:2509.21051](https://arxiv.org/abs/2509.21051), 2025-09-25): *"Performance
consistently degrades as the number of instructions increases"* — a logistic regression **on
instruction count alone** predicts performance within ~10%. Chroma's *Context Rot*
([trychroma.com](https://www.trychroma.com/research/context-rot), 2025-07-14, 18 models):
all models degrade monotonically with input length *even on trivial tasks*. This is the
empirical backbone for the 200-line rule.

Birgitta Böckeler (Thoughtworks), *Context Engineering for Coding Agents*, 2026-02-05: build
context *"gradually, and not pump too much stuff in there right from the start"*; prefer
Skills' *"lazy load"*; warns that people who *"copied a lot from a stranger"* have *"low
awareness of what's in your context"*. Her framing: *"there are no unit tests for context
engineering."*

### 8.2 Skills sprawl

- **Startup cost is real and has a documented budget.** *"At startup, only the metadata (name
  and description) from all Skills is pre-loaded"*
  ([Anthropic skills best practices](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices)).
  Claude Code caps the listing: *"if you have many skills, Claude Code shortens descriptions
  to fit the listing's character budget, which can strip the keywords Claude needs to match
  your request. **The budget scales at 1% of the model's context window**"* — per-entry
  truncation at **1,536 characters**. So sprawl doesn't just cost tokens; **it silently
  degrades routing for the skills you actually care about**.
- Documented collision failure: *"If descriptions are vague or overlap, Claude may load the
  wrong skill or miss one that would help."* Body-size guidance: *"Keep SKILL.md body under
  **500 lines**."* Anthropic's own line: *"The context window is a public good."*
- **`/skill-doctor` is exactly the anti-sprawl tool**: *"see what each of your skills costs
  and how often it gets used… It flags skills in the listing that have never been invoked."*
  ⚠️ Docs say ≥v2.1.252, changelog says 2.1.261 — two primary sources disagree.
- **Skills don't fire reliably.** Vercel measured it: *"In 56% of eval cases, **the skill was
  never invoked**. The agent had access to the documentation but didn't use it."*
- Community complaints on index bloat (ANECDOTAL): `gwerbin`, 2026-05-05 — *"Even having too
  many skills can be an issue because the list of skill names and descriptions end up in
  context"*; `Ferret7446`, 2026-08-10 — *"skills also need a double layer of progressive
  discovery"*; `porphyra`, 2026-06-05 — *"when I have too many skills I have to spend time
  toggling checkboxes."*
- **Pro-skills case.** Simon Willison, 2025-10-16: *"each skill only takes up a few dozen
  extra tokens, with the full details only loaded in should the user request"* vs *"GitHub's
  official MCP on its own famously consumes tens of thousands of tokens of context."*
  ([simonwillison.net](https://simonwillison.net/2025/Oct/16/claude-skills/))
- **The decision rule worth quoting.** Anthropic, 2026-06-18: *"**Procedures belong in
  skills. CLAUDE.md is for facts Claude should hold all the time**: build commands, monorepo
  layout, team conventions."*
  ([claude.com/blog](https://claude.com/blog/steering-claude-code-skills-hooks-rules-subagents-and-more))

### 8.3 Hooks that block

- **Mechanics.** PreToolUse `exit 2` or `permissionDecision: "deny"` blocks the call;
  PostToolUse exit 2 *cannot* block (the tool already ran); Stop hooks block turn-end.
  *"Hooks can tighten restrictions but not loosen them"*. Non-determinism trap, from the
  docs: *"Since hooks run in parallel, the order is non-deterministic. **Avoid having more
  than one hook modify the same tool's input.**"*
  ([hooks docs](https://code.claude.com/docs/en/hooks))
- **"Hooks fighting the agent" is real enough that Anthropic shipped a circuit breaker.**
  *"Claude Code overrides a Stop hook after it **blocks eight times in a row** without
  progress"*, plus a cap after three consecutive `continue: true`. Live example: issue
  [#92242](https://github.com/anthropics/claude-code/issues/92242) (open, 2026-09-04) — a
  stop hook re-firing 50+ times with the same message.
- **Auto-format hooks vs the agent's read state** is the most-reported friction: issues
  [#71585](https://github.com/anthropics/claude-code/issues/71585) (2026-06-26, "file
  modified since read" conflating formatter writes with human edits),
  [#76361](https://github.com/anthropics/claude-code/issues/76361) (2026-07-10, *"231 failed
  Edit calls"*), [#87056](https://github.com/anthropics/claude-code/issues/87056)
  (2026-08-16, editor autosaves silently overwriting agent edits).
- **Flaky/silent hooks are worse than none.** Open issues #68970 (Windows hooks fire at ~1.2%
  rate), #75071 (one schema-invalid matcher **silently disables ALL** settings.json hooks),
  #84439, #87657, #92489. Recurring sentiment: a silently disabled guard is worse than an
  absent one.
- **The positive case is the vendor's own strongest claim.** *"An instruction like 'never
  edit `.env`' in CLAUDE.md or a skill is a **request, not a guarantee**. A `PreToolUse` hook
  that blocks the edit is enforcement."* And: *"**The model choosing to run a formatter is
  different from the formatter running automatically.**"* Practitioner corroboration, HN
  `unshavedyak`, 2025-08-08: memory-file instructions like "ALWAYS format and lint" gave only
  a *"70% success rate"* before deterministic enforcement.
- **Hooks vs CI — no vendor guidance exists**; the practitioner consensus is a latency split.
  HN `bobjordan`, Jan 2026: *"Linters: Run in pre-commit hooks. Agent writes code → instant
  feedback → fix → iterate in seconds"* vs *"Tests: Run in CI. Commit → push → wait
  minutes/hours."* `cadamsdotcom`, Oct 2025: *"A 100 line script finishes in milliseconds and
  burns no tokens."*
- **Perf discipline**: default command-hook timeout 10 minutes (30s for `UserPromptSubmit`);
  `async: true` runs non-blocking but *"can't block anything"*; and *"**Hook output lands in
  context.**"* — so a chatty formatter hook is a context tax.

### 8.4 Over-engineered agent workflows

**The vendor walked its own multi-agent hype back.** Anthropic's June 2025 multi-agent post
measured *"multi-agent systems use about **15× more tokens** than chats"* and already carved
coding out: *"most coding tasks involve fewer truly parallelizable tasks than research, and
LLM agents are not yet great at coordinating and delegating to other agents in real time"*
([anthropic.com/engineering](https://www.anthropic.com/engineering/multi-agent-research-system),
2025-06-13). By 2026-01-23 the framing had hardened: *"Multi-agent implementations typically
use **3–10× more tokens** than single-agent approaches"*; *"Every additional agent represents
another potential point of failure"*; and — naming the BMAD/Spec-Kit shape exactly — *"Teams
build elaborate multi-agent systems with separate agents for planning, execution, review, and
iteration, only to discover that they suffered from **lost context at each handoff**."*
([claude.com/blog](https://claude.com/blog/building-multi-agent-systems-when-and-how-to-use-them))

Anthropic's 2026-08-13 research post *Patterns and problems in emerging multi-agent systems*
documents conformity collapse (18 of 30 agents independently creating a branch named
`mvp-game-loop`), and hidden-profile group accuracy of **17–36% vs near-100% individually**.
Top HN comment ([49316271](https://news.ycombinator.com/item?id=49316271), 200 pts):
*"A single agent having all relevant information consistently scores significantly higher
than groups with partial information."*

**Cognition, 2025-06-12, "Don't Build Multi-Agents"**
([cognition.com/blog](https://cognition.com/blog/dont-build-multi-agents)) — Walden Yan's two
principles: *"Share context, and share full agent traces, not just individual messages"* and
*"Actions carry implicit decisions, and conflicting decisions carry bad results."*
Recommendation: start single-threaded and linear.

**MEASURED refutation.** Tran & Kiela, [arXiv:2604.02460](https://arxiv.org/abs/2604.02460)
(2026-04-02): under equal thinking-token budgets, *"single-agent systems (SAS) can match or
outperform MAS"* — *"many reported advantages of multi-agent systems are better explained by
unaccounted computation and context effects."* Also
[arXiv:2606.30524](https://arxiv.org/abs/2606.30524) (2026-06-29): a single-agent pipeline
matched multi-agent quality while **cutting tokens 86% and running 2× faster**.

**Spec-driven bloat.** François Zaninotto, *"Spec-Driven Development: The Waterfall Strikes
Back"*, 2025-11-12
([marmelab.com](https://marmelab.com/blog/2025/11/12/spec-driven-development-waterfall-strikes-back.html);
HN 225 pts, [45935763](https://news.ycombinator.com/item?id=45935763)) — a trivial "display
the current date" feature produced **8 files and 1,300 lines of text** via GitHub spec-kit.
*"SDD is a step in the wrong direction… spending 80% of your time reading instead of
thinking."* On BMAD, HN `taffydavid`, 2026-05-03: *"I just spent a week training up in spec
driven development through bmad, which was awful… **unnecessary ceremony around the specs**,
generating fields of spec documents which presumably fill up the context window quickly."*
Worth noting: BMAD has essentially **zero organic HN traction** — every submission sits at
1–4 points with 0 comments.

**"Bitter lesson" framing exists, verbatim.** Gregor Zunic (Browser Use), *"The Bitter Lesson
of Agent Frameworks"*, 2026-01-16: *"**Every abstraction is a liability. Every 'helper' is a
failure point.**"* / *"The less you build, the more it works."* The 2026-04-19 follow-up is
MEASURED: their harness went from *"thousands of lines of element extractors, DOM indexers,
click wrappers"* to **~600 lines across four files**.

**Swarms and tmux coordinators.** Simon Willison's standing challenge, 2026-01-27
([46786824](https://news.ycombinator.com/item?id=46786824)): *"**No, I'm still waiting to see
concrete evidence that the 'swarms of parallel agents' thing is worthwhile.**"* The datapoint
that undercut the pro-swarm case the same week: `embedding-shapes`, *"One Human + One Agent =
One Browser From Scratch in 20K LOC"* — *"**One human using one agent seems far more
effective than one human using thousands of agents.**"* On the tmux/parallel-spec thread
([47218318](https://news.ycombinator.com/item?id=47218318), 189 pts, 2026-03-02): `ramoz` —
*"my problem with these extensive self-orchestrated multi-agent / spec modes is the type of
drift and rot… that a lot of the time end up in merge conflicts"*; `medi8r` — *"it looks
cognitively like being a pilot landing a plane _all day long_, and not what I signed up
for."* `pydry`, 2026-04-14: *"**Multi agent orchestration seems like a scam to manufacture
demand for tokens.**"* And `dakolli` coined the label for the genre, 2026-07-20:
*"**meta-agentic engineering** — people building tools for agents to use agents."*

**What practitioners abandoned.** Armin Ronacher, *"Agentic Coding Things That Didn't Work"*,
2025-07-30 ([lucumr.pocoo.org](https://lucumr.pocoo.org/2025/7/30/things-that-didnt-work/)):
on subagents *"I haven't found it easier to use"*; on parallelism *"Tasks that don't
parallelize well — especially those mixing reads and writes — create chaos"*; and the core
finding — *"**simply taking time to talk to the machine and give clear instructions
outperforms elaborate pre-written prompts**."* Plus: *"it encourages mental disengagement.
When you stop thinking like an engineer, quality drops."* On MCP (2025-08-18): *"the more
tools you have, the more you're contributing to context rot."*

### 8.5 What people say DOES work

- **A machine-checkable success signal, above everything else.** Anthropic's best-practices
  page leads with it: *"Claude stops when the work looks done. Without a check it can run,
  'looks done' is the only signal available, and **you become the verification loop**."*
  Escalation ladder: in-prompt check → `/goal` condition → Stop hook as a deterministic gate
  → adversarial review subagent. Their listed failure mode: *"The trust-then-verify gap… **If
  you can't verify it, don't ship it.**"*
- **Simon Willison's "vibe engineering"** (2025-10-07,
  [simonwillison.net](https://simonwillison.net/2025/Oct/7/vibe-engineering/)) is the
  canonical list: *"Automated testing. If your project has a robust, comprehensive and stable
  test suite agentic coding tools can **fly** with it."* plus planning in advance,
  comprehensive documentation, *"Good version control habits"*, CI/formatting/linting
  automation, code review culture, really good manual QA, strong research skills. Thesis:
  *"AI tools **amplify existing expertise**."*
- **Design the loop, not the org chart.** Willison, 2025-09-30
  ([designing-agentic-loops](https://simonwillison.net/2025/Sep/30/designing-agentic-loops/)):
  *"My preferred definition of an LLM agent is something that runs tools in a loop to achieve
  a goal. **The art of using them well is to carefully design the tools and loop for them to
  use.**"* His safety line — the **lethal trifecta** (2025-06-16): private data + untrusted
  content + external communication; avoid combining all three.
- **Small threads, frequent resets.** Thorsten Ball, 2025-05-15
  ([ampcode.com/how-i-use-amp](https://ampcode.com/how-i-use-amp)): *"after the context
  window reaches 100k tokens, things start to feel blurry, imprecise"*; *"**a lot of the
  problems that people who are new to working with agents run into can be traced back to them
  not starting new threads often enough.**"*
- **The agent itself is trivially simple — the harness is where people over-build.** Ball,
  2025-04-15: *"**It's an LLM, a loop, and enough tokens.**… less than 400 lines of code."*
  Geoffrey Huntley's Ralph (2025-07-14, [ghuntley.com/ralph](https://ghuntley.com/ralph/)) is
  `while :; do cat PROMPT.md | claude-code ; done` — and the anti-orchestration argument is
  his: *"Consider what microservices would look like if the microservices (agents) themselves
  are non-deterministic — a red hot mess… **Ralph is monolithic.**"* His parallelism rule is
  asymmetric: many subagents for *reads*, one for builds/tests.
- **Simple code wins in agentic contexts.** Ronacher, 2025-06-12: *"**Simple code
  significantly outperforms complex code in agentic contexts**"*; *"Have the agent do 'the
  dumbest possible thing that will work'"*; prefer long descriptive function names over class
  hierarchies.
- **Plan-then-implement, but not always.** Anthropic's own caveat: *"Plan mode is useful, but
  also adds overhead… **If you could describe the diff in one sentence, skip the plan.**"*
- **Fresh-session review**, with a real anti-over-engineering warning attached: *"A fresh
  context improves code review since Claude won't be biased toward code it just wrote."* but
  *"A reviewer prompted to find gaps will usually report some, even when the work is sound…
  **Chasing every finding leads to over-engineering**: extra abstraction layers, defensive
  code, and tests for cases that can't happen."*
- **Git worktrees: useful, but the bottleneck moves to you.** MEASURED support exists — CAID
  ([arXiv:2603.21489](https://arxiv.org/abs/2603.21489), 2026-03-23) found structured
  delegation with **isolated worktrees + test-verified merge** beat single-agent baselines by
  +25.6% on PaperBench, and that naive concurrency fails because agents overwrite each other.
  But the review tax is the recurring complaint. HN `hombre_fatal`
  ([44116872](https://news.ycombinator.com/item?id=44116872)): *"My bottleneck… is reviewing
  their code, QAing it."* `bjackman`: *"This strategy only makes sense if you can trivially
  evaluate each agent's results, which I haven't found to be the case."* Even Willison's
  *pro*-parallel post (2025-10-05) concedes: *"**I can only focus on reviewing and landing one
  significant change at a time.**"*
- **Established-codebase advice** ([Ask HN 46292682](https://news.ycombinator.com/item?id=46292682),
  2025-12-16): break into *"small, testable chunks"*, write tests before implementation,
  **encode conventions into linting rules** rather than prose, review between steps. Risk to
  watch, from `clbrmbr`: *"Really well-written PR messages and clean code that doesn't do the
  right thing"* — review for correctness, not polish.

### 8.6 AI attribution in commits

**⚠️ Correcting a common premise: Anthropic never made the co-author trailer opt-in.** As of
2026-09-07 the docs still read: *"**Default**: unset, so Claude Code adds `Co-Authored-By:
<name> <noreply@anthropic.com>`"* ([settings reference](https://code.claude.com/docs/en/settings-reference)).
What *did* change: `includeCoAuthoredBy` was deprecated in **v2.0.62 (2025-12-09)** in favour
of the `attribution` object, and `attribution.sessionUrl` (v2.1.183, 2026-06-19) drops the
`Claude-Session` link. Issue
[#27083](https://github.com/anthropics/claude-code/issues/27083) ("Co-Authored-By attribution
should be opt-in, not opt-out", 2026-02-20) was **closed as not planned**, as was
[#66602](https://github.com/anthropics/claude-code/issues/66602), which cited US Copyright
Office guidance (88 Fed. Reg. 16190): *"Applicants should not list an AI technology or the
company that provided it as an author or co-author simply because they used it."*

**Scale:** GitHub's commit-search API (2026-09-07) returns **~13.2M** commits matching
`"Co-Authored-By: Claude"` and **~4.9M** matching `"Generated with Claude Code"`.

**The "it's advertising" argument** is dominant. Akseli Lahtinen (KDE), 2026-05-26, *"Stop
advertising in your commits!"*: *"If your tool adds advertisements in your work by default,
it's a bad tool… Disclose your 'AI' tools in a merge request if needed but **leave them out of
the damn commits, those are for technical information and not for advertising**."*
(HN 192 pts, [48283914](https://news.ycombinator.com/item?id=48283914)). In-thread,
`0123456789ABCDE`: *"co-authorship implies ability to hold author rights, which afaik an
algorithm can't do… should i add alembic as a co-author after making some changes to the
database schema?"* And the taxonomy objection from `kuschku`: *"that would usually be marked
as `Generated-By` or `Generator`… Claude is only using 'Co-Authored-By' … to anthropomorphize
the machine."*

**The strongest institutional version is the Linux kernel's.** Christian Brauner, commit
`816d9992d9ed`, 2026-07-01, simplifying the tag to bare `Assisted-by: LLM`: *"The requirement
to identify specific models used in the Assisted-by tag **provides free advertising to
proprietary software companies while adding little or no useful information**."* The kernel's
`coding-assistants.rst` (Sasha Levin, 2025-12-23) is also the cleanest DCO statement: *"**AI
agents MUST NOT add Signed-off-by tags. Only humans can legally certify the Developer
Certificate of Origin.**"*

**Projects that explicitly ban the trailer:** Bitcoin Core `doc/AI_POLICY.md` (commit
`f5d7cc66ec`, 2026-07-24) — *"**Do not include agents as authors or co-authors of your
commits**"*; Apache Airflow AGENTS.md — *"Agents cannot be authors, humans can be, Agents are
assistants"*; VTR, rotki, Microsoft Playwright, dosbox-staging. GitHub code search finds
**~888** files containing "Do not add Co-Authored-By" and **~1,464** containing "Never add
Co-Authored-By" — almost all in `CLAUDE.md`/`AGENTS.md`.

**Projects that require or encourage it:** the ASF's generative-tooling guidance, Halide
(*"you **must** note this… using a `Co-authored-by` trailer"*), NiceGUI, color.js,
containers/ramalama (`Assisted-by:` / `Generated-by:`). The most sensible middle position is
the Arches project's: *"If your AI tool inserts something like that, **please adjust the
language to accurately reflect the AI's role in this particular contribution**."*

**Outright AI-code bans (context for why trailers read as a liability signal):** Gentoo
Council 2024-04-14 — *"expressly forbidden"*; NetBSD — LLM output is *"presumed to be tainted
code"*; QEMU (Berrangé, commit `3d40db0efc22`, 2025-06-24); GNOME/Loupe (2025-02-26); Servo
(*"**No.**"*); Codeberg e.V. (member vote 358–144, 2026-07-22). Debian's GR 2026-002 (425
ballots, concluded 2026-08-28) landed on the *soft* option: *"We **encourage** our
contributors to disclose whether a contribution was made with AI assistance, but do not
require them to do so."* Rust's LLM policy (2026-04-21): *"It's fine to use LLMs to answer
questions, analyze, distill, refine, check, suggest, review. **But not to create.**"*

**The reputational cost is the practical argument for a solo OSS repo.** HN `sph`,
2026-03-30: *"**When I open a repo and I see most commits co-authored by Claude, I can quickly
dismiss the entire project.**"* Indradhanush Gupta, 2026-08-30: *"A carpenter may sign their
work. But they don't add the name of the saw they used to cut wood, do they?… **If a piece of
work is under my name, I own it. Fully.**"* Also concrete tooling breakage: GitHub renders
co-authored commits as two authors and adds Claude to the repo's contributor graph
([community #197389](https://github.com/orgs/community/discussions/197389), 2026-05-30).
The counter-argument, from `panny`: *"You need to clearly define what commits are AI authored
or you are conspiring to commit copyfraud."*

⚠️ **Enforcement caveat:** the trailer is partly model-generated, not purely config-driven —
several open issues report `includeCoAuthoredBy: false` and even CLAUDE.md prohibitions being
ignored ([#7543](https://github.com/anthropics/claude-code/issues/7543),
[#45137](https://github.com/anthropics/claude-code/issues/45137),
[#91546](https://github.com/anthropics/claude-code/issues/91546) — *"~318 commits"* affected
across six repos, 2026-09-02). **A `commit-msg` git hook is the only reliable block**, and
note issue #26580: a commit-msg hook rejecting the trailer causes a silent `exit 1`.

### 8.7 2026 data bearing on how much process is worth it

- **METR RCT (MEASURED, the anchor).** 2025-07-10: 16 experienced OSS devs, 246 issues on
  their own mature repos (avg 22k+ stars) — **AI-allowed tasks took 19% LONGER** (CI +2% to
  +39%), while the same devs *forecast −24%* and *believed afterwards −20%*.
  ([metr.org](https://metr.org/blog/2025-07-10-early-2025-ai-experienced-os-dev-study/),
  [arXiv:2507.09089](https://arxiv.org/abs/2507.09089); HN 775 pts)
- **METR's 2026 follow-up exists and was ABANDONED.** 2026-02-24: 57 devs, 143 repos, 800+
  tasks. Preliminary and explicitly disclaimed: returning devs −18%, new devs −4%, **both CIs
  crossing zero**. METR's own verdict: the data *"give[s] an unreliable signal."*
  ⚠️ **The "37-point swing from −19% to +18%" J-curve circulating in consultancy blogs is a
  misreading — do not repeat it.** No third-party replication of the original RCT exists.
- **METR self-report survey (SELF-REPORTED).** 2026-05-11, n=349: median **3× self-reported
  speed**. METR's own caveat: prior work found people **overestimated AI's time effect by 40
  percentage points on average**.
- **DORA 2025 (MEASURED survey, n≈5,000, 2025-09-24).** 90% use AI at work; **>80%
  self-report higher productivity**; **30% report little or no trust** in AI output. The
  finding that matters: *"AI's primary role in software development is that of an
  **amplifier**. It magnifies the strengths of high-performing organizations and the
  dysfunctions of struggling ones."* **The negative relationship with delivery instability
  persists.** Two of the seven AI Capabilities are directly relevant to a solo repo: **strong
  version control practices** and **working in small batches**.
  ([dora.dev/dora-report-2025](https://dora.dev/dora-report-2025/))
- **DORA 2026 ROI report (MODELED, not measured), 2026-05-11:** first-year ROI 39%; **change
  failure rate 5% → 6%** post-adoption; gains of **35–40% on greenfield but ≤10% on legacy
  code**; a named **"verification tax"** J-curve. ⚠️ There is **no full "State of AI-assisted
  Software Development 2026"** as of 2026-09-07.
- **Stack Overflow 2025 (MEASURED survey, n=49,000+, 2025-07-29).** **46% actively distrust AI
  accuracy vs 33% who trust**; only **3% "highly trust"**. Top frustration: **66% cite "AI
  solutions that are almost right, but not quite"**. On agents: **52% don't use agents or
  prefer copilot mode**. ⚠️ **The 2026 survey opened 2026-06-23 and results are not
  published** — third-party "2026 survey" articles recycle 2025 numbers.
- **GitClear (MEASURED repo telemetry, 211M changed lines, Feb 2025).** 2024 was *"the first
  year GitClear has ever measured where the number of 'Copy/Pasted' lines exceeded the count
  of 'Moved' lines"*; moved (refactoring) fell from 25% of changed lines in 2021 to 9.5% in
  2024; commits containing a duplicate block went **0.45% (2022) → 6.66% (2024)**.
  ⚠️ Their 2026 numbers could not be fetched (gitclear.com 403s).
- **LinearB (MEASURED telemetry, millions of PRs, 2026-08-14).** The single most
  decision-relevant number here: **"yield rate"** — share of merged code that survives — is
  **high-80s to low-90s % for human-authored code, drops 2–3 points for AI-assisted-but-
  human-controlled, and collapses to ~30% for fully autonomous agent output**. AI code review
  recovers 2–3 points.
  ([linearb.io](https://linearb.io/blog/elite-engineering-teams-double-pr-output-ai-benchmarks))
- **Anthropic's own C-compiler case study (MEASURED, n=1, 2026-02-05)**: 16 agents, ~2,000
  sessions, ~2 weeks, 100k LOC, **~$20,000 API cost** — failure mode: *"every agent would hit
  the same bug, fix that bug, and then **overwrite each other's changes**."* Their lesson:
  *"it's important that the task verifier is nearly perfect."*
- **Benchmark hygiene warning** ([anthropic.com/engineering](https://www.anthropic.com/engineering/infrastructure-noise),
  2026-02-05): a **6-point Terminal-Bench gap** attributable purely to infrastructure
  resourcing. Their rule: *be skeptical of leaderboard gaps under 3 percentage points.*

**Pattern to name explicitly:** every *measured* result (METR RCT, GitClear, DORA
instability, LinearB yield, IFScale, context rot) is more pessimistic than every
*self-reported* result (METR survey 3×, DORA >80% feel faster).

### What this means for ktags

The sentiment evidence points one way: **cap the prose, spend the effort on verification.**
Concretely — `AGENTS.md` at ~100 lines with **no architecture/overview section** (the ETH
study says overviews cost >20% inference for no gain); conventions encoded as `golangci-lint`
rules and `go vet` analyzers rather than prose bullets; one deterministic Stop-hook gate
rather than a hook farm; **no planner/coder/reviewer persona chain and no tmux swarm** — use
subagents only for read-heavy investigation and a single fresh-context review of the diff;
strip AI trailers with both the `attribution` setting *and* a `commit-msg` hook, because the
setting alone has documented failures. And keep a skeptic's prior on your own speed-up.

### Flagged as unverified in this section

No METR follow-up result should be cited as a measured speed-up. No DORA "State of AI-assisted
Software Development 2026" and no Stack Overflow 2026 results exist as of 2026-09-07.
GitClear's 2026 numbers could not be fetched (403). LinearB's "AI code waits 4.6× longer for
first review" could not be traced to a LinearB primary page. `/skill-doctor` version conflicts
between docs (≥2.1.252) and changelog (2.1.261). The "~100 tokens per skill" figure has no
Anthropic primary source. NetBSD's and Fedora's policy dates could not be confirmed.
**Reddit (r/ClaudeAI, r/ExperiencedDevs) was inaccessible from the research environment (403),
so no Reddit sentiment is represented — a real gap.** HN comment quotes were retrieved via the
Algolia API; individual comment permalinks were not resolved one-by-one.

---

## 9. Recommended baseline for ktags

Design principle, derived from every measured result in §6 and §8:
**instructions are suggestions; hooks, worktrees, required checks and rulesets are the
contract.** Keep the prose small, spend the effort on things that fail red.

### 9.1 Files and directories to create

**Tool-agnostic core**

| Path | Purpose |
|---|---|
| `AGENTS.md` | The single entry point, **~100 lines max, no architecture section**: build/test/lint commands, the handful of conventions agents keep getting wrong, "read `docs/adr/` before architectural choices", "no AI attribution in commits, ever". |
| `CLAUDE.md` | Two lines: `@AGENTS.md` plus any Claude-only note. The only supported way to make Claude Code read `AGENTS.md`. |
| `.agents/skills/<name>/SKILL.md` | Canonical skills, **spec frontmatter only** (`name`, `description`, optional `license`/`compatibility`/`metadata`). Read natively by Codex, OpenCode and Gemini CLI. Start with 2–4. |
| `.mcp.json` | Project MCP servers for Claude Code (`{"mcpServers": …}`). Only if you actually need one. |
| `docs/adr/NNNN-slug.md` | MADR 4.0 ADRs, agent-shaped: MUST/MUST NOT, `Consequences`, and a verification command where one exists. The 12 decisions already listed in `docs/STATUS.md` are ADR-001..012. |
| `docs/plans/YYYY-MM-DD-slug.md` | Self-contained implementation plans. Plan mode does not persist in Claude Code, Codex or OpenCode — this file is what survives `/clear`, compaction and a switch of agent. Archive under `docs/plans/archive/`. |
| `lefthook.yml` | `pre-commit`: `golangci-lint fmt`/`run`, `go mod tidy -diff`, `go test ./...`. `commit-msg`: the AI-trailer guard. |
| `scripts/commit-msg-guard.sh` | Five-line `grep -Ei` that blocks `Co-Authored-By: …Claude/Codex`, `Claude-Session:`, `🤖 Generated with`. No linter has this rule off the shelf. |
| `.golangci.yml` | `version: "2"`, `linters.default: standard` + the CLI/TUI set, `formatters: [gofumpt, gci]`, `forbidigo` banning `fmt.Print*` in TUI packages. |
| `.testcoverage.yml` | Coverage floor for `vladopajic/go-test-coverage` (start `total: 70`, `package: 60`). |
| `.goreleaser.yaml` | `version: 2`, `gomod: {proxy: true}`, `archives.formats: [tar.gz]`, `homebrew_casks:` (not the deprecated `brews:`), `sboms:`, `signs:` with cosign v3 `--bundle`. |
| `Makefile` | One place both humans, hooks and CI call: `make fmt lint test race cover vuln`. Prevents drift between the three. |

**Claude-Code-specific (committed)**

| Path | Purpose |
|---|---|
| `.claude/settings.json` | `permissions.deny` (secrets, `Bash(curl *)`), `permissions.defaultMode: "plan"`, `attribution: {commit:"", pr:"", sessionUrl:false}`, and the hooks below. Deny rules apply **before** workspace trust; allow rules don't. |
| `.claude/skills` → `../.agents/skills` | Committed symlink. **Verified working on v2.1.263** — gives Claude Code the same skills as every other agent with zero duplication. |
| `.claude/agents/reviewer.md` | The independent reviewer. A non-fork subagent already starts with no conversation history and no parent auto-memory. Give it `tools: Read, Grep, Glob, Bash` so it can *run* the tests, and prompt it with the diff only — never "review your own work" (label bias, §6.8). |
| `.claude/rules/tui.md` | `paths: ["internal/tui/**"]` — Bubble Tea conventions loaded only when those files are touched. Zero cost otherwise; keeps `AGENTS.md` short. |
| `.claude/hooks/gate.sh` | `Stop` hook: `go build ./... && go vet ./... && go test ./...`. Exit 2 keeps the turn going. This is the "looks done → actually done" gate. |
| `.worktreeinclude` | Copies gitignored local config into each worktree. |

**Other agents (committed)**

| Path | Purpose |
|---|---|
| `.codex/config.toml` | Project overrides: `approval_policy`, `sandbox_mode`, `project_doc_max_bytes`. Applies only in a *trusted* project and cannot override provider/auth keys — safe to commit. |
| `opencode.json` | `$schema`, `permission: {edit: "ask", bash: "ask"}` (OpenCode defaults to allowing everything), `instructions: ["AGENTS.md", "docs/adr/*.md"]`. |

**GitHub**

| Path | Purpose |
|---|---|
| `.github/ISSUE_TEMPLATE/1-bug.yml` | Issue form with `type: Bug` — auto-stamps the org issue type, so `gh issue list --type Bug` works with zero triage. Use the `upload` element to demand a terminal capture. |
| `.github/ISSUE_TEMPLATE/2-feature.yml`, `config.yml` | Feature form + `blank_issues_enabled: false`. |
| `.github/pull_request_template.md` | Checklist: linked issue, plan file, tests, no AI trailers. |
| `.github/workflows/ci.yml` | `build`, `lint`, `test`, `race`, `cover`, `vuln` jobs **plus one `ci-required` aggregator** with `if: always()` and `needs: [...]` — the only required check. |
| `.github/workflows/release.yml` | release-please (or `svu`) **and** GoReleaser chained as jobs in **one** workflow — a `GITHUB_TOKEN` tag push does not trigger a separate workflow. |
| `.github/dependabot.yml` | `gomod` + `github-actions`, grouped minor/patch. |
| `ruleset.json` | Exported ruleset, applied with `gh api -X POST repos/:owner/ktags/rulesets --input ruleset.json`. Rulesets-as-code in `.github/` does not exist. |
| `CODEOWNERS` | Two lines, `* @you`. Useful only for auto-requesting review on outside PRs. **Do not** enable the Code Owners ruleset rule. |

### 9.2 The GitHub configuration, concretely

1. **Create a free GitHub organization and put `ktags` in it before opening any issue.**
   Issue types and issue fields are org-only, and transferring *out* of an org later strips
   issue types from every issue.
2. **One ruleset on `main`**: require a PR with **approvals = 0**; require exactly one status
   check, `ci-required`, **pinned to the GitHub Actions app**; block force pushes; restrict
   deletions; require linear history; allowed merge method = **squash only**; squash commit
   message = **"Pull request title"**.
3. **Bypass actor: you, "For pull requests only".** `main` becomes unpushable even by you.
4. **Turn off** "Allow GitHub Actions to create and approve pull requests".
5. Agents get a fine-grained PAT scoped to `ktags` with `contents: read` +
   `pull-requests: write`, no admin, and merge only via `gh pr merge --auto --squash`.
   **Test `--auto` against the ruleset early** — there is a credible unresolved report of it
   silently never firing with rulesets.
6. **State model**: issue **type** for kind, a Projects v2 single-select **`Status`** field for
   state, **sub-issues** for hierarchy, labels only for orthogonal tags (`area/tui`,
   `area/ansible`, `good first issue`).

### 9.3 The loop

```
gh issue develop <n> --checkout --worktree .worktrees/<n>   # issue → branch → isolated tree
cd .worktrees/<n> && claude                                  # or: claude --worktree <n>
  └─ plan  → docs/plans/YYYY-MM-DD-<slug>.md  (committed)
  └─ implement, Stop hook gates on build+vet+test
codex review --base main            # different model, fresh process, can run the code
gh pr create --fill                 # ci-required must go green; you read the diff
gh pr merge --auto --squash
```

Compact status back into the plan file after each verified phase and start the next phase in a
**fresh session** (ACE-FCA). Archive the plan when the PR merges.

### 9.4 Sequencing — do these in order

1. `AGENTS.md` + `CLAUDE.md` shim + `.claude/settings.json` (deny + attribution) +
   `commit-msg` hook. **Half a day, and it is the whole "no AI attribution" guarantee.**
2. Go module skeleton, `Makefile`, `.golangci.yml`, `lefthook.yml`, `ci.yml` with the
   `ci-required` aggregator.
3. Create the org, move the repo, apply `ruleset.json`, add issue forms.
4. Turn the 12 decisions in `docs/STATUS.md` into `docs/adr/0001..0012`.
5. Only then: skills (2–4), `.claude/agents/reviewer.md`, `.claude/rules/tui.md`.
6. Later, if ever: release-please, mutation testing, Projects v2 automation.

**Deliberately not recommended**: Spec Kit, Kiro, BMAD, OpenSpec (for now); agent teams; a
tmux coordinator; husky; a custom handoff-schema guard script; `slsa-github-generator`;
GoReleaser Pro; cloud coding agents for *authoring*; labels as a state machine; commitlint;
CODEOWNERS-based review requirements; a required approval count above 0.

---

## 10. What in the reference design is now redundant or replaceable

The reference system was a sensible design for its time. Most of its *ideas* survive; most
of its *implementations* have been absorbed into the tools.

| Reference-design element | Verdict in Sept 2026 | Replacement |
|---|---|---|
| **`AGENTS.md` as single entry, `CLAUDE.md` = `@AGENTS.md`** | ✅ **Keep — still the only portable answer.** Claude Code still does not read `AGENTS.md` natively; its own docs prescribe exactly this shim | unchanged (§1) |
| Long `AGENTS.md` carrying architecture/overview | ❌ **Cut.** MEASURED: repo overviews *"are not helpful"* and context files raise inference cost **>20%** ([arXiv:2602.11988](https://arxiv.org/abs/2602.11988)); Anthropic targets **<200 lines** | ~100 lines of conventions only; `.claude/rules/` with `paths:` for file-scoped rules (§1, §8.1) |
| **Skills in `.agents/skills/<name>/SKILL.md`** | ✅ **Keep — this is now the converged location.** Codex's primary project dir; OpenCode reads it; Gemini CLI *prefers it over its own* | add a committed `.claude/skills -> ../.agents/skills` symlink (verified working, §2) |
| Many skills | ⚠️ **Cap them.** The listing is re-sent every turn at 1% of context and silently truncates descriptions when crowded, degrading routing | 2–4 skills; `/skill-doctor` to prune (§2, §8.2) |
| **Guard script with schema-validated handoff metadata between phases** | 🔻 **Redundant.** Both CLIs now emit validated structured output natively | `claude -p --output-format json --json-schema '<schema>'` → `.structured_output`; `codex exec --output-schema <file> -o out.json` (§3, §4) |
| **Independent review in a fresh session** | ✅ **Keep the idea, drop the machinery.** A non-fork Claude Code subagent already starts with no conversation history and no parent auto-memory | `.claude/agents/reviewer.md`, or the native `codex review --base main` (§3, §4). Guard against reviewer over-reporting (§8.5) |
| **Coordinator spawning phases as separate tmux sessions** | ❌ **Drop.** Anthropic ships this natively (`teammateMode: "tmux"`), but it is *experimental*, and every measured result argues against multi-agent orchestration at solo scale: **3–10× tokens**, lost context at each handoff, single-agent matching MAS at equal budget | sequential sessions + `git worktree`; `gh issue develop <n> --checkout --worktree <path>` (§3, §5.8, §8.4) |
| **husky hooks** | 🔻 **Replace.** husky needs a Node toolchain a Go repo otherwise wouldn't have | a Go-native hooks manager, backed by the same checks re-run in CI (§6) |
| **GitLab labels as a state machine** | ❌ **Drop the state-machine part.** Labels are multi-valued and nothing prevents contradictory states | issue **type** for kind, a **Projects v2 single-select `Status`** for state, **sub-issues** for hierarchy, labels only for orthogonal tags (§5.1) |
| **"No AI attribution in commits" as a written rule** | ⚠️ **Keep the rule, add teeth.** A rule in `AGENTS.md` is a request, not a guarantee — and there are documented cases of the setting *and* the CLAUDE.md prohibition being ignored | `attribution: {commit:"", pr:"", sessionUrl:false}` in committed `.claude/settings.json` **plus** a `commit-msg` hook **plus** a CI check (§3, §6, §8.6) |
| Bespoke sandboxing / "don't touch main" conventions | 🔻 **Redundant.** Worktree isolation is enforced by the client and *cannot be turned off*: it blocks edits into the main checkout, commands whose cwd resolves there, and git redirects via `-C`/`--git-dir`/`GIT_DIR` | `claude --worktree`, `isolation: worktree` on a subagent, `codex sandbox` (§3, §4) |
| Custom MCP wiring per tool | ⚠️ **Still unavoidable.** `{"mcpServers": …}` is a shared *shape*, not a shared *file*: Claude Code `.mcp.json`, Codex `[mcp_servers]` in `config.toml`, OpenCode `mcp` in `opencode.json` | write three, or generate three from one source (§4) |

---

## 11. Consolidated caveats

**Method limits of this report.** The session's web-search budget (200 queries) was exhausted
partway through; later sourcing leans on direct fetches of primary documentation, the GitHub
REST API via `gh`, `proxy.golang.org`, and the arXiv API. Reddit (r/ClaudeAI,
r/ExperiencedDevs) was unreachable (403), so Reddit sentiment is absent from §8 — a real gap.
Several tool versions and CLI behaviours were verified **directly on this machine** rather
than from docs, and those are marked as such in-line.

**The load-bearing claims that rest on thin evidence:**

- The "fresh session review helps" result is **one unreplicated n=30 synthetic study**
  ([arXiv:2603.12123](https://arxiv.org/abs/2603.12123)). No study measures fresh-session
  review on *real* AI-written PRs. The cross-model asymmetry result is stronger and points
  somewhere slightly different: use a **different** model, not merely a fresh one.
- All AI-code-review precision/recall claims from vendors are unaudited; the independent
  numbers (3.5–5.1% precision, 56.3% rejection) are far worse than the marketing.
- "Solo maintainers cannot approve their own PR" is well-established behaviour with **no
  quotable GitHub docs sentence**. Verify before relying on approvals = 0.
- The auto-merge-fails-with-rulesets report is community-sourced only. **Test it early.**
- No canonical reference implementation of release-please + GoReleaser was found; the
  composition is sound but you assemble it yourself.
- Whether GitHub Copilot's new PR approval works on *self-authored* PRs is undocumented — which
  is precisely the case that would matter to a solo maintainer.
- Governance claims (Linux Foundation / Agentic AI Foundation stewardship of AGENTS.md and the
  Agent Skills spec) come from secondary sources only.

**One-line honesty check on the whole exercise:** the only randomised trial of experienced
developers working on their own mature repos found them **19% slower** with AI while believing
they were 20% faster, and METR's 2026 follow-up was withdrawn as unreliable. Every process
recommendation here should be treated as a hypothesis to test against something measurable in
`ktags` itself — merge-to-revert rate, or churn on files an agent touched — not as settled
practice.

---

## 12. Gap analysis against what is already in the repo

While this research ran, the working system was set up in `ktags` (commits `8961397
chore(workflow): agent working system` and `551ae00`). It **independently converges with
almost every recommendation above**, which is a good sign for both. What exists:

`AGENTS.md` (**88 lines** — inside the 200-line ceiling), `CLAUDE.md` = `@AGENTS.md` + a short
Claude-only block, `.agents/skills/{task,review}/SKILL.md` exposed to Claude Code through a
`.claude/skills` symlink, `.claude/agents/reviewer.md` on a **different model** (`fable`
reviewing `opus` work — which is exactly the cross-model asymmetry §6.8 recommends, not merely
a fresh session), `.claude/settings.json` with `attribution` + `permissions` + `hooks`,
`.githooks/{commit-msg,pre-push}` driven by `core.hooksPath` via `make hooks`,
`scripts/guard.sh`, `.codex/config.toml`, `opencode.json` with `instructions: ["AGENTS.md"]`,
`.golangci.yml`, issue forms and a PR template, and `.github/workflows/ci.yml` with
`guard` / `go` / `lint` jobs.

Two deliberate divergences from this report that are **fine as they are**:

- **Raw `.githooks/` + `core.hooksPath` instead of lefthook.** Zero dependencies, and the
  commit-msg hook already documents the right mental model: *"CI re-runs the same check on
  every PR, so bypassing with `--no-verify` only delays the failure."* Only adopt lefthook if
  you start wanting parallel jobs, globs and `stage_fixed`.
- **`govulncheck@latest` rather than a pin** — acceptable for a security scanner, where you
  usually *want* the newest database and detection logic.

> **Baseline note:** this table was written against commit `551ae00` on 2026-09-07. The
> working tree was being edited in parallel while the report was assembled — a `ci-required`
> job, `.github/dependabot.yml` and `.claude/hooks/` had already appeared uncommitted by the
> time of writing. Re-check each row against the current tree before acting on it.

Concrete gaps worth closing, all verified in §5 and §6:

| Gap | Fix |
|---|---|
| `actions/checkout@v4` | **v7.0.1 (2026-07-20)**; v7 also blocks fork checkout under `pull_request_target`/`workflow_run` |
| `actions/setup-go@v5` | **v7.0.0 (2026-07-16)**; `cache: true` is the default since v6.3.0, so the explicit `with: cache` line can go |
| `golangci/golangci-lint-action@v8` | **v9.3.0 (2026-06-29)** — v9 moved to node24; the pinned `version: v2.13.2` is already current |
| Three separate required checks (`guard`, `go`, `lint`) | Add one **`ci-required`** aggregator with `if: always()` and `needs: [guard, go, lint]`. GitHub's own docs warn that a *workflow* skipped by a path filter leaves its check **Pending and blocks merging forever**; one stable aggregator name removes that whole class of deadlock |
| No coverage floor | `vladopajic/go-test-coverage@v2` (v2.19.0) with `.testcoverage.yml`; merge a `go build -cover` + `GOCOVERDIR` PTY run for the TUI path |
| `go test -race` and coverage in one job | Split: race is 2–20× slower and shouldn't gate the fast feedback loop |
| No `go mod tidy -diff` | One line, catches agent-authored `go.mod` drift without mutating the tree |
| No `GOTOOLCHAIN=local` in CI | Stops an agent- or bot-authored `go.mod` bump silently pulling a different toolchain |
| No ruleset / required-check pinning yet | §9.2 — approvals **0**, `ci-required` pinned to the GitHub Actions app, squash-only, you as the sole "for pull requests only" bypass actor |
| Repo is not in an organization | Blocks issue **types** and issue **fields** entirely; a free org fixes it, and moving *out* of an org later destroys issue types |
| `.claude/settings.json` has uncommitted local edits | Additional `git push origin main*` deny rules — commit them or move them to `settings.local.json`; note that **deny rules apply before workspace trust, allow rules do not** |

The remaining items from §9 that are not yet started: `docs/adr/0001..0012` (the twelve
decisions already listed in `docs/STATUS.md`), `docs/plans/`, `.claude/rules/tui.md` with
`paths:` scoping, `.worktreeinclude`, `.goreleaser.yaml`, and `dependabot.yml`.
