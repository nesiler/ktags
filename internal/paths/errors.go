package paths

// Error reports a rejected path setting. Its text names only that setting and its own value, so
// it can reach the operator without exposing other configuration.
type Error struct {
	// Setting is the environment variable at fault (HOME for built-in defaults).
	Setting string
	// Problem is the measured fact, quoting at most this setting's own value or root.
	Problem string
	// Next is the corrective action for the operator.
	Next string
	// Err is the filesystem cause, when there is one.
	Err error
}

func (e *Error) Error() string {
	msg := e.Setting + ": " + e.Problem
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg + "\n  next: " + e.Next
}

func (e *Error) Unwrap() error {
	return e.Err
}
