package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #65 D6: the default interval is five minutes and the settings file changes it.
func TestHealthInterval(t *testing.T) {
	tests := []struct {
		name    string
		content *string
		want    time.Duration
		refused string
	}{
		{name: "no file", want: 5 * time.Minute},
		{name: "empty file", content: ptr(""), want: 5 * time.Minute},
		{name: "no interval", content: ptr("health: {}\n"), want: 5 * time.Minute},
		{name: "ten minutes", content: ptr("health:\n  interval: 10m\n"), want: 10 * time.Minute},
		{name: "lower bound", content: ptr("health:\n  interval: 1m\n"), want: time.Minute},
		{name: "upper bound", content: ptr("health:\n  interval: 24h\n"), want: 24 * time.Hour},
		{name: "too short", content: ptr("health:\n  interval: 59s\n"), refused: "outside 1m0s to 24h0m0s"},
		{name: "too long", content: ptr("health:\n  interval: 25h\n"), refused: "outside 1m0s to 24h0m0s"},
		{name: "negative", content: ptr("health:\n  interval: -5m\n"), refused: "outside"},
		{name: "not a duration", content: ptr("health:\n  interval: often\n"), refused: `"often" is not a duration`},
		{name: "unknown key", content: ptr("health:\n  intervall: 5m\n"), refused: "unknown key"},
		{name: "unknown section", content: ptr("healthcheck:\n  interval: 5m\n"), refused: "unknown key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != nil {
				if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte(*tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := healthInterval(dir)
			if tc.refused != "" {
				if err == nil || !strings.Contains(err.Error(), tc.refused) || !strings.Contains(err.Error(), "next: ") {
					t.Fatalf("healthInterval = %s, %v; want a refusal naming %q and the next step", got, err, tc.refused)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("healthInterval = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestHealthIntervalUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, settingsFile)
	if err := os.WriteFile(file, []byte("health:\n  interval: 5m\n"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := healthInterval(dir); err == nil || !strings.Contains(err.Error(), "cannot read the settings file") {
		t.Fatalf("healthInterval = %v, want a read refusal", err)
	}
}

func ptr(s string) *string { return &s }
