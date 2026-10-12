package api

import (
	"context"
	"encoding/base64"
	"regexp"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// OperationKeyHeader supplies a memory data key for one App operation
// (x-hadron-operation-key, hadron-server#1817, the first #1796 slice).
// cor:sec:040:04 — "a key is never a field on a query or a mutation" — is the
// rule it implements: a key in GraphQL variables travels in the request body,
// where any body logging, APM capture or proxy log would hold key material.
// The server stages the header request-scoped (outside GraphQL Context),
// authenticates before parsing, and consumes it exactly once; installing it as
// a persistent client default is refused by design, so it must ride per call.
const OperationKeyHeader = "X-Hadron-Operation-Key"

type operationKeyCtx struct{}

// WithOperationKey marks the GraphQL request made with ctx as carrying the
// memory data key in the OperationKeyHeader. Like WithSession it is
// deliberately per-CALL, never a client default: the key rides exactly the one
// mutation that needs it, and every other request a command makes — before or
// after — goes out without it. An empty key leaves ctx unchanged.
func WithOperationKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, operationKeyCtx{}, key)
}

// operationKeyFrom returns the key WithOperationKey stored, or "".
func operationKeyFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	key, _ := ctx.Value(operationKeyCtx{}).(string)
	return key
}

// canonicalOperationKey is the server's exact header shape
// (hadron-server requestOperationKey.ts): 43 base64 characters plus one '=',
// i.e. canonical base64 for exactly 32 bytes.
var canonicalOperationKey = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)

// ValidateOperationKey refuses everything the server's operation-key parser
// would refuse — so a malformed key is a LOCAL usage error before any request,
// not a BAD_UNLOCK_INPUT after a round-trip. Mirrors the server exactly:
// charset and length first, then the decode-and-reencode round-trip that
// rejects non-canonical spellings of valid bytes (the decoder silently drops
// the final character's low padding bits; re-encoding does not reproduce
// them). The value itself is never echoed back.
func ValidateOperationKey(key string) error {
	const shape = "operation key must be one base64-encoded 32-byte key (43 base64 characters followed by '=')"
	if !canonicalOperationKey.MatchString(key) {
		return exitcode.Newf(exitcode.Usage, "%s", shape)
	}
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 32 || base64.StdEncoding.EncodeToString(raw) != key {
		return exitcode.Newf(exitcode.Usage, "%s", shape)
	}
	return nil
}
