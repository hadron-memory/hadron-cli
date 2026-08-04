package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
)

// ImpersonationHostKey derives the store key an active impersonation token is
// filed under, from the same host key a normal credential uses. Store backends
// (keyring / file) treat the key as opaque, so a suffixed host keeps the
// impersonation token beside — not overwriting — the admin's own credential.
func ImpersonationHostKey(serverURL string) string {
	return Host(serverURL) + "#impersonation"
}

// impJWTClaims is the subset of an impersonation JWT the CLI reads locally
// (unverified — the server re-validates the session on every request; this is
// only used to skip an already-expired stored token).
type impJWTClaims struct {
	Exp int64 `json:"exp"`
	Imp *struct {
		Sid string `json:"sid"`
		Act string `json:"act"`
		Org string `json:"org"`
	} `json:"imp"`
}

// ImpersonationTokenLive reports whether a stored token is a well-formed
// impersonation JWT whose `exp` is still in the future. A non-impersonation
// token, a malformed one, or an expired one all return false so the caller
// deletes it and falls through to the real credential.
func ImpersonationTokenLive(token string) bool {
	claims, ok := decodeImpClaims(token)
	if !ok || claims.Imp == nil || claims.Imp.Sid == "" {
		return false
	}
	return claims.Exp > 0 && time.Now().Unix() < claims.Exp
}

func decodeImpClaims(token string) (impJWTClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return impJWTClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return impJWTClaims{}, false
	}
	var claims impJWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return impJWTClaims{}, false
	}
	return claims, true
}

// ResolveImpersonationToken returns a LIVE stored impersonation token for the
// server, or "" if none is filed. A stored-but-expired token is purged on a
// BEST-EFFORT basis as a side effect (across backends; purge errors are
// ignored) so `Token()` can fall through to the real credential without the
// caller re-checking. Treat the cleanup as opportunistic, not guaranteed —
// correctness rests on the liveness check, not on the delete succeeding.
func ResolveImpersonationToken(st store.Store, serverURL string) string {
	key := ImpersonationHostKey(serverURL)
	token, err := st.Get(key)
	if err != nil || token == "" {
		return ""
	}
	if !ImpersonationTokenLive(token) {
		// Best-effort cleanup across all backends; ignore errors — the caller
		// just proceeds with the real credential.
		_, _ = store.Purge(key)
		return ""
	}
	return token
}
