package run

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// List and find use one rule for what is a run: a directory named like a run ID in a customer
// directory. A stray file or symbolic link named like a run ID, and a run below a customer
// directory that is itself a symbolic link, are no run for either.
func TestStrayEntriesAreNoRuns(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, t.TempDir(), Options{})
	rec := start(t, s, acme)
	finish(t, rec, StatusSucceeded)
	loaded, err := s.Load(ctx, rec.Meta().ID)
	if err != nil {
		t.Fatal(err)
	}
	customer := filepath.Dir(loaded.Dir)
	runs := filepath.Dir(customer)

	const fileID, linkID, linkedID = "20260910T120000Z-aaaaaaaa", "20260910T120000Z-bbbbbbbb", "20260910T120000Z-cccccccc"
	if err := os.WriteFile(filepath.Join(customer, fileID), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loaded.Dir, filepath.Join(customer, linkID)); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Mkdir(filepath.Join(elsewhere, linkedID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(runs, "globex")); err != nil {
		t.Fatal(err)
	}

	listed, err := s.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].Meta.ID != rec.Meta().ID {
		t.Fatalf("List = %+v, %v; want only the real run", listed, err)
	}
	for _, id := range []string{fileID, linkID, linkedID} {
		if _, err := s.Load(ctx, id); err == nil || !strings.Contains(err.Error(), "no such run") {
			t.Errorf("Load(%s) = %v, want no such run", id, err)
		}
		if _, err := s.Events(ctx, id, 0); err == nil || !strings.Contains(err.Error(), "no such run") {
			t.Errorf("Events(%s) = %v, want no such run", id, err)
		}
	}
}
