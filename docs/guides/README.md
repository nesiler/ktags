# Guides — the binding rule files

One short file per topic. They hold the technical decisions that are too small for an ADR and the
conventions every change must follow. A rule here is an acceptance criterion even when an issue does
not repeat it; a reviewer may reject on it.

| File | Scope |
|---|---|
| [development.md](development.md) | Go layout, package boundaries, errors, exit codes, CLI conventions, config and state locations, dependencies |
| [ui.md](ui.md) | Interaction standard for the TUI: screens, states, keys, mouse, palette, confirmations, run view, colours, tests |
| [ansible.md](ansible.md) | Go ↔ Ansible contract: playbook shape, `result.json`, events, variables, pins, lint, what a playbook may never do |
| [testing.md](testing.md) | Test layers, evidence standard, fake-green catalogue, break-see-red, lab discipline |
| [security.md](security.md) | Secrets, SSH and host keys, subprocess environment, audit, confirmations, what never appears anywhere |

How these relate to the rest:

- `docs/adr/` records **why** a big decision was taken; a guide records **how** to work within it.
  When a guide rule needs a rationale longer than two sentences, it becomes an ADR and the guide
  links to it.
- `docs/workflow.md` says how a task moves; guides say what the result must look like.
- Open questions are marked **Open:** with the options; they are not rules yet. Settling one is a
  `type:decision` issue.
- Guides change only through an `area:workflow` issue (`docs/workflow.md §7`), because every task
  in flight is measured against them.
