package connect

import "context"

// Script is the scripted outcome of a fake adapter: the error each check returns (nil passes).
type Script struct {
	Endpoint error
	Auth     error
	Authz    error
	// Hang makes the endpoint check wait until its context ends, like a silent network.
	Hang bool
	// HangAuthz makes the authorization check wait until its context ends, like a command
	// that never returns.
	HangAuthz bool
}

func (s Script) authz(ctx context.Context) error {
	if s.HangAuthz {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.Authz
}

func (s Script) endpoint(ctx context.Context) error {
	if s.Hang {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.Endpoint
}

// FakeSSH is an SSH adapter that returns its Script without touching the network.
type FakeSSH struct{ Script }

// Reach returns Script.Endpoint.
func (f FakeSSH) Reach(ctx context.Context, _ SSHTarget) error { return f.endpoint(ctx) }

// Authenticate returns Script.Auth.
func (f FakeSSH) Authenticate(context.Context, SSHTarget) error { return f.Auth }

// Sudo returns Script.Authz.
func (f FakeSSH) Sudo(ctx context.Context, _ SSHTarget) error { return f.authz(ctx) }

// FakeAPI is a Kubernetes or Rancher adapter that returns its Script without touching the network.
type FakeAPI struct{ Script }

// Reach returns Script.Endpoint.
func (f FakeAPI) Reach(ctx context.Context, _ APITarget) error { return f.endpoint(ctx) }

// Authenticate returns Script.Auth.
func (f FakeAPI) Authenticate(context.Context, APITarget) error { return f.Auth }

// Authorize returns Script.Authz.
func (f FakeAPI) Authorize(ctx context.Context, _ APITarget) error { return f.authz(ctx) }
