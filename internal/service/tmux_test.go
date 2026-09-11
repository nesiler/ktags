package service

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #12-K3: tmux is not a product dependency (ADR-0001). No Go file of the module, tests
// included, and no module requirement may name it. This file is the one exception.
func TestNoTmuxDependency(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	self, err := filepath.Abs("tmux_test.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// spikes are throwaway modules outside the product; .git holds no source.
			if name := d.Name(); path != root && (name == "spikes" || name == ".git" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if path == self || (!strings.HasSuffix(path, ".go") && d.Name() != "go.mod" && d.Name() != "go.sum") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		if strings.Contains(strings.ToLower(string(data)), "tmux") {
			t.Errorf("%s names tmux; the service must not depend on it", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("checked only %d files below %s; the walk did not see the module", checked, root)
	}
}
