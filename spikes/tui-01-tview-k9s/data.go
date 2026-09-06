package main

import (
	"fmt"
	"math/rand"
)

// data.go holds every piece of fake data used by this spike. It is intentionally
// self-contained and free of any tview/tcell import so that it can be dropped into
// the sibling spikes unchanged and the comparison stays fair.

// Status values used for customers, health checks and nodes.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Node is a single machine belonging to a customer cluster.
type Node struct {
	Name     string
	Role     string // server | agent
	NodeIP   string
	AccessIP string
	RKE2     string
	Status   string // Ready | NotReady
	Longhorn bool
}

// HealthCheck is one line of the health table.
type HealthCheck struct {
	Name   string
	Status string // ok | warn | fail
	Detail string
}

// Customer is one isolated RKE2 cluster operated through ktags.
type Customer struct {
	Name        string
	Environment string // prod | staging | test
	Access      string // direct | jump
	JumpHost    string
	Rancher     string
	Nodes       []Node
	Health      []HealthCheck
	HealthState string // ok | warn | fail
	LastHealth  string
	LastBackup  string
	LockedBy    string
	LockReason  string
}

// Locked reports whether the customer is under a maintenance lock.
func (c *Customer) Locked() bool { return c.LockedBy != "" }

// LockMessage is the toast shown when an action is refused because of a lock.
func (c *Customer) LockMessage() string {
	return fmt.Sprintf("locked by %s: %s", c.LockedBy, c.LockReason)
}

// RKE2Version is the version every fake node reports.
const RKE2Version = "v1.35.7+rke2r1"

// Secret is one revealable secret of a customer.
type Secret struct {
	Key   string
	Value string
}

// Secrets returns the fake secret store of a customer.
func Secrets(c *Customer) []Secret {
	return []Secret{
		{Key: "rke2_token", Value: "K10" + hash32(c.Name) + "::server:9f3c1b7ae5d24081"},
		{Key: "rancher_admin_password", Value: "Rr-" + hash32(c.Name+"rancher") + "-2f7Q"},
	}
}

func hash32(s string) string {
	var h uint32 = 2166136261
	for _, r := range s {
		h ^= uint32(r)
		h *= 16777619
	}
	return fmt.Sprintf("%08x", h)
}

// Customers returns the shared fake customer list.
func Customers() []Customer {
	return []Customer{
		{
			Name:        "acme",
			Environment: "prod",
			Access:      "direct",
			Rancher:     "rancher.acme.example",
			LastHealth:  "ok, 2h ago",
			LastBackup:  "ok, 6h ago",
			HealthState: StatusOK,
			Nodes: []Node{
				{Name: "acme-srv-1", Role: "server", NodeIP: "10.10.1.11", AccessIP: "203.0.113.11", RKE2: RKE2Version, Status: "Ready", Longhorn: true},
				{Name: "acme-agt-1", Role: "agent", NodeIP: "10.10.1.21", AccessIP: "203.0.113.21", RKE2: RKE2Version, Status: "Ready", Longhorn: true},
			},
			Health: []HealthCheck{
				{"nodes_ready", StatusOK, "2/2 nodes Ready"},
				{"node_versions", StatusOK, "all nodes on " + RKE2Version},
				{"bad_pods", StatusOK, "no pods in CrashLoopBackOff"},
				{"helm_releases", StatusOK, "12 releases deployed"},
				{"rancher_ping", StatusOK, "rancher.acme.example 200 in 84ms"},
				{"longhorn_nodes", StatusOK, "2/2 schedulable, 0 degraded volumes"},
				{"etcd", StatusOK, "1 member healthy, db 42 MiB"},
				{"disk", StatusOK, "max usage 54% on acme-srv-1 /var"},
				{"last_snapshot", StatusOK, "etcd snapshot 6h ago (s3)"},
			},
		},
		{
			Name:        "globex",
			Environment: "staging",
			Access:      "jump",
			JumpHost:    "jump.globex.example",
			Rancher:     "rancher.globex.example",
			LastHealth:  "WARN (disk 87%), 1d ago",
			LastBackup:  "none",
			HealthState: StatusWarn,
			Nodes: []Node{
				{Name: "globex-srv-1", Role: "server", NodeIP: "10.10.2.11", AccessIP: "203.0.113.31", RKE2: RKE2Version, Status: "Ready", Longhorn: false},
			},
			Health: []HealthCheck{
				{"nodes_ready", StatusOK, "1/1 nodes Ready"},
				{"node_versions", StatusOK, "all nodes on " + RKE2Version},
				{"bad_pods", StatusWarn, "1 pod Pending: monitoring/loki-0"},
				{"helm_releases", StatusOK, "7 releases deployed"},
				{"rancher_ping", StatusOK, "rancher.globex.example 200 in 131ms"},
				{"longhorn_nodes", StatusWarn, "longhorn not installed"},
				{"etcd", StatusOK, "1 member healthy, db 21 MiB"},
				{"disk", StatusWarn, "87% on globex-srv-1 /var/lib/rancher"},
				{"last_snapshot", StatusFail, "no etcd snapshot found"},
			},
		},
		{
			Name:        "initech",
			Environment: "test",
			Access:      "direct",
			Rancher:     "rancher.initech.example",
			LastHealth:  "ok, 10m ago",
			LastBackup:  "ok, 1h ago",
			HealthState: StatusOK,
			LockedBy:    "ayse",
			LockReason:  "maintenance window",
			Nodes: []Node{
				{Name: "initech-srv-1", Role: "server", NodeIP: "10.10.3.11", AccessIP: "203.0.113.41", RKE2: RKE2Version, Status: "Ready", Longhorn: true},
				{Name: "initech-srv-2", Role: "server", NodeIP: "10.10.3.12", AccessIP: "203.0.113.42", RKE2: RKE2Version, Status: "Ready", Longhorn: true},
				{Name: "initech-srv-3", Role: "server", NodeIP: "10.10.3.13", AccessIP: "203.0.113.43", RKE2: RKE2Version, Status: "Ready", Longhorn: true},
				{Name: "initech-agt-1", Role: "agent", NodeIP: "10.10.3.21", AccessIP: "203.0.113.44", RKE2: RKE2Version, Status: "Ready", Longhorn: false},
			},
			Health: []HealthCheck{
				{"nodes_ready", StatusOK, "4/4 nodes Ready"},
				{"node_versions", StatusOK, "all nodes on " + RKE2Version},
				{"bad_pods", StatusOK, "no pods in CrashLoopBackOff"},
				{"helm_releases", StatusOK, "9 releases deployed"},
				{"rancher_ping", StatusOK, "rancher.initech.example 200 in 62ms"},
				{"longhorn_nodes", StatusOK, "3/3 schedulable, 0 degraded volumes"},
				{"etcd", StatusOK, "3 members healthy, db 88 MiB"},
				{"disk", StatusOK, "max usage 41% on initech-srv-2 /var"},
				{"last_snapshot", StatusOK, "etcd snapshot 1h ago (s3)"},
			},
		},
		{
			Name:        "umbrella",
			Environment: "prod",
			Access:      "direct",
			Rancher:     "rancher.umbrella.example",
			LastHealth:  "FAIL (1 node NotReady), 30m ago",
			LastBackup:  "ok, 12h ago",
			HealthState: StatusFail,
			Nodes: []Node{
				{Name: "umbrella-srv-1", Role: "server", NodeIP: "10.10.4.11", AccessIP: "203.0.113.51", RKE2: RKE2Version, Status: "NotReady", Longhorn: true},
			},
			Health: []HealthCheck{
				{"nodes_ready", StatusFail, "0/1 nodes Ready (umbrella-srv-1 NotReady)"},
				{"node_versions", StatusOK, "all nodes on " + RKE2Version},
				{"bad_pods", StatusFail, "6 pods NodeLost in kube-system"},
				{"helm_releases", StatusWarn, "1 release pending-upgrade: cert-manager"},
				{"rancher_ping", StatusFail, "rancher.umbrella.example connection refused"},
				{"longhorn_nodes", StatusFail, "0/1 schedulable, 3 degraded volumes"},
				{"etcd", StatusWarn, "1 member unreachable"},
				{"disk", StatusOK, "max usage 38% on umbrella-srv-1 /var"},
				{"last_snapshot", StatusOK, "etcd snapshot 12h ago (s3)"},
			},
		},
	}
}

// RunStep is one emitted line of a fake Ansible run.
type RunStep struct {
	Play   string // non-empty: a new play starts
	Task   string // non-empty: a new task starts, formatted "role : name"
	Host   string // host the result belongs to
	Result string // ok | changed | skipping | failed
}

// RunScript builds the deterministic fake Ansible run for a customer. kind is
// "install" or "health". The result is roughly 8 seconds of output at 50-150ms
// per line.
func RunScript(kind string, c *Customer) []RunStep {
	hosts := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		hosts = append(hosts, n.Name)
	}
	if len(hosts) == 0 {
		hosts = []string{"localhost"}
	}

	type play struct {
		name  string
		tasks []string
	}
	var plays []play
	switch kind {
	case "backup":
		plays = []play{
			{"preflight", []string{
				"common : Gather facts",
				"common : Check free space on backup target",
			}},
			{"etcd backup", []string{
				"backup : Trigger rke2 etcd-snapshot save",
				"backup : Wait for snapshot file",
				"backup : Upload snapshot to s3",
				"backup : Prune snapshots older than 30d",
			}},
			{"longhorn backup", []string{
				"backup : List longhorn volumes",
				"backup : Create volume backups",
				"backup : Wait for backup completion",
			}},
			{"report", []string{
				"backup : Render summary",
			}},
		}
	case "health":
		plays = []play{
			{"preflight", []string{
				"common : Gather facts",
				"common : Check ssh reachability",
				"common : Read /etc/os-release",
			}},
			{"cluster health", []string{
				"health : Query node readiness",
				"health : Compare rke2 versions",
				"health : List pods not Running",
				"health : List helm releases",
				"health : Ping rancher endpoint",
				"health : Inspect longhorn nodes",
				"health : Check etcd members",
				"health : Read disk usage",
				"health : Find last etcd snapshot",
			}},
			{"report", []string{
				"health : Render summary",
			}},
		}
	default:
		plays = []play{
			{"preflight", []string{
				"common : Gather facts",
				"common : Assert supported distribution",
				"common : Check outbound connectivity",
				"common : Install base packages",
			}},
			{"rke2 servers", []string{
				"rke2 : Create /etc/rancher/rke2 directory",
				"rke2 : Template config.yaml",
				"rke2 : Download rke2 installer",
				"rke2 : Run rke2 installer",
				"rke2 : Enable rke2-server service",
				"rke2 : Wait for kube-apiserver",
				"rke2 : Fetch kubeconfig",
			}},
			{"rke2 agents", []string{
				"rke2 : Template agent config.yaml",
				"rke2 : Run rke2 installer",
				"rke2 : Enable rke2-agent service",
				"rke2 : Wait for node registration",
			}},
			{"cluster addons", []string{
				"addons : Apply cilium values",
				"addons : Install cert-manager",
				"addons : Install longhorn",
				"addons : Install rancher agent",
				"addons : Wait for addon rollout",
			}},
			{"post checks", []string{
				"health : Query node readiness",
				"health : List pods not Running",
				"health : Render summary",
			}},
		}
	}

	rnd := rand.New(rand.NewSource(int64(len(c.Name)*7919 + len(kind))))
	steps := make([]RunStep, 0, 96)
	for _, p := range plays {
		steps = append(steps, RunStep{Play: p.name})
		for _, t := range p.tasks {
			steps = append(steps, RunStep{Task: t})
			for _, h := range hosts {
				// Agents are skipped in server plays and vice versa.
				if p.name == "rke2 agents" && !hostIsAgent(c, h) {
					steps = append(steps, RunStep{Host: h, Result: "skipping"})
					continue
				}
				if p.name == "rke2 servers" && hostIsAgent(c, h) {
					steps = append(steps, RunStep{Host: h, Result: "skipping"})
					continue
				}
				res := "ok"
				switch n := rnd.Intn(10); {
				case n < 4 && kind == "install":
					res = "changed"
				case n == 9 && c.HealthState == StatusFail:
					res = "failed"
				}
				steps = append(steps, RunStep{Host: h, Result: res})
			}
		}
	}
	return steps
}

func hostIsAgent(c *Customer, host string) bool {
	for _, n := range c.Nodes {
		if n.Name == host {
			return n.Role == "agent"
		}
	}
	return false
}

// RunSummary is the table shown when a run finishes; it mirrors the health table.
func RunSummary(kind string, c *Customer) []HealthCheck {
	switch kind {
	case "health":
		return c.Health
	case "backup":
		return []HealthCheck{
			{"etcd_snapshot", StatusOK, "etcd-snapshot-" + c.Name + "-1730 saved"},
			{"snapshot_upload", StatusOK, "uploaded to s3://ktags-backups/" + c.Name},
			{"longhorn_backups", StatusOK, fmt.Sprintf("%d volume backup(s) completed", len(c.Nodes))},
			{"retention", StatusOK, "pruned 2 snapshots older than 30d"},
		}
	}
	return []HealthCheck{
		{"nodes_ready", c.Health[0].Status, c.Health[0].Detail},
		{"rke2_installed", StatusOK, fmt.Sprintf("%d node(s) on %s", len(c.Nodes), RKE2Version)},
		{"addons", StatusOK, "cilium, cert-manager, longhorn, rancher-agent"},
		{"kubeconfig", StatusOK, "written to ~/.ktags/" + c.Name + "/kubeconfig"},
		{"rancher_ping", c.Health[4].Status, c.Health[4].Detail},
		{"last_snapshot", c.Health[8].Status, c.Health[8].Detail},
	}
}

// CommandNames are the commands accepted by the ":" prompt.
var CommandNames = []string{"add", "backup", "health", "install", "quit", "secret"}

// CommandHelp describes each command for the help overlay and the prompt hint.
var CommandHelp = map[string]string{
	"add":     "add a new customer (form)",
	"backup":  "run an etcd/longhorn backup",
	"health":  "run the health checks",
	"install": "install or converge the cluster",
	"quit":    "leave ktags",
	"secret":  "reveal a customer secret",
}

// CustomerNameRe is the validation pattern for a new customer name.
const CustomerNameRe = `^[a-z][a-z0-9-]{1,30}$`

// NewCustomer builds a customer freshly created through the :add form. Its health
// is "never checked" until a run happens.
func NewCustomer(name, env, access, jump, rancher string, nodes []Node) Customer {
	checks := make([]HealthCheck, 0, 9)
	for _, n := range []string{"nodes_ready", "node_versions", "bad_pods", "helm_releases",
		"rancher_ping", "longhorn_nodes", "etcd", "disk", "last_snapshot"} {
		checks = append(checks, HealthCheck{Name: n, Status: StatusWarn, Detail: "never checked – run :health"})
	}
	return Customer{
		Name:        name,
		Environment: env,
		Access:      access,
		JumpHost:    jump,
		Rancher:     rancher,
		Nodes:       nodes,
		Health:      checks,
		HealthState: StatusWarn,
		LastHealth:  "never",
		LastBackup:  "none",
	}
}
