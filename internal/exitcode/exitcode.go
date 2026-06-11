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
