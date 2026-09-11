package fleet

import (
	"cmp"
	"slices"
	"time"

	"github.com/nesiler/ktags/internal/core/health"
)

// State is the attention state of one customer in the fleet summary.
type State string

// States, from the most to the least urgent. Only ok is green.
const (
	// StateInvalid: the customer's inventory record is refused, so nothing about it can be trusted.
	StateInvalid State = "invalid"
	// StateFail: the latest health result fails, fresh or stale.
	StateFail State = "fail"
	// StateUnreachable: the latest connection check could not reach the customer.
	StateUnreachable State = "unreachable"
	// StateStale: the latest health result is older than its interval allows.
	StateStale State = "stale"
	// StateNever: the customer has never been measured.
	StateNever State = "never"
	// StateWarn: the latest health result is fresh and warns.
	StateWarn State = "warn"
	// StateOK: the latest health result is fresh and passes.
	StateOK State = "ok"
)

// States lists every state in sort order.
var States = []State{StateInvalid, StateFail, StateUnreachable, StateStale, StateNever, StateWarn, StateOK}

// Connection is whether the customer could be reached when last checked.
type Connection string

// Connection states. Unknown means no connection result exists; it is never reachable.
const (
	ConnReachable   Connection = "reachable"
	ConnUnreachable Connection = "unreachable"
	ConnUnknown     Connection = "unknown"
)

// ConnectionState is the latest connection result of one customer.
type ConnectionState struct {
	State Connection
	// Detail is the measured fact of a failed connection; it has passed the redactor.
	Detail string
	// MeasuredAt is zero when the connection was never checked.
	MeasuredAt time.Time
}

// Measurement is everything cached about one customer. Health.Customer names the customer.
type Measurement struct {
	Health     health.View
	Connection ConnectionState
	// Versions maps a component, for example "rke2", to its measured version.
	Versions map[string]string
}

// Source hands out the cached measurements. It answers from memory: it never measures and never
// touches the network.
type Source interface {
	Measurements() []Measurement
}

// Unmeasured is the measurement of a customer the source knows nothing about: never measured,
// stale, connection unknown.
func Unmeasured(customer string) Measurement {
	return Measurement{
		Health:     health.View{Customer: customer, Health: health.HealthUnknown, Stale: true},
		Connection: ConnectionState{State: ConnUnknown},
	}
}

// Entry is one customer of the fleet summary.
type Entry struct {
	Customer string
	// Invalid is set when the customer's inventory record is refused.
	Invalid bool
	Measurement
}

// StateOf is the attention state of e. A failure outranks unreachability, which outranks stale
// and missing data; a health value this build does not know counts as a failure, never as ok.
func StateOf(e Entry) State {
	h := e.Health
	switch {
	case e.Invalid:
		return StateInvalid
	case h.Health == health.HealthFail:
		return StateFail
	case e.Connection.State == ConnUnreachable:
		return StateUnreachable
	case h.MeasuredAt.IsZero():
		return StateNever
	case h.Stale:
		return StateStale
	case h.Health == health.HealthWarn:
		return StateWarn
	case h.Health == health.HealthOK:
		return StateOK
	default:
		return StateFail
	}
}

// Runbook is the runbook the entry's row shows: that of the first failing critical check, else
// of the first check that is not ok. Empty when every check is ok or none names a runbook.
func Runbook(e Entry) string {
	first := ""
	for _, c := range e.Health.Checks {
		if c.Status == health.StatusOK || c.Runbook == "" {
			continue
		}
		if c.Severity == health.SeverityCritical && c.Status != health.StatusNotConfigured {
			return c.Runbook
		}
		if first == "" {
			first = c.Runbook
		}
	}
	return first
}

// Sort orders entries by state, most urgent first, then by customer. The order depends only on
// the entries, never on the order they came in.
func Sort(entries []Entry) {
	slices.SortStableFunc(entries, func(a, b Entry) int {
		return cmp.Or(
			cmp.Compare(slices.Index(States, StateOf(a)), slices.Index(States, StateOf(b))),
			cmp.Compare(a.Customer, b.Customer),
		)
	})
}
