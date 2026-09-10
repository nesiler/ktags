package run

import (
	"time"

	"github.com/nesiler/ktags/internal/actions"
)

// FormatVersion is the only on-disk run format this build reads and writes. It is stored in
// every meta, event and result document as "v".
const FormatVersion = 1

// Status is the state of a run as reconstructed from disk.
type Status string

// Statuses. Succeeded, failed and cancelled are final and come from result.json; running means
// no result was written yet; incomplete means result.json exists but cannot be trusted.
const (
	StatusRunning    Status = "running"
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusIncomplete Status = "incomplete"
)

func (s Status) final() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled
}

// Target is what the run operated on, as recorded in meta.json.
type Target struct {
	Kind     actions.TargetKind `json:"kind"`
	Customer string             `json:"customer,omitempty"`
	Node     string             `json:"node,omitempty"`
}

// Meta is the immutable description of a run, written once when it starts.
type Meta struct {
	Version   int       `json:"v"`
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Target    Target    `json:"target"`
	StartedAt time.Time `json:"started_at"`
}

// Event is one persisted progress report. IDs are 1, 2, 3, … within a run; a client resumes by
// asking for the events after the last ID it has seen.
type Event struct {
	Version int       `json:"v"`
	ID      uint64    `json:"id"`
	Time    time.Time `json:"time"`
	Step    string    `json:"step,omitempty"`
	Message string    `json:"message"`
}

// Result is the final outcome of a run. LastEventID is the ID of the last event written before
// it, so a reader can tell whether events are missing.
type Result struct {
	Version     int       `json:"v"`
	Status      Status    `json:"status"`
	Summary     string    `json:"summary"`
	FinishedAt  time.Time `json:"finished_at"`
	LastEventID uint64    `json:"last_event_id"`
}

// Run is one run as reconstructed from its directory.
type Run struct {
	// Dir is the run directory.
	Dir  string
	Meta Meta
	// Status is running until a readable result exists; incomplete when the directory is damaged.
	Status Status
	// Result is nil while the run is running or when result.json cannot be read.
	Result *Result
	// LastEventID is the ID of the last complete event line in events.jsonl.
	LastEventID uint64
	// Problem describes the damage when Status is incomplete, or a torn final event line.
	Problem string
}
