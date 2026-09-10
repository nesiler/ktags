package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/nesiler/ktags/internal/actions"
)

// ErrClosed is returned by Subscription.Next after Close.
var ErrClosed = errors.New("run: subscription closed")

// Recorder writes one run: it appends events, writes the final result once and serves live
// subscribers. It is safe for concurrent use. Appending never waits for a subscriber.
type Recorder struct {
	store *Store
	dir   string
	meta  Meta

	mu     sync.Mutex
	events *os.File
	// closed is set once events.jsonl is synced and closed by Finish.
	closed bool
	// broken is the write error that stopped appends; a partial line may follow the last event.
	broken error
	last   uint64
	// window holds the most recent events, at most store.window, ending with ID last.
	window    []Event
	result    *Result
	subs      map[*Subscription]struct{}
	reportErr error
}

// Meta returns the run's metadata as written to meta.json.
func (r *Recorder) Meta() Meta {
	return r.meta
}

// Append redacts and persists one event and wakes the subscribers. It returns the event as
// written, with its ID.
func (r *Recorder) Append(e actions.Event) (Event, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.closed:
		return Event{}, &Error{Run: r.meta.ID, Problem: "the run has finished; no more events are accepted", Next: "report progress before Finish"}
	case r.broken != nil:
		return Event{}, &Error{Run: r.meta.ID, Problem: "an earlier event could not be written; no more events are accepted", Next: "check free space and permissions, then finish the run", Err: r.broken}
	}
	event := Event{
		Version: FormatVersion,
		ID:      r.last + 1,
		Time:    r.store.clock().UTC(),
		Step:    r.store.redact(e.Step),
		Message: r.store.redact(e.Message),
	}
	line, err := json.Marshal(event)
	if err != nil {
		return Event{}, &Error{Run: r.meta.ID, Problem: "cannot encode an event", Next: "report this as a bug", Err: err}
	}
	if _, err := r.events.Write(append(line, '\n')); err != nil {
		r.broken = err
		return Event{}, &Error{Run: r.meta.ID, Problem: "cannot append to " + eventsFile, Next: "check free space and permissions, then finish the run", Err: err}
	}
	r.last = event.ID
	r.window = append(r.window, event)
	if len(r.window) > r.store.window {
		r.window = r.window[len(r.window)-r.store.window:]
	}
	r.wake()
	return event, nil
}

// Report makes the Recorder an actions.Progress. The first failure is kept for Err; the action
// is not interrupted by a full disk.
func (r *Recorder) Report(e actions.Event) {
	if _, err := r.Append(e); err != nil {
		r.mu.Lock()
		if r.reportErr == nil {
			r.reportErr = err
		}
		r.mu.Unlock()
	}
}

// Err returns the first error Report met, or nil.
func (r *Recorder) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reportErr
}

// Finish syncs and closes events.jsonl, then writes result.json atomically. It is final: after a
// successful Finish no event or second result is accepted. When writing the result fails, the
// events are kept, the run still reads as running, and Finish may be called again.
func (r *Recorder) Finish(status Status, summary string) (Result, error) {
	if !status.final() {
		return Result{}, &Error{Run: r.meta.ID, Problem: fmt.Sprintf("status %q is not a final status", status), Next: `finish with "succeeded", "failed" or "cancelled"`}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.result != nil {
		return *r.result, &Error{Run: r.meta.ID, Problem: "the run has already finished", Next: "read the result instead of finishing again"}
	}
	if !r.closed {
		if err := r.events.Sync(); err != nil {
			return Result{}, &Error{Run: r.meta.ID, Problem: "cannot sync " + eventsFile, Next: "check the disk, then finish the run again", Err: err}
		}
		if err := r.events.Close(); err != nil {
			return Result{}, &Error{Run: r.meta.ID, Problem: "cannot close " + eventsFile, Next: "check the disk, then finish the run again", Err: err}
		}
		r.closed = true
		r.wake()
	}
	result := Result{
		Version:     FormatVersion,
		Status:      status,
		Summary:     r.store.redact(summary),
		FinishedAt:  r.store.clock().UTC(),
		LastEventID: r.last,
	}
	body, err := json.Marshal(result)
	if err != nil {
		return Result{}, &Error{Run: r.meta.ID, Problem: "cannot encode the result", Next: "report this as a bug", Err: err}
	}
	if err := r.store.fs.writeAtomic(filepath.Join(r.dir, resultFile), append(body, '\n')); err != nil {
		return Result{}, &Error{Run: r.meta.ID, Problem: "cannot write " + resultFile + "; the events are kept", Next: "check free space and permissions, then finish the run again", Err: err}
	}
	r.result = &result
	return result, nil
}

// Subscribe starts a live subscription that returns the events with an ID above after. A client
// that reconnects passes the last ID it has seen and receives each later event exactly once.
func (r *Recorder) Subscribe(after uint64) (*Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if after > r.last {
		return nil, &Error{Run: r.meta.ID, Problem: fmt.Sprintf("cursor %d is beyond the last event %d", after, r.last), Next: "resume from the last event ID the client received"}
	}
	if len(r.subs) >= r.store.maxSubscribers {
		return nil, &Error{Run: r.meta.ID, Problem: fmt.Sprintf("already %d subscribers", len(r.subs)), Next: "close an idle client, or replay the persisted events instead"}
	}
	s := &Subscription{rec: r, cursor: after, wakeup: make(chan struct{}, 1)}
	r.subs[s] = struct{}{}
	return s, nil
}

// wake signals every subscriber without waiting; the caller holds r.mu. A subscriber that has
// not consumed its previous signal keeps that one, which is enough: it reads up to r.last.
func (r *Recorder) wake() {
	for s := range r.subs {
		select {
		case s.wakeup <- struct{}{}:
		default:
		}
	}
}

// Subscription is one client's position in a run's events. It holds no queued events of its
// own beyond one catch-up batch, so an idle subscriber costs the Recorder nothing. Next and
// Cursor are for one goroutine; Close may be called from any.
type Subscription struct {
	rec     *Recorder
	wakeup  chan struct{}
	cursor  uint64
	pending []Event
	// closed is guarded by rec.mu.
	closed bool
}

// Cursor returns the ID of the last event Next returned, or the starting cursor.
func (s *Subscription) Cursor() uint64 {
	return s.cursor
}

// Next returns the next event. It blocks until one is appended, the run finishes (io.EOF once
// every event was returned), ctx ends, or the subscription is closed (ErrClosed). A subscriber
// that fell out of the in-memory window catches up from events.jsonl.
func (s *Subscription) Next(ctx context.Context) (Event, error) {
	for {
		if len(s.pending) > 0 {
			event := s.pending[0]
			s.pending = s.pending[1:]
			s.cursor = event.ID
			return event, nil
		}
		if err := ctx.Err(); err != nil {
			return Event{}, err
		}
		r := s.rec
		r.mu.Lock()
		if s.closed {
			r.mu.Unlock()
			return Event{}, ErrClosed
		}
		last, done := r.last, r.closed
		if s.cursor < last {
			if len(r.window) > 0 && s.cursor+1 >= r.window[0].ID {
				event := r.window[s.cursor+1-r.window[0].ID]
				r.mu.Unlock()
				s.cursor = event.ID
				return event, nil
			}
			r.mu.Unlock()
			if err := s.catchUp(); err != nil {
				return Event{}, err
			}
			continue
		}
		r.mu.Unlock()
		if done {
			return Event{}, io.EOF
		}
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-s.wakeup:
		}
	}
}

// catchUp reads the next batch after the cursor from events.jsonl. Lines are only ever
// appended whole, so the file holds at least every event up to the recorder's last ID.
func (s *Subscription) catchUp() error {
	r := s.rec
	scan, err := readEvents(filepath.Join(r.dir, eventsFile), s.cursor, r.store.window)
	if err != nil {
		return &Error{Run: r.meta.ID, Problem: "cannot replay " + eventsFile, Next: "inspect the run directory " + r.dir, Err: err}
	}
	if len(scan.events) == 0 {
		return &Error{Run: r.meta.ID, Problem: fmt.Sprintf("%s ends at event %d, before the recorded events", eventsFile, scan.last), Next: "inspect the run directory " + r.dir}
	}
	s.pending = scan.events
	return nil
}

// Close ends the subscription and frees its slot. A pending Next returns ErrClosed.
func (s *Subscription) Close() {
	r := s.rec
	r.mu.Lock()
	defer r.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(r.subs, s)
	select {
	case s.wakeup <- struct{}{}:
	default:
	}
}
