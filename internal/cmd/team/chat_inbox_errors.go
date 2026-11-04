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
