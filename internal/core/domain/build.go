// Package domain contains transport-independent application values.
package domain

// Build identifies the ktags executable presented to operators.
type Build struct {
	Name    string
	Version string
}
