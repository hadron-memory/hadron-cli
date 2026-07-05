package api

import (
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
		{"http 127.0.0.1 ok", "http://127.0.0.1:3000", "hdr_user_x", false, false},
		{"http ::1 ok", "http://[::1]:3000", "hdr_user_x", false, false},
		{"http allowed via env", "http://internal.corp", "hdr_user_x", true, false},
		{"non-http scheme with token rejected", "ftp://srv.example.com", "hdr_user_x", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.allow {
				t.Setenv(EnvAllowHTTP, "1")
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
