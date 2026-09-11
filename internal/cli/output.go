package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/nesiler/ktags/internal/service"
)

// Exit codes (docs/guides/development.md §3). Scripts rely on them.
const (
	exitOK      = 0
	exitUsage   = 1
	exitRefused = 2
	exitFailed  = 3
)

// schema names the version of every --json document. A change that breaks a reader of a
// document gets a new schema version.
const schema = "ktags.cli/v1"

// document is the one JSON value a --json command prints on stdout.
type document struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	Data   any    `json:"data"`
}

type errorDoc struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
	Exit    int    `json:"exit"`
}

type helpDoc struct {
	Usage string `json:"usage"`
}

type versionDoc struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// exitError ends a command with an exit code. A silent one only carries the code: the command
// has already printed its output, for example the result of a failed run.
type exitError struct {
	exit    int
	code    string
	message string
	hint    string
	// usage is the command's usage text, printed after a usage error.
	usage  string
	silent bool
}

func (e *exitError) Error() string {
	return e.message
}

func usageError(cmd *cobra.Command, message string) *exitError {
	return &exitError{exit: exitUsage, code: "usage", message: message, hint: cmd.CommandPath() + " --help", usage: cmd.Long}
}

func silentExit(code int) *exitError {
	return &exitError{exit: code, silent: true}
}

// classify turns any error into its exit code and operator text.
func classify(err error) *exitError {
	var ee *exitError
	if errors.As(err, &ee) {
		return ee
	}
	var pe *service.Error
	if errors.As(err, &pe) {
		exit := exitUsage
		if pe.Code == service.CodeConflict {
			exit = exitRefused
		}
		return &exitError{exit: exit, code: pe.Code, message: pe.Message, hint: pe.Hint}
	}
	return &exitError{exit: exitUsage, code: "error", message: err.Error()}
}

// report prints err for the operator on stderr and, with --json, as an error document on
// stdout. It returns the exit code.
func (e *env) report(err error) int {
	x := classify(err)
	if x.silent {
		return x.exit
	}
	_, _ = fmt.Fprintf(e.s.Err, "ktags: %s\n", x.message)
	switch {
	case x.usage != "":
		_, _ = fmt.Fprintf(e.s.Err, "\n%s", x.usage)
	case x.hint != "":
		_, _ = fmt.Fprintf(e.s.Err, "  next: %s\n", x.hint)
	}
	if e.json {
		_ = writeJSON(e.s.Out, "error", errorDoc{Code: x.code, Message: x.message, Hint: x.hint, Exit: x.exit})
	}
	return x.exit
}

// emit prints the command's document when --json is set. A command emits at most once, at its
// end; an error after emitting must be silent.
func (e *env) emit(kind string, data any) error {
	if !e.json {
		return nil
	}
	return writeJSON(e.s.Out, kind, data)
}

func writeJSON(w io.Writer, kind string, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(document{Schema: schema, Kind: kind, Data: data})
}

// nonNil makes an empty list encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
