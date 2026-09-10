// Package run keeps durable records of action runs under the ktags state root (ADR-0002), so a
// run stays observable after its terminal disconnects or the service restarts.
//
// Each run owns one directory, <state>/runs/<customer>/<run-id>/, holding meta.json (written
// once, never changed), events.jsonl (append-only, one versioned event per line, IDs counting up
// from 1) and result.json (written once, atomically, when the run ends). A run without a result is
// running; a result that cannot be read is reported as incomplete while its events stay readable.
// A Recorder writes one run and serves live subscribers from a bounded in-memory window; a
// subscriber that falls behind catches up from events.jsonl, so it never slows the action down.
// Every persisted free-text field passes through the Store's redactor first.
package run
