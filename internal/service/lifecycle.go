package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

const (
	defaultTimeout = 10 * time.Second
	defaultPoll    = 50 * time.Millisecond
)

// Launcher starts the service as a process of its own, outside the client that asks for it.
type Launcher interface {
	// Launch requests a start in the background. It returns once the request is made, not
	// once the service answers.
	Launch(ctx context.Context) error
	// Unload removes what Launch set up, after the service has stopped. It succeeds when
	// nothing is loaded.
	Unload(ctx context.Context) error
}

// Status is what the lifecycle commands report about the service.
type Status struct {
	Running    bool
	Socket     string
	Protocol   int
	PID        int
	Service    string
	Version    string
	ActiveRuns int
	// StaleAgent is set when the launcher's installed definition differs from the one this
	// build would write; the running service keeps the old one until it is stopped and started.
	StaleAgent bool
}

// staler is a Launcher whose installed definition can differ from the one Launch would write.
type staler interface {
	Stale() (bool, error)
}

// StaleFix replaces a stale launcher definition. Stop refuses while runs are active, so the
// fix never interrupts work.
const StaleFix = "ktags service stop && ktags service start"

// StopResult is the outcome of a stop.
type StopResult struct {
	// WasRunning is false when no service answered; the stop then only unloaded.
	WasRunning bool
	// Cancelled are the runs the stop cancelled, in their final state.
	Cancelled []RunInfo
}

// Lifecycle starts, inspects and stops the service on one socket. Start is idempotent: a
// service that answers is reported, never launched again.
type Lifecycle struct {
	Socket   string
	Launcher Launcher
	// Timeout bounds the wait for a launched service to answer and for a stopped one to go
	// away. Defaults to 10s.
	Timeout time.Duration
	// Poll is the interval of those waits. Defaults to 50ms.
	Poll time.Duration
}

// Status reports the service. A service that cannot be reached is not running, which is not an
// error; a service that answers wrongly (another protocol version, another owner) or not at all
// within Timeout is.
func (l Lifecycle) Status(ctx context.Context) (Status, error) {
	st := Status{Socket: l.Socket}
	timeout := l.timeout()
	helloCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	hello, err := Client{Socket: l.Socket}.Hello(helloCtx)
	if err != nil {
		var pe *Error
		switch {
		case ctx.Err() != nil:
			return st, ctx.Err()
		case helloCtx.Err() != nil:
			return st, &Error{Code: CodeUnavailable, Message: fmt.Sprintf("the ktags service on %s did not answer within %s", l.Socket, timeout), Hint: "end the silent service with SIGTERM to the PID in " + filepath.Join(filepath.Dir(l.Socket), lockName), err: err}
		case errors.As(err, &pe) && pe.Code == CodeUnavailable:
			return st, nil
		}
		return st, err
	}
	st.Running = true
	// An unreadable definition is left to ktags doctor, which names its own fix.
	if s, ok := l.Launcher.(staler); ok {
		if stale, err := s.Stale(); err == nil {
			st.StaleAgent = stale
		}
	}
	st.Protocol = ProtocolVersion
	st.PID = hello.PID
	st.Service = hello.Service
	st.Version = hello.Version
	st.ActiveRuns = hello.ActiveRuns
	return st, nil
}

func (l Lifecycle) timeout() time.Duration {
	if l.Timeout <= 0 {
		return defaultTimeout
	}
	return l.Timeout
}

// Start launches the service unless one already answers, and waits until it does. started
// reports whether this call launched it.
func (l Lifecycle) Start(ctx context.Context) (st Status, started bool, err error) {
	st, err = l.Status(ctx)
	if err != nil || st.Running {
		return st, false, err
	}
	if l.Launcher == nil {
		return st, false, &Error{Code: CodeInternal, Message: "no way to launch the ktags service is configured", Hint: "report this as a bug"}
	}
	if err := l.Launcher.Launch(ctx); err != nil {
		return st, false, err
	}
	st, err = l.await(ctx, true)
	return st, err == nil, err
}

// Stop stops the service. While runs are active the service refuses, unless cancelRuns is set;
// then the runs are cancelled and their results recorded before the service exits. After the
// service has gone, the launcher unloads it. Stopping a service that is not running succeeds.
func (l Lifecycle) Stop(ctx context.Context, cancelRuns bool) (StopResult, error) {
	st, err := l.Status(ctx)
	if err != nil {
		return StopResult{}, err
	}
	var res StopResult
	if st.Running {
		runs, err := Client{Socket: l.Socket}.Stop(ctx, cancelRuns)
		if err != nil {
			return res, err
		}
		res = StopResult{WasRunning: true, Cancelled: runs}
		if _, err := l.await(ctx, false); err != nil {
			return res, err
		}
	}
	if l.Launcher != nil {
		if err := l.Launcher.Unload(ctx); err != nil {
			return res, err
		}
	}
	return res, nil
}

// await polls the service until its running state is want, for at most Timeout. An answer
// that is an error (another protocol version, silence) ends the wait at once.
func (l Lifecycle) await(ctx context.Context, want bool) (Status, error) {
	timeout, poll := l.timeout(), l.Poll
	if poll <= 0 {
		poll = defaultPoll
	}
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		st, err := l.Status(ctx)
		// Status returns the caller's cancellation as its error.
		switch {
		case err != nil:
			return st, err
		case st.Running == want:
			return st, nil
		case time.Now().After(deadline):
			return st, l.timedOut(want, timeout)
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (l Lifecycle) timedOut(want bool, timeout time.Duration) error {
	if want {
		return &Error{Code: CodeUnavailable, Message: fmt.Sprintf("the ktags service did not answer on %s within %s of its launch", l.Socket, timeout), Hint: "run `ktags service run` in a terminal to see why it does not start"}
	}
	return &Error{Code: CodeUnavailable, Message: fmt.Sprintf("the ktags service accepted the stop but still answers on %s after %s", l.Socket, timeout), Hint: "check `ktags service status` again; if it keeps answering, report this as a bug"}
}

// NoLauncher is the launcher of platforms without a service manager integration yet; systemd
// user services can take its place later without changing the client contract (ADR-0001).
type NoLauncher struct {
	// GOOS names the platform in the refusal.
	GOOS string
}

// Launch refuses and names the foreground command.
func (n NoLauncher) Launch(context.Context) error {
	return &Error{Code: CodeUnavailable, Message: "starting the ktags service in the background is supported on macOS (launchd) only; this system is " + n.GOOS, Hint: "run `ktags service run` under your own service manager"}
}

// Unload has nothing to remove.
func (NoLauncher) Unload(context.Context) error { return nil }
