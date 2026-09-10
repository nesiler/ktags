package mask

import (
	"regexp"
	"testing"
	"time"
)

// A value group at the very start of a match must not stall the search: appendGroupSpans has to
// advance by at least one byte. No production pattern has such a group today; this guards the loop.
func TestAppendGroupSpansAdvancesOnGroupAtMatchStart(t *testing.T) {
	done := make(chan []span, 1)
	go func() {
		done <- appendGroupSpans(nil, pattern{re: regexp.MustCompile(`(a)`), valueOnly: true}, "aaa")
	}()
	select {
	case got := <-done:
		if len(got) != 3 {
			t.Fatalf("spans = %v, want one per byte", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("appendGroupSpans did not advance past a group at the match start")
	}
}
