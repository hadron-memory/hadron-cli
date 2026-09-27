package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #576: the raw path (`hadron api`, RawGraphQL) and the curated path (MapError)
// must classify a non-200-with-typed-envelope the same way — by the
// extensions.code, not the HTTP status. Before this, raw returned on the 4xx
// (and 5xx-non-transport) branch before reading the envelope, so a 403/404/503
// carrying a code got the generic status mapping on the raw side only.
func TestRawGraphQLPrefersEnvelopeCodeLikeMapError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"403 + WORKER_TAKEN → Conflict", 403, `{"errors":[{"message":"held","extensions":{"code":"WORKER_TAKEN"}}]}`, exitcode.Conflict},
		{"404 + BAD_USER_INPUT → Usage", 404, `{"errors":[{"message":"bad","extensions":{"code":"BAD_USER_INPUT"}}]}`, exitcode.Usage},
		{"503 + NOT_FOUND → NotFound", 503, `{"errors":[{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]}`, exitcode.NotFound},
		// no envelope code: the status still decides, on both paths.
		{"403 plain → Error", 403, `not json`, exitcode.Error},
		{"401 plain → AuthRequired", 401, ``, exitcode.AuthRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			_, err := RawGraphQL(context.Background(), srv.URL, "", "query Q { __typename }", nil, srv.Client())
			if err == nil {
				t.Fatal("a non-200 must be an error")
			}
			if got := exitcode.FromError(err); got != tc.want {
				t.Errorf("raw path exit = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRawGraphQLSkipsNullErrorsWhenClassifyingHTTP200(t *testing.T) {
	for _, tc := range []struct {
		name, errors, message string
		want                  int
	}{
		{"later typed refusal", `[null,{"message":"no such node","extensions":{"code":"NOT_FOUND"}}]`, "no such node", exitcode.NotFound},
		{"only null", `[null]`, "malformed GraphQL response", exitcode.Error},
		{"null and empty object", `[null,{}]`, "malformed GraphQL response", exitcode.Error},
		{"code without message", `[null,{"extensions":{"code":"NOT_FOUND"}}]`, "GraphQL error: NOT_FOUND", exitcode.NotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"errors":` + tc.errors + `}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			result, err := RawGraphQL(context.Background(), srv.URL, "", "query Q { __typename }", nil, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if string(result.Body) != body {
				t.Fatalf("raw body changed: %q, want %q", result.Body, body)
			}
			err = result.Err()
			if got := exitcode.FromError(err); got != tc.want || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("classified error = %v (exit %d), want %q (exit %d)", err, got, tc.message, tc.want)
			}
		})
	}
}

func TestRawGraphQLProtectsBodyOnlySessionRefOnInitialHTTP(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{}}`)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r}, nil
	})}
	_, err := RawGraphQL(context.Background(), "http://remote.example", "", "mutation X { x }", map[string]any{"sessionRef": "s-1"}, client)
	if !errors.Is(err, ErrRedirectPolicy) || requests != 0 {
		t.Errorf("raw body-only sessionRef must refuse before transport: err=%v requests=%d", err, requests)
	}
	_, err = RawGraphQL(context.Background(), "http://remote.example", "", "query X { x }", map[string]any{"appRef": "hrn:app:example:team"}, client)
	if err != nil || requests != 1 {
		t.Errorf("ordinary anonymous raw POST must still pass: err=%v requests=%d", err, requests)
	}
	_, err = RawGraphQL(context.Background(), "http://remote.example", "", "mutation X($fields: JSON!) { x(fields: $fields) }", map[string]any{"fields": map[string]any{"sessionRef": "external-id"}}, client)
	if err != nil || requests != 2 {
		t.Errorf("nested sessionRef JSON is ordinary data: err=%v requests=%d", err, requests)
	}
	_, err = RawGraphQL(context.Background(), "http://remote.example", "", `mutation X { x(sessionRef: "s-1") }`, nil, client)
	if !errors.Is(err, ErrRedirectPolicy) || requests != 2 {
		t.Errorf("inline sessionRef must refuse before transport: err=%v requests=%d", err, requests)
	}
	_, err = RawGraphQL(context.Background(), "http://remote.example", "", "mutation X($s: ID!) { x(sessionRef: $s) }", map[string]any{"s": "s-1"}, client)
	if !errors.Is(err, ErrRedirectPolicy) || requests != 2 {
		t.Errorf("renamed session variable must refuse before transport: err=%v requests=%d", err, requests)
	}
}

func TestRawGraphQLProtectsBodyOnlySessionRefAcrossHosts(t *testing.T) {
	landed := 0
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer other.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/graphql", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	_, err := RawGraphQL(context.Background(), origin.URL, "", "mutation X { x }", map[string]any{"sessionRef": "s-1"}, origin.Client())
	if !errors.Is(err, ErrRedirectPolicy) || landed != 0 {
		t.Errorf("raw body-only sessionRef must not cross hosts: err=%v landed=%d", err, landed)
	}
	_, err = RawGraphQL(context.Background(), origin.URL, "", "query X { x }", map[string]any{"appRef": "hrn:app:example:team"}, origin.Client())
	if err != nil || landed != 1 {
		t.Errorf("session-free raw POST must follow secure redirect: err=%v landed=%d", err, landed)
	}
	_, err = RawGraphQL(context.Background(), origin.URL, "", "mutation X($fields: JSON!) { x(fields: $fields) }", map[string]any{"fields": map[string]any{"sessionRef": "external-id"}}, origin.Client())
	if err != nil || landed != 2 {
		t.Errorf("nested sessionRef JSON must follow secure redirect: err=%v landed=%d", err, landed)
	}
	_, err = RawGraphQL(context.Background(), origin.URL, "", `mutation X { x(sessionRef: "s-1") }`, nil, origin.Client())
	if !errors.Is(err, ErrRedirectPolicy) || landed != 2 {
		t.Errorf("inline sessionRef must not cross hosts: err=%v landed=%d", err, landed)
	}
	_, err = RawGraphQL(context.Background(), origin.URL, "", "mutation X($s: ID!) { x(sessionRef: $s) }", map[string]any{"s": "s-1"}, origin.Client())
	if !errors.Is(err, ErrRedirectPolicy) || landed != 2 {
		t.Errorf("renamed session variable must not cross hosts: err=%v landed=%d", err, landed)
	}
}
