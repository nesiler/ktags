package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/nesiler/ktags/internal/paths"
)

// rootMode is the only mode a root may have (internal/paths: private to the operator).
const rootMode fs.FileMode = 0o700

func init() {
	roots := []struct {
		name string
		pick func(paths.Roots) paths.Root
	}{
		{"config", func(r paths.Roots) paths.Root { return r.Config }},
		{"data", func(r paths.Roots) paths.Root { return r.Data }},
		{"state", func(r paths.Roots) paths.Root { return r.State }},
		{"runtime", func(r paths.Roots) paths.Root { return r.Runtime }},
	}
	for i, root := range roots {
		pick := root.pick
		register(Check{
			ID:    "roots." + root.name,
			Title: "the " + root.name + " root is a private directory of the operator",
			Order: 20 + i,
			Run: func(_ context.Context, env Env) []Result {
				return []Result{checkRoot(pick(env.Roots), env.UID)}
			},
		})
	}
}

func checkRoot(root paths.Root, uid int) Result {
	p := quote(root.Path)
	from := fmt.Sprintf("%s (from %s)", root.Path, root.Setting)
	info, err := os.Stat(root.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Result{Status: StatusFail, Evidence: from + " does not exist", Fix: "mkdir -p -m 700 " + p}
	case err != nil:
		return Result{Status: StatusFail, Evidence: fmt.Sprintf("cannot inspect %s: %v", from, errors.Unwrap(err)), Fix: "chmod u+rwx " + quote(filepath.Dir(root.Path))}
	case !info.IsDir():
		return Result{Status: StatusFail, Evidence: from + " is not a directory", Fix: fmt.Sprintf("mv %s %s && mkdir -m 700 %s", p, quote(root.Path+".bak"), p)}
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != uid {
		return Result{
			Status:   StatusFail,
			Evidence: fmt.Sprintf("%s is owned by uid %d, not by the operator (uid %d)", from, st.Uid, uid),
			Fix:      fmt.Sprintf("sudo chown %d %s && sudo chmod 700 %s", uid, p, p),
		}
	}
	if perm := info.Mode().Perm(); perm != rootMode {
		return Result{Status: StatusFail, Evidence: fmt.Sprintf("%s has mode %04o; want 0700", from, perm), Fix: "chmod 700 " + p}
	}
	return Result{Status: StatusOK, Evidence: from + ", mode 0700, owned by the operator"}
}
