package service

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/run"
)

// failWrite starts a long run, makes its directory read-only and lets it end, so Finish cannot
// write result.json. It returns once the run has left the manager, with the run directory.
func failWrite(f *fixture) (string, string) {
	f.t.Helper()
	started := f.start("fake long", acme, nil)
	f.detachAfter(started.ID, 2)
	loaded, err := f.store.Load(context.Background(), started.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	chmod(f.t, loaded.Dir, 0o500)
	f.srv.manager.mu.Lock()
	a := f.srv.manager.active[started.ID]
	f.srv.manager.mu.Unlock()
	if a == nil {
		f.t.Fatalf("run %s is not active before its gate opened", started.ID)
	}
	close(f.long.gate)
	select {
	case <-a.done:
	case <-time.After(10 * time.Second):
		f.t.Fatalf("run %s did not end", started.ID)
	}
	return started.ID, loaded.Dir
}

// wantIncomplete checks the state the decision on #48 prescribes for a run without a result.
func wantIncomplete(t *testing.T, what string, info RunInfo, id, dir string) {
	t.Helper()
	want := "the run has no result: it could not be written, or the service stopped during the run; next: check free space and permissions of " + dir + "; see the service log for run " + id
	if info.ID != id || info.Status != string(run.StatusIncomplete) || info.Result != nil || info.Problem != want {
		t.Fatalf("%s: %+v\nwant status incomplete, no result, problem %q", what, info, want)
	}
}

// #48-K1: a follower that arrives after the failed write, run.status and run.list all report the
// run incomplete; the service log holds the masked write error with the run ID.
func TestLateFollowerSeesIncompleteAfterFailedWrite(t *testing.T) {
	var logged bytes.Buffer
	f := newFixture(t, setup{opts: func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(&logged, nil))
		// Stands for a mask hit, so the test sees whether the log line passed the redactor.
		o.Redact = func(s string) string { return strings.ReplaceAll(mask.Mask(s), "denied", "D-MASKED") }
	}})
	id, dir := failWrite(f)

	events, info := f.follow(id, 0, true)
	if !slices.Equal(events, ids(1, 5)) || info.LastEventID != 5 {
		t.Fatalf("late follower events %v last %d, want [1..5] and 5", events, info.LastEventID)
	}
	wantIncomplete(t, "late follower", info, id, dir)

	status, err := f.client.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	wantIncomplete(t, "status", status, id, dir)

	runs, err := f.client.Runs(context.Background())
	if err != nil || len(runs) != 1 {
		t.Fatalf("Runs = %+v, %v; want the one run", runs, err)
	}
	wantIncomplete(t, "list", runs[0], id, dir)

	line := logged.String()
	for _, want := range []string{"the result could not be written", "run=" + id, "status=succeeded", "permission D-MASKED"} {
		if !strings.Contains(line, want) {
			t.Errorf("service log %q, want %q", line, want)
		}
	}
	for _, not := range []string{"permission denied", "next:"} {
		if strings.Contains(line, not) {
			t.Errorf("service log %q holds %q; want only the masked write error", line, not)
		}
	}
}

// restart closes the fixture's service and starts another on the same state root, with a new
// store, as a new service process would.
func restart(f *fixture) Client {
	f.t.Helper()
	if err := f.srv.Close(); err != nil {
		f.t.Fatal(err)
	}
	store, err := run.NewStore(f.state, run.Options{Redact: mask.Mask})
	if err != nil {
		f.t.Fatal(err)
	}
	runtime := mkdir(f.t, shortDir(f.t), "rt")
	opts := f.options()
	opts.Store = store
	srv, err := Listen(context.Background(), runtime, opts)
	if err != nil {
		f.t.Fatalf("Listen after restart: %v", err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()
	f.t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			f.t.Errorf("Close: %v", err)
		}
		if err := <-served; err != nil {
			f.t.Errorf("Serve: %v", err)
		}
	})
	return Client{Socket: SocketPath(runtime)}
}

// #48-K1: after a service restart the run is still incomplete, for status and a follower.
func TestIncompleteAfterRestart(t *testing.T) {
	f := newFixture(t, setup{})
	id, dir := failWrite(f)
	client := restart(f)

	status, err := client.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	wantIncomplete(t, "status after restart", status, id, dir)
	info, err := client.Events(context.Background(), id, 0, true, func(Event) error { return nil })
	if err != nil {
		t.Fatalf("Events after restart: %v", err)
	}
	wantIncomplete(t, "follower after restart", info, id, dir)
}

// #48-K2: runs that wrote their result read back unchanged, before and after a restart.
func TestFinishedRunsUnchangedAfterRestart(t *testing.T) {
	var logged bytes.Buffer
	f := newFixture(t, setup{opts: func(o *Options) { o.Logger = slog.New(slog.NewTextHandler(&logged, nil)) }})
	succeeded := f.start("fake quick", global, nil)
	f.follow(succeeded.ID, 0, true)
	failed := f.start("fake fail", global, nil)
	f.follow(failed.ID, 0, true)
	cancelled := f.start("fake long", acme, nil)
	f.detachAfter(cancelled.ID, 2)
	if _, err := f.client.Cancel(context.Background(), cancelled.ID); err != nil {
		t.Fatal(err)
	}
	f.follow(cancelled.ID, 0, true)
	want := map[string]string{succeeded.ID: "succeeded", failed.ID: "failed", cancelled.ID: "cancelled"}

	check := func(what string, client Client) {
		t.Helper()
		for id, status := range want {
			info, err := client.Status(context.Background(), id)
			if err != nil {
				t.Fatalf("%s: Status %s: %v", what, id, err)
			}
			if info.Status != status || info.Result == nil || info.Result.Status != status || info.Problem != "" {
				t.Fatalf("%s: run %s = %+v, want %s with its result and no problem", what, id, info, status)
			}
		}
	}
	check("before restart", f.client)
	check("after restart", restart(f))
	// Every run above has left the manager, so its log line, if any, is written.
	if logged.Len() != 0 {
		t.Fatalf("service log %q, want nothing for runs that wrote their result", logged.String())
	}
}

// settle re-reads a run it was handed as running once the run is not active, and leaves active
// runs and runs with a result alone.
func TestSettleReloadsAFinishedRun(t *testing.T) {
	f := newFixture(t, setup{})
	m := f.srv.manager
	ctx := context.Background()

	done := f.start("fake quick", global, nil)
	f.follow(done.ID, 0, true)
	stale, err := f.store.Load(ctx, done.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale.Status, stale.Result = run.StatusRunning, nil // read just before the result was written
	if got := m.settle(ctx, stale); got.Status != run.StatusSucceeded || got.Result == nil || got.Problem != "" {
		t.Fatalf("settle of a run that finished meanwhile = %+v, want succeeded", got)
	}

	active := f.start("fake long", acme, nil)
	f.detachAfter(active.ID, 2)
	loaded, err := f.store.Load(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.settle(ctx, loaded); got.Status != run.StatusRunning || got.Problem != "" {
		t.Fatalf("settle of an active run = %+v, want running", got)
	}

	// A run that vanished between the reads keeps what was read, and that was a missing result.
	gone := run.Run{Dir: "/state/runs/acme/" + missingRun, Meta: run.Meta{ID: missingRun}, Status: run.StatusRunning, Problem: "events.jsonl ends in a partial line after event 2; it is ignored"}
	got := m.settle(ctx, gone)
	want := "the run has no result: it could not be written, or the service stopped during the run; next: check free space and permissions of " + gone.Dir + "; see the service log for run " + missingRun + "; " + gone.Problem
	if got.Status != run.StatusIncomplete || got.Problem != want {
		t.Fatalf("settle of a vanished run = %+v, want incomplete with problem %q", got, want)
	}
}
