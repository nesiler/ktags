package inventory

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCA is a freshly generated self-signed CA certificate in PEM and the PEM of its private key.
func testCA(t *testing.T) (cert, key string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "acme test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// #65 D2: the public CA fields accept certificates and an https API URL.
func TestValidateAcceptsCAFields(t *testing.T) {
	ca, _ := testCA(t)
	other, _ := testCA(t)
	r := sample()
	r.Cluster.KubeAPIURL = "https://203.0.113.11:6443"
	r.Cluster.KubeCA = ca
	r.Cluster.RancherCA = "\n" + ca + "\n" + other
	if err := Validate(r); err != nil {
		t.Fatalf("Validate = %v", err)
	}
	// Every field is optional: a record without them is the first schema unchanged.
	if err := Validate(sample()); err != nil {
		t.Fatalf("Validate(sample) = %v", err)
	}
}

func TestValidateRefusesCAFields(t *testing.T) {
	ca, key := testCA(t)
	body := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(ca), "-----END CERTIFICATE-----"), "-----BEGIN CERTIFICATE-----")
	tests := []struct {
		name   string
		mutate func(*Record)
		field  string
		// secret must never appear in the error text.
		secret string
	}{
		{"private key as kube CA", func(r *Record) { r.Cluster.KubeAPIURL = "https://203.0.113.11:6443"; r.Cluster.KubeCA = key }, "ktags_cluster.kube_ca", key},
		{"private key after a certificate", func(r *Record) { r.Cluster.RancherCA = ca + key }, "ktags_cluster.rancher_ca", key},
		{"text before the certificate", func(r *Record) { r.Cluster.RancherCA = "token=abc123secretvalue\n" + ca }, "ktags_cluster.rancher_ca", "abc123secretvalue"},
		{"text after the certificate", func(r *Record) { r.Cluster.RancherCA = ca + "password: hunter2value\n" }, "ktags_cluster.rancher_ca", "hunter2value"},
		{"public key block", func(r *Record) {
			r.Cluster.RancherCA = "-----BEGIN PUBLIC KEY-----\n" + body + "\n-----END PUBLIC KEY-----\n"
		}, "ktags_cluster.rancher_ca", ""},
		{"certificate that does not parse", func(r *Record) {
			r.Cluster.RancherCA = "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"
		}, "ktags_cluster.rancher_ca", ""},
		{"certificate with PEM headers", func(r *Record) {
			r.Cluster.RancherCA = strings.Replace(ca, "-----BEGIN CERTIFICATE-----\n", "-----BEGIN CERTIFICATE-----\nProc-Type: 4,ENCRYPTED\n\n", 1)
		}, "ktags_cluster.rancher_ca", ""},
		{"unterminated block", func(r *Record) {
			r.Cluster.RancherCA = strings.TrimSuffix(strings.TrimSpace(ca), "-----END CERTIFICATE-----")
		}, "ktags_cluster.rancher_ca", ""},
		{"only white space", func(r *Record) { r.Cluster.RancherCA = " \n" }, "ktags_cluster.rancher_ca", ""},
		{"bundle too large", func(r *Record) { r.Cluster.RancherCA = strings.Repeat(ca, maxCABytes/len(ca)+1) }, "ktags_cluster.rancher_ca", ""},
		{"kube CA without the API URL", func(r *Record) { r.Cluster.KubeCA = ca }, "ktags_cluster.kube_ca", ""},
		{"kube API over http", func(r *Record) { r.Cluster.KubeAPIURL = "http://203.0.113.11:6443" }, "ktags_cluster.kube_api_url", ""},
		{"kube API with credentials", func(r *Record) { r.Cluster.KubeAPIURL = "https://admin:s3cretpass@203.0.113.11:6443" }, "ktags_cluster.kube_api_url", "s3cretpass"},
		{"kube API with a token query", func(r *Record) { r.Cluster.KubeAPIURL = "https://203.0.113.11:6443/?x=1" }, "ktags_cluster.kube_api_url", ""},
		{"kube API with a bad port", func(r *Record) { r.Cluster.KubeAPIURL = "https://203.0.113.11:70000" }, "ktags_cluster.kube_api_url", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := sample()
			tc.mutate(&r)
			err := Validate(r)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("Validate = %v, want a ValidationError", err)
			}
			found := false
			for _, p := range verr.Problems {
				found = found || p.Field == tc.field
			}
			if !found {
				t.Fatalf("problems %+v, want one on %s", verr.Problems, tc.field)
			}
			if tc.secret != "" && strings.Contains(err.Error(), strings.TrimSpace(tc.secret)) {
				t.Fatalf("the error quotes the refused value: %v", err)
			}
		})
	}
}

// The CA fields survive a save and a strict load; a record without them is written without them.
func TestCAFieldsRoundTrip(t *testing.T) {
	ca, _ := testCA(t)
	r := sample()
	r.Cluster.KubeAPIURL = "https://203.0.113.11:6443"
	r.Cluster.KubeCA = ca
	r.Cluster.RancherCA = ca
	dir := filepath.Join(t.TempDir(), r.Customer.ID)
	if err := Save(context.Background(), dir, r); err != nil {
		t.Fatal(err)
	}
	got, err := Load(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cluster.KubeCA != ca || got.Cluster.RancherCA != ca || got.Cluster.KubeAPIURL != r.Cluster.KubeAPIURL {
		t.Fatalf("loaded cluster %+v, want the saved CA fields", got.Cluster)
	}
	if KnownHostsPath(dir) != filepath.Join(dir, "known_hosts") {
		t.Fatalf("known_hosts path %s", KnownHostsPath(dir))
	}
}

// A private key in a CA field is named a secret, not only a format error.
func TestCAPrivateKeyIsNamedASecret(t *testing.T) {
	ca, key := testCA(t)
	r := sample()
	r.Cluster.RancherCA = ca + key
	err := Validate(r)
	if err == nil || !strings.Contains(err.Error(), "looks like a secret (a private key)") {
		t.Fatalf("Validate = %v, want the private key named a secret", err)
	}
}
