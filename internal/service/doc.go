// Package service is the one ktags service per operator (ADR-0001) and its local protocol. The
// service owns action execution: CLI and TUI are clients that ask it to start, cancel and observe
// runs, so a run outlives the terminal that started it.
//
// Transport: a Unix socket, service.sock, in the ktags runtime root. The root must be private
// (0700) and the socket is 0600. Every connection is checked against the service owner's uid
// before a byte of the request is read. A lock on service.lock beside the socket keeps a second
// service from taking over the socket of a running one.
//
// Protocol: one request per connection, one JSON object per line in each direction. Every
// message carries "protocol". A request with another protocol version is answered with a
// protocol_mismatch error that says which side to upgrade; the error envelope
// {"protocol":N,"error":{"code","message","hint"}} is frozen across versions so that any client
// can read it. run.events streams "event" messages and ends with one "run" message holding the
// final state.
package service
