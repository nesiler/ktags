package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/agent"

	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/paths"
)

func labRecord() inventory.Record {
	return inventory.Record{
		Schema:   inventory.SchemaVersion,
		Customer: inventory.Customer{ID: "acme", Name: "Acme", Environment: inventory.EnvTest},
		Cluster: inventory.Cluster{
			ID: "acme-1", Name: "Acme", RancherURL: "https://rancher.acme.example.test", RancherCA: "RANCHER CA",
			Nodes: []inventory.Node{
				{ID: "srv-1", Name: "srv-1", Role: inventory.RoleServer, Address: "203.0.113.11"},
				{ID: "agt-1", Name: "agt-1", Role: inventory.RoleAgent, Address: "203.0.113.12"},
			},
			Access: inventory.Access{Direct: &inventory.DirectAccess{User: "ops", Port: 2222}},
		},
	}
}

// The probe measures every node over direct SSH, the Kubernetes API only when the record names
// it, and a customer without direct access as not configured.
func TestProbeTarget(t *testing.T) {
	r := labRecord()
	got := probeTarget(r)
	if len(got.Nodes) != 2 || got.Nodes[1].Node != "agt-1" || got.Nodes[1].Host != "203.0.113.12" || got.Nodes[1].Port != 2222 || got.Nodes[1].User != "ops" || got.Nodes[1].Customer != "acme" {
		t.Fatalf("nodes %+v", got.Nodes)
	}
	if got.SSHUnsupported != "" || got.Kube != nil || got.Rancher == nil || got.Rancher.CA != "RANCHER CA" {
		t.Fatalf("target %+v, want SSH, no kube, Rancher with its CA", got)
	}
	r.Cluster.KubeAPIURL, r.Cluster.KubeCA = "https://203.0.113.11:6443", "KUBE CA"
	r.Cluster.Access = inventory.Access{Jump: &inventory.JumpAccess{Host: "203.0.113.1", Port: 22, User: "ops"}}
	got = probeTarget(r)
	if len(got.Nodes) != 0 || !strings.Contains(got.SSHUnsupported, "direct") || got.Kube == nil || got.Kube.CA != "KUBE CA" || got.Kube.URL != r.Cluster.KubeAPIURL {
		t.Fatalf("target %+v, want no SSH targets, the reason and the kube API", got)
	}
	r.Cluster.RancherURL = ""
	if probeTarget(r).Rancher != nil {
		t.Fatal("a record without a Rancher URL got a Rancher target")
	}
}

func TestAgentSigners(t *testing.T) {
	if _, _, err := agentSigners("")(context.Background()); err == nil || !strings.Contains(err.Error(), "SSH_AUTH_SOCK is not set") {
		t.Fatalf("no socket: %v", err)
	}
	dir, err := os.MkdirTemp("", "ag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "agent.sock")
	if _, _, err := agentSigners(socket)(context.Background()); err == nil || !strings.Contains(err.Error(), "cannot reach the SSH agent") {
		t.Fatalf("no agent: %v", err)
	}
	keyring := agent.NewKeyring()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyring.Add(agent.AddedKey{PrivateKey: priv}); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = agent.ServeAgent(keyring, c) }()
		}
	}()
	signers, release, err := agentSigners(socket)(context.Background())
	if err != nil || len(signers) != 1 {
		t.Fatalf("agent with one key: %d signers, %v", len(signers), err)
	}
	release()
}

func testRoots(t *testing.T) paths.Roots {
	t.Helper()
	home := t.TempDir()
	roots, err := paths.Resolve(paths.Env{Getenv: func(k string) string {
		if k == "KTAGS_HOME" {
			return home
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	return roots
}

// The real composition has one health check per connection check and the settings interval; a
// refused settings file refuses it.
func TestRealHealth(t *testing.T) {
	roots := testRoots(t)
	cfg, err := realHealth(roots, paths.Env{Getenv: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range cfg.checks {
		names = append(names, c.Name)
	}
	if got := strings.Join(names, ","); got != "ssh.endpoint,ssh.auth,ssh.sudo,kube.endpoint,kube.auth,kube.authz,rancher.endpoint,rancher.auth,rancher.authz" || cfg.interval != 5*time.Minute || cfg.connection == nil {
		t.Fatalf("checks %s interval %s connection %v", got, cfg.interval, cfg.connection != nil)
	}
	if err := os.MkdirAll(roots.Config.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roots.Config.Path, settingsFile), []byte("health:\n  interval: 10s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := realHealth(roots, paths.Env{Getenv: func(string) string { return "" }}); err == nil || !strings.Contains(err.Error(), "is refused") {
		t.Fatalf("realHealth with a refused setting = %v", err)
	}
}

// A health composition that cannot be built refuses the service start instead of serving
// without health.
func TestServiceRunRefusesAFailedHealthComposition(t *testing.T) {
	h := newHarness(t)
	h.deps.healthFor = func(paths.Roots, paths.Env) (*healthConfig, error) {
		return nil, errTest("the settings file is refused")
	}
	code, _, stderr := h.ktags("service", "run")
	if code != 1 || !strings.Contains(stderr, "the settings file is refused") {
		t.Fatalf("service run exited %d\n%s", code, stderr)
	}
	if _, err := os.Stat(h.socket); err == nil {
		t.Fatal("the service listened without its health composition")
	}
}

type errTest string

func (e errTest) Error() string { return string(e) }
