package inventory

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAcceptsSample(t *testing.T) {
	if err := Validate(sample()); err != nil {
		t.Fatalf("Validate(sample) = %v", err)
	}
}

func TestValidateRefuses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
		field  string
		// secret is a value that must never appear in the error text.
		secret string
	}{
		{"schema version", func(r *Record) { r.Schema = 2 }, "ktags_schema", ""},
		{"customer id upper case", func(r *Record) { r.Customer.ID = "Acme" }, "ktags_customer.id", ""},
		{"customer id too long", func(r *Record) { r.Customer.ID = strings.Repeat("a", 41) }, "ktags_customer.id", ""},
		{"customer id trailing dash", func(r *Record) { r.Customer.ID = "acme-" }, "ktags_customer.id", ""},
		{"cluster id single char", func(r *Record) { r.Cluster.ID = "a" }, "ktags_cluster.id", ""},
		{"empty customer name", func(r *Record) { r.Customer.Name = "  " }, "ktags_customer.name", ""},
		{"control char in name", func(r *Record) { r.Cluster.Name = "a\nb" }, "ktags_cluster.name", ""},
		{"invalid environment", func(r *Record) { r.Customer.Environment = "production" }, "ktags_customer.environment", ""},
		{"missing environment", func(r *Record) { r.Customer.Environment = "" }, "ktags_customer.environment", ""},
		{"duplicate node id", func(r *Record) { r.Cluster.Nodes[1].ID = "srv-1" }, "ktags_cluster.nodes[1].id", ""},
		{"no nodes", func(r *Record) { r.Cluster.Nodes = nil }, "ktags_cluster.nodes", ""},
		{"invalid role", func(r *Record) { r.Cluster.Nodes[0].Role = "master" }, "ktags_cluster.nodes[0].role", ""},
		{"address with port", func(r *Record) { r.Cluster.Nodes[0].Address = "203.0.113.11:22" }, "ktags_cluster.nodes[0].address", ""},
		{"address with scheme", func(r *Record) { r.Cluster.Nodes[0].Address = "ssh://node1" }, "ktags_cluster.nodes[0].address", ""},
		{"address empty", func(r *Record) { r.Cluster.Nodes[0].Address = "" }, "ktags_cluster.nodes[0].address", ""},
		{"address bad label", func(r *Record) { r.Cluster.Nodes[0].Address = "node_1.example.com" }, "ktags_cluster.nodes[0].address", ""},
		{"rancher url http", func(r *Record) { r.Cluster.RancherURL = "http://rancher.example.com" }, "ktags_cluster.rancher_url", ""},
		{"rancher url no host", func(r *Record) { r.Cluster.RancherURL = "https:///dashboard" }, "ktags_cluster.rancher_url", ""},
		{"rancher url not a url", func(r *Record) { r.Cluster.RancherURL = "rancher.example.com" }, "ktags_cluster.rancher_url", ""},
		{"rancher url bad port", func(r *Record) { r.Cluster.RancherURL = "https://rancher.example.com:99999" }, "ktags_cluster.rancher_url", ""},
		{"rancher url with query", func(r *Record) { r.Cluster.RancherURL = "https://rancher.example.com/?x=1" }, "ktags_cluster.rancher_url", ""},
		{"direct port zero", func(r *Record) { r.Cluster.Access.Direct.Port = 0 }, "ktags_cluster.access.direct.port", ""},
		{"direct user empty", func(r *Record) { r.Cluster.Access.Direct.User = "" }, "ktags_cluster.access.direct.user", ""},
		{"jump host with port", func(r *Record) { r.Cluster.Access.Jump.Host = "jump:22" }, "ktags_cluster.access.jump.host", ""},
		{"jump port too high", func(r *Record) { r.Cluster.Access.Jump.Port = 70000 }, "ktags_cluster.access.jump.port", ""},
		{"tailscale ref empty", func(r *Record) { r.Cluster.Access.Tailscale.AuthKeyRef = "" }, "ktags_cluster.access.tailscale.auth_key_ref", ""},

		// Secret-looking inline values are refused and never echoed.
		{"tailscale key inline", func(r *Record) { r.Cluster.Access.Tailscale.AuthKeyRef = "tskey-auth-kExample123" },
			"ktags_cluster.access.tailscale.auth_key_ref", "tskey-auth-kExample123"},
		{"rancher url with credentials", func(r *Record) { r.Cluster.RancherURL = "https://admin:hunter2example@rancher.example.com" },
			"ktags_cluster.rancher_url", "hunter2example"},
		{"age identity in name", func(r *Record) { r.Customer.Name = "AGE-SECRET-KEY-1EXAMPLEEXAMPLE" },
			"ktags_customer.name", "AGE-SECRET-KEY-1EXAMPLEEXAMPLE"},
		{"pem block in name", func(r *Record) { r.Cluster.Name = "-----BEGIN EXAMPLE PRIVATE KEY-----" },
			"ktags_cluster.name", "EXAMPLE PRIVATE KEY"},
		{"rke2 token in node name", func(r *Record) { r.Cluster.Nodes[0].Name = "K10abcdef0123456789abcdef::server:example" },
			"ktags_cluster.nodes[0].name", "K10abcdef0123456789abcdef"},
		{"password pair in name", func(r *Record) { r.Customer.Name = "Acme password: example-pass" },
			"ktags_customer.name", "example-pass"},
		{"bearer token in name", func(r *Record) { r.Cluster.Name = "Bearer exampletoken123" },
			"ktags_cluster.name", "exampletoken123"},
		{"secret in jump user", func(r *Record) { r.Cluster.Access.Jump.User = "token=example-leak" },
			"ktags_cluster.access.jump.user", "example-leak"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := sample()
			tt.mutate(&r)
			err := Validate(r)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("Validate accepted the record (err = %v)", err)
			}
			found := false
			for _, p := range verr.Problems {
				if p.Field == tt.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("problems %+v do not name %s", verr.Problems, tt.field)
			}
			if tt.secret != "" && strings.Contains(err.Error(), tt.secret) {
				t.Fatalf("error quotes the secret: %q", err)
			}
			if tt.secret != "" && !strings.Contains(err.Error(), "looks like a secret") {
				t.Fatalf("error %q does not say the value looks like a secret", err)
			}
		})
	}
}

func TestValidateAcceptsEdgeValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{"ipv6 address", func(r *Record) { r.Cluster.Nodes[0].Address = "2001:db8::1" }},
		{"two-char id", func(r *Record) { r.Customer.ID = "a1" }},
		{"forty-char id", func(r *Record) { r.Cluster.ID = strings.Repeat("a", 40) }},
		{"rancher url with port", func(r *Record) { r.Cluster.RancherURL = "https://rancher.example.com:8443" }},
		{"unicode display name", func(r *Record) { r.Customer.Name = "Acme Türkiye A.Ş." }},
		{"no access methods", func(r *Record) { r.Cluster.Access = Access{} }},
		{"staging", func(r *Record) { r.Customer.Environment = EnvStaging }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := sample()
			tt.mutate(&r)
			if err := Validate(r); err != nil {
				t.Fatalf("Validate refused a valid record: %v", err)
			}
		})
	}
}

func TestDeriveID(t *testing.T) {
	tests := []struct{ name, want string }{
		{"Acme Corp", "acme-corp"},
		{"  Globex -- Main  ", "globex-main"},
		{"Acme Türkiye A.Ş.", "acme-t-rkiye-a"},
		{strings.Repeat("ab ", 30), "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-a"},
		{"***", ""},
	}
	for _, tt := range tests {
		if got := DeriveID(tt.name); got != tt.want {
			t.Errorf("DeriveID(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
