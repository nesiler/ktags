package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/core/domain"
	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/mask"
	"github.com/nesiler/ktags/internal/run"
	"github.com/nesiler/ktags/internal/service"
)

// gateAction reports two events, waits for the gate, then reports a third and succeeds.
type gateAction struct{ gate chan struct{} }

func (gateAction) Descriptor() actions.Descriptor {
	return actions.Descriptor{
		ID: "fake gate", Title: "Fake gate", Help: "Reports two events, waits for the gate, reports one more.",
		Target: actions.TargetCustomer, Effect: actions.EffectMutating,
	}
}

func (gateAction) Check(context.Context, actions.Request) error { return nil }

func (a gateAction) Run(ctx context.Context, _ actions.Request, p actions.Progress) (actions.Result, error) {
	p.Report(actions.Event{Step: "start", Message: "event 1"})
	p.Report(actions.Event{Step: "start", Message: "event 2"})
	select {
	case <-a.gate:
	case <-ctx.Done():
		return actions.Result{}, ctx.Err()
	}
	p.Report(actions.Event{Step: "after", Message: "event 3"})
	return actions.Result{Status: actions.StatusSucceeded, Summary: "gate passed"}, nil
}

// startService runs a real ktags service on a private socket with one staging customer.
func startService(t *testing.T, action actions.Action) service.Client {
	t.Helper()
	base, err := os.MkdirTemp("", "kt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	dirs := map[string]string{}
	for _, name := range []string{"rt", "st", "da"} {
		dirs[name] = filepath.Join(base, name)
		if err := os.Mkdir(dirs[name], 0o700); err != nil {
			t.Fatal(err)
		}
	}
	store, err := run.NewStore(dirs["st"], run.Options{Redact: mask.Mask})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := actions.NewRegistry(action)
	if err != nil {
		t.Fatal(err)
	}
	dir := inventory.CustomerDir(dirs["da"], "acme")
	if err := os.MkdirAll(filepath.Dir(inventory.RecordPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	record := inventory.Record{
		Schema:   inventory.SchemaVersion,
		Customer: inventory.Customer{ID: "acme", Name: "ACME", Environment: inventory.Environment("staging")},
		Cluster: inventory.Cluster{
			ID: "acme-main", Name: "acme-1", RancherURL: "https://rancher.acme.example.com",
			Nodes:  []inventory.Node{{ID: "srv-1", Name: "acme-srv-1", Role: inventory.RoleServer, Address: "203.0.113.11"}},
			Access: inventory.Access{Direct: &inventory.DirectAccess{User: "ops", Port: 22}},
		},
	}
	if err := inventory.Save(context.Background(), dir, record); err != nil {
		t.Fatal(err)
	}
	srv, err := service.Listen(context.Background(), dirs["rt"], service.Options{
		Registry: registry, Store: store, DataRoot: dirs["da"],
		Build: domain.Build{Name: "ktags", Version: "test"}, Redact: mask.Mask,
	})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve() }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-served; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return service.Client{Socket: service.SocketPath(dirs["rt"])}
}

// #17-K2 against a real service: start a fake action from the palette, leave the run view and
// quit while it runs; the run finishes in the service; a second TUI shows the same run with
// its stored result and every event.
func TestRunSurvivesQuitAndReopen(t *testing.T) {
	gate := gateAction{gate: make(chan struct{})}
	client := startService(t, gate)
	wallClock := func(o *Options) { o.Now = time.Now }

	first := connected(t, client, wallClock)
	first.key(":")
	first.typeText("fake gate")
	first.key("enter")
	if first.m.overlay != overlayDialog || first.m.dialog.kind != dialogConfirm {
		t.Fatalf("a mutating action on staging opened overlay %d dialog %+v, want the [y/N] confirmation", first.m.overlay, first.m.dialog)
	}
	first.key("y")
	first.until("the first two events", func(m Model) bool { return m.run != nil && len(m.run.lines) == 2 })
	id := first.m.run.id
	first.key("esc")
	if first.m.screen != screenFleet || !strings.Contains(first.m.status, "continues in the ktags service") {
		t.Fatalf("after Esc: screen %d, status %q; want the fleet and a detach line", first.m.screen, first.m.status)
	}
	first.key("q")
	first.until("quit", func(Model) bool { return first.quit })
	if want := "1 run continue in the ktags service; reopen ktags or run: ktags run list"; first.m.ExitLine() != want {
		t.Fatalf("exit line %q, want %q", first.m.ExitLine(), want)
	}

	close(gate.gate)
	final, err := client.Events(context.Background(), id, 0, true, func(service.Event) error { return nil })
	if err != nil || final.Status != "succeeded" {
		t.Fatalf("run after the TUI quit: %+v, %v; want succeeded in the service", final, err)
	}

	second := connected(t, client, wallClock)
	second.key("tab", "right", "right")
	runs := second.m.customerRuns("acme")
	if len(runs) != 1 || runs[0].ID != id || runs[0].Status != "succeeded" {
		t.Fatalf("Runs tab of the reopened TUI %+v, want run %s succeeded", runs, id)
	}
	second.key("enter")
	// The run list already carries the result; the replay has ended once the stream is closed.
	second.until("the replayed run", func(m Model) bool { return m.run != nil && m.run.stop == nil && m.run.info.Result != nil })
	r := second.m.run
	if r.id != id || r.info.Result.Summary != "gate passed" || len(r.lines) != 3 || r.lines[0].ID != 1 || r.lines[2].ID != 3 {
		t.Fatalf("reopened run %s result %+v lines %+v, want run %s, gate passed, events 1..3", r.id, r.info.Result, r.lines, id)
	}
	if frame := second.frame(); !strings.Contains(frame, "result    ✓ succeeded  gate passed") || !strings.Contains(frame, "event 3") {
		t.Fatalf("run view does not show the stored result:\n%s", frame)
	}
}
