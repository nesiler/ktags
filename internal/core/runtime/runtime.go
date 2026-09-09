// Package runtime coordinates actions without depending on delivery adapters.
package runtime

import (
	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/core/domain"
)

// Runtime exposes application operations to adapters.
type Runtime struct {
	version actions.Version
}

// New creates an application runtime from its actions.
func New(version actions.Version) Runtime {
	return Runtime{version: version}
}

// Version returns the executable build identity.
func (r Runtime) Version() domain.Build {
	return r.version.Run()
}
