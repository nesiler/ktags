// Package cli adapts command-line input and output to the application runtime.
package cli

import (
	"fmt"
	"io"

	"github.com/nesiler/ktags/internal/core/domain"
)

const unavailable = "ktags: nothing to run yet; see README.md and the open GitHub issues\n"

// Runtime is the application behaviour used by the command-line adapter.
type Runtime interface {
	Version() domain.Build
}

// Run dispatches command-line arguments and returns the process exit code.
func Run(runtime Runtime, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		build := runtime.Version()
		_, _ = fmt.Fprintln(stdout, build.Name, build.Version)
		return 0
	}

	_, _ = fmt.Fprint(stderr, unavailable)
	return 2
}
