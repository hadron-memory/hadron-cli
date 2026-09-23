// Package exitcode defines the hadron CLI's stable, documented exit
// codes. These are part of the public contract (D8): agents and
// scripts branch on them, so values must never be repurposed.
package exitcode

import (
	"errors"
	"fmt"
)

const (
	OK           = 0 // success
	Error        = 1 // generic failure
	Usage        = 2 // invalid usage: bad flags, bad input, not-yet-implemented
	AuthRequired = 3 // no credentials, or credentials rejected
	NotFound     = 4 // requested entity does not exist (or is not visible)
	Conflict     = 5 // state conflict, e.g. duplicate install
	Cancelled    = 6 // interrupted or timed out waiting for the user
	// Unavailable (#394) is the one failure the server never refused: the
	// request did not reach it, or its answer did not reach us — a gateway
	// 5xx, a reset connection, a timeout. Separated from Error because it is
	// the only class that is safe to retry blind, AND the only one after
	// which a mutation's outcome is genuinely unknown. A script branching on
	// 1 vs 7 is branching on "the server refused this" vs "ask again".
	Unavailable = 7
	// Forbidden (#619) is AUTHENTICATED BUT NOT PERMITTED — the server knows
	// who you are and will not let you do this.
	//
	// It is NOT AuthRequired, and that distinction is the whole reason it
	// exists. 3 means "no credentials, or credentials rejected", whose remedy
	// is `hadron auth login`; printing that at someone already signed in is a
	// false remedy, and the CLI has shipped one of those before (#626, where
	// `whoami` told a rejected token it had no sessions and an App key to log
	// in). Nor is it Error: 1 is "something failed", which leaves an agent
	// unable to tell a permission boundary from a bug or an outage.
	//
	// The class is large and growing. FORBIDDEN is named at 23 sites in the
	// SDL today and already arrives at the CLI; hadron-server#1220 migrates
	// ~59 more call sites onto it, 17 of them inside seven shared gates. Every
	// one of those reached a script as 1 before this.
	//
	// A script branching on 3 vs 8 is branching on "sign in" vs "ask someone
	// for access" — two different humans and two different next actions.
	//
	// Which is why ONE FORBIDDEN exits 3 instead: an MCP-only key (#681,
	// api.IsMCPOnlyCredential). The server refuses it with the same code, but
	// the next action is a different credential, not anyone's permission.
	Forbidden = 8
)

// CodedError carries an exit code alongside an error. The root
// command unwraps it to decide the process exit code.
type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

// ErrSilent marks an error whose message has already been rendered
// by the command; the root handler sets the exit code but prints
// nothing further.
var ErrSilent = errors.New("silent")

// Silent returns a CodedError that only carries an exit code.
func Silent(code int) *CodedError {
	return &CodedError{Code: code, Err: ErrSilent}
}

// New wraps err with an exit code.
func New(code int, err error) *CodedError {
	return &CodedError{Code: code, Err: err}
}

// Newf creates a CodedError from a format string.
func Newf(code int, format string, args ...any) *CodedError {
	return &CodedError{Code: code, Err: fmt.Errorf(format, args...)}
}

// FromError extracts the exit code from an error chain, defaulting
// to Error for any unrecognized failure.
func FromError(err error) int {
	if err == nil {
		return OK
	}
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return Error
}
