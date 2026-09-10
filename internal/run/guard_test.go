package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/actions"
)

var errFault = errors.New("injected fault")

// faultyEvents wraps the events file of a Recorder and fails the calls that are switched on.
type faultyEvents struct {
	eventFile
	partialWrite, failSync, failClose bool
}

func (f *faultyEvents) Write(p []byte) (int, error) {
	if f.partialWrite {
		n, _ := f.eventFile.Write(p[:len(p)/2])
		return n, errors.New("no space left on device")
	}
	return f.eventFile.Write(p)
}

func (f *faultyEvents) Sync() error {
	if f.failSync {
		return errFault
	}
	return f.eventFile.Sync()
}

func (f *faultyEvents) Close() error {
	if f.failClose {
		return errFault
	}
	return f.eventFile.Close()
}

// faultyTemp fails one step of the atomic write: "chmod", "sync" or "close".
type faultyTemp struct {
	tempFile
	op string
}

func (f faultyTemp) Chmod(m os.FileMode) error {
	if f.op == "chmod" {
		return errFault
	}
	return f.tempFile.Chmod(m)
}

func (f faultyTemp) Sync() error {
	if f.op == "sync" {
		return errFault
	}
	return f.tempFile.Sync()
}

func (f faultyTemp) Close() error {
	err := f.tempFile.Close()
	if f.op == "close" {
		return errFault
	}
	return err
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errFault }

func cancelled() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func wantErr(t *testing.T, what string, err error, text string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), text) {
		t.Fatalf("%s = %v, want an error containing %q", what, err, text)
	}
}

func TestNewStoreRefusesRelativeRootAndFillsDefaults(t *testing.T) {
	for _, root := range []string{"state", "./state", ""} {
		if _, err := NewStore(root, Options{Redact: identity}); err == nil {
			t.Errorf("NewStore accepted the relative state root %q", root)
		}
	}
	s, err := NewStore(t.TempDir(), Options{Redact: identity})
	if err != nil {
		t.Fatal(err)
	}
	if s.window != defaultWindow || s.maxSubscribers != defaultMaxSubscribers {
		t.Fatalf("window %d subscribers %d, want the defaults", s.window, s.maxSubscribers)
	}
	rec, err := s.Start(context.Background(), "cluster health", acme) // default clock and random source
	if err != nil || !runIDPattern.MatchString(rec.Meta().ID) {
		t.Fatalf("Start with defaults = %v, %v", rec, err)
	}
}

func TestStartFailures(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		_, err := s.Start(cancelled(), "cluster health", acme)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start = %v, want context.Canceled", err)
		}
	})
	t.Run("random source", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{Random: failingReader{}})
		_, err := s.Start(context.Background(), "cluster health", acme)
		wantErr(t, "Start", err, "cannot generate a run ID")
	})
	t.Run("runs directory", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		if err := os.WriteFile(s.root, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := s.Start(context.Background(), "cluster health", acme)
		wantErr(t, "Start", err, "cannot create the runs directory")
	})
	t.Run("meta write", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		s.fs.rename = func(string, string) error { return errFault }
		_, err := s.Start(context.Background(), "cluster health", acme)
		wantErr(t, "Start", err, "cannot write "+metaFile)
	})
	t.Run("existing events file", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		// An events.jsonl that appears beside the new meta.json must not be appended to.
		s.fs.rename = func(oldpath, newpath string) error {
			if err := os.Rename(oldpath, newpath); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(filepath.Dir(newpath), eventsFile), nil, 0o600)
		}
		_, err := s.Start(context.Background(), "cluster health", acme)
		wantErr(t, "Start", err, "cannot create "+eventsFile)
	})
}

// A clock beyond year 9999 cannot be encoded as JSON; every document write must refuse it
// instead of writing an empty line.
func TestUnencodableTimeIsRefused(t *testing.T) {
	var far atomic.Bool
	normal := (&fakeClock{t: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}).now
	clock := func() time.Time {
		if far.Load() {
			return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
		}
		return normal()
	}
	s := newStore(t, t.TempDir(), Options{Clock: clock})

	far.Store(true)
	_, err := s.Start(context.Background(), "cluster health", acme)
	wantErr(t, "Start", err, "cannot encode the run metadata")

	far.Store(false)
	rec := start(t, s, acme)
	far.Store(true)
	_, err = rec.Append(actions.Event{Message: "tick"})
	wantErr(t, "Append", err, "cannot encode an event")
	_, err = rec.Finish(StatusSucceeded, "ok")
	wantErr(t, "Finish", err, "cannot encode the result")

	far.Store(false)
	finish(t, rec, StatusSucceeded)
	if got, err := s.Load(context.Background(), rec.Meta().ID); err != nil || got.Status != StatusSucceeded || got.LastEventID != 0 {
		t.Fatalf("after refused writes: status %q last event %d err %v", got.Status, got.LastEventID, err)
	}
}

// #10-K2: a failed event write can leave a partial line; later appends must not follow it.
func TestFailedAppendStopsFurtherAppends(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	appendN(t, rec, 2)
	faulty := &faultyEvents{eventFile: rec.events, partialWrite: true}
	rec.events = faulty

	rec.Report(actions.Event{Message: "third"})
	wantErr(t, "Err after a failed write", rec.Err(), "cannot append to "+eventsFile)
	faulty.partialWrite = false // the disk has space again
	_, err := rec.Append(actions.Event{Message: "fourth"})
	wantErr(t, "Append after a failed write", err, "an earlier event could not be written")
	rec.Report(actions.Event{Message: "fifth"})
	wantErr(t, "Err keeps the first failure", rec.Err(), "cannot append to "+eventsFile)

	finish(t, rec, StatusFailed)
	got, err := s.Load(context.Background(), rec.Meta().ID)
	if err != nil || got.Status != StatusFailed || got.LastEventID != 2 || !strings.Contains(got.Problem, "partial line") {
		t.Fatalf("status %q last event %d problem %q err %v; want failed at 2 with a partial-line note", got.Status, got.LastEventID, got.Problem, err)
	}
	events, err := s.Events(context.Background(), rec.Meta().ID, 0)
	if err != nil || !equalIDs(eventIDs(events), []uint64{1, 2}) {
		t.Fatalf("Events = %v, %v; want 1 and 2", eventIDs(events), err)
	}
}

// #10-K2: when events.jsonl cannot be synced or closed, no result is written and Finish can be retried.
func TestFinishEventsFileFailures(t *testing.T) {
	cases := map[string]func(*faultyEvents) *bool{
		"sync":  func(f *faultyEvents) *bool { return &f.failSync },
		"close": func(f *faultyEvents) *bool { return &f.failClose },
	}
	for name, flag := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t, t.TempDir(), Options{})
			rec := start(t, s, acme)
			appendN(t, rec, 2)
			faulty := &faultyEvents{eventFile: rec.events}
			rec.events = faulty
			*flag(faulty) = true

			_, err := rec.Finish(StatusSucceeded, "ok")
			wantErr(t, "Finish", err, "cannot "+name+" "+eventsFile)
			if _, err := os.Stat(filepath.Join(rec.dir, resultFile)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("result.json after a failed %s: %v", name, err)
			}
			*flag(faulty) = false
			finish(t, rec, StatusSucceeded)
			if got, err := s.Load(context.Background(), rec.Meta().ID); err != nil || got.Status != StatusSucceeded || got.LastEventID != 2 {
				t.Fatalf("after retried Finish: status %q last event %d err %v", got.Status, got.LastEventID, err)
			}
		})
	}
}

// #10-K2: every step of the atomic result write that fails leaves no result and no temp file.
func TestAtomicWriteStepFailures(t *testing.T) {
	cases := map[string]func(fs *fileSystem){
		"create temp": func(fs *fileSystem) {
			fs.createTemp = func(string, string) (tempFile, error) { return nil, errFault }
		},
	}
	for _, op := range []string{"chmod", "sync", "close"} {
		cases[op] = func(fs *fileSystem) {
			fs.createTemp = func(dir, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return faultyTemp{f, op}, err
			}
		}
	}
	for name, breakFS := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t, t.TempDir(), Options{})
			rec := start(t, s, acme)
			appendN(t, rec, 2)
			breakFS(&s.fs)
			if _, err := rec.Finish(StatusSucceeded, "ok"); err == nil {
				t.Fatal("Finish succeeded with a failing file system")
			}
			entries, err := os.ReadDir(rec.dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != metaFile && entry.Name() != eventsFile {
					t.Errorf("run directory holds %s after a failed result write", entry.Name())
				}
			}
		})
	}

	t.Run("os sync dir", func(t *testing.T) {
		err := osFS.syncDir(filepath.Join(t.TempDir(), "missing"))
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("syncDir of a missing directory = %v, want the open error", err)
		}
	})

	t.Run("sync dir", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		rec := start(t, s, acme)
		s.fs.syncDir = func(string) error { return errFault }
		_, err := rec.Finish(StatusSucceeded, "ok")
		wantErr(t, "Finish", err, "cannot write "+resultFile)
	})
}

// #10-K1: a run directory copied below a second customer must not resolve silently.
func TestRunIDBelowTwoCustomersIsRefused(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	appendN(t, rec, 1)
	finish(t, rec, StatusSucceeded)
	copyDir := filepath.Join(s.root, "globex", rec.Meta().ID)
	if err := os.MkdirAll(copyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{metaFile, eventsFile, resultFile} {
		data, err := os.ReadFile(filepath.Join(rec.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(copyDir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Load(context.Background(), rec.Meta().ID)
	wantErr(t, "Load", err, "more than one customer")
	_, err = s.Events(context.Background(), rec.Meta().ID, 0)
	wantErr(t, "Events", err, "more than one customer")

	_, err = s.Load(context.Background(), "20260910T120000Z-deadbeef")
	wantErr(t, "Load of an unknown run", err, "no such run")
}

// A glob or path in place of a run ID must not resolve to an existing run.
func TestLoadRefusesPatternsAsRunIDs(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	id := rec.Meta().ID
	for _, bad := range []string{"*", id[:len(id)-1] + "?", "../acme/" + id, "acme/" + id} {
		_, err := s.Load(context.Background(), bad)
		wantErr(t, "Load("+bad+")", err, "is not a run ID")
		_, err = s.Events(context.Background(), bad, 0)
		wantErr(t, "Events("+bad+")", err, "is not a run ID")
	}
}

// #10-K1
func TestDamagedMetaMakesRunIncomplete(t *testing.T) {
	cases := map[string]func(m Meta) []byte{
		"garbage": func(Meta) []byte { return []byte("not json") },
		"unknown field": func(m Meta) []byte {
			data, _ := json.Marshal(m)
			return append(data[:len(data)-1], []byte(`,"args":"x"}`)...)
		},
		"future version": func(m Meta) []byte {
			m.Version = FormatVersion + 1
			data, _ := json.Marshal(m)
			return data
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t, t.TempDir(), Options{})
			rec := start(t, s, acme)
			appendN(t, rec, 2)
			finish(t, rec, StatusSucceeded)
			if err := os.WriteFile(filepath.Join(rec.dir, metaFile), corrupt(rec.Meta()), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := s.Load(context.Background(), rec.Meta().ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != StatusIncomplete || got.Meta.ID != rec.Meta().ID || got.Meta.Action != "" || got.LastEventID != 2 || !strings.Contains(got.Problem, metaFile) {
				t.Fatalf("status %q meta %+v last event %d problem %q; want incomplete naming %s", got.Status, got.Meta, got.LastEventID, got.Problem, metaFile)
			}
		})
	}
}

// #10-K1: unreadable parts of a run directory are named, not guessed.
func TestUnreadableRunFiles(t *testing.T) {
	t.Run("result is a directory", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		rec := start(t, s, acme)
		if err := os.Mkdir(filepath.Join(rec.dir, resultFile), 0o700); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Load(context.Background(), rec.Meta().ID)
		if got.Status != StatusIncomplete || !strings.Contains(got.Problem, resultFile+" is unreadable") {
			t.Fatalf("status %q problem %q; want incomplete, %s unreadable", got.Status, got.Problem, resultFile)
		}
	})
	t.Run("events is a directory", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		rec := start(t, s, acme)
		path := filepath.Join(rec.dir, eventsFile)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Load(context.Background(), rec.Meta().ID)
		if got.Status != StatusIncomplete || !strings.Contains(got.Problem, "is a directory") {
			t.Fatalf("status %q problem %q; want incomplete with the read error", got.Status, got.Problem)
		}
	})
	t.Run("events missing", func(t *testing.T) {
		s := newStore(t, t.TempDir(), Options{})
		rec := start(t, s, acme)
		finish(t, rec, StatusSucceeded)
		if err := os.Remove(filepath.Join(rec.dir, eventsFile)); err != nil {
			t.Fatal(err)
		}
		got, err := s.Load(context.Background(), rec.Meta().ID)
		if err != nil || got.Status != StatusSucceeded || got.Problem != "" {
			t.Fatalf("status %q problem %q err %v; a run without events is not damaged", got.Status, got.Problem, err)
		}
		events, err := s.Events(context.Background(), rec.Meta().ID, 0)
		if err != nil || len(events) != 0 {
			t.Fatalf("Events = %v, %v; want none", events, err)
		}
	})
}

// #10-K1
func TestListSkipsStrayEntriesAndReportsErrors(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	finish(t, rec, StatusSucceeded)
	stray := map[string]bool{ // path below runs/ → is a directory
		"README":         false,
		"acme/notes":     true,
		"acme/notes.txt": false,
		// a file named like a run ID is not a run directory
		"acme/20260910T000000Z-00000000": false,
	}
	for path, dir := range stray {
		full := filepath.Join(s.root, path)
		var err error
		if dir {
			err = os.Mkdir(full, 0o700)
		} else {
			err = os.WriteFile(full, nil, 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	runs, err := s.List(context.Background())
	if err != nil || len(runs) != 1 || runs[0].Meta.ID != rec.Meta().ID {
		t.Fatalf("List = %+v, %v; want only %s", runs, err, rec.Meta().ID)
	}

	if _, err := s.List(cancelled()); !errors.Is(err, context.Canceled) {
		t.Fatalf("List with a cancelled context = %v", err)
	}
	if _, err := s.Load(cancelled(), rec.Meta().ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Load with a cancelled context = %v", err)
	}
	if _, err := s.Events(cancelled(), rec.Meta().ID, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Events with a cancelled context = %v", err)
	}

	// Run as a normal user: root reads a mode-0 directory, and this check then fails loudly.
	group := filepath.Join(s.root, "acme")
	if err := os.Chmod(group, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(group, 0o700) })
	_, err = s.List(context.Background())
	wantErr(t, "List with an unreadable customer directory (tests must not run as root)", err, "cannot list the runs directory")
	if err := os.Chmod(group, 0o700); err != nil {
		t.Fatal(err)
	}

	other := newStore(t, t.TempDir(), Options{})
	if err := os.WriteFile(other.root, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = other.List(context.Background())
	wantErr(t, "List with runs/ as a file", err, "cannot list the runs directory")
}

// #10-K3: a live subscriber behind the window reads from disk; a shortened or damaged file is an
// error, not an endless loop.
func TestCatchUpReportsDamagedEvents(t *testing.T) {
	cases := map[string]struct{ content, want string }{
		"truncated": {"", "before the recorded events"},
		"damaged":   {"not json\n", "cannot replay " + eventsFile},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t, t.TempDir(), Options{Window: 2})
			rec := start(t, s, acme)
			appendN(t, rec, 5)
			if !equalIDs(eventIDs(rec.window), []uint64{4, 5}) {
				t.Fatalf("window holds %v, want the last 2 events", eventIDs(rec.window))
			}
			sub, err := rec.Subscribe(0)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(rec.dir, eventsFile), []byte(c.content), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err = sub.Next(ctx)
			wantErr(t, "Next", err, c.want)
		})
	}
}

// #10-K3: one catch-up batch holds at most the window, so a far-behind subscriber is bounded.
func TestCatchUpBatchIsBounded(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{Window: 4})
	rec := start(t, s, acme)
	appendN(t, rec, 20)
	sub, err := rec.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	if e, err := sub.Next(context.Background()); err != nil || e.ID != 1 {
		t.Fatalf("Next = %+v, %v", e, err)
	}
	if !equalIDs(eventIDs(sub.pending), []uint64{2, 3, 4}) {
		t.Fatalf("pending after the first catch-up = %v, want 2..4", eventIDs(sub.pending))
	}
}

// #10-K3: Close must not wait for a subscriber that has not consumed its last wake-up.
func TestCloseDoesNotWaitForAPendingWake(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	sub, err := rec.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	appendN(t, rec, 1) // fills the wake-up slot; the subscriber never reads it
	closed := make(chan struct{})
	go func() {
		sub.Close()
		sub.Close() // a second Close is a no-op
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked on a pending wake-up")
	}
	if _, err := sub.Next(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after Close = %v, want ErrClosed", err)
	}
}

// #10-K3
func TestNextStopsOnContext(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	appendN(t, rec, 1)
	sub, err := rec.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Next(cancelled()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next with a cancelled context and an event ready = %v, want context.Canceled", err)
	}
	if e, err := sub.Next(context.Background()); err != nil || e.ID != 1 {
		t.Fatalf("Next = %+v, %v", e, err)
	}

	parent, cancel := context.WithCancel(context.Background())
	ctx := &waitingCtx{Context: parent, waiting: make(chan struct{})}
	blocked := make(chan error, 1)
	go func() {
		_, err := sub.Next(ctx)
		blocked <- err
	}()
	select {
	case <-ctx.waiting:
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked Next never waits on its context")
	}
	cancel()
	select {
	case err := <-blocked:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked Next after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a blocked Next ignores its context")
	}
}
