package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/nesiler/ktags/internal/core/fleet"
	"github.com/nesiler/ktags/internal/core/health"
	"github.com/nesiler/ktags/internal/core/schedule"
	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/mask"
)

// healthConfig composes the health engine into the service. The service measures only when one
// is given: without checks there is nothing to measure, and every customer stays never measured.
type healthConfig struct {
	checks   []health.Check
	interval time.Duration
	// clock drives the engine, its schedule and the service's fleet stamp; nil is the system clock.
	clock schedule.Clock
	// connection is the customers' measured connection state; nil leaves it unknown.
	connection func(customer string) fleet.ConnectionState
}

// newHealthEngine builds the engine over the customers whose inventory record loads. A refused
// record is not measured; the fleet summary shows it as invalid.
func newHealthEngine(ctx context.Context, cfg *healthConfig, dataRoot string) (*health.Engine, error) {
	customers, err := validCustomers(ctx, dataRoot)
	if err != nil {
		return nil, err
	}
	clock := cfg.clock
	if clock == nil {
		clock = schedule.System()
	}
	return health.New(health.Options{
		Clock:     clock,
		Redact:    mask.Mask,
		Checks:    cfg.checks,
		Customers: customers,
		Interval:  cfg.interval,
	})
}

// validCustomers lists the IDs of the customers whose record loads, by directory name (the
// inventory binds a record to it). A stray file fails to load and is skipped like a refused
// record. An unreadable customers directory refuses the start: a partial fleet would look
// complete.
func validCustomers(ctx context.Context, dataRoot string) ([]string, error) {
	root := inventory.CustomersDir(dataRoot)
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list the customers directory %s for the health schedule: %w", root, err)
	}
	var ids []string
	for _, e := range entries {
		if _, err := inventory.Load(ctx, inventory.CustomerDir(dataRoot, e.Name())); err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		ids = append(ids, e.Name())
	}
	return ids, nil
}

// healthSource hands the engine's cache and the probe's connection state to the fleet
// summary. It only reads memory.
//
// Without a connection source the connection is unknown. A fresh ok health result next to an
// unknown connection stays ok: the checks that produced it reached the customer. Only a
// measured unreachable connection demotes a customer (fleet.StateOf).
type healthSource struct {
	views      func() []health.View
	connection func(customer string) fleet.ConnectionState
}

func (s healthSource) Measurements() []fleet.Measurement {
	views := s.views()
	out := make([]fleet.Measurement, 0, len(views))
	for _, v := range views {
		conn := fleet.ConnectionState{State: fleet.ConnUnknown}
		if s.connection != nil {
			conn = s.connection(v.Customer)
		}
		out = append(out, fleet.Measurement{Health: v, Connection: conn})
	}
	return out
}
