package paths_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/paths"
)

// The chmod hint is a shell command: pasted into sh, it must name the root itself, also when the
// path holds characters a shell would expand or unescape.
func TestChmodHintIsQuotedForTheShell(t *testing.T) {
	for _, name := range []string{"plain", "dollar $HOME", "backtick `id`", `back\slash`, "single 'quote'", `mix $'\n"`} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), name)
			roots, err := paths.Resolve(env(map[string]string{"KTAGS_HOME": home}, ""))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(roots.Data.Path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(roots.Data.Path, 0o750); err != nil {
				t.Fatal(err)
			}
			var pathErr *paths.Error
			if err := paths.Ensure(context.Background(), roots); !errors.As(err, &pathErr) {
				t.Fatalf("Ensure: %v, want a *paths.Error", err)
			}
			arg, ok := strings.CutPrefix(pathErr.Next, "chmod 700 ")
			if !ok {
				t.Fatalf("next %q, want a chmod command", pathErr.Next)
			}
			out, err := exec.Command("sh", "-c", "printf %s "+arg).Output()
			if err != nil {
				t.Fatalf("sh: %v", err)
			}
			if string(out) != roots.Data.Path {
				t.Fatalf("the shell reads the hint %q as %q, want %q", pathErr.Next, out, roots.Data.Path)
			}
		})
	}
}

// A private root owned by another user is refused with the owner and a chown, and left as it is.
func TestEnsureRefusesForeignOwner(t *testing.T) {
	roots := tempRoots(t)
	if err := os.MkdirAll(roots.Data.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	paths.SetCurrentUID(t, os.Getuid()+1)

	err := paths.Ensure(context.Background(), roots)
	var pathErr *paths.Error
	if !errors.As(err, &pathErr) || pathErr.Setting != "KTAGS_HOME" {
		t.Fatalf("Ensure error = %v, want a KTAGS_HOME *paths.Error", err)
	}
	if !strings.Contains(pathErr.Problem, "is owned by user ID") || !strings.HasPrefix(pathErr.Next, "sudo chown ") {
		t.Fatalf("error = %q, want the owner and a chown", err)
	}
	assertOnlyNames(t, err, "KTAGS_HOME", nil)
	info, statErr := os.Stat(roots.Data.Path)
	if statErr != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("root after refusal: %v, %v; want it left at 0700", info, statErr)
	}
}

// The roots Ensure creates belong to the operator, so the owner check passes them.
func TestEnsureAcceptsOwnRoots(t *testing.T) {
	roots := tempRoots(t)
	paths.SetCurrentUID(t, os.Getuid())
	if err := paths.Ensure(context.Background(), roots); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := paths.Ensure(context.Background(), roots); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
}
