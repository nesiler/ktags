package connect

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// A host key lookup that is not a key mismatch is refused, so no algorithm list is guessed:
// the probe key matching a pinned key, or the lookup failing on its own.
func TestPinnedAlgorithmsRefusesAnUnexpectedLookup(t *testing.T) {
	for name, err := range map[string]error{
		"probe key accepted": nil,
		"lookup failed":      errors.New("lookup failed"),
	} {
		t.Run(name, func(t *testing.T) {
			callback := func(string, net.Addr, ssh.PublicKey) error { return err }
			algos, got := pinnedAlgorithms(callback, "203.0.113.10:22", &net.TCPAddr{})
			if got == nil || algos != nil || !strings.Contains(got.Error(), "cannot look up the pinned host key") {
				t.Fatalf("pinnedAlgorithms = %v, %v, want a refusal", algos, got)
			}
		})
	}
}

// A URL the request cannot be built from is a failure of the API check, not a crash.
func TestHTTPSUnbuildableRequest(t *testing.T) {
	api := HTTPSAPI{Kind: KindKubeAPI, Path: "/version"}
	err := api.Reach(context.Background(), APITarget{Customer: "acme", URL: "https://kube\x7f.example.test:6443"})
	var f *Failure
	if !errors.As(err, &f) || f.Kind != KindKubeAPI || !strings.Contains(f.Detail, "cannot build the request") {
		t.Fatalf("Reach = %v, want a %s failure saying the request cannot be built", err, KindKubeAPI)
	}
}

// The host key is never verified against nothing: no known_hosts source and an unreadable
// file are host-key failures before any network use.
func TestRealSSHRefusesWithoutPinnedKeys(t *testing.T) {
	dialled := false
	dial := func(context.Context, string, string) (net.Conn, error) {
		dialled = true
		return nil, errors.New("dialled")
	}
	malformed := writeKnownHosts(t, "this is not a known_hosts line")
	for name, client := range map[string]SSHClient{
		"no known_hosts source": {Dial: dial},
		"malformed known_hosts": {KnownHosts: func(string) string { return malformed }, Dial: dial},
	} {
		t.Run(name, func(t *testing.T) {
			err := client.Reach(context.Background(), sshTarget)
			var f *Failure
			if !errors.As(err, &f) || f.Kind != KindHostKey {
				t.Fatalf("Reach = %v, want a %s failure", err, KindHostKey)
			}
		})
	}
	if dialled {
		t.Fatal("the adapter dialled without a pinned key source")
	}
}

// The sudo probe keeps at most maxStderr bytes of remote text and never fails the write.
func TestCappedStderr(t *testing.T) {
	c := &capped{max: maxStderr}
	for range 3 {
		if n, err := c.Write([]byte(strings.Repeat("x", 300))); n != 300 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	if got := len(c.String()); got != maxStderr {
		t.Fatalf("kept %d bytes, want %d", got, maxStderr)
	}
}

// A failed TLS check says why: a pinned CA without a certificate, or a certificate that does
// not chain to the pinned CA.
func TestHTTPSFailureDetails(t *testing.T) {
	srv := tlsServer(t, status(200))
	api := HTTPSAPI{Kind: KindKubeAPI, Path: "/version"}
	for ca, want := range map[string]string{
		"not a pem block": "holds no certificate",
		otherCA(t):        "TLS verification failed",
	} {
		err := api.Reach(context.Background(), APITarget{Customer: "acme", URL: srv.URL, CA: ca})
		var f *Failure
		if !errors.As(err, &f) || f.Kind != KindKubeAPI || !strings.Contains(f.Detail, want) {
			t.Fatalf("Reach = %v, want a %s failure saying %q", err, KindKubeAPI, want)
		}
	}
}
