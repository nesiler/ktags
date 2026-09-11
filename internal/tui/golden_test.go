package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/nesiler/ktags/internal/service"
)

func stripped(s string) string {
	return ansi.Strip(s)
}

// requireGolden runs m in a Bubble Tea program at w×h with colours off, quits, and compares
// the final frame with testdata/<test>.golden (docs/guides/ui.md §13). Update with -update
// and review the diff.
func requireGolden(t *testing.T, m Model, w, h int) {
	t.Helper()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(w, h), teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.Ascii)))
	if err := tm.Quit(); err != nil {
		t.Fatal(err)
	}
	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(15*time.Second)).(Model)
	if !ok {
		t.Fatal("the final model is not a tui.Model")
	}
	golden.RequireEqual(t, stripped(final.View().Content))
}

func unavailableErr() error {
	return &service.Error{Code: service.CodeUnavailable, Message: "cannot reach the ktags service at /tmp/kt/runtime/service.sock", Hint: "start the ktags service, then retry"}
}

// #17-K1: every screen and connection state at 100×30: empty, the 15-customer mixed fleet,
// connecting, unavailable, mismatch, reconnecting, an active and a finished run, and the
// overlays of the pipeline.
func TestGolden(t *testing.T) {
	menuAction := func(d *driver, customer, action string) {
		d.selectCustomer(customer)
		d.key("enter")
		for i, a := range d.m.menu.items {
			if a.ID == action {
				for range i {
					d.key("down")
				}
			}
		}
		d.key("enter")
	}
	cases := []struct {
		name  string
		w, h  int
		setup func(t *testing.T) Model
	}{
		{"empty", 100, 30, func(t *testing.T) Model { return connected(t, emptyFake()).m }},
		{"mixed", 100, 30, func(t *testing.T) Model { return connected(t, newFake()).m }},
		{"health_fail", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.selectCustomer("mike")
			d.key("tab", "right")
			return d.m
		}},
		{"health_stale_missed", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.selectCustomer("lima")
			d.key("tab", "right")
			return d.m
		}},
		{"runs_tab", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.selectCustomer("mike")
			d.key("tab", "right", "right")
			return d.m
		}},
		{"connecting", 100, 30, func(t *testing.T) Model {
			f := newFake()
			f.block = make(chan struct{})
			t.Cleanup(func() { close(f.block) })
			return newDriver(t, f).m
		}},
		{"unavailable", 100, 30, func(t *testing.T) Model {
			f := newFake()
			f.err = unavailableErr()
			d := newDriver(t, f)
			d.until("unavailable", func(m Model) bool { return m.conn == connUnavailable })
			return d.m
		}},
		{"protocol_mismatch", 100, 30, func(t *testing.T) Model {
			f := newFake()
			f.err = &service.Error{Code: service.CodeProtocolMismatch, Message: "the service speaks ktags protocol 2, this side speaks 1", Hint: "this ktags is older than the running service: upgrade ktags to the build that started the service"}
			d := newDriver(t, f)
			d.until("mismatch", func(m Model) bool { return m.conn == connMismatch })
			return d.m
		}},
		{"reconnecting", 100, 30, func(t *testing.T) Model {
			f := newFake()
			clock := testNow
			d := connected(t, f, func(o *Options) { o.Now = func() time.Time { return clock } })
			d.selectCustomer("mike")
			clock = testNow.Add(2 * time.Minute)
			f.set(func(f *fakeClient) { f.err = unavailableErr() })
			d.apply(tickMsg{})
			d.until("reconnecting", func(m Model) bool { return m.conn == connReconnecting })
			return d.m
		}},
		{"run_active", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.selectCustomer("bravo")
			d.key("tab", "right", "right", "enter")
			d.until("three events", func(m Model) bool { return m.run != nil && len(m.run.lines) == 3 })
			return d.m
		}},
		{"run_finished", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.selectCustomer("mike")
			d.key("tab", "right", "right", "enter")
			d.until("the result", func(m Model) bool { return m.run != nil && m.run.info.Result != nil && len(m.run.lines) == 2 })
			return d.m
		}},
		{"palette", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.key(":")
			return d.m
		}},
		{"palette_target", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.selectCustomer("delta")
			d.key(":")
			d.typeText("fake touch d")
			return d.m
		}},
		{"palette_unknown", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.key(":")
			d.typeText("instal")
			return d.m
		}},
		{"menu", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.selectCustomer("delta")
			d.key("enter")
			return d.m
		}},
		{"confirm", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			menuAction(d, "delta", "fake touch")
			return d.m
		}},
		{"type_name", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			menuAction(d, "mike", "fake touch")
			d.typeText("mi")
			return d.m
		}},
		{"refused_record", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			menuAction(d, "zulu", "fake touch")
			return d.m
		}},
		{"help", 100, 30, func(t *testing.T) Model {
			d := connected(t, newFake())
			d.key("?")
			return d.m
		}},
		{"too_small", 79, 23, func(t *testing.T) Model { return connected(t, newFake()).m }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			requireGolden(t, c.setup(t), c.w, c.h)
		})
	}
}
