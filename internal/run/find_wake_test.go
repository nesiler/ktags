package run

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// #10 review round 3 [High]: the state root is a literal path, never a glob pattern.
func TestFindTreatsTheStateRootLiterally(t *testing.T) {
	s := newStore(t, filepath.Join(t.TempDir(), "state[1]*?"), Options{})
	rec := start(t, s, acme)
	appendN(t, rec, 1)
	finish(t, rec, StatusSucceeded)
	id := rec.Meta().ID
	run, err := s.Load(context.Background(), id)
	if err != nil {
		t.Fatalf("Load below a root with glob metacharacters: %v", err)
	}
	if run.Status != StatusSucceeded {
		t.Fatalf("status = %s, want %s", run.Status, StatusSucceeded)
	}
	events, err := s.Events(context.Background(), id, 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("Events = %d events, %v; want 1 event", len(events), err)
	}
}

// Listing the runs directory can fail; the error names the setting to check.
func TestFindReportsAnUnreadableRunsDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	finish(t, rec, StatusSucceeded)
	if err := os.Chmod(s.root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(s.root, 0o700) })
	_, err := s.Load(context.Background(), rec.Meta().ID)
	wantErr(t, "Load", err, "cannot search the runs directory")
}

// Before the first run the runs directory does not exist; that is "no such run", not an error.
func TestFindWithoutRunsDirectory(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	_, err := s.Load(context.Background(), "20260910T120000Z-deadbeef")
	wantErr(t, "Load before any run", err, "no such run")
}

// #10 review round 3 [Medium]: Append, not only Finish, wakes a subscriber blocked in Next.
func TestAppendWakesBlockedSubscriber(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	sub, err := rec.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx := &waitingCtx{Context: parent, waiting: make(chan struct{})}
	type result struct {
		event Event
		err   error
	}
	got := make(chan result, 1)
	go func() {
		e, err := sub.Next(ctx)
		got <- result{e, err}
	}()
	<-ctx.waiting
	appendN(t, rec, 1)
	r := <-got
	if r.err != nil || r.event.ID != 1 {
		t.Fatalf("Next blocked across Append = event %d, %v; want event 1", r.event.ID, r.err)
	}
}
