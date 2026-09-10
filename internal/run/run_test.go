package run

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/actions"
)

// fakeClock advances one second per call, so every timestamp and run ID is deterministic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(time.Second)
	return c.t
}

// seqRandom returns 0, 1, 2, … as four-byte big-endian numbers.
type seqRandom struct {
	mu sync.Mutex
	n  uint32
}

func (r *seqRandom) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], r.n)
	r.n++
	return copy(p, b[:]), nil
}

func identity(s string) string { return s }

func newStore(t *testing.T, root string, opts Options) *Store {
	t.Helper()
	if opts.Redact == nil {
		opts.Redact = identity
	}
	if opts.Clock == nil {
		opts.Clock = (&fakeClock{t: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)}).now
	}
	if opts.Random == nil {
		opts.Random = &seqRandom{}
	}
	s, err := NewStore(root, opts)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

var acme = actions.Target{Kind: actions.TargetCustomer, Customer: "acme"}

func start(t *testing.T, s *Store, target actions.Target) *Recorder {
	t.Helper()
	rec, err := s.Start(context.Background(), "cluster health", target)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return rec
}

func appendN(t *testing.T, rec *Recorder, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := rec.Append(actions.Event{Step: "probe", Message: fmt.Sprintf("event %d", i+1)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func finish(t *testing.T, rec *Recorder, status Status) {
	t.Helper()
	if _, err := rec.Finish(status, "done: "+string(status)); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func eventIDs(events []Event) []uint64 {
	ids := make([]uint64, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	return ids
}

func span(from, to uint64) []uint64 {
	var ids []uint64
	for id := from; id <= to; id++ {
		ids = append(ids, id)
	}
	return ids
}

func equalIDs(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// drain reads a subscription until io.EOF.
func drain(t *testing.T, sub *Subscription) []uint64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var ids []uint64
	for {
		e, err := sub.Next(ctx)
		if errors.Is(err, io.EOF) {
			return ids
		}
		if err != nil {
			t.Fatalf("Next after %v: %v", ids, err)
		}
		ids = append(ids, e.ID)
	}
}

// #10-K1
func TestRestartedReaderReconstructsRuns(t *testing.T) {
	root := t.TempDir()
	writer := newStore(t, root, Options{})
	want := []struct {
		target actions.Target
		status Status
	}{
		{acme, StatusSucceeded},
		{actions.Target{Kind: actions.TargetGlobal}, StatusFailed},
		{actions.Target{Kind: actions.TargetNode, Customer: "globex", Node: "srv-1"}, StatusCancelled},
		{acme, StatusRunning},
	}
	for _, w := range want {
		rec := start(t, writer, w.target)
		appendN(t, rec, 2)
		if w.status != StatusRunning {
			finish(t, rec, w.status)
		}
	}

	// A second Store on the same root stands for a restarted service or client.
	reader := newStore(t, root, Options{})
	runs, err := reader.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != len(want) {
		t.Fatalf("List returned %d runs, want %d", len(runs), len(want))
	}
	for i, w := range want {
		got := runs[i]
		if got.Status != w.status || got.Meta.Target != (Target(w.target)) || got.Meta.Action != "cluster health" {
			t.Errorf("run %d: status %q target %+v action %q, want %q %+v", i, got.Status, got.Meta.Target, got.Meta.Action, w.status, w.target)
		}
		if got.LastEventID != 2 || got.Problem != "" {
			t.Errorf("run %d: last event %d problem %q, want 2 and none", i, got.LastEventID, got.Problem)
		}
		if w.status == StatusRunning {
			if got.Result != nil {
				t.Errorf("run %d: running run has a result %+v", i, got.Result)
			}
			continue
		}
		if got.Result == nil || got.Result.Summary != "done: "+string(w.status) || got.Result.LastEventID != 2 {
			t.Errorf("run %d: result %+v", i, got.Result)
		}
		events, err := reader.Events(context.Background(), got.Meta.ID, 1)
		if err != nil || !equalIDs(eventIDs(events), []uint64{2}) || events[0].Message != "event 2" {
			t.Errorf("run %d: Events after 1 = %+v, %v", i, events, err)
		}
		loaded, err := reader.Load(context.Background(), got.Meta.ID)
		if err != nil || loaded.Status != w.status {
			t.Errorf("run %d: Load = %q, %v", i, loaded.Status, err)
		}
	}
}

func TestListWithoutRunsIsEmpty(t *testing.T) {
	runs, err := newStore(t, t.TempDir(), Options{}).List(context.Background())
	if err != nil || len(runs) != 0 {
		t.Fatalf("List = %v, %v; want no runs", runs, err)
	}
}

// #10-K2
func TestPartialResultIsReportedIncomplete(t *testing.T) {
	cases := []struct {
		name    string
		corrupt func(valid []byte) []byte
	}{
		{"truncated", func(valid []byte) []byte { return valid[:len(valid)/2] }},
		{"empty", func([]byte) []byte { return nil }},
		{"garbage", func([]byte) []byte { return []byte("\x00\x00\x00") }},
		{"two documents", func(valid []byte) []byte { return append(bytes.Clone(valid), valid...) }},
		{"unknown field", func([]byte) []byte {
			return []byte(`{"v":1,"status":"succeeded","summary":"","finished_at":"2026-09-10T12:00:00Z","last_event_id":2,"extra":1}`)
		}},
		{"not final", func([]byte) []byte {
			return []byte(`{"v":1,"status":"running","summary":"","finished_at":"2026-09-10T12:00:00Z","last_event_id":2}`)
		}},
		{"future version", func([]byte) []byte {
			return []byte(`{"v":2,"status":"succeeded","summary":"","finished_at":"2026-09-10T12:00:00Z","last_event_id":2}`)
		}},
		{"events missing", func([]byte) []byte {
			return []byte(`{"v":1,"status":"succeeded","summary":"","finished_at":"2026-09-10T12:00:00Z","last_event_id":3}`)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStore(t, t.TempDir(), Options{})
			rec := start(t, s, acme)
			appendN(t, rec, 2)
			finish(t, rec, StatusSucceeded)
			path := filepath.Join(rec.dir, resultFile)
			valid, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, c.corrupt(valid), 0o600); err != nil {
				t.Fatal(err)
			}

			got, err := s.Load(context.Background(), rec.Meta().ID)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.Status != StatusIncomplete || got.Result != nil || !strings.Contains(got.Problem, resultFile) {
				t.Fatalf("got status %q result %+v problem %q; want incomplete naming %s", got.Status, got.Result, got.Problem, resultFile)
			}
			events, err := s.Events(context.Background(), rec.Meta().ID, 0)
			if err != nil || !equalIDs(eventIDs(events), []uint64{1, 2}) {
				t.Fatalf("events after a damaged result = %v, %v; want 1 and 2", eventIDs(events), err)
			}
		})
	}
}

// failingFile writes half of what it is given and then fails, like a full disk.
type failingFile struct{ tempFile }

func (f failingFile) Write(p []byte) (int, error) {
	n, _ := f.tempFile.Write(p[:len(p)/2])
	return n, errors.New("no space left on device")
}

// #10-K2
func TestFailedResultWriteLeavesNoResult(t *testing.T) {
	cases := map[string]func(fs *fileSystem){
		"partial write": func(fs *fileSystem) {
			fs.createTemp = func(dir, pattern string) (tempFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingFile{f}, err
			}
		},
		"rename": func(fs *fileSystem) {
			fs.rename = func(string, string) error { return errors.New("rename refused") }
		},
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
			got, err := s.Load(context.Background(), rec.Meta().ID)
			if err != nil || got.Status != StatusRunning || got.LastEventID != 2 {
				t.Fatalf("after failed Finish: status %q last event %d err %v; want running with 2 events", got.Status, got.LastEventID, err)
			}

			s.fs = osFS
			if _, err := rec.Finish(StatusSucceeded, "ok"); err != nil {
				t.Fatalf("retried Finish: %v", err)
			}
			got, err = s.Load(context.Background(), rec.Meta().ID)
			if err != nil || got.Status != StatusSucceeded || got.LastEventID != 2 {
				t.Fatalf("after retried Finish: status %q last event %d err %v", got.Status, got.LastEventID, err)
			}
		})
	}
}

func TestTornEventLineIsIgnored(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	appendN(t, rec, 2)
	finish(t, rec, StatusFailed)
	appendRaw(t, filepath.Join(rec.dir, eventsFile), `{"v":1,"id":3,"ti`)

	got, err := s.Load(context.Background(), rec.Meta().ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || got.LastEventID != 2 || !strings.Contains(got.Problem, "partial line") {
		t.Fatalf("status %q last event %d problem %q; want failed, 2, a partial-line note", got.Status, got.LastEventID, got.Problem)
	}
	events, err := s.Events(context.Background(), rec.Meta().ID, 0)
	if err != nil || !equalIDs(eventIDs(events), []uint64{1, 2}) {
		t.Fatalf("Events = %v, %v; want 1 and 2", eventIDs(events), err)
	}
}

func TestDamagedEventLineMakesRunIncomplete(t *testing.T) {
	cases := map[string]string{
		"garbage":       "not json\n",
		"gap":           `{"v":1,"id":4,"time":"2026-09-10T12:00:00Z","message":"x"}` + "\n",
		"duplicate":     `{"v":1,"id":2,"time":"2026-09-10T12:00:00Z","message":"x"}` + "\n",
		"wrong version": `{"v":9,"id":3,"time":"2026-09-10T12:00:00Z","message":"x"}` + "\n",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			s := newStore(t, t.TempDir(), Options{})
			rec := start(t, s, acme)
			appendN(t, rec, 2)
			appendRaw(t, filepath.Join(rec.dir, eventsFile), line)

			got, err := s.Load(context.Background(), rec.Meta().ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != StatusIncomplete || got.LastEventID != 2 || !strings.Contains(got.Problem, "line 3") {
				t.Fatalf("status %q last event %d problem %q; want incomplete at line 3", got.Status, got.LastEventID, got.Problem)
			}
			events, err := s.Events(context.Background(), rec.Meta().ID, 0)
			if err == nil || !equalIDs(eventIDs(events), []uint64{1, 2}) {
				t.Fatalf("Events = %v, %v; want 1 and 2 and an error", eventIDs(events), err)
			}
		})
	}
}

func TestMissingMetaMakesRunIncomplete(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	finish(t, rec, StatusSucceeded)
	if err := os.Remove(filepath.Join(rec.dir, metaFile)); err != nil {
		t.Fatal(err)
	}
	runs, err := s.List(context.Background())
	if err != nil || len(runs) != 1 {
		t.Fatalf("List = %v, %v", runs, err)
	}
	if runs[0].Status != StatusIncomplete || runs[0].Meta.ID != rec.Meta().ID || !strings.Contains(runs[0].Problem, metaFile) {
		t.Fatalf("got %+v; want incomplete naming %s", runs[0], metaFile)
	}
}

func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// #10-K3
func TestSlowSubscriberDoesNotBlockAppend(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{Window: 4})
	rec := start(t, s, acme)
	idle, err := rec.Subscribe(0) // never reads while the action runs
	if err != nil {
		t.Fatal(err)
	}
	const total = 1000
	done := make(chan error, 1)
	go func() {
		for i := 0; i < total; i++ {
			if _, err := rec.Append(actions.Event{Message: "tick"}); err != nil {
				done <- err
				return
			}
		}
		_, err := rec.Finish(StatusSucceeded, "ok")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the action is blocked by a subscriber that does not read")
	}
	if got := drain(t, idle); !equalIDs(got, span(1, total)) {
		t.Fatalf("the late reader got %d events (first %v); want 1..%d once each", len(got), got[:min(len(got), 5)], total)
	}
}

// #10-K3
func TestResumeHasNoDuplicateIDs(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := newStore(t, root, Options{Window: 4})
	rec := start(t, s, acme)
	appendN(t, rec, 3)
	first, err := rec.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := first.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	first.Close() // the client disconnects
	if _, err := first.Next(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after Close = %v, want ErrClosed", err)
	}

	appendN(t, rec, 20) // events 4..23; 4..19 leave the in-memory window
	resumed, err := rec.Subscribe(first.Cursor())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Subscribe(24); err == nil {
		t.Fatal("Subscribe accepted a cursor beyond the last event")
	}
	finish(t, rec, StatusSucceeded)
	if got := drain(t, resumed); !equalIDs(got, span(4, 23)) {
		t.Fatalf("resumed reader got %v, want 4..23", got)
	}

	// A restarted client without the recorder resumes from the persisted events.
	events, err := newStore(t, root, Options{}).Events(ctx, rec.Meta().ID, 21)
	if err != nil || !equalIDs(eventIDs(events), []uint64{22, 23}) {
		t.Fatalf("replay after 21 = %v, %v; want 22 and 23", eventIDs(events), err)
	}
}

func TestConcurrentAppendAndSubscribe(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{Window: 8})
	rec := start(t, s, acme)
	const readers, total = 4, 500
	results := make([][]uint64, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		sub, err := rec.Subscribe(0)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			for {
				e, err := sub.Next(ctx)
				if err != nil {
					return
				}
				results[i] = append(results[i], e.ID)
			}
		}()
	}
	for i := 0; i < total; i++ {
		rec.Report(actions.Event{Message: "tick"})
	}
	finish(t, rec, StatusSucceeded)
	wg.Wait()
	if err := rec.Err(); err != nil {
		t.Fatal(err)
	}
	for i, got := range results {
		if !equalIDs(got, span(1, total)) {
			t.Errorf("reader %d got %d events, want 1..%d once each", i, len(got), total)
		}
	}
}

func TestNextHonoursContextAndClose(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	sub, err := rec.Subscribe(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sub.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next with a cancelled context = %v", err)
	}
	blocked := make(chan error, 1)
	go func() {
		_, err := sub.Next(context.Background())
		blocked <- err
	}()
	sub.Close()
	if err := <-blocked; !errors.Is(err, ErrClosed) {
		t.Fatalf("blocked Next after Close = %v, want ErrClosed", err)
	}
}

func TestSubscriberLimit(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{MaxSubscribers: 2})
	rec := start(t, s, acme)
	a, errA := rec.Subscribe(0)
	_, errB := rec.Subscribe(0)
	if errA != nil || errB != nil {
		t.Fatal(errA, errB)
	}
	if _, err := rec.Subscribe(0); err == nil {
		t.Fatal("a third subscriber was accepted with MaxSubscribers 2")
	}
	a.Close()
	if _, err := rec.Subscribe(0); err != nil {
		t.Fatalf("Subscribe after Close: %v", err)
	}
}

func TestRejectsUnsafeNames(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	targets := map[string]actions.Target{
		"traversal":           {Kind: actions.TargetCustomer, Customer: "../acme"},
		"slash":               {Kind: actions.TargetCustomer, Customer: "acme/x"},
		"upper case":          {Kind: actions.TargetCustomer, Customer: "Acme"},
		"global dir":          {Kind: actions.TargetCustomer, Customer: "_global"},
		"empty customer":      {Kind: actions.TargetCustomer},
		"global with name":    {Kind: actions.TargetGlobal, Customer: "acme"},
		"node without node":   {Kind: actions.TargetNode, Customer: "acme"},
		"customer with node":  {Kind: actions.TargetCustomer, Customer: "acme", Node: "srv-1"},
		"unknown target kind": {Kind: "fleet", Customer: "acme"},
	}
	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Start(context.Background(), "cluster health", target); err == nil {
				t.Fatalf("Start accepted %+v", target)
			}
		})
	}
	if _, err := s.Start(context.Background(), " ", acme); err == nil {
		t.Error("Start accepted an empty action ID")
	}
	for _, id := range []string{"../../etc", "20260910T120000Z-0000000", "*", ""} {
		if _, err := s.Load(context.Background(), id); err == nil {
			t.Errorf("Load accepted run ID %q", id)
		}
	}
	if _, err := os.Stat(s.root); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("refused starts created %s", s.root)
	}
}

func TestFinishIsFinal(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	appendN(t, rec, 1)
	if _, err := rec.Finish(StatusRunning, "x"); err == nil {
		t.Fatal("Finish accepted a non-final status")
	}
	finish(t, rec, StatusCancelled)
	before, err := os.ReadFile(filepath.Join(rec.dir, resultFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rec.Finish(StatusSucceeded, "again"); err == nil {
		t.Error("a second Finish was accepted")
	}
	if _, err := rec.Append(actions.Event{Message: "late"}); err == nil {
		t.Error("Append after Finish was accepted")
	}
	after, err := os.ReadFile(filepath.Join(rec.dir, resultFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("result.json changed after Finish: %s -> %s (%v)", before, after, err)
	}
	if got, _ := s.Load(context.Background(), rec.Meta().ID); got.Status != StatusCancelled || got.LastEventID != 1 {
		t.Fatalf("status %q last event %d, want cancelled with 1 event", got.Status, got.LastEventID)
	}
}

func TestRedactorAppliesToPersistedText(t *testing.T) {
	const secret = "tskey-example-0000"
	mask := func(s string) string { return strings.ReplaceAll(s, secret, "[masked]") }
	if _, err := NewStore(t.TempDir(), Options{}); err == nil {
		t.Fatal("NewStore accepted a missing redactor")
	}
	s := newStore(t, t.TempDir(), Options{Redact: mask})
	rec := start(t, s, acme)
	event, err := rec.Append(actions.Event{Step: "join " + secret, Message: "auth key " + secret})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(event.Step+event.Message, secret) {
		t.Errorf("returned event holds the secret: %+v", event)
	}
	if _, err := rec.Finish(StatusFailed, "rejected "+secret); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{metaFile, eventsFile, resultFile} {
		data, err := os.ReadFile(filepath.Join(rec.dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(secret)) {
			t.Errorf("%s holds the unmasked secret", name)
		}
		if !bytes.Contains(data, []byte("[masked]")) && name != metaFile {
			t.Errorf("%s does not hold the masked text", name)
		}
	}
}

func TestRunFilesArePrivate(t *testing.T) {
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	finish(t, rec, StatusSucceeded)
	for path, want := range map[string]os.FileMode{
		s.root:                             dirMode,
		filepath.Dir(rec.dir):              dirMode,
		rec.dir:                            dirMode,
		filepath.Join(rec.dir, metaFile):   fileMode,
		filepath.Join(rec.dir, eventsFile): fileMode,
		filepath.Join(rec.dir, resultFile): fileMode,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s has mode %04o, want %04o", path, got, want)
		}
	}
}
