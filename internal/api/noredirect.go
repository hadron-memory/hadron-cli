package api

import "context"

type noRedirectKey struct{}

// WithNoRedirect marks ONE call as non-replayable across a redirect (hadron-cli
// #1568, gate ruling #69494). net/http follows 307/308 by re-sending the POST
// body, so a one-send mutation such as `auth token delete` would reach the
// endpoint twice. A call whose context carries this marker refuses to follow ANY
// redirect hop, in the same CheckRedirect that already guards session and
// bearer-token replay. It is a per-call opt-in like WithSession: no client's
// default changes, and every other operation keeps following redirects.
func WithNoRedirect(ctx context.Context) context.Context {
	return context.WithValue(ctx, noRedirectKey{}, true)
}

func noRedirectFrom(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(noRedirectKey{}).(bool)
	return v
}
