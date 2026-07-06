package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/urlsec"
)

func TestValidateEndpoint(t *testing.T) {
	t.Setenv(urlsec.EnvAllowHTTP, "") // isolate from an ambient override

	cases := []struct {
		name    string
		raw     string
		server  string // trusted server host the endpoint must match
		wantErr bool
	}{
		{"https same-origin ok", "https://auth.example.com/authorize", "auth.example.com", false},
		{"https with path+query ok", "https://auth.example.com/oauth/authorize?x=1", "auth.example.com", false},
		{"host case-insensitive ok", "https://Auth.Example.com/authorize", "auth.example.com", false},
		{"http loopback same-origin ok", "http://127.0.0.1:8080/authorize", "127.0.0.1:8080", false},
		{"http localhost ok", "http://localhost/authorize", "localhost", false},
		{"http sub.localhost ok", "http://auth.localhost/authorize", "auth.localhost", false},
		// #115: same scheme, but a DIFFERENT host — the exfiltration path.
		{"cross-origin host rejected", "https://evil.example.com/token", "auth.example.com", true},
		{"sibling subdomain rejected", "https://auth.example.com/token", "srv.example.com", true},
		{"port mismatch rejected", "http://localhost:9090/authorize", "localhost:8080", true},
		// Scheme failures (independent of host).
		{"http public rejected", "http://auth.example.com/authorize", "auth.example.com", true},
		{"custom scheme on loopback rejected", "vscode://localhost/redir", "localhost", true},
		{"file scheme rejected", "file:///etc/passwd", "auth.example.com", true},
		{"leading dash rejected", "-oParam=x", "auth.example.com", true},
		{"empty host rejected", "https:///authorize", "auth.example.com", true},
		{"port-only host rejected", "https://:443/authorize", "auth.example.com", true},
		{"empty string rejected", "", "auth.example.com", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateEndpoint("authorization_endpoint", c.raw, c.server)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateEndpoint(%q, server=%q) err=%v, wantErr=%v", c.raw, c.server, err, c.wantErr)
			}
		})
	}
}

func TestValidateEndpointAllowHTTP(t *testing.T) {
	// A public http endpoint is admitted only under the explicit escape hatch —
	// and even then only for http, same-origin, never a foreign scheme.
	t.Setenv(urlsec.EnvAllowHTTP, "1")
	if err := validateEndpoint("token_endpoint", "http://auth.example.com/token", "auth.example.com"); err != nil {
		t.Errorf("http public same-origin should be allowed under %s=1, got %v", urlsec.EnvAllowHTTP, err)
	}
	if err := validateEndpoint("token_endpoint", "vscode://auth.example.com/token", "auth.example.com"); err == nil {
		t.Errorf("%s=1 must not admit a non-http scheme", urlsec.EnvAllowHTTP)
	}
	if err := validateEndpoint("token_endpoint", "http://evil.example.com/token", "auth.example.com"); err == nil {
		t.Errorf("%s=1 must not admit a cross-origin host", urlsec.EnvAllowHTTP)
	}
}

// TestDiscoverRejectsMaliciousEndpoint proves the guard fires at the discovery
// chokepoint: a server that points authorization_endpoint at a custom-scheme
// handler is refused before any URL ever reaches the OS opener (#120).
func TestDiscoverRejectsMaliciousEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "vscode://evil/steal",
			"token_endpoint":         srvURL(r) + "/oauth/token",
			"registration_endpoint":  srvURL(r) + "/oauth/register",
		})
	}))
	defer srv.Close()

	_, err := Discover(context.Background(), srv.URL, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "authorization_endpoint") {
		t.Fatalf("expected an authorization_endpoint validation error, got %v", err)
	}
}

// TestDiscoverRejectsCrossOriginEndpoint proves #115: a discovery doc that keeps
// the same scheme but points token_endpoint at an ATTACKER host is refused, so
// the auth code + PKCE verifier can never be POSTed off-origin.
func TestDiscoverRejectsCrossOriginEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": srvURL(r) + "/oauth/authorize",
			// Loopback (so it clears the scheme gate) but a DIFFERENT port than the
			// server — i.e. a different origin the host check must reject.
			"token_endpoint":        "http://127.0.0.1:1/oauth/token",
			"registration_endpoint": srvURL(r) + "/oauth/register",
		})
	}))
	defer srv.Close()

	_, err := Discover(context.Background(), srv.URL, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "token_endpoint") || !strings.Contains(err.Error(), "cross-origin") {
		t.Fatalf("expected a cross-origin token_endpoint rejection, got %v", err)
	}
}

func srvURL(r *http.Request) string { return "http://" + r.Host }

func TestOpenInBrowserRejectsDashPrefix(t *testing.T) {
	if err := OpenInBrowser("-oProxyCommand=evil"); err == nil {
		t.Error("OpenInBrowser must refuse a target beginning with '-'")
	}
}
