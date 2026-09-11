package service

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/core/fleet"
	"github.com/nesiler/ktags/internal/core/health"
	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/mask"
)

var fleetNow = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

// fixedClock stands still at fleetNow; waits use real time.
type fixedClock struct{}

func (fixedClock) Now() time.Time                         { return fleetNow }
func (fixedClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// engineSource is the health engine as a fleet source: its cached views, connection unknown.
type engineSource struct {
	engine   *health.Engine
	versions map[string]map[string]string
}

func (s engineSource) Measurements() []fleet.Measurement {
	var out []fleet.Measurement
	for _, v := range s.engine.Views() {
		out = append(out, fleet.Measurement{Health: v, Connection: fleet.ConnectionState{State: fleet.ConnUnknown}, Versions: s.versions[v.Customer]})
	}
	return out
}

type staticSource []fleet.Measurement

func (s staticSource) Measurements() []fleet.Measurement { return s }

func saveCustomer(t *testing.T, data, id string, env inventory.Environment) {
	t.Helper()
	dir := inventory.CustomerDir(data, id)
	if err := os.MkdirAll(filepath.Dir(inventory.RecordPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	record := inventory.Record{
		Schema:   inventory.SchemaVersion,
		Customer: inventory.Customer{ID: id, Name: strings.ToUpper(id), Environment: env},
		Cluster: inventory.Cluster{
			ID: id + "-main", Name: id + "-1", RancherURL: "https://rancher." + id + ".example.com",
			Nodes:  []inventory.Node{{ID: "srv-1", Name: id + "-srv-1", Role: inventory.RoleServer, Address: "203.0.113.11"}},
			Access: inventory.Access{Direct: &inventory.DirectAccess{User: "ops", Port: 22}},
		},
	}
	if err := inventory.Save(context.Background(), dir, record); err != nil {
		t.Fatal(err)
	}
}

func brokenCustomer(t *testing.T, data, id string) {
	t.Helper()
	path := inventory.RecordPath(inventory.CustomerDir(data, id))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ktags_schema: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// #16-K1, #16-K3: fleet.list joins the inventory with the engine's cache and the active runs,
// sorts by attention, and never measures: the check count stays where the one manual run left it.
func TestFleetListsCachedState(t *testing.T) {
	var calls atomic.Int64
	engine, err := health.New(health.Options{
		Clock:  fixedClock{},
		Redact: mask.Mask,
		Checks: []health.Check{{Name: "ssh.endpoint", Severity: health.SeverityCritical, Run: func(_ context.Context, customer string) error {
			calls.Add(1)
			if customer == "zed" {
				return errors.New("dial tcp 203.0.113.11:22: connection refused")
			}
			return nil
		}}},
		Customers: []string{"acme", "beta", "zed", "ghost"},
		Interval:  time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "acme", "zed", "ghost"); err != nil {
		t.Fatal(err)
	}
	source := engineSource{engine: engine, versions: map[string]map[string]string{"acme": {"rke2": "v1.30.4+rke2r1"}}}
	f := newFixture(t, setup{opts: func(o *Options) { o.Fleet = source; o.Clock = fixedClock{} }})
	saveCustomer(t, f.data, "acme", inventory.EnvProd)
	saveCustomer(t, f.data, "beta", inventory.EnvStaging)
	saveCustomer(t, f.data, "zed", inventory.EnvTest)
	brokenCustomer(t, f.data, "broken")
	run := f.start("fake long", Target{Kind: "customer", Customer: "beta"}, nil)
	run2 := f.start("fake long", Target{Kind: "customer", Customer: "beta"}, nil)
	defer close(f.long.gate)
	wantRuns := []ActiveRunInfo{{ID: run.ID, Action: "fake long"}, {ID: run2.ID, Action: "fake long"}}
	if run2.ID < run.ID {
		wantRuns[0], wantRuns[1] = wantRuns[1], wantRuns[0]
	}

	before := calls.Load()
	if before != 3 {
		t.Fatalf("the manual run made %d check calls, want 3", before)
	}
	var first FleetInfo
	for i := 0; i < 3; i++ {
		info, err := f.client.Fleet(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = info
		} else if !reflect.DeepEqual(info, first) {
			t.Fatalf("listing %d differs from the first:\n%+v\n%+v", i, info, first)
		}
	}
	if got := calls.Load(); got != before {
		t.Fatalf("listing the fleet ran %d checks; a listing must measure nothing", got-before)
	}

	if !first.Now.Equal(fleetNow) {
		t.Fatalf("now %s, want the service clock %s", first.Now, fleetNow)
	}
	var order, states []string
	for _, c := range first.Customers {
		order, states = append(order, c.ID), append(states, c.State)
	}
	if want := []string{"broken", "zed", "beta", "acme"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order %v (states %v), want %v; ghost is not in the inventory", order, states, want)
	}
	if want := []string{"invalid", "fail", "never", "ok"}; !reflect.DeepEqual(states, want) {
		t.Fatalf("states %v, want %v", states, want)
	}
	broken, zed, beta, acme := first.Customers[0], first.Customers[1], first.Customers[2], first.Customers[3]
	if broken.Problem == "" || broken.Health != "unknown" || broken.MeasuredAt != nil {
		t.Fatalf("broken %+v, want its problem and never measured", broken)
	}
	if len(zed.Checks) != 1 || zed.Checks[0].Status != "failed" || zed.Checks[0].Severity != "critical" ||
		!strings.Contains(zed.Checks[0].Detail, "connection refused") || !zed.Checks[0].MeasuredAt.Equal(fleetNow) {
		t.Fatalf("zed checks %+v, want the failed check with its evidence", zed.Checks)
	}
	if beta.MeasuredAt != nil || !beta.Stale || beta.Health != "unknown" || !reflect.DeepEqual(beta.ActiveRuns, wantRuns) {
		t.Fatalf("beta %+v, want never measured, stale, with its active run", beta)
	}
	if acme.MeasuredAt == nil || !acme.MeasuredAt.Equal(fleetNow) || acme.Stale || acme.Environment != "prod" || acme.Cluster != "acme-1" ||
		acme.IntervalSeconds != 3600 || acme.Trigger != "manual" || acme.Connection.State != "unknown" ||
		!reflect.DeepEqual(acme.Versions, map[string]string{"rke2": "v1.30.4+rke2r1"}) || len(acme.ActiveRuns) != 0 {
		t.Fatalf("acme %+v", acme)
	}
}

// Without a source nothing has been measured: every customer is never measured, never green, and
// the lists and maps are empty, not null.
func TestFleetWithoutSource(t *testing.T) {
	f := newFixture(t, setup{})
	info, err := f.client.Fleet(context.Background())
	if err != nil || info.Customers == nil || len(info.Customers) != 0 {
		t.Fatalf("empty inventory: %+v %v, want an empty list", info, err)
	}
	saveCustomer(t, f.data, "acme", inventory.EnvProd)
	info, err = f.client.Fleet(context.Background())
	if err != nil || len(info.Customers) != 1 {
		t.Fatalf("%+v %v", info, err)
	}
	c := info.Customers[0]
	if c.State != "never" || c.Health != "unknown" || !c.Stale || c.Connection.State != "unknown" || c.MeasuredAt != nil {
		t.Fatalf("acme %+v, want never measured", c)
	}
	if c.Checks == nil || c.ActiveRuns == nil || c.Versions == nil {
		t.Fatalf("acme %+v: checks, active runs and versions must be empty, not null", c)
	}
	if info.Now.IsZero() {
		t.Fatal("now is zero; the service clock must stamp the summary")
	}
}

// Free text from a source passes the service's redactor before it reaches a client, and an
// unset connection state is sent as unknown.
func TestFleetRedactsDetails(t *testing.T) {
	secret := "password: hunter2-example"
	source := staticSource{{
		Health: health.View{Customer: "acme", Health: health.HealthFail, MeasuredAt: fleetNow, Checks: []health.CheckResult{
			{Check: "ssh.auth", Severity: health.SeverityCritical, Status: health.StatusFailed, Detail: "server said " + secret, MeasuredAt: fleetNow, Duration: 1500 * time.Millisecond},
		}},
		Connection: fleet.ConnectionState{Detail: "remote said " + secret, MeasuredAt: fleetNow},
	}}
	f := newFixture(t, setup{opts: func(o *Options) { o.Fleet = source }})
	saveCustomer(t, f.data, "acme", inventory.EnvProd)
	info, err := f.client.Fleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "hunter2-example") {
		t.Fatalf("the secret reached the client: %s", raw)
	}
	c := info.Customers[0]
	if c.Connection.State != "unknown" || !strings.HasPrefix(c.Connection.Detail, "remote said") || c.Checks[0].DurationMS != 1500 {
		t.Fatalf("acme %+v", c)
	}
}

// An inventory that cannot be read fails the listing; it is never an empty fleet.
func TestFleetInventoryUnreadable(t *testing.T) {
	f := newFixture(t, setup{})
	saveCustomer(t, f.data, "acme", inventory.EnvProd)
	chmod(t, inventory.CustomersDir(f.data), 0o000)
	_, err := f.client.Fleet(context.Background())
	wantCode(t, err, CodeInternal)
}

// A service that cannot be reached is reported as such, not as a malformed reply.
func TestFleetServiceUnreachable(t *testing.T) {
	_, err := Client{Socket: filepath.Join(shortDir(t), "none.sock")}.Fleet(context.Background())
	wantCode(t, err, CodeUnavailable)
}

// A fleet.list reply without its payload is malformed, never an empty fleet.
func TestFleetReplyWithoutPayload(t *testing.T) {
	socket := filepath.Join(shortDir(t), "s.sock")
	l, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = bufio.NewReader(conn).ReadString('\n')
		_, _ = conn.Write([]byte(`{"protocol":1}` + "\n"))
	}()
	_, err = Client{Socket: socket}.Fleet(context.Background())
	wantCode(t, err, CodeMalformedReply)
}
