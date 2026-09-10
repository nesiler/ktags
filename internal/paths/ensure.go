package paths

import (
	"context"
	"fmt"
	"os"
)

// dirMode is private to the operator: roots hold inventories, secret references, run records and
// the service socket. It also matches the XDG specification's mode for directories it creates.
const dirMode os.FileMode = 0o700

// Ensure creates every root with mode 0700. It is idempotent: existing private roots are left
// as they are. An existing root that is not a directory, or that grants group or other access,
// is refused rather than repaired, so the operator sees and decides on the looser setup.
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
	// MkdirAll already refused an existing non-directory, so only the mode is left to check.
	if perm := info.Mode().Perm(); perm&^dirMode != 0 {
		return &Error{
			Setting: root.Setting,
			Problem: fmt.Sprintf("the %s root %q has mode %04o; group and others must have no access",
				root.Name, root.Path, perm),
			Next: fmt.Sprintf("chmod 700 %q", root.Path),
		}
	}
	return nil
}
