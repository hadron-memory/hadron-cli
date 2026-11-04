package team

import (
	"errors"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type postRegistrationState string

func (e postRegistrationState) Error() string { return "host registration unavailable" }

const (
	postNotRegistered postRegistrationState = "not-registered"
	postNotLive       postRegistrationState = "not-live"
)

// Only fixed local reasons or known C1 delivery codes choose text. Upstream
// messages (including credentials/proofs echoed by a proxy) never reach it.
func postHostPreflightFailure(err error, call string) error {
	var reg postRegistrationState
	if errors.As(err, &reg) {
		if reg == postNotLive {
			return exitcode.Newf(exitcode.Conflict, "host registration is not live; check hadron team host status --config <host.yaml>; nothing posted")
		}
		return exitcode.Newf(exitcode.Conflict, "this host is not registered; use hadron team host run --config <host.yaml> to register, then check hadron team host status --config <host.yaml>; nothing posted")
	}
	code := exitcode.FromError(api.MapError(err))
	var classified interface{ DeliveryCode() string }
	if errors.As(err, &classified) {
		switch classified.DeliveryCode() {
		case "APP_KEY_SCOPE_REQUIRED", "HOST_KEY_REQUIRED", "FORBIDDEN", "forbidden":
			code = exitcode.Forbidden
		case "APP_KEY_SESSION_BOUND", "BAD_USER_INPUT", "WAKE_PROOF_INVALID":
			code = exitcode.Usage
		case "WAKE_PROOF_STALE":
			code = exitcode.Conflict
		case "WAKE_PROOF_REQUESTER_MISMATCH":
			code = exitcode.Forbidden
		case "APP_NOT_FOUND":
			code = exitcode.NotFound
		case "UNAUTHENTICATED", "unauthenticated":
			code = exitcode.AuthRequired
		case "unavailable", "transport-error", "no-page", "no-cursor", "bad-deliver-after", "bad-busy-deadline":
			code = exitcode.Unavailable
		}
	}
	// call is an internal literal, never a source error/code or user input.
	switch code {
	case exitcode.AuthRequired:
		return exitcode.Newf(code, "%s requires authentication; check the configured credential; nothing posted", call)
	case exitcode.Forbidden:
		return exitcode.Newf(code, "%s is not authorized for this App; nothing posted", call)
	case exitcode.Unavailable:
		return exitcode.Newf(code, "%s is unavailable; nothing posted", call)
	case exitcode.NotFound:
		return exitcode.Newf(code, "%s target was not found for this caller; nothing posted", call)
	default:
		return exitcode.Newf(code, "%s failed; nothing posted", call)
	}
}
