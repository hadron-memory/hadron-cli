package auth

import (
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/hadron-memory/hadron-cli/internal/auth/store"
)

// TokenSource says where the active token came from.
type TokenSource string

const (
	SourceEnv   TokenSource = "HADRON_TOKEN"
	SourceStore TokenSource = "store"
	SourceNone  TokenSource = ""
)

// Host extracts the host key used for token storage from a server URL.
func Host(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return serverURL
	}
	return u.Host
}

// ResolveToken returns the active token for a server, preferring the
// HADRON_TOKEN environment variable over the token store. A genuine "no
// credential" (ErrNotFound or an empty token) yields SourceNone with a nil
// error; any OTHER store error — a corrupt/truncated auth.json, a permission
// or keychain-access failure — is propagated so it fails loud instead of
// masquerading as a logged-out state (#125).
func ResolveToken(st store.Store, serverURL string) (string, TokenSource, error) {
	if env := os.Getenv(store.EnvToken); env != "" {
		return env, SourceEnv, nil
	}
	host := Host(serverURL)
	token, err := st.Get(host)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "", SourceNone, nil
	case err != nil:
		return "", SourceNone, fmt.Errorf("reading the stored credential for %s: %w", host, err)
	case token == "":
		return "", SourceNone, nil
	default:
		return token, SourceStore, nil
	}
}
