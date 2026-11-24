package team

import (
	"errors"
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type inboxPublicMessage struct {
	exit int
	text string
}

// Membership, never character shape: every printable reason/message is fixed.
var inboxMessages = map[string]inboxPublicMessage{
	"INBOX_UNBOUND":                 {exitcode.Usage, "inbox requires this worktree's complete worker session binding; check hadron team session whoami --check"},
	"INBOX_SERVER_MISMATCH":         {exitcode.Conflict, "inbox server differs from the worker binding; select the binding's server with --server"},
	"INBOX_APP_MISMATCH":            {exitcode.Conflict, "inbox App differs from the worker binding; select the bound App with --app"},
	"INBOX_BINDING_CHANGED":         {exitcode.Conflict, "worker binding changed; acknowledgment refused; run inbox from the current binding"},
	"SESSION_NOT_LIVE":              {exitcode.Conflict, "this worker session is no longer live; check or resume the worker session before reading again"},
	"SESSION_NOT_FOUND":             {exitcode.NotFound, "this worker session was not found for the caller; check the worker session binding"},
	"WAKE_PROOF_STALE":              {exitcode.Conflict, "the proof is stale; run hadron team chat inbox again (unacknowledged batches may show again)"},
	"WAKE_PROOF_INVALID":            {exitcode.Usage, "the proof is invalid; use the unmodified ids from this session's inbox JSON output"},
	"WAKE_PROOF_REQUESTER_MISMATCH": {exitcode.Forbidden, "these proofs belong to a different worker session; use the original live session"},
	"APP_KEY_SESSION_BOUND":         {exitcode.Usage, "a host key cannot drive the self inbox; use the caller credential that owns this worker session"},
	"APP_NOT_FOUND":                 {exitcode.NotFound, "the inbox App was not found for this caller"},
	"FORBIDDEN":                     {exitcode.Forbidden, "this caller is not permitted to read or acknowledge the inbox"},
	"UNAUTHENTICATED":               {exitcode.AuthRequired, "the inbox caller is not authenticated"},
	"BAD_USER_INPUT":                {exitcode.Usage, "the server refused the inbox input; check the supplied cursor or acknowledgment ids"},
	"GRAPHQL_VALIDATION_FAILED":     {exitcode.Usage, "this server does not accept the inbox operation; check the server version"},
}

type inboxRefusal struct{ reason string }

func (e *inboxRefusal) Error() string {
	if m, ok := inboxMessages[e.reason]; ok {
		return e.reason + ": " + m.text
	}
	return "inbox refused"
}
func inboxLocal(reason string) error {
	m, ok := inboxMessages[reason]
	if !ok {
		return exitcode.Newf(exitcode.Error, "inbox refused")
	}
	return exitcode.New(m.exit, &inboxRefusal{reason: reason})
}
func inboxReason(err error) (string, inboxPublicMessage, bool) {
	var own *inboxRefusal
	if errors.As(err, &own) {
		m, ok := inboxMessages[own.reason]
		return own.reason, m, ok
	}
	for _, reason := range []string{"UNAUTHENTICATED", "FORBIDDEN", "SESSION_NOT_FOUND", "SESSION_NOT_LIVE", "WAKE_PROOF_REQUESTER_MISMATCH", "WAKE_PROOF_STALE", "WAKE_PROOF_INVALID", "APP_KEY_SESSION_BOUND", "APP_NOT_FOUND", "BAD_USER_INPUT", "GRAPHQL_VALIDATION_FAILED"} {
		if api.HasErrorCode(err, reason) {
			return reason, inboxMessages[reason], true
		}
	}
	return "", inboxPublicMessage{}, false
}
func inboxFailure(err error, message string) error {
	if reason, m, ok := inboxReason(err); ok {
		return exitcode.Newf(m.exit, "%s: %s", reason, m.text)
	}
	return exitcode.Newf(exitcode.FromError(api.MapError(err)), "%s", message)
}

// The stages of the identity step that can fail for a reason that is not the
// binding file's (hadron-cli#3004, #3069): the server URL cannot be resolved
// (config could not be read), the caller's App context is unusable, the CLI
// could not set up its server client (local: no server was contacted), or the
// server read that compares the context with the binding's App failed.
type inboxStage int

const (
	inboxStageAppContext inboxStage = iota + 1
	inboxStageClient
	inboxStageAppRead
	inboxStageServer
)

// inboxIdentityStageError marks an error from the App-identity step with the
// stage that produced it, so the command can say WHICH step failed and why
// instead of blaming a binding file that was read fine.
type inboxIdentityStageError struct {
	stage inboxStage
	err   error
}

func (e *inboxIdentityStageError) Error() string { return e.err.Error() }
func (e *inboxIdentityStageError) Unwrap() error { return e.err }

// inboxCauseClass names a failure by its exit class only: the response body,
// URL and any server text are untrusted and never printed (the same rule as
// every other inbox failure).
func inboxCauseClass(code int) string {
	switch code {
	case exitcode.Unavailable:
		return "server unavailable (a gateway or network error)"
	case exitcode.Error:
		return "server error"
	case exitcode.AuthRequired:
		return "not authenticated"
	case exitcode.Forbidden:
		return "not permitted"
	case exitcode.NotFound:
		return "not found"
	default:
		return "the server could not answer"
	}
}

// inboxIdentityFailure reports a failed identity step. A known SERVER code on the
// read (FORBIDDEN, UNAUTHENTICATED, APP_NOT_FOUND, ...) keeps its fixed public
// message; the local INBOX_* refusals are returned unwrapped and never get here.
// An App-context failure prints only fixed CLI wording; every other failure of
// the step names the stage and the cause class. The exit class is always the one
// the cause has today. recheck says it was the second read, before an ack.
func inboxIdentityFailure(err error, fallback string, recheck bool) error {
	var stage *inboxIdentityStageError
	if !errors.As(err, &stage) {
		return inboxFailure(err, fallback)
	}
	if _, _, known := inboxReason(err); known {
		return inboxFailure(err, fallback)
	}
	code := exitcode.FromError(api.MapError(stage.err))
	verb := "could not confirm"
	if recheck {
		verb = "could not re-confirm"
	}
	read := "could not read"
	if recheck {
		read = "could not re-read"
	}
	switch stage.stage {
	case inboxStageAppContext:
		// Never an error's own text: Factory.App() can fail for a malformed
		// value (a usage error) or a config fault, and only the CLI's fixed
		// words below are printed for either (the value and any path stay out).
		if code == exitcode.Usage {
			return exitcode.Newf(code, "%s the App context (--app or the configured App): it is not a valid App reference; name the App by id, hrn:app: URN or <org>:<slug> (the value is not shown; `hadron config get app` shows the configured one)", read)
		}
		return exitcode.Newf(code, "%s the App context: a local configuration problem (`hadron config get app` shows what is set)", read)
	case inboxStageServer:
		// Factory.Server() fails only when config cannot be read. Only the CLI's fixed words are printed, never the
		// cause's text or a path; the exit class is the cause's own, as for the other stages (cli#3069).
		return exitcode.Newf(code, "%s the server URL: a local configuration problem (`hadron config get server` shows what is set)", read)
	case inboxStageClient:
		// Local: nothing was sent, so never claim the server failed to answer.
		return exitcode.Newf(code, "%s the App for this worktree: the CLI could not set up its server call (%s)", verb, inboxClientClass(code))
	}
	msg := fmt.Sprintf("%s the App for this worktree: %s", verb, inboxCauseClass(code))
	if code == exitcode.Unavailable {
		msg += "; this read is safe to retry"
	}
	return exitcode.Newf(code, "%s", msg)
}

// inboxClientClass names a local client-setup failure without server wording.
func inboxClientClass(code int) string {
	if code == exitcode.AuthRequired {
		return "not authenticated; run hadron auth login or set HADRON_TOKEN"
	}
	return "check the server and credential configuration"
}

func inboxUnconfirmed(streamsErr io.Writer, jsonMode bool, err error) error {
	failure := inboxFailure(err, "inbox acknowledgment unconfirmed")
	if _, _, known := inboxReason(err); known {
		_, _ = fmt.Fprintln(streamsErr, "Inbox output completed; acknowledgment refused. "+failure.Error())
		return exitcode.Silent(exitcode.FromError(failure))
	}
	_, _ = fmt.Fprintln(streamsErr, failure.Error())
	if jsonMode {
		_, _ = fmt.Fprintln(streamsErr, "Inbox output completed. Pass this output's batches[].id values as {\"batchIds\":[...]} to hadron team chat inbox --ack-file - in the same live session; do not redisplay to acknowledge.")
	} else {
		_, _ = fmt.Fprintln(streamsErr, "Acknowledgment unconfirmed; run hadron team chat inbox again (unacknowledged batches show again).")
	}
	return exitcode.Silent(exitcode.FromError(failure))
}
