package api

import (
	"net/http"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func TestRequireSecureURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		token   string
		allow   bool // set HADRON_ALLOW_HTTP=1
		wantErr bool
	}{
		{"https ok", "https://srv.hadronmemory.com", "hdr_user_x", false, false},
		{"http with token rejected", "http://srv.hadronmemory.com", "hdr_user_x", false, true},
		{"http anonymous ok", "http://srv.hadronmemory.com", "", false, false},
		{"http localhost ok", "http://localhost:8080", "hdr_user_x", false, false},
		{"http *.localhost ok", "http://api.localhost:8080", "hdr_user_x", false, false},
		{"http 127.0.0.1 ok", "http://127.0.0.1:3000", "hdr_user_x", false, false},
		{"http ::1 ok", "http://[::1]:3000", "hdr_user_x", false, false},
		{"http allowed via env", "http://internal.corp", "hdr_user_x", true, false},
		{"non-http scheme with token rejected", "ftp://srv.example.com", "hdr_user_x", false, true},
		{"env override does not permit ftp", "ftp://srv.example.com", "hdr_user_x", true, true},
		// A loopback host must NOT launder an exotic scheme past the guard —
		// url.Hostname() still reports "localhost" for these (regression cover).
		{"ftp on loopback rejected", "ftp://localhost:3000", "hdr_user_x", false, true},
		{"custom scheme on loopback rejected", "vscode://localhost/x", "hdr_user_x", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Pin the override in EVERY subtest so an ambient HADRON_ALLOW_HTTP=1
			// in the dev/CI env can't silently disable the rejection cases.
			if tc.allow {
				t.Setenv(EnvAllowHTTP, "1")
			} else {
				t.Setenv(EnvAllowHTTP, "")
			}
			err := RequireSecureURL(tc.url, tc.token)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("RequireSecureURL(%q) = nil, want error", tc.url)
				}
				if got := exitcode.FromError(err); got != exitcode.Usage {
					t.Errorf("exit code = %d, want Usage", got)
				}
				return
			}
			if err != nil {
				t.Errorf("RequireSecureURL(%q) = %v, want nil", tc.url, err)
			}
		})
	}
}

// NewClient must refuse a credentialed cleartext-http server before returning
// a client (the token would otherwise ride in cleartext).
func TestNewClientRejectsCredentialedHTTP(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "") // isolate from an ambient override
	if _, err := NewClient("http://srv.hadronmemory.com", "hdr_user_x", nil); err == nil {
		t.Fatal("NewClient over http with a token should error")
	}
	// Anonymous http is fine, and loopback is carved out for local dev/tests.
	if _, err := NewClient("http://srv.hadronmemory.com", "", nil); err != nil {
		t.Errorf("anonymous http should be allowed: %v", err)
	}
	if _, err := NewClient("http://127.0.0.1:8080", "hdr_user_x", nil); err != nil {
		t.Errorf("loopback http with token should be allowed: %v", err)
	}
}

// The redirect guard refuses a hop to a non-secure target (a same-host
// https→http downgrade would otherwise carry the bearer in cleartext — #121)
// while allowing https / loopback and preserving the 10-hop cap.
func TestSecureRedirectGuard(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	c := withSecureRedirects(&http.Client{})

	mustReq := func(u string) *http.Request {
		r, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			t.Fatalf("build request %q: %v", u, err)
		}
		return r
	}

	if err := c.CheckRedirect(mustReq("http://srv.hadronmemory.com/graphql"), nil); err == nil {
		t.Error("a redirect to cleartext http must be refused")
	}
	if err := c.CheckRedirect(mustReq("https://srv.hadronmemory.com/graphql"), nil); err != nil {
		t.Errorf("an https redirect must be allowed: %v", err)
	}
	if err := c.CheckRedirect(mustReq("http://127.0.0.1:8080/graphql"), nil); err != nil {
		t.Errorf("a loopback redirect must be allowed: %v", err)
	}
	// The 10-hop cap is preserved now that we own CheckRedirect.
	via := make([]*http.Request, 10)
	if err := c.CheckRedirect(mustReq("https://srv.hadronmemory.com/graphql"), via); err == nil {
		t.Error("should stop after 10 redirects")
	}
}
