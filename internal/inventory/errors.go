package inventory

import "strings"

// Error reports a failed load or save of a customer record. Its text never quotes field values,
// so a secret pasted into the wrong field cannot reach a log or the terminal through it.
type Error struct {
	// File is the ktags.yml path the operation was about.
	File string
	// Problem is the measured fact.
	Problem string
	// Next is the corrective action for the operator.
	Next string
	// Err is the cause, when there is one.
	Err error
}

func (e *Error) Error() string {
	msg := e.File + ": " + e.Problem
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg + "\n  next: " + e.Next
}

func (e *Error) Unwrap() error {
	return e.Err
}

// FieldProblem is one refused field. Field is a path such as ktags_cluster.nodes[1].id.
type FieldProblem struct {
	Field   string
	Problem string
}

// ValidationError lists every refused field of a record.
type ValidationError struct {
	Problems []FieldProblem
}

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		lines = append(lines, p.Field+": "+p.Problem)
	}
	return "invalid record: " + strings.Join(lines, "; ")
}
