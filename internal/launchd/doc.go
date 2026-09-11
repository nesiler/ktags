// Package launchd runs the ktags service as a user launchd agent on macOS (ADR-0001). It is the
// infrastructure side of service.Launcher: it writes the job definition and calls launchctl, so
// the service package itself never starts a subprocess.
package launchd
