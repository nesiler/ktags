@AGENTS.md

## Claude Code specifics

- Subagents that implement or research use `model: opus`; the `reviewer` subagent uses `fable`.
  Do not spawn `fable` for mechanical work.
- Prefer `/task <issue>` and `/review <pr>`; both are the shared skills in `.agents/skills/`
  exposed through the `.claude/skills` symlink.
- Use `--worktree` (or `isolation: worktree` on subagents) when running more than one task at a
  time; never two tasks in one checkout.
