// Package tui is the full-screen ktags client. It renders what the local ktags service reports
// and sends actions to it; the service is its only data source and does all the work
// (ADR-0001). Leaving a run view or quitting only detaches: runs continue in the service and
// reopening shows them again (ADR-0002). Screens, keys, states and confirmations follow the
// operator interaction standard in docs/guides/ui.md (ADR-0003).
package tui
