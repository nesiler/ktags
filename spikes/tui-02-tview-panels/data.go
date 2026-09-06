package main

// Fake, in-memory data for the spike. No SSH, no Ansible, no files on disk.
// Identical content in every ktags TUI spike so the variants stay comparable.

// Node is one machine of a customer cluster.
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

// Customer is one isolated cluster.
type Customer struct {
	Name     string
	Env      string // prod | staging | test
	Access   string // direct | jump
	JumpHost string
	Rancher  string
	Nodes    []Node
	Health   []HealthCheck

	HealthStatus string // ok | warn | fail
	HealthAge    string
	BackupStatus string // ok | none
	BackupAge    string

	Lock string // empty, or "ayse: maintenance window"
}

const rke2Version = "v1.35.7+rke2r1"

func node(name, role, nodeIP, accessIP, status string, longhorn bool) Node {
	return Node{
		Name:     name,
		Role:     role,
		NodeIP:   nodeIP,
		AccessIP: accessIP,
		RKE2:     rke2Version,
		Status:   status,
		Longhorn: longhorn,
	}
}

func healthOK() []HealthCheck {
	return []HealthCheck{
		{"nodes_ready", "ok", "all nodes Ready"},
		{"node_versions", "ok", "all on " + rke2Version},
		{"bad_pods", "ok", "no CrashLoopBackOff / Pending pods"},
		{"helm_releases", "ok", "12 releases deployed"},
		{"rancher_ping", "ok", "HTTP 200 in 84ms"},
		{"longhorn_nodes", "ok", "all volumes healthy"},
		{"etcd", "ok", "quorum ok, db 128 MiB"},
		{"disk", "ok", "max 41% used"},
		{"last_snapshot", "ok", "6h ago, 512 MiB"},
	}
}

func fakeCustomers() []Customer {
	acme := Customer{
		Name: "acme", Env: "prod", Access: "direct",
		Rancher: "rancher.acme.example",
		Nodes: []Node{
			node("acme-srv-1", "server", "10.10.1.11", "203.0.113.11", "Ready", true),
			node("acme-agt-1", "agent", "10.10.1.21", "203.0.113.21", "Ready", true),
		},
		Health:       healthOK(),
		HealthStatus: "ok", HealthAge: "2h ago",
		BackupStatus: "ok", BackupAge: "6h ago",
	}

	globex := Customer{
		Name: "globex", Env: "staging", Access: "jump",
		JumpHost: "jump.globex.example",
		Rancher:  "rancher.globex.example",
		Nodes: []Node{
			node("globex-srv-1", "server", "10.10.2.11", "203.0.113.31", "Ready", false),
		},
		Health: []HealthCheck{
			{"nodes_ready", "ok", "1/1 Ready"},
			{"node_versions", "ok", "all on " + rke2Version},
			{"bad_pods", "ok", "no bad pods"},
			{"helm_releases", "ok", "7 releases deployed"},
			{"rancher_ping", "ok", "HTTP 200 in 132ms"},
			{"longhorn_nodes", "warn", "longhorn not installed"},
			{"etcd", "ok", "single node, db 64 MiB"},
			{"disk", "warn", "/var/lib/rancher 87% used"},
			{"last_snapshot", "fail", "no snapshot found"},
		},
		HealthStatus: "warn", HealthAge: "1d ago",
		BackupStatus: "none", BackupAge: "never",
	}

	initech := Customer{
		Name: "initech", Env: "test", Access: "direct",
		Rancher: "rancher.initech.example",
		Nodes: []Node{
			node("initech-srv-1", "server", "10.10.3.11", "203.0.113.41", "Ready", true),
			node("initech-srv-2", "server", "10.10.3.12", "203.0.113.42", "Ready", true),
			node("initech-srv-3", "server", "10.10.3.13", "203.0.113.43", "Ready", true),
			node("initech-agt-1", "agent", "10.10.3.21", "203.0.113.44", "Ready", false),
		},
		Health:       healthOK(),
		HealthStatus: "ok", HealthAge: "10m ago",
		BackupStatus: "ok", BackupAge: "1h ago",
		Lock: "ayse: maintenance window",
	}

	umbrella := Customer{
		Name: "umbrella", Env: "prod", Access: "direct",
		Rancher: "rancher.umbrella.example",
		Nodes: []Node{
			node("umbrella-srv-1", "server", "10.10.4.11", "203.0.113.51", "NotReady", true),
		},
		Health: []HealthCheck{
			{"nodes_ready", "fail", "umbrella-srv-1 NotReady (kubelet down)"},
			{"node_versions", "ok", "all on " + rke2Version},
			{"bad_pods", "fail", "4 pods Pending, 1 CrashLoopBackOff"},
			{"helm_releases", "warn", "1 release in failed state"},
			{"rancher_ping", "fail", "connection refused"},
			{"longhorn_nodes", "warn", "1 volume degraded"},
			{"etcd", "ok", "quorum ok, db 96 MiB"},
			{"disk", "ok", "max 55% used"},
			{"last_snapshot", "ok", "12h ago, 780 MiB"},
		},
		HealthStatus: "fail", HealthAge: "30m ago",
		BackupStatus: "ok", BackupAge: "12h ago",
	}

	return []Customer{acme, globex, initech, umbrella}
}

// fakeSecrets are the values shown by the ":secret" dialog.
var fakeSecrets = []struct {
	Key   string
	Value string
}{
	{"rke2_token", "K10a4f2c9e1b7::server:9f3c1d8e5a2b4c6d7e8f9a0b1c2d3e4f"},
	{"rancher_admin_password", "Zx8-qP2v-Lm4t-Rn7w"},
}
