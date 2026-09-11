// Package shell quotes words for a POSIX shell, so a command ktags suggests to the operator can
// be pasted as it is printed.
package shell

import "strings"

// Quote makes s one word for a POSIX shell. Nothing is special inside single quotes; a single
// quote in s closes the quoting, is escaped with a backslash, and reopens it.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
