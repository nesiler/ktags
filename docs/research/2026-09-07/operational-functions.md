# Operational function catalogue — RKE2 fleet, 15+ isolated customer clusters (7 September 2026)

What a platform engineer actually needs over the lifecycle, from first contact with a customer VM to
decommissioning. Merged from a research pass against current RKE2 / Longhorn / Rancher / Cilium docs
and the ops-platform experience. Legend: **function** — why — layer — frequency — priority.
`(!)` = commonly forgotten. Priority: P0 must exist in v1, P1 soon after, P2 later.
"Done" = already implemented in ops-platform and carried over.

## Phase 0 — First contact / pre-flight (day-0)

- **Reachability check from the laptop before anything runs** — prove SSH path (direct / jump / Tailscale), sudo, key in agent — ssh — per node — P0 (partly done)
- **VM fitness report** — CPU, RAM (Rancher needs 8 GB+), disk layout, virt type, OS build — Ansible facts — once — P0
- **Hostname, /etc/hosts, unique machine-id `(!)`** — cloned VMs with duplicate machine-id break kubelet/CNI identity — Ansible — once — P0
- **Time sync verified in sync, not just installed `(!)`** — etcd and TLS die on skew — Ansible + chronyc — once, weekly check — P0
- **Swap off, persisted** — kubelet — Ansible — once — P0 (done)
- **Kernel modules & sysctl** (br_netfilter, overlay, ip_forward, `fs.inotify.max_user_instances` `(!)`, vm.max_map_count) — Ansible — once — P0 (partly done)
- **Disk & mount plan** — separate volumes for `/var/lib/rancher` and the Longhorn path; playbook never formats — Ansible — once — P0 (done: disk_check)
- **Longhorn node prereqs** (open-iscsi, nfs-common `(!)`, cryptsetup, dmsetup) — Ansible — once — P0 (partly done)
- **Users/keys/sudo baseline from one authoritative team key list** — Ansible — once, on-demand — P0 (done)
- **Host firewall** (6443, 9345, 2379/2380, 8472 VXLAN, 10250, Longhorn 3260/8500-8501) — nftables via Ansible — once — P0 (missing; direct mode exposes ports)
- **MTU discovery `(!)`** — measure path MTU between nodes, set Cilium MTU explicitly — SSH probe — once — P0
- **DNS sanity from node** (resolvers, systemd-resolved stub vs CoreDNS upstream) `(!)` — SSH — once — P1
- **Provider firewall / security groups: out of scope, documented per customer** — inventory doc — once — P0 as documentation
- **Registry reachability / airgap decision** — curl probe to docker.io, registry.rancher.com — once — P1

## Phase 1 — Build & bring-up (day-1)

- **RKE2 install, pinned version, checksum verified** — Ansible — once/upgrade — P0 (done)
- **config.yaml as a templated, versioned artifact** (tls-san, cidrs, disable list, cni, labels/taints) — Ansible template — P0 (done)
- **First server, then additional servers one at a time `(!)`** — parallel joins corrupt etcd — serial 1 — P0 (done, untested with >1 server)
- **Agent join** — P0 (done)
- **tls-san including VIP/LB/jump names and future names `(!)`** — later SAN change forces apiserver cert regeneration — P0
- **Disable replaced bundled components** (ingress-nginx when Traefik, kube-proxy with Cilium) — P0 (done)
- **CIS hardening profile** — customer audits — P1
- **Registry mirrors / registries.yaml** — airgap, rate limits — P1
- **Cilium install + values** (kube-proxy replacement, MTU, IPAM, Hubble) — P0 (done)
- **Post-install network validation**: cilium status, connectivity test, CoreDNS, cross-node pod traffic — P0 (partly done)
- **cert-manager + ClusterIssuer(s)** — P0 (cert-manager done; issuers missing)
- **Ingress + a canary Ingress with real TLS `(!)`** — proves DNS+ACME+ingress end to end — P0
- **Longhorn install with explicit data path, replica count, over-provisioning** — P0 (done)
- **Longhorn backup target (S3/NFSv4) set at install `(!)`** — clusters run for months without one — P0
- **Recurring snapshot/backup jobs with retention** — Longhorn RecurringJob — P0
- **etcd snapshot schedule + off-node copy at install** — RKE2 config — P0 (snapshot + peer copy done; schedule missing)
- **Rancher install, bootstrap password to secret store, admin URL + TLS** — P0 (done)
- **rancher-backup operator + scheduled encrypted backup** — P0
- **Kubeconfig retrieval → k9s bridge** — SSH + tunnel — P0
- **Acceptance test: one command running all the above checks, "cluster is done" gate** — P0 (done: verify role)

## Phase 2 — Operate (day-2)

### Node & OS
- **OS patching: drain → reboot → uncordon → verify, serial 1** — monthly — P0
- **Reboot-required detection `(!)`** — weekly — P1
- **Disk pressure / inode watch on /var/lib/rancher, Longhorn path, /var/log** — weekly — P0 (done in health)
- **Journal size cap `(!)`** — once — P1
- **Log/journal fetch for triage** (rke2-server/agent, containerd) — on-demand — P0
- **Image GC / orphaned image cleanup** — quarterly — P2

### RKE2 lifecycle
- **Version upgrade: servers one at a time, then agents; drain before, uncordon after** — quarterly — P0
- **Pre-upgrade gate: healthy etcd, fresh snapshot, no degraded volumes `(!)`** — P0
- **Rollback path** — emergency — P1
- **Node removal / replacement** (drain, delete, etcd member remove, wipe, rejoin) — on-demand — P0
- **Certificate expiry check + rotation `(!)`** — RKE2 certs are one year; auto-rotate only on restart within 90 days — monthly — P0
- **Join-token rotation, leaked-token response** — yearly/emergency — P1
- **apiserver SAN change → cert regeneration** — on-demand — P1
- **config.yaml drift detection `(!)`** — Ansible check mode — weekly — P1 (done: --check)

### etcd
- **On-demand snapshot before risky change** — P0 (done)
- **Verify integrity + off-node copy exists `(!)`** — weekly — P0 (done)
- **Restore drill** (`--cluster-reset --cluster-reset-restore-path`, wipe `db/` on other servers) — quarterly/emergency — P0
- **Member health & leader** — weekly — P0 (done)
- **DB size vs quota + defrag `(!)`** — monthly — P1

### Networking
- **cilium status + agent restart count** — weekly — P0 (done)
- **Connectivity test after upgrades** — per change — P1
- **kube-proxy-replacement assertion `(!)`** — per change — P1 (done)
- **CoreDNS health** — weekly — P0
- **Ingress reachability + TLS expiry from outside** — daily/weekly — P0
- **cert-manager issuer/Certificate Ready, ACME rate-limit errors `(!)`** — weekly — P0
- **NetworkPolicy sanity** — per change — P2

### Storage (Longhorn)
- **Volume state sweep: degraded / faulted / unexpected detach** — daily — P0
- **Node & disk schedulable, free space vs over-provisioning** — weekly — P0 (partly done)
- **Replica count vs node count sanity `(!)`** — per change — P0
- **Backup target reachable + last successful backup age `(!)`** — daily — P0
- **Disk eviction / replica rebalance before node removal** — on-demand — P0
- **Longhorn upgrade incl. engine image for live volumes `(!)`** — quarterly — P1

### Rancher & apps
- **Rancher health/ping** — daily — P0 (done)
- **rancher-backup last-success age + restore drill** — weekly/quarterly — P0
- **User/role/API-token inventory + expiry `(!)`** — quarterly — P1
- **Rancher upgrade within the Rancher↔Kubernetes support matrix** — twice a year — P1
- **Helm release inventory across clusters** — weekly — P0 (done)
- **Values diff, desired vs deployed** — weekly — P1
- **Helm upgrade/rollback, atomic, with history** — on-demand — P0
- **ImagePullBackOff sweep** — daily — P1

### Observability without a monitoring stack
- **Single health snapshot collector** (node pressure, NotReady, restart deltas, PVC pending, etcd size, disk %, cert expiry days, backup ages) — daily — P0 (done: health)
- **Store snapshots as timestamped JSON per customer for trends `(!)`** — P1 (done: result.json)
- **Alerting: webhook (Slack/Teams) or e-mail from the same job** — P1
- **Deadman check: the collector itself ran `(!)`** — P1

### Security & compliance
- **Leaver revocation: remove key from all nodes, rotate token, revoke Rancher tokens `(!)`** — on-demand — P0 (keys done; token/Rancher missing)
- **Secrets never in plain text: age-encrypted per operator** — always — P0 (done: sops+age)
- **Operator action audit log** — always — P0 (done)
- **Kubeconfig/token expiry tracking for the k9s bridge** — monthly — P1
- **Patch cadence, unattended-upgrades security-only, never auto-reboot** — P1

### Fleet
- **Bulk run with explicit target selection, dry-run default** — P0
- **Version matrix report across customers** — weekly — P0
- **Drift detection: inventory vs reality** — weekly — P0 (partly: --check)
- **Per-customer lock so two engineers cannot collide `(!)`** — always — P0 (done, per laptop)
- **Scheduled jobs without a central server: run backups/health in-cluster (CronJob / systemd timer on the node), verify from the laptop `(!)`** — P0
- **Runbook link attached to every failing check** — P1

## Phase 3 — Decommission
- **Customer sign-off + final backup export handed over `(!)`** — P0
- **Ordered teardown**: scale down → Longhorn detach/delete → uninstall Longhorn → `rke2-uninstall.sh` per node — P0
- **Unregister from Rancher, delete project/users/tokens** — P0
- **Data wipe of Longhorn disks and /var/lib/rancher; provider-side disks out of scope, documented** — P0
- **Revoke team keys, rotate shared creds, delete backup credentials, set backup retention date `(!)`** — P0
- **Archive inventory, encrypted kubeconfigs, audit log, final version matrix; delete live secrets** — P1

## ktags-specific additions (from the ops-platform review)
- **Local backup copy**: pull the latest verified etcd snapshot (and Longhorn backup manifest) to the operator's laptop on demand, encrypted with the operator's age key; third copy besides node and peer/S3 — P1
- **Own access door, with customer consent**: optional Tailscale node or jump-host setup as a *secondary* path so the team keeps access if the customer's primary route (VPN, firewall rule, bastion) breaks; recorded in the inventory as an explicit, customer-approved decision — P1
- Customer config **export/import** (age passphrase) for hand-over between operators — P0
- **Team member onboarding/offboarding** as one action (age key, SSH key, push to all nodes of the customers they hold) — P0
- **k9s bridge** (kubeconfig via SSH tunnel, temporary, audited) — P0
- **"What changed since last run"** diff between two health snapshots — P1
- **Maintenance mode** flag on a customer (suppresses alerts, blocks non-emergency actions) — P1

## Sources
- RKE2 manual upgrades: https://docs.rke2.io/upgrades/manual
- RKE2 backup and restore (snapshot schedule, S3 flags, cluster-reset restore, wipe db/ on other servers): https://docs.rke2.io/datastore/backup_restore
- Longhorn backup target (S3 secret, `s3://bucket@region/`, NFSv4): https://longhorn.io/docs/1.9.1/snapshots-and-backups/backup-and-restore/set-backup-target/
- Rancher backup operator: https://ranchermanager.docs.rancher.com/how-to-guides/new-user-guides/backup-restore-and-disaster-recovery/back-up-rancher
- Rancher backup storage configuration: https://ranchermanager.docs.rancher.com/reference-guides/backup-restore-configuration/storage-configuration
- Cilium troubleshooting (`cilium status`, `connectivity test`): https://docs.cilium.io/en/stable/operations/troubleshooting/
