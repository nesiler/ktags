package connect

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/nesiler/ktags/internal/mask"
)

// The contract suite (#65-K2): every scenario states the results the Runner must produce, and
// both the scripted fake adapter and the real adapter against a local server must produce them.
// No test here leaves the loopback interface.

// contractTimeout bounds one check in the suite; the hang scenarios wait for it.
const contractTimeout = 750 * time.Millisecond

// outcome is the part of a Result the contract fixes.
type outcome struct {
	Check   string
	Status  Status
	Kind    Kind
	Runbook string
}

func project(rs []Result) []outcome {
	out := make([]outcome, 0, len(rs))
	for _, r := range rs {
		out = append(out, outcome{r.Check, r.Status, r.Kind, r.Runbook})
	}
	return out
}

func contractRunner(t *testing.T, ssh SSH, api API) *Runner {
	t.Helper()
	r, err := New(Options{SSH: ssh, Kubernetes: api, Rancher: api, Redact: mask.Mask, Now: time.Now, Timeout: contractTimeout})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// --- local SSH server ---

type sshServerOptions struct {
	// authorized is the one key the server accepts; nil accepts none.
	authorized ssh.PublicKey
	sudoExit   uint32
	sudoStderr string
	// extraHostKeys are offered besides the pinned ed25519 key.
	extraHostKeys []ssh.Signer
	// rejectSession refuses every session; noExit ends the command without an exit status;
	// hangExec never ends it.
	rejectSession, noExit, hangExec bool
}

type sshServer struct {
	addr    string
	hostKey ssh.Signer
}

func newEd25519(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newECDSA(t *testing.T) ssh.Signer {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// listen serves every accepted connection with serve until the test ends.
func listen(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(wg.Wait)
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			wg.Go(func() { serve(c) })
		}
	})
	return ln.Addr().String()
}

// closedAddr is a loopback address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// silentAddr accepts TCP and never answers, like a filtered path after the handshake.
func silentAddr(t *testing.T) string {
	t.Helper()
	return listen(t, func(c net.Conn) { _, _ = io.Copy(io.Discard, c) })
}

func startSSH(t *testing.T, o sshServerOptions) sshServer {
	t.Helper()
	host := newEd25519(t)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if o.authorized != nil && bytes.Equal(key.Marshal(), o.authorized.Marshal()) {
			return &ssh.Permissions{}, nil
		}
		return nil, errors.New("key not authorized")
	}}
	for _, k := range o.extraHostKeys {
		cfg.AddHostKey(k)
	}
	cfg.AddHostKey(host)
	addr := listen(t, func(c net.Conn) { serveSSH(c, cfg, o) })
	return sshServer{addr: addr, hostKey: host}
}

func serveSSH(c net.Conn, cfg *ssh.ServerConfig, o sshServerOptions) {
	defer func() { _ = c.Close() }()
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" || o.rejectSession {
			_ = nc.Reject(ssh.UnknownChannelType, "session only")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			return
		}
		for req := range creqs {
			if req.Type != "exec" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			if o.hangExec {
				continue
			}
			if o.noExit {
				_ = ch.Close()
				break
			}
			if o.sudoStderr != "" {
				_, _ = io.WriteString(ch.Stderr(), o.sudoStderr)
			}
			_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{o.sudoExit}))
			_ = ch.Close()
			break
		}
	}
}

func writeKnownHosts(t *testing.T, lines ...string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func targetAt(t *testing.T, addr string) SSHTarget {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return SSHTarget{Customer: "acme", Node: "srv-1", Host: host, Port: p, User: "ops"}
}

func staticSigners(s ...ssh.Signer) func(context.Context) ([]ssh.Signer, func(), error) {
	return func(context.Context) ([]ssh.Signer, func(), error) { return s, func() {}, nil }
}

func dnsDialer(context.Context, string, string) (net.Conn, error) {
	return nil, &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "srv-1.example.test", IsNotFound: true}}
}

// sshReal is a real SSH adapter set up for one scenario.
type sshReal func(t *testing.T) (SSHClient, SSHTarget)

// pinned is a server that authorises the operator's key, pinned in known_hosts.
func pinned(o sshServerOptions) sshReal {
	return func(t *testing.T) (SSHClient, SSHTarget) {
		op := newEd25519(t)
		if o.authorized == nil {
			o.authorized = op.PublicKey()
		}
		srv := startSSH(t, o)
		file := writeKnownHosts(t, knownhosts.Line([]string{srv.addr}, srv.hostKey.PublicKey()))
		return SSHClient{KnownHosts: func(string) string { return file }, Signers: staticSigners(op)}, targetAt(t, srv.addr)
	}
}

var (
	sshAllOK = []outcome{
		{"ssh.endpoint", StatusOK, "", ""},
		{"ssh.auth", StatusOK, "", ""},
		{"ssh.sudo", StatusOK, "", ""},
	}
	sshEndpointFails = func(kind Kind, runbook string) []outcome {
		return []outcome{
			{"ssh.endpoint", StatusFailed, kind, runbook},
			{"ssh.auth", StatusSkipped, "", runbook},
			{"ssh.sudo", StatusSkipped, "", runbook},
		}
	}
)

func TestContractSSH(t *testing.T) {
	cases := []struct {
		name string
		fake Script
		real sshReal
		want []outcome
	}{
		{"all ok", Script{}, pinned(sshServerOptions{}), sshAllOK},
		{
			// The client asks only for the pinned algorithm; a server that also has other key
			// types is not a changed key.
			"server with more host key types", Script{},
			pinned(sshServerOptions{extraHostKeys: []ssh.Signer{newECDSA(t)}}), sshAllOK,
		},
		{
			"host key changed", Script{Endpoint: &Failure{Kind: KindHostKey, Detail: "mismatch"}},
			func(t *testing.T) (SSHClient, SSHTarget) {
				op := newEd25519(t)
				srv := startSSH(t, sshServerOptions{authorized: op.PublicKey()})
				file := writeKnownHosts(t, knownhosts.Line([]string{srv.addr}, newEd25519(t).PublicKey()))
				return SSHClient{KnownHosts: func(string) string { return file }, Signers: staticSigners(op)}, targetAt(t, srv.addr)
			},
			sshEndpointFails(KindHostKey, RunbookHostKeyChanged),
		},
		{
			"host not pinned", Script{Endpoint: &Failure{Kind: KindHostKey, Detail: "unknown"}},
			func(t *testing.T) (SSHClient, SSHTarget) {
				op := newEd25519(t)
				srv := startSSH(t, sshServerOptions{authorized: op.PublicKey()})
				file := writeKnownHosts(t, knownhosts.Line([]string{"203.0.113.99"}, srv.hostKey.PublicKey()))
				return SSHClient{KnownHosts: func(string) string { return file }, Signers: staticSigners(op)}, targetAt(t, srv.addr)
			},
			sshEndpointFails(KindHostKey, RunbookHostKeyChanged),
		},
		{
			"no known_hosts file", Script{Endpoint: &Failure{Kind: KindHostKey, Detail: "no file"}},
			func(t *testing.T) (SSHClient, SSHTarget) {
				op := newEd25519(t)
				srv := startSSH(t, sshServerOptions{authorized: op.PublicKey()})
				missing := filepath.Join(t.TempDir(), "known_hosts")
				return SSHClient{KnownHosts: func(string) string { return missing }, Signers: staticSigners(op)}, targetAt(t, srv.addr)
			},
			sshEndpointFails(KindHostKey, RunbookHostKeyChanged),
		},
		{
			"connection refused", Script{Endpoint: &Failure{Kind: KindTCP, Detail: "refused"}},
			func(t *testing.T) (SSHClient, SSHTarget) {
				addr := closedAddr(t)
				file := writeKnownHosts(t, knownhosts.Line([]string{addr}, newEd25519(t).PublicKey()))
				return SSHClient{KnownHosts: func(string) string { return file }}, targetAt(t, addr)
			},
			sshEndpointFails(KindTCP, RunbookSSHUnreachable),
		},
		{
			"name does not resolve", Script{Endpoint: &Failure{Kind: KindDNS, Detail: "no such host"}},
			func(t *testing.T) (SSHClient, SSHTarget) {
				file := writeKnownHosts(t)
				return SSHClient{KnownHosts: func(string) string { return file }, Dial: dnsDialer}, SSHTarget{Customer: "acme", Node: "srv-1", Host: "srv-1.example.test", Port: 22, User: "ops"}
			},
			sshEndpointFails(KindDNS, RunbookSSHUnreachable),
		},
		{
			"silent network", Script{Hang: true},
			func(t *testing.T) (SSHClient, SSHTarget) {
				addr := silentAddr(t)
				file := writeKnownHosts(t, knownhosts.Line([]string{addr}, newEd25519(t).PublicKey()))
				return SSHClient{KnownHosts: func(string) string { return file }}, targetAt(t, addr)
			},
			sshEndpointFails(KindTimeout, RunbookSSHUnreachable),
		},
		{
			"key refused", Script{Auth: &Failure{Kind: KindSSHAuth, Detail: "refused"}},
			func(t *testing.T) (SSHClient, SSHTarget) {
				client, target := pinned(sshServerOptions{authorized: newEd25519(t).PublicKey()})(t)
				return client, target
			},
			[]outcome{
				{"ssh.endpoint", StatusOK, "", ""},
				{"ssh.auth", StatusFailed, KindSSHAuth, RunbookSSHAuthFailed},
				{"ssh.sudo", StatusSkipped, "", RunbookSSHAuthFailed},
			},
		},
		{
			"server hangs up before the key exchange", Script{Endpoint: &Failure{Kind: KindTCP, Detail: "closed"}},
			func(t *testing.T) (SSHClient, SSHTarget) {
				addr := listen(t, func(c net.Conn) { _ = c.Close() })
				file := writeKnownHosts(t, knownhosts.Line([]string{addr}, newEd25519(t).PublicKey()))
				return SSHClient{KnownHosts: func(string) string { return file }}, targetAt(t, addr)
			},
			sshEndpointFails(KindTCP, RunbookSSHUnreachable),
		},
		{
			"session refused", Script{Authz: &Failure{Kind: KindSudo, Detail: "no session"}},
			pinned(sshServerOptions{rejectSession: true}),
			[]outcome{{"ssh.endpoint", StatusOK, "", ""}, {"ssh.auth", StatusOK, "", ""}, {"ssh.sudo", StatusFailed, KindSudo, RunbookSSHSudoMissing}},
		},
		{
			"sudo ends without an exit status", Script{Authz: &Failure{Kind: KindSudo, Detail: "no exit status"}},
			pinned(sshServerOptions{noExit: true}),
			[]outcome{{"ssh.endpoint", StatusOK, "", ""}, {"ssh.auth", StatusOK, "", ""}, {"ssh.sudo", StatusFailed, KindSudo, RunbookSSHSudoMissing}},
		},
		{
			"sudo never answers", Script{HangAuthz: true},
			pinned(sshServerOptions{hangExec: true}),
			[]outcome{{"ssh.endpoint", StatusOK, "", ""}, {"ssh.auth", StatusOK, "", ""}, {"ssh.sudo", StatusFailed, KindTimeout, RunbookSSHUnreachable}},
		},
		{
			"no passwordless sudo", Script{Authz: &Failure{Kind: KindSudo, Detail: "password required"}},
			pinned(sshServerOptions{sudoExit: 1, sudoStderr: "sudo: a password is required"}),
			[]outcome{
				{"ssh.endpoint", StatusOK, "", ""},
				{"ssh.auth", StatusOK, "", ""},
				{"ssh.sudo", StatusFailed, KindSudo, RunbookSSHSudoMissing},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/fake", func(t *testing.T) {
			sshTarget := SSHTarget{Customer: "acme", Node: "srv-1", Host: "203.0.113.11", Port: 22, User: "ops"}
			res, err := contractRunner(t, FakeSSH{Script: tc.fake}, FakeAPI{}).SSH(context.Background(), sshTarget)
			assertOutcomes(t, res, err, tc.want)
		})
		t.Run(tc.name+"/real", func(t *testing.T) {
			client, target := tc.real(t)
			res, err := contractRunner(t, client, FakeAPI{}).SSH(context.Background(), target)
			assertOutcomes(t, res, err, tc.want)
		})
	}
}

func assertOutcomes(t *testing.T, res []Result, err error, want []outcome) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	got := project(res)
	if len(got) != len(want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("result %d: got %+v (detail %q)\nwant %+v", i, got[i], res[i].Detail, want[i])
		}
	}
}

// --- local HTTPS servers ---

func pemOf(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

// otherCA is a self-signed CA no test server chains to.
func otherCA(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "other test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func tlsServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

type apiReal func(t *testing.T) (HTTPSAPI, APITarget)

func served(h http.HandlerFunc, ca func(t *testing.T, srv *httptest.Server) string) apiReal {
	return func(t *testing.T) (HTTPSAPI, APITarget) {
		srv := tlsServer(t, h)
		return HTTPSAPI{Kind: KindKubeAPI, Path: "/version"}, APITarget{Customer: "acme", URL: srv.URL, CA: ca(t, srv)}
	}
}

func serverCA(_ *testing.T, srv *httptest.Server) string { return pemOf(srv.Certificate()) }

func TestContractKubernetes(t *testing.T) {
	notConfigured := fmtNotConfigured()
	answered := []outcome{
		{"kube.endpoint", StatusOK, "", ""},
		{"kube.auth", StatusNotConfigured, "", RunbookKubeNotConfigured},
		{"kube.authz", StatusSkipped, "", RunbookKubeNotConfigured},
	}
	endpointFails := func(kind Kind) []outcome {
		return []outcome{
			{"kube.endpoint", StatusFailed, kind, RunbookKubeAPIUnreachable},
			{"kube.auth", StatusSkipped, "", RunbookKubeAPIUnreachable},
			{"kube.authz", StatusSkipped, "", RunbookKubeAPIUnreachable},
		}
	}
	apiFailure := &Failure{Kind: KindKubeAPI, Detail: "x"}
	cases := []struct {
		name string
		fake Script
		real apiReal
		want []outcome
	}{
		// An unauthenticated request refused with 401 still proves the server is there.
		{"answers over pinned TLS", Script{Auth: notConfigured}, served(status(http.StatusUnauthorized), serverCA), answered},
		{"redirect is an answer", Script{Auth: notConfigured}, served(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://"+closedAddr(t)+"/", http.StatusFound)
		}, serverCA), answered},
		{"certificate from another CA", Script{Endpoint: apiFailure}, served(status(http.StatusOK), func(t *testing.T, _ *httptest.Server) string { return otherCA(t) }), endpointFails(KindKubeAPI)},
		// Without a pinned CA the system trust store decides; a private CA fails, never passes.
		{"no pinned CA, private certificate", Script{Endpoint: apiFailure}, served(status(http.StatusOK), func(*testing.T, *httptest.Server) string { return "" }), endpointFails(KindKubeAPI)},
		{"pinned CA without a certificate", Script{Endpoint: apiFailure}, served(status(http.StatusOK), func(*testing.T, *httptest.Server) string { return "not a pem block" }), endpointFails(KindKubeAPI)},
		// 500 is the first answer that is not a healthy server; 499 still is one.
		{"server error", Script{Endpoint: apiFailure}, served(status(http.StatusInternalServerError), serverCA), endpointFails(KindKubeAPI)},
		{"highest client error", Script{Auth: notConfigured}, served(status(499), serverCA), answered},
		{"server hangs up", Script{Endpoint: apiFailure}, func(t *testing.T) (HTTPSAPI, APITarget) {
			addr := listen(t, func(c net.Conn) { _ = c.Close() })
			return HTTPSAPI{Kind: KindKubeAPI, Path: "/version"}, APITarget{Customer: "acme", URL: "https://" + addr}
		}, endpointFails(KindKubeAPI)},
		{"server offers only TLS 1.1", Script{Endpoint: apiFailure}, func(t *testing.T) (HTTPSAPI, APITarget) {
			srv := httptest.NewUnstartedServer(status(http.StatusOK))
			srv.TLS = &tls.Config{MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11}
			srv.StartTLS()
			t.Cleanup(srv.Close)
			return HTTPSAPI{Kind: KindKubeAPI, Path: "/version"}, APITarget{Customer: "acme", URL: srv.URL, CA: pemOf(srv.Certificate())}
		}, endpointFails(KindKubeAPI)},
		{"connection refused", Script{Endpoint: &Failure{Kind: KindTCP, Detail: "refused"}}, func(t *testing.T) (HTTPSAPI, APITarget) {
			return HTTPSAPI{Kind: KindKubeAPI, Path: "/version"}, APITarget{Customer: "acme", URL: "https://" + closedAddr(t)}
		}, endpointFails(KindTCP)},
		{"name does not resolve", Script{Endpoint: &Failure{Kind: KindDNS, Detail: "no such host"}}, func(*testing.T) (HTTPSAPI, APITarget) {
			return HTTPSAPI{Kind: KindKubeAPI, Path: "/version", Dial: dnsDialer}, APITarget{Customer: "acme", URL: "https://kube.example.test:6443"}
		}, endpointFails(KindDNS)},
		{"silent network", Script{Hang: true}, func(t *testing.T) (HTTPSAPI, APITarget) {
			return HTTPSAPI{Kind: KindKubeAPI, Path: "/version"}, APITarget{Customer: "acme", URL: "https://" + silentAddr(t)}
		}, endpointFails(KindTimeout)},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/fake", func(t *testing.T) {
			res, err := contractRunner(t, FakeSSH{}, FakeAPI{Script: tc.fake}).Kubernetes(context.Background(), APITarget{Customer: "acme", URL: "https://203.0.113.11:6443"})
			assertOutcomes(t, res, err, tc.want)
		})
		t.Run(tc.name+"/real", func(t *testing.T) {
			api, target := tc.real(t)
			res, err := contractRunner(t, FakeSSH{}, api).Kubernetes(context.Background(), target)
			assertOutcomes(t, res, err, tc.want)
		})
	}
}

func fmtNotConfigured() error { return errors.Join(ErrNotConfigured) }

// Rancher uses the same adapter with its own kind and path.
func TestContractRancher(t *testing.T) {
	var path string
	srv := tlsServer(t, func(w http.ResponseWriter, r *http.Request) { path = r.URL.Path; _, _ = io.WriteString(w, "pong") })
	api := HTTPSAPI{Kind: KindRancherAPI, Path: "/ping"}
	want := []outcome{
		{"rancher.endpoint", StatusOK, "", ""},
		{"rancher.auth", StatusNotConfigured, "", RunbookRancherNotConfigured},
		{"rancher.authz", StatusSkipped, "", RunbookRancherNotConfigured},
	}
	res, err := contractRunner(t, FakeSSH{}, api).Rancher(context.Background(), APITarget{Customer: "acme", URL: srv.URL + "/", CA: pemOf(srv.Certificate())})
	assertOutcomes(t, res, err, want)
	if path != "/ping" {
		t.Fatalf("requested %q, want /ping", path)
	}
	res, err = contractRunner(t, FakeSSH{}, FakeAPI{Script: Script{Auth: ErrNotConfigured}}).Rancher(context.Background(), rancTarget)
	assertOutcomes(t, res, err, want)

	res, err = contractRunner(t, FakeSSH{}, api).Rancher(context.Background(), APITarget{Customer: "acme", URL: srv.URL, CA: otherCA(t)})
	assertOutcomes(t, res, err, []outcome{
		{"rancher.endpoint", StatusFailed, KindRancherAPI, RunbookRancherUnreachable},
		{"rancher.auth", StatusSkipped, "", RunbookRancherUnreachable},
		{"rancher.authz", StatusSkipped, "", RunbookRancherUnreachable},
	})
}

// A secret the remote side prints into an error never reaches a result.
func TestRealSSHDetailIsRedacted(t *testing.T) {
	client, target := pinned(sshServerOptions{sudoExit: 1, sudoStderr: "sudo: token=s3cr3tvalue123 rejected"})(t)
	res, err := contractRunner(t, client, FakeAPI{}).SSH(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	sudo := res[2]
	if sudo.Status != StatusFailed || !strings.Contains(sudo.Detail, "exited 1") {
		t.Fatalf("sudo result %+v, want a failure that names the exit status", sudo)
	}
	if strings.Contains(sudo.Detail, "s3cr3tvalue123") {
		t.Fatalf("sudo detail leaks the secret: %q", sudo.Detail)
	}
}

// Reach never logs in: a server that refuses every key still passes the endpoint check, and the
// agent is not asked for keys.
func TestRealSSHReachDoesNotLogIn(t *testing.T) {
	srv := startSSH(t, sshServerOptions{})
	file := writeKnownHosts(t, knownhosts.Line([]string{srv.addr}, srv.hostKey.PublicKey()))
	asked := false
	client := SSHClient{KnownHosts: func(string) string { return file }, Signers: func(context.Context) ([]ssh.Signer, func(), error) {
		asked = true
		return nil, func() {}, nil
	}}
	if err := client.Reach(context.Background(), targetAt(t, srv.addr)); err != nil {
		t.Fatalf("Reach = %v, want nil", err)
	}
	if asked {
		t.Fatal("Reach asked the agent for keys")
	}
}

// An agent without keys or an unreadable agent is an authentication failure, not unclassified.
func TestRealSSHWithoutKeys(t *testing.T) {
	srv := startSSH(t, sshServerOptions{})
	file := writeKnownHosts(t, knownhosts.Line([]string{srv.addr}, srv.hostKey.PublicKey()))
	for name, tc := range map[string]struct {
		signers func(context.Context) ([]ssh.Signer, func(), error)
		detail  string
	}{
		"empty agent":   {staticSigners(), "the SSH agent holds no keys"},
		"agent error":   {func(context.Context) ([]ssh.Signer, func(), error) { return nil, nil, errors.New("agent socket gone") }, "agent socket gone"},
		"no key source": {nil, "no SSH key source configured"},
	} {
		t.Run(name, func(t *testing.T) {
			client := SSHClient{KnownHosts: func(string) string { return file }, Signers: tc.signers}
			err := client.Authenticate(context.Background(), targetAt(t, srv.addr))
			var f *Failure
			if !errors.As(err, &f) || f.Kind != KindSSHAuth || !strings.Contains(f.Detail, tc.detail) {
				t.Fatalf("Authenticate = %v, want an %s failure saying %q", err, KindSSHAuth, tc.detail)
			}
		})
	}
}
