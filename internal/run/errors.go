package run

// Error reports a failed run-record operation. Its text never quotes event or result content,
// so a secret the redactor missed cannot reach the terminal through it.
type Error struct {
	// Run is the run ID, or the run directory when the ID is not known.
	Run string
	// Problem is the measured fact.
	Problem string
	// Next is the corrective action for the operator.
	Next string
	// Err is the cause, when there is one.
	Err error
}

func (e *Error) Error() string {
	msg := "run"
	if e.Run != "" {
		msg += " " + e.Run
	}
	msg += ": " + e.Problem
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg + "\n  next: " + e.Next
}

func (e *Error) Unwrap() error {
	return e.Err
}
