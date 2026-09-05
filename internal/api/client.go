// Package api wraps the Hadron GraphQL endpoint: a genqlient client
// for typed operations, a raw escape hatch for `hadron api`, and the
// mapping from transport/GraphQL errors to exit codes.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
			return fmt.Errorf("%w: refusing to follow a redirect to %s over %s — the bearer token would be sent in cleartext",
				ErrRedirectPolicy, req.URL.Redacted(), req.URL.Scheme)
		}
		// Preserve the net/http default cap of 10 hops (overridden once we set
		// CheckRedirect ourselves).
		if len(via) >= 10 {
			return fmt.Errorf("%w: stopped after 10 redirects", ErrRedirectPolicy)
		}
		return nil
	}
	return &c
}

// ErrRedirectPolicy marks a refusal WE made about a redirect, as opposed to a
// network failure. net/http wraps a CheckRedirect error in *url.Error, which
// satisfies net.Error — so without this sentinel the transport classifier
// (#394) would read our own HTTPS-downgrade refusal as a lost request and
// report it as retryable exit 7. The server answered; retrying cannot help.
var ErrRedirectPolicy = errors.New("redirect refused by policy")

// bearerDoer injects the Authorization header on every request.
type bearerDoer struct {
	token string
	inner *http.Client
}

func (d *bearerDoer) Do(req *http.Request) (*http.Response, error) {
	if d.token != "" {
		req.Header.Set("Authorization", "Bearer "+d.token)
	}
	resp, err := d.inner.Do(req)
	if err != nil || resp.StatusCode < 500 {
		return resp, err
	}
	return classifyGatewayResponse(resp)
}

// classifyGatewayResponse decides, FROM THE RAW BODY, whether a 5xx is the API
// forming an opinion or the edge losing the request (#544).
//
// It has to happen here because this is the last place the raw body exists.
// genqlient parses the body itself and, when it is not JSON, SYNTHESISES a
// one-entry error list holding the whole body as a message:
//
//	Errors: gqlerror.List{&gqlerror.Error{Message: string(respBody)}}
//
// so downstream `len(Response.Errors) > 0` — which is what classifyTransport
// used to ask — is TRUE for every gateway page ever served. The branch that
// was supposed to catch `<html>502 Bad Gateway</html>` could only fire for a
// gateway returning valid JSON with no `errors` array, which no gateway does.
// Every practical gateway 5xx was reported as a refusal: exit 1, no
// idempotency caveat, on the failure class #394 exists to separate.
//
// Asking the body directly also makes this path agree with `hadron api`'s
// (`transportStatus`), which has always classified an HTML 502 correctly. That
// divergence is what hid the bug: the same question was answered two ways, and
// only the answer nobody tested was wrong.
func classifyGatewayResponse(resp *http.Response) (*http.Response, error) {
	// Read in full rather than up to a cap. genqlient's own error path does
	// io.ReadAll with no limit, so this adds no exposure — while a cap would
	// add a failure mode, truncating a large but legitimate errors[] into
	// invalid JSON and reclassifying the API's opinion as a lost request.
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		// The body was cut short mid-read: headers arrived, so the request
		// certainly reached the server and a mutation may already have
		// committed. That is a lost answer, not a refusal.
		return nil, &gatewayError{status: resp.StatusCode, body: nil}
	}
	if hasGraphQLErrorsEnvelope(body) {
		// The API's own opinion, however bad the status. Hand the response
		// back intact — genqlient parses it and the extension-code mapping in
		// MapError still applies.
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
	return nil, &gatewayError{status: resp.StatusCode, body: body}
}

// gatewayError is a 5xx whose body was not a GraphQL envelope — the edge, not
// the API. classifyTransport recognises it and renders the user-facing
// sentence; this Error() is the fallback rendering for anything that prints
// the error directly.
//
// The body is kept but rendered as a BOUNDED, single-line snippet. Dumping it
// whole is what the old behaviour did, and it put a page of HTML in front of
// the user inside a JSON envelope; dropping it entirely would lose the only
// clue about which hop failed.
type gatewayError struct {
	status int
	body   []byte
}

const gatewaySnippetMax = 120

func (e *gatewayError) Error() string {
	snippet := strings.Join(strings.Fields(string(e.body)), " ")
	if len(snippet) > gatewaySnippetMax {
		snippet = snippet[:gatewaySnippetMax] + "…"
	}
	if snippet == "" {
		return fmt.Sprintf("HTTP %d from a gateway (empty body)", e.status)
	}
	return fmt.Sprintf("HTTP %d from a gateway: %s", e.status, snippet)
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
