// Package hostid holds the one rule for a host's id within a team App, shared
// by every command and package that takes or stores one (hadron-cli#3239).
//
// It is the server's HOST_ID_PATTERN (hadron-server src/lib/hostOperations,
// #4241, #5259): letters, digits, ".", "_", ":" or "-", starting with a letter or
// digit, at most 128 characters. Checking it here only refuses before a request
// what the server would refuse anyway; the server's refusal is still mapped when
// the two disagree.
package hostid

import "regexp"

// Pattern is the host id rule as a regular expression.
var Pattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Valid reports whether id is a host id. It does not trim: a caller that
// accepts surrounding space trims first.
func Valid(id string) bool { return Pattern.MatchString(id) }
