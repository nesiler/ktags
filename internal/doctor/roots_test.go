package doctor

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRootOK(t *testing.T) {
	env := testEnv(t)
	if err := os.MkdirAll(env.Roots.Config.Path, 0o700); err != nil {
		t.Fatal(err)
	}
	r := only(t, runCheck(t, "roots.config", env))
	want(t, r, StatusOK, "")
	if !strings.Contains(r.Evidence, env.Roots.Config.Path) || !strings.Contains(r.Evidence, "KTAGS_HOME") {
		t.Fatalf("evidence %q lacks the path or its setting", r.Evidence)
	}
}

// Each root check measures its own root, and its fix creates it.
func TestRootMissingIsCreatedByItsFix(t *testing.T) {
	env := testEnv(t)
	for _, root := range env.Roots.All() {
		id := "roots." + root.Name
		r := only(t, runCheck(t, id, env))
		want(t, r, StatusFail, "mkdir -p -m 700 "+quote(root.Path))
		if !strings.Contains(r.Evidence, root.Path+" (from KTAGS_HOME) does not exist") {
			t.Fatalf("%s: evidence %q", id, r.Evidence)
		}
		sh(t, r.Fix)
		want(t, only(t, runCheck(t, id, env)), StatusOK, "")
	}
}

// The mode must be exactly 0700: group or other access is refused, and so is a root the
// operator cannot write.
func TestRootModeIsFixedByChmod(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o770, 0o500} {
		t.Run(strconv.FormatUint(uint64(mode), 8), func(t *testing.T) {
			env := testEnv(t)
			path := env.Roots.Data.Path
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			r := only(t, runCheck(t, "roots.data", env))
			want(t, r, StatusFail, "chmod 700 "+quote(path))
			if !strings.Contains(r.Evidence, "has mode 0"+strconv.FormatUint(uint64(mode), 8)+"; want 0700") {
				t.Fatalf("evidence %q", r.Evidence)
			}
			sh(t, r.Fix)
			want(t, only(t, runCheck(t, "roots.data", env)), StatusOK, "")
		})
	}
}

func TestRootNotADirectory(t *testing.T) {
	env := testEnv(t)
	path := env.Roots.State.Path
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := only(t, runCheck(t, "roots.state", env))
	want(t, r, StatusFail, "")
	if !strings.Contains(r.Evidence, "is not a directory") {
		t.Fatalf("evidence %q", r.Evidence)
	}
	sh(t, r.Fix)
	want(t, only(t, runCheck(t, "roots.state", env)), StatusOK, "")
	// The file is kept, not deleted.
	if data, err := os.ReadFile(path + ".bak"); err != nil || string(data) != "x" {
		t.Fatalf("the moved file: %q, %v", data, err)
	}
}

// A root of another user is refused; the fix hands it to the operator. A test cannot chown,
// so it measures against another operator UID instead.
func TestRootOfAnotherUser(t *testing.T) {
	env := testEnv(t)
	path := env.Roots.Runtime.Path
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	env.UID = os.Getuid() + 1
	r := only(t, runCheck(t, "roots.runtime", env))
	uid := strconv.Itoa(env.UID)
	want(t, r, StatusFail, "sudo chown "+uid+" "+quote(path)+" && sudo chmod 700 "+quote(path))
	if !strings.Contains(r.Evidence, "not by the operator (uid "+uid+")") {
		t.Fatalf("evidence %q", r.Evidence)
	}
}

func TestRootThatCannotBeInspected(t *testing.T) {
	env := testEnv(t)
	path := env.Roots.Config.Path
	parent := filepath.Dir(path)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	r := only(t, runCheck(t, "roots.config", env))
	want(t, r, StatusFail, "chmod u+rwx "+quote(parent))
	if !strings.Contains(r.Evidence, "cannot inspect") || !strings.Contains(r.Evidence, "permission denied") {
		t.Fatalf("evidence %q", r.Evidence)
	}
	sh(t, r.Fix)
	want(t, only(t, runCheck(t, "roots.config", env)), StatusOK, "")
}
