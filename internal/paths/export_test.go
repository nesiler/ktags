package paths

import "testing"

// SetCurrentUID makes Ensure treat uid as the operator for the rest of the test.
func SetCurrentUID(t *testing.T, uid int) {
	old := currentUID
	currentUID = func() int { return uid }
	t.Cleanup(func() { currentUID = old })
}
