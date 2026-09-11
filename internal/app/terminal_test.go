package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Only a real terminal may answer a confirmation; redirected stdin is refused, not read.
func TestIsTerminal(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if isTerminal(file) {
		t.Fatal("a regular file counts as a terminal")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	if isTerminal(null) {
		t.Fatal("/dev/null, a character device, counts as a terminal")
	}
}
