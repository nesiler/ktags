package tui

import (
	"testing"
	"time"
)

// docs/guides/ui.md §12: a frame renders in under 16 ms on the 15-customer fixture. The
// average over many frames keeps one slow scheduling slice from deciding the result.
func TestFrameBudget(t *testing.T) {
	d := connected(t, newFake())
	d.selectCustomer("mike")
	d.key("tab", "right")
	const frames = 100
	start := time.Now()
	for range frames {
		_ = d.m.View()
	}
	if per := time.Since(start) / frames; per > 16*time.Millisecond {
		t.Fatalf("a frame of the 15-customer fleet takes %s, over the 16ms budget", per)
	} else {
		t.Logf("a frame of the 15-customer fleet takes %s", per)
	}
}
