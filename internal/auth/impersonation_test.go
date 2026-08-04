package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
)

// makeImpToken builds an UNSIGNED JWT-shaped string (header.payload.sig) whose
// payload carries the given exp + an `imp` claim — enough for the CLI's local
// liveness precheck (the server does the real verification).
func makeImpToken(t *testing.T, exp int64, withImp bool) string {
	t.Helper()
	claims := map[string]any{"exp": exp, "sub": "target"}
	if withImp {
		claims["imp"] = map[string]string{"sid": "sess1", "act": "admin1", "org": "org1"}
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"HS256"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}

func TestImpersonationTokenLive(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	past := time.Now().Add(-time.Hour).Unix()

	if !ImpersonationTokenLive(makeImpToken(t, future, true)) {
		t.Error("a live imp token must be reported live")
	}
	if ImpersonationTokenLive(makeImpToken(t, past, true)) {
		t.Error("an expired imp token must be reported dead")
	}
	if ImpersonationTokenLive(makeImpToken(t, future, false)) {
		t.Error("a token without an imp claim is not an impersonation token")
	}
	if ImpersonationTokenLive("not.a.jwt.at.all") {
		t.Error("a malformed token must be reported dead")
	}
	if ImpersonationTokenLive("") {
		t.Error("an empty token must be reported dead")
	}
}

// mutableStore is an in-memory Store double. `deleted` records Delete calls;
// note ResolveImpersonationToken's expired-token cleanup goes through
// store.Purge (all backends, best-effort), not this double, so the tests
// assert the RESOLUTION result rather than the cleanup side effect.
type mutableStore struct {
	tokens  map[string]string
	deleted []string
}

func (m *mutableStore) Name() string { return "mutable" }
func (m *mutableStore) Get(host string) (string, error) {
	if v, ok := m.tokens[host]; ok && v != "" {
		return v, nil
	}
	return "", store.ErrNotFound
}
func (m *mutableStore) Set(host, token string) error { m.tokens[host] = token; return nil }
func (m *mutableStore) Delete(host string) error {
	m.deleted = append(m.deleted, host)
	delete(m.tokens, host)
	return nil
}

func TestResolveImpersonationToken(t *testing.T) {
	const server = "https://s.example"
	key := ImpersonationHostKey(server)

	// A live token is returned as-is.
	live := makeImpToken(t, time.Now().Add(time.Hour).Unix(), true)
	st := &mutableStore{tokens: map[string]string{key: live}}
	if got := ResolveImpersonationToken(st, server); got != live {
		t.Errorf("live token = %q, want it returned verbatim", got)
	}

	// No token filed → "".
	empty := &mutableStore{tokens: map[string]string{}}
	if got := ResolveImpersonationToken(empty, server); got != "" {
		t.Errorf("absent token = %q, want empty", got)
	}

	// An EXPIRED token resolves to "" so the caller falls through to the real
	// credential — a lapsed session must never wedge the CLI.
	expired := &mutableStore{
		tokens: map[string]string{key: makeImpToken(t, time.Now().Add(-time.Hour).Unix(), true)},
	}
	if got := ResolveImpersonationToken(expired, server); got != "" {
		t.Errorf("expired token = %q, want empty", got)
	}

	// A non-impersonation token filed under the key is ignored too.
	notImp := &mutableStore{
		tokens: map[string]string{key: makeImpToken(t, time.Now().Add(time.Hour).Unix(), false)},
	}
	if got := ResolveImpersonationToken(notImp, server); got != "" {
		t.Errorf("non-impersonation token = %q, want empty", got)
	}
}

func TestImpersonationHostKeyDistinctFromCredential(t *testing.T) {
	const server = "https://s.example"
	if ImpersonationHostKey(server) == Host(server) {
		t.Error("the impersonation key must not collide with the normal credential key")
	}
}
