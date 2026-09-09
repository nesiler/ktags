// Package app is the executable's composition root.
package app

import (
	"io"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/cli"
	"github.com/nesiler/ktags/internal/core/domain"
	coreruntime "github.com/nesiler/ktags/internal/core/runtime"
)

// Run wires the application and executes the command-line adapter.
func Run(version string, args []string, stdout, stderr io.Writer) int {
	build := domain.Build{Name: "ktags", Version: version}
	versionAction := actions.NewVersion(build)
	runtime := coreruntime.New(versionAction)
	return cli.Run(runtime, args, stdout, stderr)
}
