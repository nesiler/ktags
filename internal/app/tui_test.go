package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/nesiler/ktags/internal/actions"
	"github.com/nesiler/ktags/internal/inventory"
	"github.com/nesiler/ktags/internal/paths"
	"github.com/nesiler/ktags/internal/service"
	"github.com/nesiler/ktags/internal/tui"
)

// touchAction is a customer action, so the TUI's context menu and hint bar have something to
// offer.
type touchAction struct{}

func (touchAction) Descriptor() actions.Descriptor {
	return actions.Descriptor{
		ID: "test touch", Title: "Test touch", Help: "Reports one event and succeeds.",
		Target: actions.TargetCustomer, Effect: actions.EffectMutating,
	}
}

func (touchAction) Check(context.Context, actions.Request) error { return nil }

func (touchAction) Run(_ context.Context, _ actions.Request, p actions.Progress) (actions.Result, error) {
	p.Report(actions.Event{Step: "touch", Message: "touched"})
	return actions.Result{Status: actions.StatusSucceeded, Summary: "touched"}, nil
}

func saveCustomer(t *testing.T, data, id string) {
	t.Helper()
	dir := inventory.CustomerDir(data, id)
	if err := os.MkdirAll(filepath.Dir(inventory.RecordPath(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	record := inventory.Record{
		Schema:   inventory.SchemaVersion,
		Customer: inventory.Customer{ID: id, Name: strings.ToUpper(id), Environment: inventory.Environment("staging")},
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

// settle runs the model's commands to completion. It is enough for a model without polling
// and without an open run.
func settle(m tui.Model, first tea.Cmd) tui.Model {
	queue := []tea.Cmd{first}
	for len(queue) > 0 {
		cmd := queue[0]
		queue = queue[1:]
		if cmd == nil {
			continue
		}
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		next, more := m.Update(msg)
		m = next.(tui.Model)
		queue = append(queue, more)
	}
	return m
}

// #17-K3: every action the TUI shows, in the palette, the context menu, the hint bar and the
// help, is in the service's registry and discoverable through the CLI; the run operations
// map to CLI commands that exist.
func TestTUISurfacesAreRegisteredAndInTheCLI(t *testing.T) {
	h := newHarness(t)
	h.startService()
	saveCustomer(t, filepath.Join(h.home, "data"), "acme")

	zones := zone.New()
	t.Cleanup(zones.Close)
	m := tui.New(tui.Options{Client: h.client, Version: "test", Zones: zones})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = settle(next.(tui.Model), m.Init())

	registered, err := h.client.Actions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, a := range registered {
		known[a.ID] = true
	}
	seen := map[string]bool{}
	for _, s := range m.Surfaces() {
		seen[s.Where] = true
		if s.Action == "" {
			words := strings.Fields(strings.TrimPrefix(s.CLI, "ktags "))
			var cmd []string
			for _, w := range words {
				if !strings.HasPrefix(w, "<") {
					cmd = append(cmd, w)
				}
			}
			h.want(0, append(cmd, "--help")...)
			continue
		}
		if !known[s.Action] {
			t.Fatalf("the %s shows %q, which the registry does not hold", s.Where, s.Action)
		}
		if !strings.HasPrefix(s.CLI, "ktags action run "+s.Action) {
			t.Fatalf("the %s gives %q as the CLI of %q", s.Where, s.CLI, s.Action)
		}
		stdout, _ := h.want(0, append([]string{"action", "show"}, strings.Fields(s.Action)...)...)
		contains(t, stdout, s.Action+": ", "usage   ktags action run "+s.Action)
	}
	for _, where := range []string{"palette", "menu", "hint bar", "help", "Runs tab", "run view"} {
		if !seen[where] {
			t.Fatalf("no surface in the %s; the fixture must exercise every place: %v", where, seen)
		}
	}
	h.want(0, "service", "stop")
}

// `ktags` alone on a terminal starts the service and opens the TUI on its socket; the exit
// line goes to stdout.
func TestTUIOpensOnATerminal(t *testing.T) {
	h := newHarness(t)
	var got tui.Options
	calls := 0
	h.deps.screen = true
	h.deps.tui = func(_ context.Context, opts tui.Options, out io.Writer) error {
		calls++
		got = opts
		_, _ = fmt.Fprintln(out, "exit line")
		return nil
	}
	stdout, _ := h.want(0)
	if calls != 1 || got.StartErr != nil || got.Version != "test" || h.launcher.launches != 1 {
		t.Fatalf("tui calls %d, options %+v, launches %d; want one TUI on a started service", calls, got, h.launcher.launches)
	}
	if c, ok := got.Client.(service.Client); !ok || c.Socket != h.socket {
		t.Fatalf("TUI client %#v, want the service socket %s", got.Client, h.socket)
	}
	contains(t, stdout, "exit line")
	h.want(0, "service", "status")
	h.want(0, "service", "stop")
}

type failLauncher struct{}

func (failLauncher) Launch(context.Context) error {
	return errors.New("launchctl bootstrap failed: exit status 5")
}

func (failLauncher) Unload(context.Context) error { return nil }

// A service that cannot start still opens the TUI, in the unavailable state with the cause.
func TestTUIOpensWithTheStartFailure(t *testing.T) {
	h := newHarness(t)
	h.deps.launcher = func(paths.Roots) service.Launcher { return failLauncher{} }
	h.deps.screen = true
	var got tui.Options
	h.deps.tui = func(_ context.Context, opts tui.Options, _ io.Writer) error {
		got = opts
		return nil
	}
	h.want(0)
	if got.StartErr == nil || !strings.Contains(got.StartErr.Error(), "launchctl bootstrap failed") {
		t.Fatalf("start error %v, want the launcher failure handed to the TUI", got.StartErr)
	}
}

// A failing TUI is exit 1 with the reason.
func TestTUIFailureExitsOne(t *testing.T) {
	h := newHarness(t)
	h.deps.launcher = func(paths.Roots) service.Launcher { return failLauncher{} }
	h.deps.screen = true
	h.deps.tui = func(context.Context, tui.Options, io.Writer) error {
		return errors.New("open /dev/tty: device not configured")
	}
	_, stderr := h.want(1)
	contains(t, stderr, "ktags: the TUI failed: open /dev/tty")
}

// Without a terminal on stdin and stdout `ktags` alone is a usage error, never a TUI.
func TestNoTUIWithoutATerminal(t *testing.T) {
	h := newHarness(t)
	h.deps.tui = func(context.Context, tui.Options, io.Writer) error {
		t.Fatal("the TUI opened without a terminal")
		return nil
	}
	_, stderr := h.want(1)
	contains(t, stderr, "the ktags TUI needs a terminal on stdin and stdout")
}

// An unresolvable root is reported before anything starts.
func TestTUIRefusesBadRoots(t *testing.T) {
	h := newHarness(t)
	h.deps.env = paths.Env{Getenv: func(key string) string {
		if key == "KTAGS_HOME" {
			return "relative/home"
		}
		return ""
	}}
	h.deps.screen = true
	h.deps.tui = func(context.Context, tui.Options, io.Writer) error {
		t.Fatal("the TUI opened with unresolvable roots")
		return nil
	}
	_, stderr := h.want(1)
	contains(t, stderr, "ktags: KTAGS_HOME")
}
