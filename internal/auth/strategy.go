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
	ServerURL string
	// LoginProvider selects the server-side identity provider used for the
	// browser hop. Empty and "github" preserve the legacy authorize request;
	// "google" asks a provider-aware server to use Google.
	LoginProvider string
	IO            *output.IOStreams
	HTTPClient    *http.Client
	OpenBrowser   func(url string) error
	// CodeEntry asks for code-entry mode up front (hadron auth login
	// --no-browser): the CLI prints the authorize address, asks the server to
	// show the code on a page, and waits for it to be pasted. No loopback
	// listener is started. Without it the CLI still enters this mode by itself
	// when no browser can be opened on the host.
	CodeEntry bool
	// ReadCode reads a pasted code. Nil reads one line from a terminal on
	// IO.In without echo and refuses when IO.In is not a terminal.
	ReadCode CodeReader
}

// Strategy is a way to obtain a token interactively. BrowserStrategy
// (loopback PKCE) is the only v1 implementation; an RFC 8628 device
// flow can slot in here once hadron-server supports it.
type Strategy interface {
	Name() string
	Login(ctx context.Context, opts LoginOptions) (*Token, error)
}
