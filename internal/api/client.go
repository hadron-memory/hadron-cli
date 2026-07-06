// Package api wraps the Hadron GraphQL endpoint: a genqlient client
// for typed operations, a raw escape hatch for `hadron api`, and the
// mapping from transport/GraphQL errors to exit codes.
package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/urlsec"
)

const graphqlPath = "/graphql"

// EnvAllowHTTP opts out of the HTTPS-enforcement guard for a trusted local or
// self-hosted server (set to "1").
const EnvAllowHTTP = urlsec.EnvAllowHTTP

// RequireSecureURL refuses to transmit the bearer token over a non-https
// server URL — cleartext credentials are trivially captured by an on-path
// attacker on the shared CI/dev machines this CLI runs on (#114). Carve-outs:
// a loopback host (local dev, incl. the test httptest servers) and
// HADRON_ALLOW_HTTP=1 (a trusted self-hosted backend). An empty token means no
// credential rides, so the check is a no-op — anonymous http is allowed.
func RequireSecureURL(serverURL, token string) error {
	if token == "" {
		return nil
	}
	u, err := url.Parse(serverURL)
	if err != nil {
		return err
	}
	if schemeIsSecure(u) {
		return nil
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "(none)"
	}
	return exitcode.Newf(exitcode.Usage,
		"refusing to send credentials to %s over %s — use https, or set %s=1 for a trusted local/self-hosted server",
		serverURL, scheme, EnvAllowHTTP)
}

// schemeIsSecure reports whether the bearer token may ride on u: https, an http
// loopback host, or — via the HADRON_ALLOW_HTTP escape hatch — cleartext http.
// The scheme is allow-listed explicitly: a loopback host must NOT green-light a
// token on ftp/ssh/vscode/other non-HTTP schemes (which url.Hostname() would
// still report as "localhost").
func schemeIsSecure(u *url.URL) bool {
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return urlsec.IsLoopbackHost(u.Hostname()) || os.Getenv(EnvAllowHTTP) == "1"
	}
	return false
}

// withSecureRedirects returns a shallow copy of client whose CheckRedirect
// refuses any redirect hop to a non-secure target. Go forwards the
// Authorization header across SAME-host redirects, so without this a
// misconfigured https endpoint that 30x-redirects to http://<same-host> would
// put the bearer token on the wire in cleartext despite the initial-URL guard
// (#114 / #121). The caller's client is copied, not mutated (it is shared).
func withSecureRedirects(client *http.Client) *http.Client {
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !schemeIsSecure(req.URL) {
			return fmt.Errorf("refusing to follow a redirect to %s over %s — the bearer token would be sent in cleartext",
				req.URL.Redacted(), req.URL.Scheme)
		}
		// Preserve the net/http default cap of 10 hops (overridden once we set
		// CheckRedirect ourselves).
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &c
}

// bearerDoer injects the Authorization header on every request.
type bearerDoer struct {
	token string
	inner *http.Client
}

func (d *bearerDoer) Do(req *http.Request) (*http.Response, error) {
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	return d.inner.Do(req)
}

// Endpoint joins the server base URL with the GraphQL path.
func Endpoint(serverURL string) string {
	return strings.TrimRight(serverURL, "/") + graphqlPath
}

// NewClient returns a genqlient client for the given server,
// authenticating with token (may be empty for anonymous calls).
func NewClient(serverURL, token string, httpClient *http.Client) (graphql.Client, error) {
	if _, err := url.ParseRequestURI(serverURL); err != nil {
		return nil, err
	}
	if err := RequireSecureURL(serverURL, token); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if token != "" {
		httpClient = withSecureRedirects(httpClient)
	}
	return graphql.NewClient(Endpoint(serverURL), &bearerDoer{token: token, inner: httpClient}), nil
}
