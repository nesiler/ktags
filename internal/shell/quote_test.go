package shell

import (
	"os/exec"
	"testing"
)

// A quoted word reaches the shell unchanged, whatever it holds.
func TestQuoteRoundTrip(t *testing.T) {
	for _, s := range []string{"", "plain", "/k/with space", "$HOME", "`id`", "it's", "a'b'c", `back\slash`, "new\nline", "*?[x]"} {
		out, err := exec.Command("/bin/sh", "-c", "printf %s "+Quote(s)).Output()
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if string(out) != s {
			t.Errorf("Quote(%q) = %s reached the shell as %q", s, Quote(s), out)
		}
	}
}
