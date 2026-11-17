package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func oauthFault(t *testing.T, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":"server_error","error_description":"secret stack","request_id":"o-5"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOAuthServerErrorSurfacesRequestID(t *testing.T) {
	srv := oauthFault(t, 500)
	_, err := exchangeCode(context.Background(), srv.Client(), srv.URL, "cid", "code", "ver", "http://127.0.0.1:1/cb", "")
	check(t, err)
	_, err = registerClient(context.Background(), srv.Client(), srv.URL, "http://127.0.0.1:1/cb")
	check(t, err)
}

func check(t *testing.T, err error) {
	t.Helper()
	if err == nil || exitcode.FromError(err) != exitcode.Error || !strings.Contains(err.Error(), "request id: o-5") || strings.Contains(err.Error(), "secret stack") {
		t.Errorf("want the request id and no raw text, got %v", err)
	}
}

func TestOAuthDiscoveryInternalFault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-Id", "o-5")
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"secret stack","code":"INTERNAL_SERVER_ERROR","requestId":"o-5"}`))
	}))
	t.Cleanup(srv.Close)
	_, err := Discover(context.Background(), srv.URL, srv.Client())
	check(t, err)
	_, err = DiscoverResource(context.Background(), srv.URL, srv.Client())
	check(t, err)
}

// An OAuth server_error with no request_id in the body is still an internal
// fault: the header supplies the id, and with none the sentence has no id. The
// description is never echoed.
func TestOAuthServerErrorWithoutBodyID(t *testing.T) {
	for _, hdr := range []string{"o-5", ""} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hdr != "" {
				w.Header().Set("X-Request-Id", hdr)
			}
			w.WriteHeader(500)
			_, _ = w.Write([]byte(`{"error":"server_error","error_description":"secret stack"}`))
		}))
		_, err := registerClient(context.Background(), srv.Client(), srv.URL, "http://127.0.0.1:1/cb")
		if err == nil || strings.Contains(err.Error(), "secret stack") || exitcode.FromError(err) != exitcode.Error {
			t.Errorf("hdr %q: got %v", hdr, err)
		}
		if hdr != "" && !strings.Contains(err.Error(), "request id: o-5") {
			t.Errorf("header id missing: %v", err)
		}
		if hdr == "" && !strings.Contains(err.Error(), "no request id was returned") {
			t.Errorf("want no-id sentence: %v", err)
		}
		srv.Close()
	}
}
