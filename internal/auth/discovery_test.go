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
		wantErr bool
	}{
		{"https ok", "https://auth.example.com/authorize", false},
		{"https with path+query ok", "https://auth.example.com/oauth/authorize?x=1", false},
		{"http loopback ok", "http://127.0.0.1:8080/authorize", false},
		{"http localhost ok", "http://localhost/authorize", false},
		{"http sub.localhost ok", "http://auth.localhost/authorize", false},
		{"http public rejected", "http://auth.example.com/authorize", true},
		{"custom scheme on loopback rejected", "vscode://localhost/redir", true},
		{"file scheme rejected", "file:///etc/passwd", true},
		{"leading dash rejected", "-oParam=x", true},
		{"empty host rejected", "https:///authorize", true},
		{"port-only host rejected", "https://:443/authorize", true},
		{"empty string rejected", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateEndpoint("authorization_endpoint", c.raw)
			if (err != nil) != c.wantErr {
				t.Fatalf("validateEndpoint(%q) err=%v, wantErr=%v", c.raw, err, c.wantErr)
			}
		})
	}
}

func TestValidateEndpointAllowHTTP(t *testing.T) {
	// A public http endpoint is admitted only under the explicit escape hatch —
	// and even then only for http, never a foreign scheme.
	t.Setenv(urlsec.EnvAllowHTTP, "1")
	if err := validateEndpoint("token_endpoint", "http://auth.example.com/token"); err != nil {
		t.Errorf("http public should be allowed under %s=1, got %v", urlsec.EnvAllowHTTP, err)
	}
	if err := validateEndpoint("token_endpoint", "vscode://auth.example.com/token"); err == nil {
		t.Errorf("%s=1 must not admit a non-http scheme", urlsec.EnvAllowHTTP)
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

func srvURL(r *http.Request) string { return "http://" + r.Host }

func TestOpenInBrowserRejectsDashPrefix(t *testing.T) {
	if err := OpenInBrowser("-oProxyCommand=evil"); err == nil {
		t.Error("OpenInBrowser must refuse a target beginning with '-'")
	}
}
