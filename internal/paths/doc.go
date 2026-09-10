// Package paths resolves every ktags filesystem root on the operator's laptop: config, data,
// state and runtime. It is the only package that reads $HOME, the XDG base directory variables
// and the KTAGS_* directory overrides. Resolve is pure so callers can inspect or report the roots
// without touching the disk; Ensure is the separate step that creates them with private modes.
package paths
