package paths

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/nesiler/ktags/internal/shell"
)

// dirMode is private to the operator: roots hold inventories, secret references, run records and
// the service socket. It also matches the XDG specification's mode for directories it creates.
const dirMode os.FileMode = 0o700

// currentUID is the operator's user ID; tests replace it to stand for a root another user owns.
var currentUID = os.Getuid

// Ensure creates every root with mode 0700. It is idempotent: existing private roots are left
// as they are. An existing root that is not a directory, that another user owns, or that grants
// group or other access, is refused rather than repaired, so the operator sees and decides on
// the setup.
func Ensure(ctx context.Context, roots Roots) error {
	for _, root := range roots.All() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ensure(root); err != nil {
			return err
		}
	}
	return nil
}

func ensure(root Root) error {
	if err := os.MkdirAll(root.Path, dirMode); err != nil {
		return &Error{
			Setting: root.Setting,
			Problem: fmt.Sprintf("cannot create the %s root %q", root.Name, root.Path),
			Next: fmt.Sprintf("make %q a writable directory, or set %s to another absolute path",
				root.Path, root.Override),
			Err: err,
		}
	}
	info, err := os.Stat(root.Path)
	if err != nil {
		return &Error{
			Setting: root.Setting,
			Problem: fmt.Sprintf("cannot inspect the %s root %q", root.Name, root.Path),
			Next:    fmt.Sprintf("check access to %q, or set %s to another absolute path", root.Path, root.Override),
			Err:     err,
		}
	}
	// MkdirAll already refused an existing non-directory, so owner and mode are left to check.
	// A root another user owns passes the mode check, and every later write in it would fail.
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int64(st.Uid) != int64(currentUID()) {
		uid := currentUID()
		return &Error{
			Setting: root.Setting,
			Problem: fmt.Sprintf("the %s root %q is owned by user ID %d, not by you (user ID %d)",
				root.Name, root.Path, st.Uid, uid),
			Next: fmt.Sprintf("sudo chown %d %s, or set %s to another absolute path",
				uid, shell.Quote(root.Path), root.Override),
		}
	}
	if perm := info.Mode().Perm(); perm&^dirMode != 0 {
		return &Error{
			Setting: root.Setting,
			Problem: fmt.Sprintf("the %s root %q has mode %04o; group and others must have no access",
				root.Name, root.Path, perm),
			Next: "chmod 700 " + shell.Quote(root.Path),
		}
	}
	return nil
}
