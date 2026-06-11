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
}

func newFakeAS(t *testing.T) *fakeAS {
	t.Helper()
	as := &fakeAS{clientID: "client-123", authCode: "code-456"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": as.server.URL + "/oauth/authorize",
			"token_endpoint":         as.server.URL + "/oauth/token",
			"registration_endpoint":  as.server.URL + "/oauth/register",
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
		if r.Form.Get("code") != as.authCode || r.Form.Get("client_id") != as.clientID {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "hdr_user_" + strings.Repeat("a", 64),
			"token_type":   "Bearer",
		})
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

	token, err := BrowserStrategy{}.Login(context.Background(), LoginOptions{
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
