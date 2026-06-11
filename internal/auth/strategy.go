// Package auth implements `hadron auth login` flows against the
// Hadron OAuth endpoints (spec 025: authorization-code + PKCE,
// public client via dynamic client registration).
package auth

import (
	"context"
	"net/http"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Token is the credential a login flow produces. Hadron v1 issues
// long-lived opaque hdr_user_* access tokens; there is no refresh
// token (spec 025 defers RFC 6749 refresh).
type Token struct {
	AccessToken string
}

// LoginOptions parameterizes a login flow.
type LoginOptions struct {
	ServerURL   string
	IO          *output.IOStreams
	HTTPClient  *http.Client
	OpenBrowser func(url string) error
}

// Strategy is a way to obtain a token interactively. BrowserStrategy
// (loopback PKCE) is the only v1 implementation; an RFC 8628 device
// flow can slot in here once hadron-server supports it.
type Strategy interface {
	Name() string
	Login(ctx context.Context, opts LoginOptions) (*Token, error)
}
