package connect

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

// maxBody bounds what Reach reads of an answer before closing it.
const maxBody = 64 << 10

// HTTPSAPI is the real adapter for a Kubernetes API server or a Rancher server. Reach verifies
// TLS against the target's pinned CA, or the system trust store when it has none; it never
// skips verification. It holds no credentials: authentication and authorization need the
// secret store's read side, so they report not configured until it exists (#65 D3).
type HTTPSAPI struct {
	// Kind is the failure kind of a server that answers wrongly: KindKubeAPI or KindRancherAPI.
	Kind Kind
	// Path is what Reach requests, for example "/version" or "/ping".
	Path string
	// Dial opens the TCP connection; nil means a net.Dialer.
	Dial Dialer
}

// Reach gets an answer from the server over verified TLS. Any HTTP answer below 500 counts:
// an unauthenticated request may be refused and still proves the server is there.
func (a HTTPSAPI) Reach(ctx context.Context, t APITarget) error {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if t.CA != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(t.CA)) {
			return &Failure{Kind: a.Kind, Detail: "the pinned CA of " + t.Customer + " holds no certificate"}
		}
		cfg.RootCAs = pool
	}
	dial := a.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	transport := &http.Transport{
		// No proxy from the environment: the check measures the laptop's own path.
		Proxy:             nil,
		DialContext:       dial,
		TLSClientConfig:   cfg,
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		// A redirect is an answer; following it would measure another server.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	target := strings.TrimRight(t.URL, "/") + a.Path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return &Failure{Kind: a.Kind, Detail: "cannot build the request: " + err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return a.failure(ctx, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
	_ = resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		return &Failure{Kind: a.Kind, Detail: fmt.Sprintf("%s answered HTTP %d", target, resp.StatusCode)}
	}
	return nil
}

// Authenticate is not configured: the credentials live in the secret store.
func (a HTTPSAPI) Authenticate(context.Context, APITarget) error {
	return fmt.Errorf("%w: the credentials come from the secret store, which ktags cannot read yet", ErrNotConfigured)
}

// Authorize is not configured: it needs the credentials Authenticate lacks.
func (a HTTPSAPI) Authorize(context.Context, APITarget) error {
	return fmt.Errorf("%w: the credentials come from the secret store, which ktags cannot read yet", ErrNotConfigured)
}

// failure classifies a failed request: DNS, the TCP path, or a server that does not speak
// verified TLS (the API kind).
func (a HTTPSAPI) failure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var dns *net.DNSError
	var op *net.OpError
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &dns):
		return &Failure{Kind: KindDNS, Detail: err.Error()}
	case errors.As(err, &verify), errors.As(err, &unknown), errors.As(err, &host), errors.As(err, &invalid):
		return &Failure{Kind: a.Kind, Detail: "TLS verification failed: " + err.Error()}
	case errors.As(err, &op) && op.Op == "dial":
		return &Failure{Kind: KindTCP, Detail: err.Error()}
	}
	return &Failure{Kind: a.Kind, Detail: err.Error()}
}
