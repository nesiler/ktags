package connect

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// sudoProbe proves passwordless sudo and changes nothing.
const sudoProbe = "sudo -n true"

// maxStderr bounds the remote error text kept from the sudo probe.
const maxStderr = 512

// Dialer opens a network connection; net.Dialer.DialContext is one.
type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

// SSHClient is the real SSH adapter. It verifies every host key against the customer's
// known_hosts file, which it only reads (security.md §3: no trust on first use), and logs in
// with the operator's keys, usually from the SSH agent. It never writes a key anywhere.
type SSHClient struct {
	// KnownHosts returns the path of a customer's known_hosts file; required.
	KnownHosts func(customer string) string
	// Signers returns the operator's keys and a release function called once they are no longer
	// needed (an agent connection stays open while it signs); required for Authenticate and Sudo.
	Signers func(ctx context.Context) ([]ssh.Signer, func(), error)
	// Dial opens the TCP connection; nil means a net.Dialer.
	Dial Dialer
}

// Reach resolves the host, opens TCP and verifies the host key. It does not log in.
func (c SSHClient) Reach(ctx context.Context, t SSHTarget) error {
	client, err := c.connect(ctx, t, nil)
	if client != nil {
		_ = client.Close()
	}
	return err
}

// Authenticate logs in with the operator's keys.
func (c SSHClient) Authenticate(ctx context.Context, t SSHTarget) error {
	client, err := c.login(ctx, t)
	if err != nil {
		return err
	}
	return client.Close()
}

// Sudo runs `sudo -n true`, which fails instead of asking for a password.
func (c SSHClient) Sudo(ctx context.Context, t SSHTarget) error {
	client, err := c.login(ctx, t)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	// The session runs without a context; closing the client when ctx ends is what bounds it.
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	session, err := client.NewSession()
	if err != nil {
		return &Failure{Kind: KindSudo, Detail: fmt.Sprintf("cannot open a session on %s: %v", t.address(), err)}
	}
	defer func() { _ = session.Close() }()
	stderr := &capped{max: maxStderr}
	session.Stderr = stderr
	err = session.Run(sudoProbe)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var exit *ssh.ExitError
	switch {
	case errors.As(err, &exit):
		return &Failure{Kind: KindSudo, Detail: fmt.Sprintf("%q exited %d as %s on %s: %s", sudoProbe, exit.ExitStatus(), t.User, t.address(), strings.TrimSpace(stderr.String()))}
	case err != nil:
		return &Failure{Kind: KindSudo, Detail: fmt.Sprintf("%q did not finish as %s on %s: %v", sudoProbe, t.User, t.address(), err)}
	}
	return nil
}

func (c SSHClient) login(ctx context.Context, t SSHTarget) (*ssh.Client, error) {
	if c.Signers == nil {
		return nil, &Failure{Kind: KindSSHAuth, Detail: "no SSH key source configured"}
	}
	signers, release, err := c.Signers(ctx)
	if err != nil {
		return nil, &Failure{Kind: KindSSHAuth, Detail: "cannot read the operator's SSH keys: " + err.Error()}
	}
	defer release()
	if len(signers) == 0 {
		return nil, &Failure{Kind: KindSSHAuth, Detail: "the SSH agent holds no keys"}
	}
	return c.connect(ctx, t, []ssh.AuthMethod{ssh.PublicKeys(signers...)})
}

// connect dials t and runs the SSH handshake, verifying the host key. Without auth methods it
// stops after the host key: the server then refuses the login, which Reach does not need.
func (c SSHClient) connect(ctx context.Context, t SSHTarget, auth []ssh.AuthMethod) (*ssh.Client, error) {
	if c.KnownHosts == nil {
		return nil, &Failure{Kind: KindHostKey, Detail: "no known_hosts file configured"}
	}
	file := c.KnownHosts(t.Customer)
	callback, err := knownhosts.New(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, &Failure{Kind: KindHostKey, Detail: fmt.Sprintf("the customer has no known_hosts file at %s; no host key is pinned", file)}
		}
		return nil, &Failure{Kind: KindHostKey, Detail: fmt.Sprintf("cannot read the known_hosts file %s: %v", file, err)}
	}
	dial := c.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	conn, err := dial(ctx, "tcp", t.address())
	if err != nil {
		return nil, dialFailure(ctx, err)
	}
	// The handshake reads and writes without a context; closing the connection ends it.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	algorithms, err := pinnedAlgorithms(callback, t.address(), conn.RemoteAddr())
	if err != nil {
		_ = conn.Close()
		return nil, &Failure{Kind: KindHostKey, Detail: fmt.Sprintf("%v in %s", err, file)}
	}
	var mu sync.Mutex
	verified, keyErr := false, error(nil)
	config := &ssh.ClientConfig{
		User: t.User,
		Auth: auth,
		// Only the pinned algorithms: a server offering another key type is not a changed key.
		HostKeyAlgorithms: algorithms,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			err := callback(hostname, remote, key)
			mu.Lock()
			defer mu.Unlock()
			verified, keyErr = err == nil, err
			return err
		},
	}
	sc, chans, reqs, err := ssh.NewClientConn(conn, t.address(), config)
	if err == nil {
		return ssh.NewClient(sc, chans, reqs), nil
	}
	_ = conn.Close()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	mu.Lock()
	defer mu.Unlock()
	switch {
	case keyErr != nil:
		return nil, &Failure{Kind: KindHostKey, Detail: hostKeyDetail(keyErr, t.address(), file)}
	case !verified:
		return nil, &Failure{Kind: KindTCP, Detail: fmt.Sprintf("%s closed the connection before the host key was verified: %v", t.address(), err)}
	case auth == nil:
		// The host key is verified; the refused login is expected.
		return nil, nil
	default:
		return nil, &Failure{Kind: KindSSHAuth, Detail: fmt.Sprintf("%s refused the operator's keys for user %s: %v", t.address(), t.User, err)}
	}
}

// probeKey is a key no host has; the known_hosts callback answers it with the pinned keys.
var probeKey = func() ssh.PublicKey {
	k, err := ssh.NewPublicKey(ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)))
	if err != nil {
		panic(err)
	}
	return k
}()

// pinnedAlgorithms lists the host key algorithms of the keys pinned for address, so the server
// presents a key the file can verify. No pinned key is refused before the handshake.
func pinnedAlgorithms(callback ssh.HostKeyCallback, address string, remote net.Addr) ([]string, error) {
	err := callback(address, remote, probeKey)
	var keyErr *knownhosts.KeyError
	if !errors.As(err, &keyErr) {
		return nil, fmt.Errorf("cannot look up the pinned host key of %s: %w", address, err)
	}
	if len(keyErr.Want) == 0 {
		return nil, fmt.Errorf("no host key is pinned for %s", address)
	}
	var out []string
	for _, k := range keyErr.Want {
		switch t := k.Key.Type(); t {
		case ssh.KeyAlgoRSA:
			out = append(out, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA)
		default:
			out = append(out, t)
		}
	}
	return out, nil
}

func hostKeyDetail(err error, address, file string) string {
	var revoked *knownhosts.RevokedError
	var keyErr *knownhosts.KeyError
	switch {
	case errors.As(err, &revoked):
		return fmt.Sprintf("the host key of %s is revoked in %s", address, file)
	case errors.As(err, &keyErr) && len(keyErr.Want) > 0:
		return fmt.Sprintf("the host key of %s does not match the key pinned in %s:%d", address, keyErr.Want[0].Filename, keyErr.Want[0].Line)
	default:
		return fmt.Sprintf("the host key of %s is not verified against %s: %v", address, file, err)
	}
}

// dialFailure classifies a failed dial: a name that does not resolve, or a TCP path that does
// not open. A context error is returned as is; the runner reports it as a timeout or cancel.
func dialFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return &Failure{Kind: KindDNS, Detail: err.Error()}
	}
	return &Failure{Kind: KindTCP, Detail: err.Error()}
}

// capped keeps the first max bytes written to it.
type capped struct {
	max int
	buf bytes.Buffer
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }
