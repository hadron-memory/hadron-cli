package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/urlsec"
)

func TestValidateEndpoint(t *testing.T) {
	t.Setenv(urlsec.EnvAllowHTTP, "") // isolate from an ambient override

	cases := []struct {
		name    string
		raw     string
		server  string // trusted server URL the endpoint must be same-origin with
		wantErr bool
	}{
		{"https same-origin ok", "https://auth.example.com/authorize", "https://auth.example.com", false},
		{"https with path+query ok", "https://auth.example.com/oauth/authorize?x=1", "https://auth.example.com", false},
		{"host case-insensitive ok", "https://Auth.Example.com/authorize", "https://auth.example.com", false},
		{"http loopback same-origin ok", "http://127.0.0.1:8080/authorize", "http://127.0.0.1:8080", false},
		{"http localhost ok", "http://localhost/authorize", "http://localhost", false},
		{"http sub.localhost ok", "http://auth.localhost/authorize", "http://auth.localhost", false},
		// Default port written explicitly on one side only must still match.
		{"explicit :443 on endpoint ok", "https://auth.example.com:443/authorize", "https://auth.example.com", false},
		{"explicit :443 on server ok", "https://auth.example.com/authorize", "https://auth.example.com:443", false},
		// #115: same scheme, but a DIFFERENT origin — the exfiltration path.
		{"cross-origin host rejected", "https://evil.example.com/token", "https://auth.example.com", true},
		{"sibling subdomain rejected", "https://auth.example.com/token", "https://srv.example.com", true},
		{"port mismatch rejected", "http://localhost:9090/authorize", "http://localhost:8080", true},
		{"non-default port mismatch rejected", "https://auth.example.com:8443/token", "https://auth.example.com", true},
		// Scheme failures (independent of host).
		{"http public rejected", "http://auth.example.com/authorize", "https://auth.example.com", true},
		{"custom scheme on loopback rejected", "vscode://localhost/redir", "http://localhost", true},
		{"file scheme rejected", "file:///etc/passwd", "https://auth.example.com", true},
		{"leading dash rejected", "-oParam=x", "https://auth.example.com", true},
		{"empty host rejected", "https:///authorize", "https://auth.example.com", true},
		{"port-only host rejected", "https://:443/authorize", "https://auth.example.com", true},
		{"empty string rejected", "", "https://auth.example.com", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			su, perr := neturl.Parse(c.server)
			if perr != nil {
				t.Fatalf("invalid test server URL %q: %v", c.server, perr)
			}
			err := validateEndpoint("authorization_endpoint", c.raw, su)
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
	su, _ := neturl.Parse("http://auth.example.com")
	if err := validateEndpoint("token_endpoint", "http://auth.example.com/token", su); err != nil {
		t.Errorf("http public same-origin should be allowed under %s=1, got %v", urlsec.EnvAllowHTTP, err)
	}
	if err := validateEndpoint("token_endpoint", "vscode://auth.example.com/token", su); err == nil {
		t.Errorf("%s=1 must not admit a non-http scheme", urlsec.EnvAllowHTTP)
	}
	if err := validateEndpoint("token_endpoint", "http://evil.example.com/token", su); err == nil {
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
