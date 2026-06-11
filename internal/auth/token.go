package auth

import (
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
// HADRON_TOKEN environment variable over the token store.
func ResolveToken(st store.Store, serverURL string) (string, TokenSource) {
	if env := os.Getenv(store.EnvToken); env != "" {
		return env, SourceEnv
	}
	token, err := st.Get(Host(serverURL))
	if err != nil || token == "" {
		return "", SourceNone
	}
	return token, SourceStore
}
