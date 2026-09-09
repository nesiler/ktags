// Package actions defines application operations independently of user interfaces.
package actions

import "github.com/nesiler/ktags/internal/core/domain"

// Version reports the executable build identity.
type Version struct {
	build domain.Build
}

// NewVersion creates the version action for build.
func NewVersion(build domain.Build) Version {
	return Version{build: build}
}

// Run returns the build identity.
func (v Version) Run() domain.Build {
	return v.build
}
