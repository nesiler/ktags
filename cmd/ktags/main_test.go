package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nesiler/ktags/internal/app"
)

func TestVersionDefault(t *testing.T) {
	if version == "" {
		t.Fatal("version must have a default so `ktags version` never prints an empty string")
	}
}

func TestVersionCommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "subcommand", args: []string{"version"}},
		{name: "flag", args: []string{"--version"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if got := app.Run(version, tt.args, &stdout, &stderr); got != 0 {
				t.Fatalf("exit code = %d, want 0", got)
			}
			if got, want := stdout.String(), "ktags dev\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if got := stderr.String(); got != "" {
				t.Fatalf("stderr = %q, want empty", got)
			}
		})
	}
}

// An unknown command is a usage error: exit 1 (docs/guides/development.md §3) with the usage.
func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := app.Run(version, []string{"unknown"}, &stdout, &stderr); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	for _, want := range []string{"ktags: unknown command \"unknown\"\n", "usage: ktags <command>"} {
		if got := stderr.String(); !strings.Contains(got, want) {
			t.Fatalf("stderr = %q, want it to contain %q", got, want)
		}
	}
}
