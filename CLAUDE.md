@AGENTS.md

## Claude Code specifics

- Model policy: the coordinator session runs on `fable`; fresh child sessions for implementation
  and independent review run on `opus`. Never run a dev child on `fable`.
- Prefer `/task <issue>` and `/review <pr>`; both are the shared skills in `.agents/skills/`
  exposed through the `.claude/skills` symlink. `/coordinator` runs the `ready` backlog unattended
  (one issue at a time, fresh tmux session per stage via `scripts/session.sh`).
- Use `--worktree` (or `isolation: worktree` on subagents) when running more than one task at a
  time; never two tasks in one checkout.
