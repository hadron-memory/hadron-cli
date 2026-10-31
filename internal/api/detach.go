package api

import (
	"context"
	"errors"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// detachedError is an error's public face with none of its chain: the redacted
// text, the exit code, the server code, the internal-fault detail and the
// classification a caller may branch on. It holds no raw error, so nothing the
// raw graph carried (a response body, a URL error's address, a joined cause)
// is reachable through Unwrap, errors.As, errors.Is or any serializer.
type detachedError struct {
	msg      string
	human    string
	hasHuman bool
	fault    *internalFaultError
	canceled bool
	deadline bool
	silent   bool
}

func (e *detachedError) Error() string { return e.msg }

// HumanMessage is the terminal-safe text; the root handler prefers it.
func (e *detachedError) HumanMessage() string {
	if e.hasHuman {
		return e.human
	}
	return e.msg
}

// detachedCodedError is a detachedError whose raw error carried a server code.
// It is a separate type so a detachment with no code does not answer
// ServerCode at all: the root handler would otherwise write an empty
// serverCode into the envelope, and shadow the codes it derives itself.
type detachedCodedError struct {
	*detachedError
	serverCode string
}

func (e *detachedCodedError) ServerCode() string { return e.serverCode }

// Unwrap exposes only the CLI-built internal-fault detail, which carries no
// cause; every other detachment has no chain at all.
func (e *detachedError) Unwrap() error {
	if e.fault != nil {
		return e.fault
	}
	return nil
}

// Is answers the sentinels that were reachable on the raw chain when it was
// detached. They are package-level values, not request data.
func (e *detachedError) Is(target error) bool {
	switch target {
	case context.Canceled:
		return e.canceled
	case context.DeadlineExceeded:
		return e.deadline
	case exitcode.ErrSilent:
		return e.silent
	}
	return false
}

// Detached returns err as a copy that renders and classifies the same, with
// redact applied to every rendered string, and that retains no part of err's
// chain. Use it where err's graph may hold a secret that Error() does not show
// (a wrapper with a safe message over a secret-bearing cause, a joined list):
// redacting only when the outer text changes leaves that cause reachable.
//
// Preserved: Error() and HumanMessage() (redacted), the exit code, ServerCode(),
// the internal-fault detail, and errors.Is for context.Canceled,
// context.DeadlineExceeded and exitcode.ErrSilent. Not preserved: errors.As to
// a concrete raw type (a GraphQL error list, an HTTP error, a *url.Error).
// A nil err stays nil.
func Detached(err error, redact func(string) string) error {
	if err == nil {
		return nil
	}
	d := &detachedError{
		msg:      redact(err.Error()),
		canceled: errors.Is(err, context.Canceled),
		deadline: errors.Is(err, context.DeadlineExceeded),
		silent:   errors.Is(err, exitcode.ErrSilent),
	}
	var h interface{ HumanMessage() string }
	if errors.As(err, &h) {
		d.human, d.hasHuman = redact(h.HumanMessage()), true
	}
	if fault, ok := InternalFaultOf(err); ok {
		d.fault = &internalFaultError{fault: fault}
	}
	var out error = d
	var sc interface{ ServerCode() string }
	if errors.As(err, &sc) {
		out = &detachedCodedError{detachedError: d, serverCode: sc.ServerCode()}
	}
	var coded *exitcode.CodedError
	if errors.As(err, &coded) {
		return exitcode.New(coded.Code, out)
	}
	return out
}
