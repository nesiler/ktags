# ktags

Operate customer Kubernetes (RKE2) clusters from your own laptop: a single Go binary with a
full-screen terminal UI and a classic CLI, driving Ansible playbooks underneath.

Status: **design phase**. Start with `docs/STATUS.md`; agents start with `AGENTS.md`.

```
docs/STATUS.md    where the project stands, decisions, next steps
docs/workflow.md  how a task moves from issue to merge
docs/adr/         architecture decision records
docs/research/    dated market research with sources
docs/design/      design notes
spikes/           throwaway TUI prototypes, own modules, never imported
```

Development: Go 1.27, `make hooks` once, `make check` before every push. License: Apache-2.0.
