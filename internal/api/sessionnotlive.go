package api

import "errors"

// sessionNotLiveSteps are the server's fixed `extensions.reason` codes on a
// SESSION_NOT_LIVE refusal (hadron-server#5079) with the one-line next step the
// CLI prints for each. Membership is the whole rule: a reason outside this set
// is treated exactly like no reason, and its raw text is never printed
// (hadron-cli#3111). The server names no other worker or session, and neither
// may these lines.
var sessionNotLiveSteps = map[string]string{
	"DORMANT":            "this session was dormant too long to resume; start a new session",
	"ENDED":              "this session has ended; start a new session",
	"OTHER_SESSION_LIVE": "another session of this worker is live; --force is the explicit replace",
	"LIVENESS_UNKNOWN":   "too many open sessions on this worker to check them all, and none of those checked was live; check the binding with `hadron team session whoami --check`, then retry; do not force",
	"NOT_OWNER":          "this is not your session",
}

// sessionNotLiveReasoned is a refusal that carries the validated reason, so the
// --json envelope can add it after a command rebuilt the error without the
// server's GraphQL chain.
type sessionNotLiveReasoned struct {
	error
	reason string
}

func (e *sessionNotLiveReasoned) Unwrap() error { return e.error }

// SessionNotLiveReason returns the reason code of a SESSION_NOT_LIVE refusal
// when it is one of the five fixed codes, and "" for no reason, an unknown one,
// or any other error.
func SessionNotLiveReason(err error) string {
	var carried *sessionNotLiveReasoned
	if errors.As(err, &carried) {
		return carried.reason
	}
	for _, e := range graphQLErrors(err) {
		if extensionCode(e) != "SESSION_NOT_LIVE" {
			continue
		}
		if r, ok := e.Extensions["reason"].(string); ok {
			if _, known := sessionNotLiveSteps[r]; known {
				return r
			}
		}
	}
	return ""
}

// SessionNotLiveNextStep is the fixed next step for a validated reason.
func SessionNotLiveNextStep(reason string) string { return sessionNotLiveSteps[reason] }

// WithSessionNotLiveReason keeps the raw error's validated reason on mapped for
// the --json envelope. It returns mapped unchanged when there is no reason.
func WithSessionNotLiveReason(mapped, raw error) error {
	if mapped == nil {
		return nil
	}
	if r := SessionNotLiveReason(raw); r != "" {
		return &sessionNotLiveReasoned{error: mapped, reason: r}
	}
	return mapped
}

// AnnotateSessionNotLive adds the reason's fixed next step to a SESSION_NOT_LIVE
// refusal's text and keeps the reason on the returned error for --json. With no
// reason, or one outside the fixed set, it returns text and mapped unchanged.
func AnnotateSessionNotLive(text string, build func(text string) error, raw error) error {
	if r := SessionNotLiveReason(raw); r != "" {
		text += " (" + r + ": " + SessionNotLiveNextStep(r) + ")"
	}
	return WithSessionNotLiveReason(build(text), raw)
}
