package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nesiler/ktags/internal/service"
)

// #13-K1 through the real service process and socket: discovery, a detached start, a watch
// that an interrupt detaches while the run continues, cancel and the stored final result.
func TestCLIAgainstService(t *testing.T) {
	h := newHarness(t)
	h.startService()

	stdout, _ := h.want(0, "action", "list", "--json")
	contains(t, stdout, `"schema": "ktags.cli/v1"`, `"kind": "action.list"`, `"id": "test block"`)

	stdout, _ = h.want(0, "action", "run", "test", "block", "--detach", "--json")
	var started struct {
		Data struct {
			Run service.RunInfo `json:"run"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &started); err != nil || started.Data.Run.ID == "" {
		t.Fatalf("run.started document %q: %v", stdout, err)
	}
	id := started.Data.Run.ID

	// The interrupt fires once the run has reported its first event.
	h.deps.interrupt = func(ctx context.Context) (context.Context, context.CancelFunc) {
		ictx, cancel := context.WithCancel(ctx)
		go func() {
			for ictx.Err() == nil {
				if st, err := h.client.Status(ictx, id); err == nil && st.LastEventID >= 1 {
					cancel()
					return
				}
				<-time.After(10 * time.Millisecond)
			}
		}()
		return ictx, cancel
	}
	_, stderr := h.want(0, "run", "watch", id)
	contains(t, stderr, "detached from run "+id, "next: ktags run watch "+id+" --after")
	h.deps.interrupt = nil
	stdout, _ = h.want(0, "run", "status", id)
	contains(t, stdout, "run "+id+" running", "next: ktags run watch "+id)

	stdout, _ = h.want(0, "run", "cancel", id, "--yes")
	contains(t, stdout, "cancel requested for run "+id, "run "+id+" cancelled: cancelled by the operator")

	stdout, _ = h.want(3, "run", "watch", id, "--json")
	contains(t, stdout, `"kind": "run.result"`, `"status": "cancelled"`, `"message": "waiting"`)

	stdout, _ = h.want(0, "customer", "list", "--json")
	contains(t, stdout, `"customers": []`)

	h.want(0, "service", "stop")
	if err := h.launcher.exited(t); err != nil {
		t.Fatalf("service exit: %v", err)
	}
}
