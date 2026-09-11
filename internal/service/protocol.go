package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nesiler/ktags/internal/actions"
)

// ProtocolVersion is the only protocol version this build speaks.
const ProtocolVersion = 1

// maxMessage bounds one request or reply line, so a broken peer cannot make the other side
// buffer without limit.
const maxMessage = 1 << 20

// Operations.
const (
	OpHello     = "hello"
	OpInventory = "inventory.list"
	OpActions   = "actions.list"
	OpStart     = "run.start"
	OpCancel    = "run.cancel"
	OpStatus    = "run.status"
	OpRuns      = "run.list"
	OpEvents    = "run.events"
	// OpFleet lists the cached fleet state; it measures nothing.
	OpFleet = "fleet.list"
	// OpStop asks the service to stop. It is refused while runs are active unless the request
	// sets cancel_runs; the reply lists the runs that were cancelled.
	OpStop = "service.stop"
)

// Error codes.
const (
	// CodeProtocolMismatch: client and service speak different protocol versions.
	CodeProtocolMismatch = "protocol_mismatch"
	// CodeBadRequest: the request is not a well-formed message of this protocol.
	CodeBadRequest = "bad_request"
	// CodeTooLarge: the message exceeds the size limit.
	CodeTooLarge = "too_large"
	// CodeForbidden: the connecting user does not own the service.
	CodeForbidden = "forbidden"
	// CodeInvalid: the request is well formed but refused (unknown action, bad target, bad cursor).
	CodeInvalid = "invalid"
	// CodeNotFound: the named run cannot be found or read.
	CodeNotFound = "not_found"
	// CodeConflict: the run is not in a state that allows the operation.
	CodeConflict = "conflict"
	// CodeUnavailable: the service cannot be reached or is stopping.
	CodeUnavailable = "unavailable"
	// CodeMalformedReply: the client could not read the service's reply.
	CodeMalformedReply = "malformed_reply"
	// CodeInternal: the service failed; the message says where.
	CodeInternal = "internal"
)

// Error is an operator-facing protocol error: what failed and the next step.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	err     error
}

func (e *Error) Error() string {
	if e.Hint == "" {
		return e.Message
	}
	return e.Message + "\n  next: " + e.Hint
}

func (e *Error) Unwrap() error {
	return e.err
}

// Request is one client request.
type Request struct {
	Protocol int                        `json:"protocol"`
	Op       string                     `json:"op"`
	Run      string                     `json:"run,omitempty"`
	Action   string                     `json:"action,omitempty"`
	Target   *Target                    `json:"target,omitempty"`
	Args     map[string]json.RawMessage `json:"args,omitempty"`
	After    uint64                     `json:"after,omitempty"`
	Follow   bool                       `json:"follow,omitempty"`
	// CancelRuns lets service.stop cancel the active runs instead of refusing.
	CancelRuns bool `json:"cancel_runs,omitempty"`
}

// Response is one reply line. Exactly one of the payload fields is set.
type Response struct {
	Protocol  int            `json:"protocol"`
	Error     *Error         `json:"error,omitempty"`
	Hello     *Hello         `json:"hello,omitempty"`
	Customers []CustomerInfo `json:"customers,omitempty"`
	Actions   []ActionInfo   `json:"actions,omitempty"`
	Run       *RunInfo       `json:"run,omitempty"`
	Runs      []RunInfo      `json:"runs,omitempty"`
	Event     *Event         `json:"event,omitempty"`
	Fleet     *FleetInfo     `json:"fleet,omitempty"`
}

// FleetInfo is the fleet summary: every customer of the inventory in attention order (most
// urgent first, then by customer), evaluated at Now on the service clock.
type FleetInfo struct {
	Now       time.Time    `json:"now"`
	Customers []FleetEntry `json:"customers"`
}

// FleetEntry is one customer of the fleet summary. State is the attention state computed in
// core/fleet; the other fields are the facts it was computed from.
type FleetEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Environment string `json:"environment,omitempty"`
	Cluster     string `json:"cluster,omitempty"`
	// Problem is set when the inventory record is refused.
	Problem    string         `json:"problem,omitempty"`
	State      string         `json:"state"`
	Connection ConnectionInfo `json:"connection"`
	Health     string         `json:"health"`
	// MeasuredAt is absent when the customer has never been measured.
	MeasuredAt      *time.Time `json:"measured_at,omitempty"`
	Trigger         string     `json:"trigger,omitempty"`
	Stale           bool       `json:"stale"`
	IntervalSeconds int64      `json:"interval_seconds"`
	// Missed counts the scheduled health slots that could not run.
	Missed int `json:"missed"`
	// Checking is set while a health measurement of the customer is in progress.
	Checking   bool              `json:"checking"`
	Versions   map[string]string `json:"versions"`
	ActiveRuns []ActiveRunInfo   `json:"active_runs"`
	Checks     []CheckInfo       `json:"checks"`
}

// ConnectionInfo is the latest connection result of a customer.
type ConnectionInfo struct {
	State      string     `json:"state"`
	Detail     string     `json:"detail,omitempty"`
	MeasuredAt *time.Time `json:"measured_at,omitempty"`
}

// ActiveRunInfo names a run that is executing for a customer.
type ActiveRunInfo struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

// CheckInfo is the evidence of one health check: what ran, its outcome and the measured fact.
type CheckInfo struct {
	Check      string    `json:"check"`
	Severity   string    `json:"severity"`
	Status     string    `json:"status"`
	Detail     string    `json:"detail,omitempty"`
	MeasuredAt time.Time `json:"measured_at"`
	DurationMS int64     `json:"duration_ms"`
}

// Hello identifies the running service.
type Hello struct {
	Service string `json:"service"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
	// ActiveRuns counts the runs executing in the service.
	ActiveRuns int `json:"active_runs"`
}

// Target is what a run operates on.
type Target struct {
	Kind     string `json:"kind"`
	Customer string `json:"customer,omitempty"`
	Node     string `json:"node,omitempty"`
}

func (t Target) actions() actions.Target {
	return actions.Target{Kind: actions.TargetKind(t.Kind), Customer: t.Customer, Node: t.Node}
}

// CustomerInfo is one customer from the inventory. Problem is set when its record is refused.
type CustomerInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Environment string `json:"environment,omitempty"`
	Cluster     string `json:"cluster,omitempty"`
	Nodes       int    `json:"nodes"`
	Problem     string `json:"problem,omitempty"`
}

// ActionInfo describes one registered action.
type ActionInfo struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Help   string    `json:"help"`
	Target string    `json:"target"`
	Effect string    `json:"effect"`
	Args   []ArgInfo `json:"args,omitempty"`
}

// ArgInfo describes one action argument.
type ArgInfo struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Help     string `json:"help"`
}

// RunInfo is the state of one run.
type RunInfo struct {
	ID          string      `json:"id"`
	Action      string      `json:"action"`
	Target      Target      `json:"target"`
	Status      string      `json:"status"`
	StartedAt   time.Time   `json:"started_at"`
	LastEventID uint64      `json:"last_event_id"`
	Result      *ResultInfo `json:"result,omitempty"`
	Problem     string      `json:"problem,omitempty"`
}

// ResultInfo is the final outcome of a run.
type ResultInfo struct {
	Status     string    `json:"status"`
	Summary    string    `json:"summary"`
	FinishedAt time.Time `json:"finished_at"`
}

// Event is one progress report of a run. IDs count up from 1 within a run.
type Event struct {
	ID      uint64    `json:"id"`
	Time    time.Time `json:"time"`
	Step    string    `json:"step,omitempty"`
	Message string    `json:"message"`
}

var errTooLarge = errors.New("message exceeds the size limit")

// readLine reads one newline-terminated line of at most maxMessage bytes.
func readLine(r *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > maxMessage {
			return nil, errTooLarge
		}
		if err == nil {
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

// peerVersion reads the protocol field of a message without trusting the rest of it, so a
// message of any version can be told apart from a malformed one.
func peerVersion(line []byte) (int, bool) {
	var envelope struct {
		Protocol *int `json:"protocol"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Protocol == nil {
		return 0, false
	}
	return *envelope.Protocol, true
}

// decodeStrict decodes one JSON object of this protocol's shape. It runs after peerVersion,
// whose json.Unmarshal already refuses trailing data after the object.
func decodeStrict(line []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("unknown or mistyped fields")
	}
	return nil
}

// mismatch explains a protocol version difference from the side that detected it. theirs is
// the peer's version; peer names the other side ("client" or "service").
func mismatch(theirs int, peer string) *Error {
	message := fmt.Sprintf("the %s speaks ktags protocol %d, this side speaks %d", peer, theirs, ProtocolVersion)
	var hint string
	switch {
	case peer == "client" && theirs > ProtocolVersion:
		hint = "the running service is older than the client: stop the ktags service and start it again from the upgraded ktags build"
	case peer == "client":
		hint = "the client is older than the running service: upgrade ktags to the build that started the service"
	case theirs > ProtocolVersion:
		hint = "this ktags is older than the running service: upgrade ktags to the build that started the service"
	default:
		hint = "the running service is older than this ktags: stop the ktags service and start it again from this build"
	}
	return &Error{Code: CodeProtocolMismatch, Message: message, Hint: hint}
}
