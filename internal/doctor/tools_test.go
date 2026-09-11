package doctor

import (
	"errors"
	"strings"
	"testing"
)

func TestSSH(t *testing.T) {
	env := testEnv(t)
	r := only(t, runCheck(t, "tools.ssh", env))
	want(t, r, StatusOK, "")
	if r.Evidence != "ssh is /usr/bin/ssh" {
		t.Fatalf("evidence %q", r.Evidence)
	}

	for goos, fix := range map[string]string{
		"darwin": `export PATH="/usr/bin:$PATH"`,
		"linux":  "sudo apt-get install -y openssh-client",
	} {
		env.GOOS = goos
		env.LookPath = func(file string) (string, error) {
			if file != "ssh" {
				t.Fatalf("looked for %q", file)
			}
			return "", errors.New("not found")
		}
		r := only(t, runCheck(t, "tools.ssh", env))
		want(t, r, StatusFail, fix)
		if r.Evidence != "ssh is not on PATH" {
			t.Fatalf("evidence %q", r.Evidence)
		}
	}
}

func TestPlatform(t *testing.T) {
	env := testEnv(t)
	r := only(t, runCheck(t, "platform", env))
	want(t, r, StatusOK, "")
	if !strings.HasPrefix(r.Evidence, "darwin/arm64: ") || !strings.Contains(r.Evidence, "launchd") {
		t.Fatalf("evidence %q", r.Evidence)
	}
	env.GOOS, env.GOARCH, env.Foreground = "linux", "amd64", true
	r = only(t, runCheck(t, "platform", env))
	want(t, r, StatusOK, "")
	if !strings.HasPrefix(r.Evidence, "linux/amd64: ") || !strings.Contains(r.Evidence, "ktags service run") {
		t.Fatalf("evidence %q", r.Evidence)
	}
}
