package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
)

// Client talks to the service over its socket. Each call is one connection; closing the
// connection, or cancelling ctx, detaches from a run without stopping it.
type Client struct {
	// Socket is the service socket (SocketPath).
	Socket string

	// connected, when set, is called after the connection is made and before the request is
	// written. Tests use it.
	connected func()
}

// Hello identifies the running service.
func (c Client) Hello(ctx context.Context) (Hello, error) {
	resp, err := c.one(ctx, Request{Op: OpHello})
	if err != nil {
		return Hello{}, err
	}
	if resp.Hello == nil {
		return Hello{}, malformed("the hello reply has no hello field")
	}
	return *resp.Hello, nil
}

// Customers lists the inventory.
func (c Client) Customers(ctx context.Context) ([]CustomerInfo, error) {
	resp, err := c.one(ctx, Request{Op: OpInventory})
	return resp.Customers, err
}

// Fleet returns the cached fleet summary. The service measures nothing to answer it.
func (c Client) Fleet(ctx context.Context) (FleetInfo, error) {
	resp, err := c.one(ctx, Request{Op: OpFleet})
	if err != nil {
		return FleetInfo{}, err
	}
	if resp.Fleet == nil {
		return FleetInfo{}, malformed("the fleet.list reply has no fleet field")
	}
	return *resp.Fleet, nil
}

// Actions lists the registered actions.
func (c Client) Actions(ctx context.Context) ([]ActionInfo, error) {
	resp, err := c.one(ctx, Request{Op: OpActions})
	return resp.Actions, err
}

// Runs lists every recorded run, oldest first.
func (c Client) Runs(ctx context.Context) ([]RunInfo, error) {
	resp, err := c.one(ctx, Request{Op: OpRuns})
	return resp.Runs, err
}

// Start asks the service to run action against target. It returns once the run is recorded;
// the action runs in the service.
func (c Client) Start(ctx context.Context, action string, target Target, args map[string]any) (RunInfo, error) {
	req := Request{Op: OpStart, Action: action, Target: &target}
	if len(args) > 0 {
		req.Args = make(map[string]json.RawMessage, len(args))
		for name, value := range args {
			raw, err := json.Marshal(value)
			if err != nil {
				return RunInfo{}, &Error{Code: CodeInvalid, Message: "argument " + name + " cannot be encoded", Hint: "pass a string, bool or int", err: err}
			}
			req.Args[name] = raw
		}
	}
	return c.runReply(ctx, req)
}

// Stop asks the service to stop. While runs are active it is refused with a conflict error,
// unless cancelRuns is set; it returns the runs it cancelled, in their final state.
func (c Client) Stop(ctx context.Context, cancelRuns bool) ([]RunInfo, error) {
	resp, err := c.one(ctx, Request{Op: OpStop, CancelRuns: cancelRuns})
	return resp.Runs, err
}

// Cancel asks the service to cancel an active run.
func (c Client) Cancel(ctx context.Context, id string) (RunInfo, error) {
	return c.runReply(ctx, Request{Op: OpCancel, Run: id})
}

// Status returns the current state of a run.
func (c Client) Status(ctx context.Context, id string) (RunInfo, error) {
	return c.runReply(ctx, Request{Op: OpStatus, Run: id})
}

// Events calls fn for each event of run id after the cursor. With follow it waits until the run
// ends. It returns the run's state after the last event. An error from fn ends the call and
// closes the connection; the run continues in the service.
func (c Client) Events(ctx context.Context, id string, after uint64, follow bool, fn func(Event) error) (RunInfo, error) {
	var info RunInfo
	err := c.call(ctx, Request{Op: OpEvents, Run: id, After: after, Follow: follow}, func(resp Response) (bool, error) {
		switch {
		case resp.Event != nil:
			return false, fn(*resp.Event)
		case resp.Run != nil:
			info = *resp.Run
			return true, nil
		default:
			return true, malformed("an event stream reply has neither an event nor the run")
		}
	})
	return info, err
}

func (c Client) runReply(ctx context.Context, req Request) (RunInfo, error) {
	resp, err := c.one(ctx, req)
	if err != nil {
		return RunInfo{}, err
	}
	if resp.Run == nil {
		return RunInfo{}, malformed("the " + req.Op + " reply has no run field")
	}
	return *resp.Run, nil
}

func (c Client) one(ctx context.Context, req Request) (Response, error) {
	var out Response
	err := c.call(ctx, req, func(resp Response) (bool, error) {
		out = resp
		return true, nil
	})
	return out, err
}

// call sends req and hands each reply to each until each reports done or fails. A reply of
// another protocol version or of an unknown shape is an error, never partial data.
func (c Client) call(ctx context.Context, req Request, each func(Response) (bool, error)) error {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return &Error{Code: CodeUnavailable, Message: "cannot reach the ktags service at " + c.Socket, Hint: "start the ktags service, then retry", err: err}
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if c.connected != nil {
		c.connected()
	}

	req.Protocol = ProtocolVersion
	// The service may refuse and close before it reads a byte (the owner check), so a failed
	// write is not the end: its refusal can already wait in the receive buffer.
	werr := json.NewEncoder(conn).Encode(req)
	reader := bufio.NewReader(conn)
	for {
		line, err := readLine(reader)
		if err != nil {
			if errors.Is(err, errTooLarge) {
				return malformed("a reply exceeds the size limit")
			}
			if werr != nil {
				err = werr
			}
			return c.connErr(ctx, err)
		}
		resp, err := decodeResponse(line)
		if err != nil {
			return err
		}
		if resp.Error != nil {
			return resp.Error
		}
		done, err := each(resp)
		if err != nil || done {
			return err
		}
	}
}

func (c Client) connErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &Error{Code: CodeUnavailable, Message: "the connection to the ktags service ended before its reply", Hint: "check that the ktags service is running, then retry", err: err}
}

func decodeResponse(line []byte) (Response, error) {
	version, ok := peerVersion(line)
	if !ok {
		return Response{}, malformed("a reply is not a JSON object with an integer protocol field")
	}
	if version != ProtocolVersion {
		return Response{}, mismatch(version, "service")
	}
	var resp Response
	if err := decodeStrict(line, &resp); err != nil {
		return Response{}, malformed("a reply is malformed: " + err.Error())
	}
	return resp, nil
}

func malformed(message string) *Error {
	return &Error{Code: CodeMalformedReply, Message: message, Hint: "run the client and the service from the same ktags build"}
}
