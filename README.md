# ktags

Operate customer Kubernetes (RKE2) clusters from your own laptop: a single Go binary with a
full-screen terminal UI and a classic CLI, driving Ansible playbooks underneath.

Status: **design phase**. See `docs/` for research and decisions, `spikes/` for throwaway TUI
prototypes used to pick the UI library.

## Layout

```
docs/research/    dated market research that informed the design (sources linked)
docs/decisions/   architecture decision records (ADRs), numbered, immutable once accepted
spikes/           throwaway prototypes; never imported by the real code
```

## Development

Go 1.27. Each spike has its own `go.mod`; run one with `cd spikes/<name> && go run .`.
