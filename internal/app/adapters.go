package app

import (
	"context"
	"errors"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/nesiler/ktags/internal/core/connect"
	"github.com/nesiler/ktags/internal/core/probe"
	"github.com/nesiler/ktags/internal/core/schedule"
	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/paths"
)

// realHealth composes the real connection adapters into the health checks: SSH against each
// customer's known_hosts with the operator's agent, and verified HTTPS against the Kubernetes
// API and Rancher. The interval comes from the settings file.
func realHealth(roots paths.Roots, env paths.Env) (*healthConfig, error) {
	interval, err := healthInterval(roots.Config.Path)
	if err != nil {
		return nil, err
	}
	clock := schedule.System()
	dataRoot := roots.Data.Path
	runner, err := connect.New(connect.Options{
		SSH: connect.SSHClient{
			KnownHosts: func(customer string) string {
				return inventory.KnownHostsPath(inventory.CustomerDir(dataRoot, customer))
			},
			Signers: agentSigners(env.Getenv("SSH_AUTH_SOCK")),
		},
		Kubernetes: connect.HTTPSAPI{Kind: connect.KindKubeAPI, Path: "/version"},
		Rancher:    connect.HTTPSAPI{Kind: connect.KindRancherAPI, Path: "/ping"},
		Redact:     mask.Mask,
		Now:        clock.Now,
	})
	if err != nil {
		return nil, err
	}
	p, err := probe.New(probe.Options{
		Runner: runner,
		Now:    clock.Now,
		Target: func(ctx context.Context, customer string) (probe.Target, error) {
			rec, err := inventory.Load(ctx, inventory.CustomerDir(dataRoot, customer))
			if err != nil {
				return probe.Target{}, err
			}
			return probeTarget(rec), nil
		},
	})
	if err != nil {
		return nil, err
	}
	return &healthConfig{checks: p.Checks(), interval: interval, clock: clock, connection: p.Connection}, nil
}

// probeTarget is what the probe measures for a customer record. Only direct SSH access has an
// adapter; the Kubernetes API is measured when the record names it.
func probeTarget(rec inventory.Record) probe.Target {
	c := rec.Cluster
	id := rec.Customer.ID
	var t probe.Target
	if d := c.Access.Direct; d != nil {
		for _, n := range c.Nodes {
			t.Nodes = append(t.Nodes, connect.SSHTarget{Customer: id, Node: n.ID, Host: n.Address, Port: d.Port, User: d.User})
		}
	} else {
		t.SSHUnsupported = "only direct SSH access is measured yet; this customer has no ktags_cluster.access.direct"
	}
	if c.KubeAPIURL != "" {
		t.Kube = &connect.APITarget{Customer: id, URL: c.KubeAPIURL, CA: c.KubeCA}
	}
	if c.RancherURL != "" {
		t.Rancher = &connect.APITarget{Customer: id, URL: c.RancherURL, CA: c.RancherCA}
	}
	return t
}

// agentSigners reads the operator's keys from the SSH agent at socket (SSH_AUTH_SOCK of the
// service). The agent connection stays open until the keys are released: the agent signs.
func agentSigners(socket string) func(ctx context.Context) ([]ssh.Signer, func(), error) {
	return func(ctx context.Context) ([]ssh.Signer, func(), error) {
		if socket == "" {
			return nil, nil, errors.New("SSH_AUTH_SOCK is not set for the ktags service; start an SSH agent that holds your team key, then restart the service")
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot reach the SSH agent: %w", err)
		}
		signers, err := agent.NewClient(conn).Signers()
		if err != nil {
			_ = conn.Close()
			return nil, nil, fmt.Errorf("the SSH agent did not list its keys: %w", err)
		}
		return signers, func() { _ = conn.Close() }, nil
	}
}
