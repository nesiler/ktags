package main

import (
	"bytes"
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

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := app.Run(version, []string{"unknown"}, &stdout, &stderr); got != 2 {
		t.Fatalf("exit code = %d, want 2", got)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want empty", got)
	}
	want := "ktags: nothing to run yet; see README.md and the open GitHub issues\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
