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
