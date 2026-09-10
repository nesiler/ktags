# Ansible guide — the Go ↔ Ansible contract

Binding for every change under `ansible/` and in the Go runner. The predecessor's contract
(`ops-platform/ansible/README.md`, ADR-003/006/007/008) is carried over with the laptop changes
listed in §7.

## 1. Division of labour

- **ktags decides, Ansible executes.** Go owns: which customer, which playbook, which hosts
  (`--limit`), confirmation, lock, audit, run directory, reading the result. Ansible owns: every
  change on a node, idempotently.
- Go never re-implements a node change over raw SSH. Direct SSH is allowed only for read-only
  probes (reachability, MTU, `rke2 etcd-snapshot ls`, fetching a kubeconfig for the k9s bridge)
  and each such probe is listed in the action registry with `Kind: probe`.
- Ansible never decides policy: no prompts, no `pause`, no reading of the operator's environment.

## 2. Every playbook has the same shape

```
play 1  guard      hosts: all, gather_facts: false, delegate_to localhost, run_once
play 2..n  work    the actual roles, serial where the target requires it
play last  result  writes result.json (also on failure: block/rescue/always)
```

Guard checks: run variables present, `ktags_customer == expected`, `ktags_environment == expected`,
inventory directory name equals the customer, no `LOCK.yml`, run directory exists. Guard writes a
provisional `result.json` (`ok: false`, "playbook did not complete") that the result play
overwrites; Go therefore always finds a result even when every host was unreachable.

Playbooks are one per job: `access`, `preflight`, `install`, `health`, `backup`, `node-remove`,
`upgrade`, `patch`, `decommission`, … Adding a job = adding a playbook and one registry entry.

## 3. Variables in, results out

**In** — `extra_vars.json` written by Go into the run directory (`0600`, never secrets):

| Key | Meaning |
|---|---|
| `ktags_run_id`, `ktags_run_dir` | run identity and the directory `result.json` goes to |
| `ktags_expected_customer`, `ktags_expected_environment` | guard compares with the inventory |
| `ktags_operator`, `ktags_job` | audit and `result.json` fields |
| `ktags_target_hosts` | jobs that act on a subset (access, node add/remove) |
| `ktags_team_ssh_keys` | public keys to install (not secret) |

Secrets reach Ansible only through the mechanism the secrets decision selects (`security.md §2`),
never through `-e`, environment or a plain file that outlives the run.

**Out** — `result.json` (`{ktags_run_dir}/result.json`, written with `delegate_to: localhost`,
`run_once`):

```json
{"playbook":"health","run_id":"…","customer":"acme","ok":true,
 "summary":"2/2 nodes Ready, 0 bad pods, Rancher pong, Longhorn 1 disk Ready",
 "checks":[{"name":"nodes_ready","ok":true,"severity":"fail","detail":"…","runbook":"nodes-not-ready"}],
 "data":{"nodes":{"acme-srv-1":{"access_ip":"…","hostkey":"ssh-ed25519 …"}}}}
```

Rules: metadata only (names, IPs, versions, counts, states). No pod logs, no secrets, no customer
business data. Each check has `severity` (`fail` | `warn`) and may name a `runbook`. `data` is
the only channel for facts Go must persist (host keys, IPs); its keys are documented in the
playbook header.

**Events** — the notification callback writes one JSON line per event to the path in
`KTAGS_EVENTS_PATH`: `play_start`, `task_start`, `runner_ok|changed|failed|unreachable|skipped`
(host, task, changed, `msg` on failure; `no_log` results are `"censored"`), final `stats` with
per-host recap. The TUI run view and the CLI recap read this file; nobody parses stdout.

## 4. Inventory directory contract

A customer is a plain Ansible inventory directory under the data dir. It must work with a bare
`ansible-playbook -i <dir>` for emergency use. Secrets then come from the age CLI through a
process substitution, `-e @<(age -d -i <identity> <dir>/group_vars/all/secrets.age)` (ADR-0004);
no plain-text file is written.

```
<customers>/acme/
  hosts.yml                  written by ktags (groups rke2_server, rke2_agent, node fields)
  group_vars/all/
    ktags.yml                written by ktags: customer and cluster identity, nodes, access (no secrets)
    cluster.yml              human-edited cluster variables only
    versions.yml             version pins for this customer (upgrade = a deliberate change here)
    connection.yml           generated from the access mode (SSH args, jump ProxyCommand)
    secrets.age              encrypted store (ADR-0004); Ansible's group_vars loader skips it
  known_hosts                pinned host keys of nodes and jump host
  LOCK.yml                   manual lock (owner, reason, since) when present
  README.md                  customer notes (ignored by the inventory plugin)
```

`ansible.cfg` (shipped with the content) ignores `known_hosts`, `LOCK.yml`, `README.md`, `runs/`
as inventory sources and fails hard on an unparsable source. Nothing else may be placed loose in
the directory.

## 5. Run directories

`<state>/runs/<customer>/<run_id>/` holds `extra_vars.json`, `events.jsonl`, `result.json`,
`ansible.log`. `run_id` = `YYYYMMDD-HHMMSS-<4 hex>`. The directory is created before the run and
never deleted by a playbook; retention is a ktags job. Locks live in the state dir
(`locks/<customer>.lock`, flock), not in the customer directory.

## 6. Writing roles and playbooks

- Idempotent and `--check` compatible: a second run reports `changed=0`; `--check` on a fresh host
  says what it would do and fails on nothing that is not a real problem.
- No `curl | sh`, no unpinned downloads: every artefact is fetched with a pinned version **and**
  a sha256 in `versions.yml`; roles assert the pin variables exist. Helm charts by version.
- Serial where etcd is involved: first server alone, additional servers `serial: 1`, agents
  after. Draining before and uncordoning after any reboot or upgrade, one node at a time.
- Never `fetch` customer data to the laptop. The one exception is the explicit **local backup
  copy** action, which encrypts before writing and is its own playbook.
- `no_log: true` on every task that touches a secret; `assert` over `fail` with a message that
  names the fix.
- Facts through `ansible_facts[...]`, never injected variables. Ubuntu 24.04 amd64 asserted early.
- Role variables are prefixed with the role name; shared variables are prefixed `ktags_`.
- Tags are not a control mechanism; use separate playbooks or `--limit`.

## 7. What changed from ops-platform

| ops-platform (bastion) | ktags (laptop) |
|---|---|
| `/srv/ops/{platform,inventories,logs,status}` | XDG dirs (`development.md §2`); inventories under data, runs under state |
| `ops_*` variable prefix | `ktags_*` |
| `result.json` in `/srv/ops/status/<c>/runs/` | `<state>/runs/<customer>/<run_id>/result.json` |
| bastion user = audit `user` | operator name from the team file, verified against the age identity |
| `team/members.yml` in the platform repo | team file in the config dir, exported with the customer |
| sops vars plugin | age store decrypted by the service, handed over through an inherited pipe (ADR-0004) |
| `access.yml` handles tailscale join | tailscale/jump is a **consented** secondary door, separate playbook |
| Backup: peer only | peer auto + scheduled on node + optional encrypted local copy |

## 8. Local checks

```bash
ansible-lint                          # production profile; config shipped with the content
ansible-playbook --syntax-check       # every playbook, against the example inventory
ansible-playbook --list-tasks         # part of --dry-run output in ktags
```

Both run in CI against `examples/customer-example/` (fictional data). Real runs happen only in the
maintainer's lab (`testing.md §6`).
