// Package fleet computes what the fleet summary shows for each customer: one attention state
// derived from the cached health, connection and inventory facts, and the order that puts the
// customers needing attention first. States are computed here, not in a view
// (docs/guides/ui.md §2), so the CLI and the TUI show the same state in the same order.
//
// The package reads only what it is given. A Source answers from its cache and never measures,
// so listing the fleet makes no network request. A customer without a measurement is "never
// measured" and a stale result is "stale": absence and old data are never shown as ok.
package fleet
