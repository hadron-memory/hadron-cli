package api

import "context"

// SessionHeader binds a request to a worker session (hadron-server#934,
// amending cor:aut:020:02). The server honors it only for a session the
// caller's own principal started and that is still active, and treats it as
// ATTRIBUTION, never a credential: it cannot widen what the caller may do.
const SessionHeader = "X-Hadron-Session"

type sessionKey struct{}

// WithSession marks every GraphQL request made with ctx as driven by the
// worker session id. It is deliberately per-CALL, never a client default: the
// header changes server behaviour (a heartbeat, and under hadron-server#1353
// the bound worker's own read state), so it rides only on the operations that
// ask for that — `team chat read` and `team chat mark-read` (#1353, Dara on
// team chat #1889) — and not on every request a bound worktree makes.
// An empty id leaves ctx unchanged.
func WithSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionKey{}, sessionID)
}

// sessionFrom returns the session id WithSession stored, or "".
func sessionFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sessionKey{}).(string)
	return id
}
