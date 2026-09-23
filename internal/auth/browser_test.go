package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// fakeAS is a minimal OAuth authorization server covering discovery,
// DCR, and token exchange. "Browser" interaction is simulated by the
// OpenBrowser hook GETting the loopback callback directly.
type fakeAS struct {
	server             *httptest.Server
	clientID           string
	authCode           string
	registeredRedirect string
	seenVerifier       string
	seenResource       string
	// scopesSupported is advertised in discovery; nil omits the key.
	scopesSupported []string
	// tokenScope is the raw JSON `scope` of the token response; nil omits it.
	tokenScope json.RawMessage
}

func newFakeAS(t *testing.T) *fakeAS {
	t.Helper()
	as := &fakeAS{
		clientID:        "client-123",
		authCode:        "code-456",
		scopesSupported: []string{"mcp", "account"},
		tokenScope:      json.RawMessage(`"account"`),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		meta := map[string]any{
			"authorization_endpoint": as.server.URL + "/oauth/authorize",
			"token_endpoint":         as.server.URL + "/oauth/token",
			"registration_endpoint":  as.server.URL + "/oauth/register",
		}
		if as.scopesSupported != nil {
			meta["scopes_supported"] = as.scopesSupported
		}
		_ = json.NewEncoder(w).Encode(meta)
	})
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              as.server.URL + "/mcp",
			"authorization_servers": []string{as.server.URL},
		})
	})
	mux.HandleFunc("/oauth/register", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.RedirectURIs) == 1 {
			as.registeredRedirect = body.RedirectURIs[0]
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"client_id": as.clientID})
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		as.seenVerifier = r.Form.Get("code_verifier")
		as.seenResource = r.Form.Get("resource")
		// The real server requires a resource indicator (RFC 8707)
		// bound from /authorize through /token.
		if as.seenResource != as.server.URL+"/mcp" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request", "error_description": "resource missing or mismatched"})
			return
		}
		if r.Form.Get("code") != as.authCode || r.Form.Get("client_id") != as.clientID {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		resp := map[string]any{
			"access_token": "hdr_user_" + strings.Repeat("a", 64),
			"token_type":   "Bearer",
		}
		if as.tokenScope != nil {
			resp["scope"] = as.tokenScope
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	as.server = httptest.NewServer(mux)
	t.Cleanup(as.server.Close)
	return as
}

func TestBrowserLoginHappyPath(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	var challenge string
	openBrowser := func(authorizeURL string) error {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		q := u.Query()
		challenge = q.Get("code_challenge")
		redirect := q.Get("redirect_uri")
		if redirect != as.registeredRedirect {
			return fmt.Errorf("authorize redirect_uri %q != registered %q", redirect, as.registeredRedirect)
		}
		if got := q.Get("resource"); got != as.server.URL+"/mcp" {
			return fmt.Errorf("authorize resource %q, want %q", got, as.server.URL+"/mcp")
		}
		if got := q.Get("scope"); got != "account" {
			return fmt.Errorf("authorize scope %q, want %q", got, "account")
		}
		if got := q.Get("login_provider"); got != "" {
			return fmt.Errorf("default authorize login_provider %q, want omitted", got)
		}
		// Simulate the consent redirect back to the loopback server.
		go func() {
			resp, err := http.Get(redirect + "?" + url.Values{
				"code":  {as.authCode},
				"state": {q.Get("state")},
			}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	token, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: openBrowser,
	})
	if err != nil {
		t.Fatalf("Login() error: %v", err)
	}
	if !strings.HasPrefix(token.AccessToken, "hdr_user_") {
		t.Errorf("unexpected token %q", token.AccessToken)
	}

	// PKCE: the verifier sent at exchange must hash to the challenge.
	sum := sha256.Sum256([]byte(as.seenVerifier))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != challenge {
		t.Error("code_verifier does not match code_challenge")
	}
}

func TestLoginProviderParam(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     string
		wantErr  bool
	}{
		{name: "empty preserves legacy request", provider: "", want: ""},
		{name: "GitHub preserves legacy request", provider: "github", want: ""},
		{name: "Google is sent", provider: "google", want: "google"},
		{name: "case and whitespace normalize", provider: " Google ", want: "google"},
		{name: "unknown is rejected", provider: "microsoft", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loginProviderParam(tt.provider)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("loginProviderParam(%q) expected an error", tt.provider)
				}
				return
			}
			if err != nil {
				t.Fatalf("loginProviderParam(%q) error: %v", tt.provider, err)
			}
			if got != tt.want {
				t.Errorf("loginProviderParam(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestBrowserLoginDenied(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	openBrowser := func(authorizeURL string) error {
		u, _ := url.Parse(authorizeURL)
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{
				"error":             {"access_denied"},
				"error_description": {"user said no"},
				"state":             {q.Get("state")},
			}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	_, err := BrowserStrategy{}.Login(context.Background(), LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: openBrowser,
	})
	if err == nil || !strings.Contains(err.Error(), "user said no") {
		t.Fatalf("expected denial error, got %v", err)
	}
}

func TestBrowserLoginStateMismatch(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	openBrowser := func(authorizeURL string) error {
		u, _ := url.Parse(authorizeURL)
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{
				"code":  {as.authCode},
				"state": {"forged-state"},
			}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := BrowserStrategy{}.Login(ctx, LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: openBrowser,
	})
	if err == nil || !strings.Contains(err.Error(), "state mismatch") {
		t.Fatalf("expected state mismatch error, got %v", err)
	}
}

// loginCtx bounds a test login well under loginTimeout, so a regression that
// leaves the flow waiting on a callback that never comes fails in seconds
// rather than after five minutes.
func loginCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// consentingBrowser simulates a user approving consent: it redirects to the
// loopback callback with the fake server's code and the request's state.
func consentingBrowser(as *fakeAS) func(string) error {
	return func(authorizeURL string) error {
		u, err := url.Parse(authorizeURL)
		if err != nil {
			return err
		}
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{
				"code":  {as.authCode},
				"state": {q.Get("state")},
			}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
}

// #658: the CLI needs `account`; a server whose discovery lists its scopes
// without it is refused before registration or any browser is opened. A
// present empty list is a server offering nothing, not an absent one.
func TestBrowserLoginRefusesServerWithoutAccountScope(t *testing.T) {
	tests := []struct {
		name       string
		supported  []string
		advertised string
	}{
		{name: "mcp only", supported: []string{"mcp"}, advertised: "advertises: mcp"},
		{name: "explicitly empty", supported: []string{}, advertised: "advertises: none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as := newFakeAS(t)
			as.scopesSupported = tt.supported
			io, _, _ := output.Test()

			opened := false
			_, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
				ServerURL:   as.server.URL,
				IO:          io,
				HTTPClient:  as.server.Client(),
				OpenBrowser: func(string) error { opened = true; return nil },
			})
			if err == nil {
				t.Fatal("Login() succeeded against a server that does not offer account")
			}
			if opened || as.registeredRedirect != "" {
				t.Errorf("flow continued past discovery (browser opened=%v, registered=%q)", opened, as.registeredRedirect)
			}
			if code := exitcode.FromError(err); code != exitcode.Error {
				t.Errorf("exit code %d, want %d", code, exitcode.Error)
			}
			for _, want := range []string{`"account"`, tt.advertised, "--with-token"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

// scopes_supported is optional (RFC 8414): its absence proceeds to the
// authorize step, which is where an unsupported scope is then reported.
func TestBrowserLoginProceedsWithoutScopesSupported(t *testing.T) {
	as := newFakeAS(t)
	as.scopesSupported = nil
	io, _, _ := output.Test()

	if _, err := (BrowserStrategy{}).Login(loginCtx(t), LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: consentingBrowser(as),
	}); err != nil {
		t.Fatalf("Login() error: %v", err)
	}
}

// An invalid_scope redirect is a server-compatibility failure, not the user
// declining: exit 1 (not Cancelled) with the remedy named.
func TestBrowserLoginInvalidScope(t *testing.T) {
	as := newFakeAS(t)
	io, _, _ := output.Test()

	openBrowser := func(authorizeURL string) error {
		u, _ := url.Parse(authorizeURL)
		q := u.Query()
		go func() {
			resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{
				"error": {"invalid_scope"},
				"state": {q.Get("state")},
			}.Encode())
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}

	_, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
		ServerURL:   as.server.URL,
		IO:          io,
		HTTPClient:  as.server.Client(),
		OpenBrowser: openBrowser,
	})
	if err == nil {
		t.Fatal("Login() succeeded after an invalid_scope redirect")
	}
	if code := exitcode.FromError(err); code != exitcode.Error {
		t.Errorf("exit code %d, want %d (not Cancelled: the user did not decline)", code, exitcode.Error)
	}
	for _, want := range []string{`refused the "account" OAuth scope`, "--with-token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// The granted scope in the token response decides whether the key is usable:
// one without `account` is refused, never stored as a silent `mcp` downgrade.
func TestBrowserLoginGrantedScope(t *testing.T) {
	tests := []struct {
		name    string
		granted json.RawMessage // raw JSON; nil omits the field
		wantErr bool
	}{
		{name: "account", granted: json.RawMessage(`"account"`)},
		{name: "account among several", granted: json.RawMessage(`"mcp account"`)},
		{name: "omitted means as requested (RFC 6749 5.1)", granted: nil},
		{name: "explicitly empty is an empty grant", granted: json.RawMessage(`""`), wantErr: true},
		{name: "null is present, not omitted", granted: json.RawMessage(`null`), wantErr: true},
		{name: "a non-string is not a grant", granted: json.RawMessage(`["account"]`), wantErr: true},
		{name: "mcp alone is refused", granted: json.RawMessage(`"mcp"`), wantErr: true},
		{name: "substring is not a match", granted: json.RawMessage(`"accounts"`), wantErr: true},
		{name: "a tab is not a delimiter (RFC 6749 3.3)", granted: json.RawMessage(`"mcp\taccount"`), wantErr: true},
		{name: "repeated spaces still delimit", granted: json.RawMessage(`"mcp  account"`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as := newFakeAS(t)
			as.tokenScope = tt.granted
			io, _, _ := output.Test()

			token, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
				ServerURL:   as.server.URL,
				IO:          io,
				HTTPClient:  as.server.Client(),
				OpenBrowser: consentingBrowser(as),
			})
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("Login() error: %v", err)
				}
				return
			}
			if err == nil || token != nil {
				t.Fatalf("Login() = %v, %v; want a refusal and no token", token, err)
			}
			if !strings.Contains(err.Error(), "refusing to store") {
				t.Errorf("error %q does not say the key is refused", err)
			}
		})
	}
}

// A forged error redirect — right port, wrong state — must be reported as the
// interception it is, not believed: neither a denial nor a server scope
// refusal that never happened.
func TestBrowserLoginErrorRedirectChecksStateFirst(t *testing.T) {
	for _, oauthErr := range []string{"invalid_scope", "access_denied"} {
		t.Run(oauthErr, func(t *testing.T) {
			as := newFakeAS(t)
			io, _, _ := output.Test()

			openBrowser := func(authorizeURL string) error {
				u, _ := url.Parse(authorizeURL)
				q := u.Query()
				go func() {
					resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{
						"error": {oauthErr},
						"state": {"forged-state"},
					}.Encode())
					if err == nil {
						resp.Body.Close()
					}
				}()
				return nil
			}

			_, err := BrowserStrategy{}.Login(loginCtx(t), LoginOptions{
				ServerURL:   as.server.URL,
				IO:          io,
				HTTPClient:  as.server.Client(),
				OpenBrowser: openBrowser,
			})
			if err == nil || !strings.Contains(err.Error(), "state mismatch") {
				t.Fatalf("a forged %s redirect must be a state mismatch, got %v", oauthErr, err)
			}
		})
	}
}
