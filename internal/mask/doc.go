// Package mask replaces secret material in free text before it reaches a log, the screen, an
// error or a run record (security.md §1, ADR-0002). Mask is a plain func(string) string so every
// boundary can take it as a value. Every pattern is matched against the original text and the
// matched ranges are merged before replacement, so one pattern's replacement never hides a
// secret from another. The package depends on the standard library only.
package mask
