package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/core/fleet"
	"github.com/nesiler/ktags/internal/core/health"
	"github.com/nesiler/ktags/internal/inventory"
)

// #16 follow-up: the composed source answers from the engine's cache; listing never runs a check.
func TestHealthSourceMeasuresNothing(t *testing.T) {
	var calls atomic.Int32
	engine, err := health.New(health.Options{
		Clock:     newJumpClock(),
		Redact:    func(s string) string { return s },
		Checks:    []health.Check{{Name: "count", Severity: health.SeverityCritical, Run: func(context.Context, string) error { calls.Add(1); return nil }}},
		Customers: []string{"acme", "zeta"},
		Interval:  time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "acme"); err != nil {
		t.Fatal(err)
	}
	source := healthSource{views: engine.Views}
	var got []fleet.Measurement
	for range 3 {
		got = source.Measurements()
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("%d check calls after one run and three listings, want 1", n)
	}
	if len(got) != 2 || got[0].Health.Customer != "acme" || got[0].Health.Health != health.HealthOK || got[1].Health.Health != health.HealthUnknown {
		t.Fatalf("measurements %+v, want acme ok and zeta unknown", got)
	}
	for _, m := range got {
		if m.Connection.State != fleet.ConnUnknown {
			t.Fatalf("connection %+v, want unknown: no connection result is composed", m.Connection)
		}
	}
}

func TestValidCustomers(t *testing.T) {
	data := t.TempDir()
	if ids, err := validCustomers(context.Background(), data); err != nil || ids != nil {
		t.Fatalf("no customers directory: %v, %v; want nothing and no error", ids, err)
	}
	saveCustomer(t, data, "acme")
	saveCustomer(t, data, "beta")
	writeInvalid(t, data, "bad")
	if err := os.WriteFile(filepath.Join(inventory.CustomersDir(data), "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ids, err := validCustomers(context.Background(), data)
	if err != nil || !slices.Equal(ids, []string{"acme", "beta"}) {
		t.Fatalf("customers %v, %v; want acme and beta without the refused record and the file", ids, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := validCustomers(ctx, data); err == nil {
		t.Fatal("a cancelled scan returned a fleet")
	}
}

// A customers directory that cannot be read refuses the service start: a partial fleet would
// look complete.
func TestServiceRunRefusesAnUnreadableInventory(t *testing.T) {
	h := newHarness(t)
	h.deps.health = &healthConfig{checks: []health.Check{okHealthCheck()}, interval: time.Hour, clock: newJumpClock()}
	dir := inventory.CustomersDir(filepath.Join(h.home, "data"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	type result struct {
		code   int
		stderr string
	}
	ran := make(chan result, 1)
	go func() {
		code, _, stderr := h.ktags("service", "run")
		ran <- result{code, stderr}
	}()
	var got result
	select {
	case got = <-ran:
	case <-time.After(10 * time.Second):
		// The service started instead of refusing; stop it so the test ends red, not hung.
		_, _ = h.client.Stop(context.Background(), true)
		<-ran
		t.Fatal("the service started with an unreadable inventory")
	}
	if got.code != 1 {
		t.Fatalf("service run exited %d, want 1\n%s", got.code, got.stderr)
	}
	contains(t, got.stderr, "cannot list the customers directory "+dir+" for the health schedule")
	if _, err := os.Stat(h.socket); err == nil {
		t.Fatal("the service listened with an unreadable inventory")
	}
}

// Without an injected clock the engine runs on the system clock; health.New refuses none.
func TestNewHealthEngineDefaultsToTheSystemClock(t *testing.T) {
	engine, err := newHealthEngine(context.Background(), &healthConfig{checks: []health.Check{okHealthCheck()}, interval: time.Hour}, t.TempDir())
	if err != nil || engine == nil {
		t.Fatalf("engine %v, %v; want one on the system clock", engine, err)
	}
}

func okHealthCheck() health.Check {
	return health.Check{Name: "ok", Severity: health.SeverityCritical, Run: func(context.Context, string) error { return nil }}
}

func writeInvalid(t *testing.T, data, id string) {
	t.Helper()
	path := inventory.RecordPath(inventory.CustomerDir(data, id))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("not: [valid\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}
