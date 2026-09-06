package main

// Fake data for the ktags TUI spike.
//
// Nothing here talks to SSH, Ansible or the filesystem. Every value is a
// hard-coded mock so the six screens can be compared across spikes.

import "fmt"

const rke2Version = "v1.35.7+rke2r1"

// Node is one machine belonging to a customer cluster.
type Node struct {
	Name     string
	Role     string // server | agent
	NodeIP   string
	AccessIP string
	Version  string
	Status   string // Ready | NotReady
	Longhorn bool
}

// Check is one health check result for a customer.
type Check struct {
	Name   string
	Status string // ok | warn | fail
	Detail string
}

// Customer is one isolated cluster managed by ktags.
type Customer struct {
	Name       string
	Env        string // prod | staging | test
	Access     string // direct | jump
	JumpHost   string
	Rancher    string
	Nodes      []Node
	Health     string // ok | warn | fail
	HealthNote string
	HealthAge  string
	Backup     string // ok | none
	BackupAge  string
	LockedBy   string
	LockReason string
	Checks     []Check
}

// Locked reports whether the customer is currently held by an operator.
func (c Customer) Locked() bool { return c.LockedBy != "" }

// LockMessage is the toast shown when an action is refused because of a lock.
func (c Customer) LockMessage() string {
	return fmt.Sprintf("locked by %s: %s", c.LockedBy, c.LockReason)
}

// HealthSummary is the one-line health description shown in the top bar.
func (c Customer) HealthSummary() string {
	switch c.Health {
	case "ok":
		return "ok, " + c.HealthAge
	default:
		return fmt.Sprintf("%s (%s), %s", upper(c.Health), c.HealthNote, c.HealthAge)
	}
}

// BackupSummary is the one-line backup description shown in the status bar.
func (c Customer) BackupSummary() string {
	if c.Backup == "none" {
		return "backup: none"
	}
	return "backup: ok, " + c.BackupAge
}

func upper(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'a' && r <= 'z' {
			out[i] = r - 32
		}
	}
	return string(out)
}

// seedCustomers returns the fixed customer set every spike renders.
func seedCustomers() []Customer {
	acme := Customer{
		Name: "acme", Env: "prod", Access: "direct",
		Rancher:   "rancher.acme.example",
		Health:    "ok",
		HealthAge: "2h ago",
		Backup:    "ok",
		BackupAge: "6h ago",
		Nodes: []Node{
			{"acme-srv-1", "server", "10.10.1.11", "203.0.113.11", rke2Version, "Ready", true},
			{"acme-agt-1", "agent", "10.10.1.21", "203.0.113.21", rke2Version, "Ready", true},
		},
	}
	globex := Customer{
		Name: "globex", Env: "staging", Access: "jump",
		JumpHost:   "jump.globex.example",
		Rancher:    "rancher.globex.example",
		Health:     "warn",
		HealthNote: "disk 87%",
		HealthAge:  "1d ago",
		Backup:     "none",
		Nodes: []Node{
			{"globex-srv-1", "server", "10.10.2.11", "203.0.113.31", rke2Version, "Ready", false},
		},
	}
	initech := Customer{
		Name: "initech", Env: "test", Access: "direct",
		Rancher:    "rancher.initech.example",
		Health:     "ok",
		HealthAge:  "10m ago",
		Backup:     "ok",
		BackupAge:  "1h ago",
		LockedBy:   "ayse",
		LockReason: "maintenance window",
		Nodes: []Node{
			{"initech-srv-1", "server", "10.10.3.11", "203.0.113.41", rke2Version, "Ready", true},
			{"initech-srv-2", "server", "10.10.3.12", "203.0.113.42", rke2Version, "Ready", true},
			{"initech-srv-3", "server", "10.10.3.13", "203.0.113.43", rke2Version, "Ready", true},
			{"initech-agt-1", "agent", "10.10.3.21", "203.0.113.44", rke2Version, "Ready", false},
		},
	}
	umbrella := Customer{
		Name: "umbrella", Env: "prod", Access: "direct",
		Rancher:    "rancher.umbrella.example",
		Health:     "fail",
		HealthNote: "1 node NotReady",
		HealthAge:  "30m ago",
		Backup:     "ok",
		BackupAge:  "12h ago",
		Nodes: []Node{
			{"umbrella-srv-1", "server", "10.10.4.11", "203.0.113.51", rke2Version, "NotReady", true},
		},
	}

	out := []Customer{acme, globex, initech, umbrella}
	for i := range out {
		out[i].Checks = seedChecks(out[i])
	}
	return out
}

// seedChecks builds the nine health checks the spec asks for, coloured to
// match the customer's overall health.
func seedChecks(c Customer) []Check {
	ready, total := 0, len(c.Nodes)
	for _, n := range c.Nodes {
		if n.Status == "Ready" {
			ready++
		}
	}
	longhorn := 0
	for _, n := range c.Nodes {
		if n.Longhorn {
			longhorn++
		}
	}

	nodesReady := Check{"nodes_ready", "ok", fmt.Sprintf("%d/%d Ready", ready, total)}
	if ready != total {
		nodesReady.Status = "fail"
	}

	disk := Check{"disk", "ok", "max 41% used"}
	snapshot := Check{"last_snapshot", "ok", "etcd snapshot " + c.BackupAge}
	badPods := Check{"bad_pods", "ok", "0 pods in CrashLoopBackOff"}
	rancher := Check{"rancher_ping", "ok", "https://" + c.Rancher + " 200 OK"}

	switch c.Name {
	case "globex":
		disk = Check{"disk", "warn", "globex-srv-1 /var 87% used"}
		snapshot = Check{"last_snapshot", "warn", "no snapshot recorded"}
	case "umbrella":
		badPods = Check{"bad_pods", "fail", "3 pods NotReady on umbrella-srv-1"}
		rancher = Check{"rancher_ping", "warn", "503 from ingress, retrying"}
	}

	lh := Check{"longhorn_nodes", "ok", fmt.Sprintf("%d/%d schedulable", longhorn, total)}
	if longhorn == 0 {
		lh = Check{"longhorn_nodes", "ok", "longhorn not deployed"}
	}

	etcd := Check{"etcd", "ok", "quorum healthy"}
	if c.Health == "fail" {
		etcd = Check{"etcd", "warn", "1 member unreachable"}
	}

	return []Check{
		nodesReady,
		{"node_versions", "ok", "all nodes on " + rke2Version},
		badPods,
		{"helm_releases", "ok", "7 releases deployed"},
		rancher,
		lh,
		etcd,
		disk,
		snapshot,
	}
}

// secretKeys are the fake secrets offered by the `:secret` dialog.
var secretKeys = []string{"rke2_token", "rancher_admin_password"}

// secretValue returns a fake, obviously-not-real secret for a customer.
func secretValue(customer, key string) string {
	switch key {
	case "rke2_token":
		return "K10" + customer + "d41d8cd98f00b204e9800998ecf8427e::server:9f86d081884c7d659a2f"
	case "rancher_admin_password":
		return "pw-" + customer + "-7Qx!veHt2Lm"
	}
	return "<unknown>"
}

// ---------------------------------------------------------------------------
// Fake Ansible run scripts
// ---------------------------------------------------------------------------

// runEvent is a single line the fake Ansible runner emits.
type runEvent struct {
	Kind   string // play | task | result | recap | summary
	Play   string
	Task   string
	Result string // ok | changed | skipping | failed
	Host   string
	Text   string
}

type playSpec struct {
	name  string
	tasks []string
}

// installScript builds the fake `ktags install` output for a customer.
func installScript(c Customer) []runEvent {
	plays := []playSpec{
		{"prepare nodes", []string{
			"common : install base packages",
			"common : disable swap",
			"common : apply sysctl tuning",
			"common : write /etc/rancher/rke2/registries.yaml",
		}},
		{"bootstrap rke2 servers", []string{
			"rke2_server : render config.yaml",
			"rke2_server : install rke2 " + rke2Version,
			"rke2_server : enable rke2-server.service",
			"rke2_server : wait for kube-apiserver",
		}},
		{"join rke2 agents", []string{
			"rke2_agent : render config.yaml",
			"rke2_agent : install rke2 " + rke2Version,
			"rke2_agent : enable rke2-agent.service",
		}},
		{"post install", []string{
			"longhorn : install open-iscsi",
			"rancher : register cluster",
			"backup : schedule etcd snapshots",
		}},
	}
	return expand(c, plays, false)
}

// healthScript builds the fake `ktags health` output for a customer.
func healthScript(c Customer) []runEvent {
	plays := []playSpec{
		{"collect cluster facts", []string{
			"health : gather node facts",
			"health : read rke2 version",
			"health : read disk usage",
		}},
		{"health checks", []string{
			"health : nodes_ready",
			"health : node_versions",
			"health : bad_pods",
			"health : helm_releases",
			"health : rancher_ping",
			"health : longhorn_nodes",
			"health : etcd",
			"health : disk",
			"health : last_snapshot",
		}},
	}
	return expand(c, plays, true)
}

// expand turns play specs into per-host result events.
func expand(c Customer, plays []playSpec, readOnly bool) []runEvent {
	var out []runEvent
	failedOnce := false

	for _, p := range plays {
		out = append(out, runEvent{Kind: "play", Play: p.name, Text: "PLAY [" + p.name + "]"})
		for _, t := range p.tasks {
			out = append(out, runEvent{Kind: "task", Play: p.name, Task: t, Text: "TASK [" + t + "]"})
			for _, n := range c.Nodes {
				res := "ok"
				switch {
				case readOnly:
					res = "ok"
				case n.Role == "agent" && contains(t, "rke2_server"):
					res = "skipping"
				case n.Role == "server" && contains(t, "rke2_agent"):
					res = "skipping"
				case contains(t, "longhorn") && !n.Longhorn:
					res = "skipping"
				case contains(t, "install") || contains(t, "render") || contains(t, "enable") ||
					contains(t, "write") || contains(t, "schedule") || contains(t, "register"):
					res = "changed"
				}
				if c.Health == "fail" && !failedOnce && contains(t, "wait for kube-apiserver") {
					res = "failed"
					failedOnce = true
				}
				if readOnly && c.Health != "ok" && contains(t, "disk") && n.Name == c.Nodes[0].Name {
					res = "changed" // "changed" reads as a warning in the fake output
				}
				out = append(out, runEvent{
					Kind: "result", Play: p.name, Task: t, Result: res, Host: n.Name,
					Text: res + ": [" + n.Name + "]",
				})
			}
		}
	}

	out = append(out, runEvent{Kind: "recap", Text: "PLAY RECAP"})
	out = append(out, runEvent{Kind: "summary"})
	return out
}

func contains(s, sub string) bool {
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
