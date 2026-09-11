package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/nesiler/ktags/internal/launchd"
	"github.com/nesiler/ktags/internal/service"
)

// restartFix rewrites and reloads the launchd definition: stop unloads the job, and start then
// writes this build's definition and loads it. Start alone leaves a loaded job as it is.
const restartFix = service.StaleFix

func init() {
	register(Check{ID: "service.agent", Title: "the launchd agent points at an existing ktags binary", Order: 30, Run: checkAgent})
	register(Check{ID: "service.socket", Title: "the ktags service answers on its socket", Order: 31, Run: checkSocket})
	register(Check{ID: "service.protocol", Title: "the ktags service speaks this CLI's protocol", Order: 32, Run: checkProtocol})
}

func startFix(env Env) string {
	if env.Foreground {
		return "ktags service run"
	}
	return "ktags service start"
}

// checkAgent reads the launchd definition; it asks launchctl nothing.
func checkAgent(_ context.Context, env Env) []Result {
	def := env.AgentDefinition
	if def == "" {
		return nil
	}
	program, err := launchd.Program(def)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return []Result{{Status: StatusFail, Evidence: "no launchd agent is installed at " + def, Fix: startFix(env)}}
	case err != nil:
		return []Result{{Status: StatusFail, Evidence: fmt.Sprintf("cannot read the launchd agent %s: %v", def, err), Fix: restartFix}}
	}
	info, err := os.Stat(program)
	switch {
	case err != nil:
		return []Result{{Status: StatusFail, Evidence: fmt.Sprintf("the launchd agent %s points at %s, which cannot be found: %v", def, program, errors.Unwrap(err)), Fix: restartFix}}
	case !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0:
		return []Result{{Status: StatusFail, Evidence: fmt.Sprintf("the launchd agent %s points at %s, which is not an executable file", def, program), Fix: restartFix}}
	}
	if env.AgentStale != nil {
		stale, err := env.AgentStale()
		switch {
		case err != nil:
			return []Result{{Status: StatusWarn, Evidence: fmt.Sprintf("cannot compare the launchd agent %s with this build's: %v", def, err), Fix: restartFix}}
		case stale:
			return []Result{{Status: StatusWarn, Evidence: fmt.Sprintf("stale agent definition: %s differs from the one this ktags build writes", def), Fix: restartFix}}
		}
	}
	return []Result{{Status: StatusOK, Evidence: "the launchd agent starts " + program}}
}

func checkSocket(ctx context.Context, env Env) []Result {
	st, err := env.ServiceStatus(ctx)
	var pe *service.Error
	switch {
	case len(st.Socket) > service.MaxSocketPath:
		// No service can listen there, so starting one would never turn this row green.
		return []Result{{Status: StatusFail, Evidence: fmt.Sprintf("the socket path %s is %d bytes; the limit is %d", st.Socket, len(st.Socket), service.MaxSocketPath), Fix: "set KTAGS_RUNTIME_DIR to a shorter absolute path"}}
	case errors.As(err, &pe) && pe.Code == service.CodeProtocolMismatch:
		return []Result{{Status: StatusOK, Evidence: "a ktags service answers on " + st.Socket + " (its protocol is the next check)"}}
	case err != nil:
		return []Result{{Status: StatusFail, Evidence: firstLine(err.Error()), Fix: serviceFix(env, err)}}
	case !st.Running:
		return []Result{{Status: StatusFail, Evidence: "no ktags service answers on " + st.Socket, Fix: startFix(env)}}
	}
	return []Result{{Status: StatusOK, Evidence: fmt.Sprintf("ktags service %s (pid %d) answers on %s", st.Version, st.PID, st.Socket)}}
}

func checkProtocol(ctx context.Context, env Env) []Result {
	st, err := env.ServiceStatus(ctx)
	var pe *service.Error
	switch {
	case errors.As(err, &pe) && pe.Code == service.CodeProtocolMismatch:
		return []Result{{Status: StatusFail, Evidence: pe.Message, Fix: pe.Hint}}
	case err != nil:
		return []Result{{Status: StatusWarn, Evidence: "not measured: the ktags service did not answer", Fix: serviceFix(env, err)}}
	case !st.Running:
		return []Result{{Status: StatusWarn, Evidence: "not measured: no ktags service answers", Fix: startFix(env)}}
	}
	return []Result{{Status: StatusOK, Evidence: fmt.Sprintf("protocol %d on both sides", st.Protocol)}}
}

// serviceFix is the service's own next step for an answer that is an error (another owner, a
// silent service), or a start when it has none.
func serviceFix(env Env, err error) string {
	var pe *service.Error
	if errors.As(err, &pe) && pe.Hint != "" {
		return pe.Hint
	}
	return startFix(env)
}
