# Security guide — secrets, access, audit

Binding for every change. ktags holds the keys to customers' production clusters on a laptop; the
threat model is a lost or compromised laptop, a leaked log or terminal recording, a wrong-customer
command, and a leaver who still has material. Rules below are the minimum; a change that weakens
one needs an ADR.

## 1. What never appears anywhere

Secret material never appears in: command-line arguments, environment variables, log files, run
directories, `result.json`, events, error messages, the terminal (except `secret show`, below),
git history, PR descriptions, test fixtures, documentation.

Secret material means: RKE2 join tokens, Rancher bootstrap and admin passwords, kubeconfigs and
their client keys, Tailscale auth keys, S3 backup credentials, bootstrap passwords, age identities,
SSH private keys, session cookies and API tokens of any kind.

Enforcement in code: every free-text line that goes to a log or the screen passes the mask
(`tskey-*`, `AGE-SECRET-KEY-*`, PEM blocks, `K10…` RKE2 tokens, `Bearer …`, and the values of
keys and flags whose name contains `token`, `password`, `passwd`, `api_key`/`apikey`/`api-key`,
`private_key`/`private-key` or the kube config `client-key-data`; names match
case-insensitively). `client-certificate-data` and `certificate-authority-data` are public
certificates and stay readable; `secret` and `credential` are not key classes, because they
also name non-secret things (`secret_name`, `secretRef`, `kind: Secret`). Ansible tasks
touching secrets are `no_log: true`; the events callback replaces
their results with `"censored"`. A test proves the mask on each pattern.

## 2. Secrets at rest and in flight

- Per customer, one encrypted secrets store inside the customer directory; the encryption
  boundary is the `secrets` package and nothing else reads or writes the store.
- Store format per ADR-0004: plain age through `filippo.io/age`, one armored file per customer
  at `group_vars/all/secrets.age`. Recipients are the operators responsible for that customer
  (age recipients from the operator roster); a store with fewer than two recipients is a doctor
  warning. The private identity lives in the operator's config dir with `0600` (or is a
  hardware-backed plugin identity); ktags never copies an identity anywhere.
- The **operator roster** is the one list of operators: each operator's name, SSH public key (§3)
  and age recipient. It lives in the config dir (`development.md §2`) and is exported with the
  customer (`ansible.md §7`). The export below carries the customer directory; how the roster
  travels with it (the whole roster or only that customer's recipients) is set by the roster
  decision that ADR-0004 names, not by this guide. Older text calls it the team file.
- Decrypted values live in memory for the duration of the run. If Ansible needs them, they are
  passed as an extra-vars file read from a pipe inherited by the child (ADR-0004), which leaves
  no plain-text file behind. Never `-e key=value`, never `$ENV`.
- `secret show <customer> <key>` prints the value **only** to stdout, requires `--reason`, and
  writes key name + reason (never the value) to the audit log. In the TUI the reveal dialog clears
  itself after 15 seconds and offers copy-to-clipboard; the clipboard content is not logged.
- Export/import: the whole customer directory, encrypted with an age **passphrase** the two
  operators exchange out of band. The export never includes the exporter's private identity. Import
  re-encrypts the store to the importer's recipients and records the hand-over in the audit log of
  both sides (exporter writes "exported to", importer writes "imported from").

## 3. SSH and host keys

- Host key verification is never relaxed. Each customer directory has its own `known_hosts`;
  Ansible runs with `UserKnownHostsFile=<dir>/known_hosts` and `StrictHostKeyChecking=yes`.
- First contact: the fingerprint is shown and the operator confirms it (`--accept-hostkey` states
  the trust-on-first-use explicitly and is refused for `prod` without an interactive confirmation).
  After bootstrap the key comes from the node itself (`/etc/ssh/ssh_host_ed25519_key.pub` via the
  access playbook) and pins both the bootstrap address and the operational address.
- Jump hosts are verified against the same `known_hosts`; the inner and outer hop both use the
  customer file (ProxyCommand with explicit options, not ProxyJump, so the options reach both hops).
- Each operator has one SSH key (ed25519) in the operator roster (§2); nodes get an `ops` user with
  the roster's keys written exclusively (a key removed from the roster disappears from every node
  on the next access run). Password login is not touched; sudo is NOPASSWD for that user only.
- Bootstrap credentials (customer-provided password or key) are used once, never stored; a
  password is read from a masked prompt, never from a flag.
- ktags's own SSH client (probes, k9s bridge tunnel) uses the same `known_hosts` and the same
  agent; it never writes keys to disk.

## 4. Subprocesses

- Ansible, age and its plugins, k9s, `uv` run with an **allowlisted** environment:
  `PATH HOME LANG LC_ALL TERM USER XDG_*`, `SSH_AUTH_SOCK`, plus the `ANSIBLE_*`/`KTAGS_*`
  variables ktags sets. Never the inherited environment.
- Every subprocess runs in its own process group; cancellation kills the group; timeouts are
  explicit.
- The k9s bridge: kubeconfig fetched over SSH into a `0600` temp file under the state dir, a local
  tunnel to the API server, `k9s` launched with that file, and on exit the tunnel closed and the
  file shredded. The audit log records start and end. The kubeconfig is never cached across
  sessions.
- Downloaded artefacts (RKE2, Helm, charts, Tailscale packages, ansible-core) are pinned by version
  and sha256; nothing is fetched without a checksum, nothing is piped into a shell.

## 5. Confirmation and blast radius

- The customer is always explicit. The tool compares the inventory's own `ktags_customer` with the
  directory name and the command argument; any mismatch stops before the lock.
- `prod` changes require typing the customer name in an interactive terminal; `--yes` is refused
  for `prod`. Non-prod asks `[y/N]` unless `--yes`.
- Per-customer lock (flock in the state dir) plus the manual `LOCK.yml` with owner and reason;
  a locked customer refuses every mutating action and says who holds it.
- Bulk actions across customers run in dry-run mode unless `--apply` is given, list the targets
  first, and never include `prod` customers implicitly.
- Maintenance mode on a customer blocks non-emergency mutating actions and suppresses alerts;
  entering and leaving it is audited.

## 6. Audit

- Append-only JSONL per operator in the state dir: timestamp, operator, host, command, customer,
  environment, run id, arguments (secret-free), dry-run flag, start/end, exit code, duration, log
  path, per-host recap. One line at start and one at end of every mutating command; read-only
  commands log `secret show` and the k9s bridge only.
- The audit file is never rewritten by the tool. Rotation is by size into dated files, also
  append-only. Export includes the audit history of that customer.
- Health snapshots and results are metadata only (`ansible.md §3`); the audit log contains no
  customer business data.

## 7. Leavers and rotation

- Removing an operator from the operator roster and running access on every customer they held
  removes their SSH key from all nodes. The same action lists what else must be rotated because
  the leaver could have read it: RKE2 join token, Rancher admin password and API tokens, S3 backup
  credentials, Tailscale auth keys — each with the command that rotates it.
- Secrets stores are re-encrypted to the remaining recipients as part of the same action.
- A `doctor` check warns when an operator's key is present on a node but absent from the roster,
  or when a secrets store has a recipient that is not in the roster.

## 8. Agents and the repository

- The repository contains no secrets, customer names, real IPs or hostnames; examples use
  `acme`/`globex` and documentation ranges (`203.0.113.0/24`, `10.10.0.0/16`).
- Agents are denied `.env*`, `*.key`, `*.age`, `secrets/`, `kubeconfig*`, `rke2.yaml`
  (`.claude/settings.json`). A PR that adds a fixture resembling a real token is rejected.
- Live systems are touched by the maintainer only (`testing.md §6`).
