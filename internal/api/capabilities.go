package api

import (
	"context"
	"fmt"
)

// Capabilities reads GET /capabilities — hadron-server#1925, mounted by
// #1930: which deployment switches are ON, readable before sign-in. The
// answer is `{"capabilities":[...]}` with ruled token words ("node-signing",
// "passkey-registration"); an off switch is OMITTED, never false, so a
// deployment with the switches off reads exactly like a build without the
// features. Unauthenticated, ETag-cached, rate-limited, and OUTSIDE GraphQL.
//
// Any non-200 (including an older server's plain 404), any transport
// failure, and any unparsable body read as "no capabilities": a caller must
// never pay a refused follow-up round trip (e.g. a signature query an older
// server would reject) just because discovery failed.
//
// RED-FIRST STUB: unimplemented.
func Capabilities(ctx context.Context, baseURL string) (map[string]bool, error) {
	return nil, fmt.Errorf("unimplemented")
}
