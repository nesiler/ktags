package service

import (
	"context"
	"sort"
	"time"

	"github.com/nesiler/ktags/internal/core/fleet"
)

// fleet joins the inventory with the cached measurements and the active runs. It reads the
// inventory from disk and everything else from memory; it measures nothing. The inventory is
// the fleet: a measurement of a customer outside it is left out, and a customer without one is
// listed as never measured.
func (m *manager) fleet(ctx context.Context, source fleet.Source, now time.Time) (FleetInfo, error) {
	customers, err := m.customers(ctx)
	if err != nil {
		return FleetInfo{}, err
	}
	measured := map[string]fleet.Measurement{}
	if source != nil {
		for _, x := range source.Measurements() {
			measured[x.Health.Customer] = x
		}
	}
	byID := make(map[string]CustomerInfo, len(customers))
	entries := make([]fleet.Entry, 0, len(customers))
	for _, c := range customers {
		byID[c.ID] = c
		x, ok := measured[c.ID]
		if !ok {
			x = fleet.Unmeasured(c.ID)
		}
		entries = append(entries, fleet.Entry{Customer: c.ID, Invalid: c.Problem != "", Measurement: x})
	}
	fleet.Sort(entries)

	active := m.activeByCustomer()
	info := FleetInfo{Now: now, Customers: make([]FleetEntry, 0, len(entries))}
	for _, e := range entries {
		info.Customers = append(info.Customers, m.fleetEntry(byID[e.Customer], e, active[e.Customer]))
	}
	return info, nil
}

// fleetEntry is the wire form of one entry. Every free text passes the redactor: a source's
// detail may quote what a check saw.
func (m *manager) fleetEntry(c CustomerInfo, e fleet.Entry, runs []ActiveRunInfo) FleetEntry {
	h := e.Health
	out := FleetEntry{
		ID:              c.ID,
		Name:            c.Name,
		Environment:     c.Environment,
		Cluster:         c.Cluster,
		Problem:         c.Problem,
		State:           string(fleet.StateOf(e)),
		Connection:      ConnectionInfo{State: string(e.Connection.State), Detail: m.redact(e.Connection.Detail), MeasuredAt: optionalTime(e.Connection.MeasuredAt)},
		Health:          string(h.Health),
		MeasuredAt:      optionalTime(h.MeasuredAt),
		Trigger:         string(h.Trigger),
		Stale:           h.Stale,
		IntervalSeconds: int64(h.Interval / time.Second),
		Missed:          h.Missed,
		Checking:        h.Running,
		Versions:        map[string]string{},
		ActiveRuns:      runs,
		Checks:          make([]CheckInfo, 0, len(h.Checks)),
	}
	if out.Connection.State == "" {
		out.Connection.State = string(fleet.ConnUnknown)
	}
	if out.ActiveRuns == nil {
		out.ActiveRuns = []ActiveRunInfo{}
	}
	for component, version := range e.Versions {
		out.Versions[component] = version
	}
	for _, r := range h.Checks {
		out.Checks = append(out.Checks, CheckInfo{
			Check:      r.Check,
			Severity:   string(r.Severity),
			Status:     string(r.Status),
			Detail:     m.redact(r.Detail),
			MeasuredAt: r.MeasuredAt,
			DurationMS: r.Duration.Milliseconds(),
		})
	}
	return out
}

// activeByCustomer lists the active runs of each customer, sorted by run ID. Global runs land
// under "", which no customer has.
func (m *manager) activeByCustomer() map[string][]ActiveRunInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]ActiveRunInfo{}
	for id, a := range m.active {
		meta := a.rec.Meta()
		out[meta.Target.Customer] = append(out[meta.Target.Customer], ActiveRunInfo{ID: id, Action: meta.Action})
	}
	for _, runs := range out {
		sort.Slice(runs, func(i, j int) bool { return runs[i].ID < runs[j].ID })
	}
	return out
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
