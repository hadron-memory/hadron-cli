package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// A redirect to ANOTHER host must not replay a worker session in either the
// header or the GraphQL POST body. A same-host redirect keeps both.
// Driven through NewClient, for both of its redirect policies (with a token
// and without).
func TestSessionHeaderDoesNotFollowACrossHostRedirect(t *testing.T) {
	for _, token := range []string{"hdr_user_x", ""} {
		for _, tc := range []struct {
			path        string
			crossHost   bool
			withSession bool
		}{{"away", true, true}, {"stay", false, true}, {"body-only-away", true, false}, {"body-only-stay", false, false}} {
			var got, gotBodies []string
			record := func(w http.ResponseWriter, r *http.Request) {
				got = append(got, r.Header.Get(SessionHeader))
				body, _ := io.ReadAll(r.Body)
				gotBodies = append(gotBodies, string(body))
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{}}`))
			}
			other := httptest.NewServer(http.HandlerFunc(record))
			var origin *httptest.Server
			origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/landed" {
					record(w, r)
					return
				}
				target := origin.URL + "/landed"
				if tc.crossHost {
					target = other.URL + "/landed"
				}
				http.Redirect(w, r, target, http.StatusTemporaryRedirect)
			}))
			c, err := NewClient(origin.URL, token, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if tc.withSession {
				ctx = WithSession(ctx, "s-1")
			}
			var data map[string]any
			err = c.MakeRequest(ctx, &graphql.Request{Query: "mutation X($sessionRef: ID!) { x(sessionRef: $sessionRef) }", OpName: "X", Variables: map[string]any{"sessionRef": "s-1"}}, &graphql.Response{Data: &data})
			if tc.crossHost {
				if err == nil || len(got) != 0 || len(gotBodies) != 0 {
					t.Errorf("token=%q cross-host redirect must refuse before replaying the body: err=%v headers=%q bodies=%q", token, err, got, gotBodies)
				}
			} else if err != nil || len(got) != 1 || (tc.withSession && got[0] != "s-1") || (!tc.withSession && got[0] != "") || len(gotBodies) != 1 || !strings.Contains(gotBodies[0], `"sessionRef":"s-1"`) {
				t.Errorf("token=%q same-host redirect: err=%v session=%q body=%q", token, err, got, gotBodies)
			}
			other.Close()
			origin.Close()
		}
	}
}

// The worker-session header rides ONLY on a call whose context asked for it
// (#1353): it changes server behaviour — a heartbeat, and the bound worker's
// own read state — so a bound worktree must not stamp it on every request.
func TestSessionHeaderOnlyWhenTheCallCarriesOne(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get(SessionHeader))
	}))
	defer srv.Close()
	d := &bearerDoer{token: "hdr_user_x", inner: srv.Client()}

	for _, ctx := range []context.Context{
		context.Background(),
		WithSession(context.Background(), "s-1"),
		WithSession(context.Background(), ""), // empty is no session
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := d.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	want := []string{"", "s-1", ""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d: %s = %q, want %q", i, SessionHeader, got[i], want[i])
		}
	}
}

// PR #732: a redirect to a scheme a credential may not ride on is refused
// even on the SAME host. Both policies are exercised through their
// CheckRedirect, as client_test does for the scheme guard.
func TestSessionHeaderIsStrippedOnAnInsecureRedirect(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	origin, _ := http.NewRequest(http.MethodPost, "https://srv.example/graphql", nil)
	for name, c := range map[string]*http.Client{
		"with token":    withSecureRedirects(&http.Client{}),
		"without token": withSessionRedirects(&http.Client{}),
	} {
		for _, tc := range []struct {
			url  string
			keep bool
		}{
			{"http://srv.example/graphql", false},    // same host, cleartext
			{"https://srv.example/graphql", true},    // same host, secure
			{"https://other.example/graphql", false}, // another host
		} {
			req, _ := http.NewRequest(http.MethodPost, tc.url, nil)
			req.Header.Set(SessionHeader, "s-1")
			err := c.CheckRedirect(req, []*http.Request{origin})
			if (err == nil) != tc.keep {
				t.Errorf("%s → %s: allowed = %v, want %v", name, tc.url, err == nil, tc.keep)
			}
		}
	}
}

func TestBodyOnlySessionRefCannotFollowAnInsecureRedirect(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	origin, _ := http.NewRequest(http.MethodPost, "https://srv.example/graphql", strings.NewReader(`{"variables":{"sessionRef":"s-1"}}`))
	for name, c := range map[string]*http.Client{
		"with token":    withSecureRedirects(&http.Client{}),
		"without token": withSessionRedirects(&http.Client{}),
	} {
		for _, tc := range []struct {
			url  string
			keep bool
		}{
			{"http://srv.example/graphql", false},
			{"https://srv.example/graphql", true},
			{"https://other.example/graphql", false},
		} {
			req, _ := http.NewRequest(http.MethodPost, tc.url, nil)
			// No WithSession and no session header: the POST body alone contains
			// sessionRef. CheckRedirect must protect it without parsing the body.
			if err := c.CheckRedirect(req, []*http.Request{origin}); (err == nil) != tc.keep {
				t.Errorf("%s body-only POST → %s: allowed = %v, want %v", name, tc.url, err == nil, tc.keep)
			}
		}
	}
}

// The INITIAL request never passes through the redirect policy. A session in
// its POST body must not ride cleartext even when no bearer token is present.
func TestSessionRequestIsNotSentOverCleartextHTTP(t *testing.T) {
	t.Setenv(EnvAllowHTTP, "")
	var got []string
	record := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		got = append(got, r.Header.Get(SessionHeader))
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})
	d := &bearerDoer{inner: &http.Client{Transport: record}}
	ctx := WithSession(context.Background(), "s-1")
	for _, u := range []string{"http://srv.example/graphql", "https://srv.example/graphql", "http://127.0.0.1:8080/graphql"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
		resp, err := d.Do(req)
		if u == "http://srv.example/graphql" {
			if err == nil {
				t.Fatal("cleartext session-bearing request must be refused")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	want := []string{"s-1", "s-1"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d: session = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBodyOnlySessionRefIsNotSentOverInitialHTTP(t *testing.T) {
	for _, tc := range []struct {
		name, allowHTTP string
		variables       map[string]any
		refuse          bool
	}{
		{"body-only session", "", map[string]any{"sessionRef": "s-1"}, true},
		{"ordinary anonymous post", "", map[string]any{"other": "value"}, false},
		{"explicit trusted HTTP override", "1", map[string]any{"sessionRef": "s-1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvAllowHTTP, tc.allowHTTP)
			requests := 0
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{}}`)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r}, nil
			})
			c, err := NewClient("http://srv.example", "", &http.Client{Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]any
			err = c.MakeRequest(context.Background(), &graphql.Request{Query: "mutation X { x }", OpName: "X", Variables: tc.variables}, &graphql.Response{Data: &data})
			if tc.refuse {
				if !errors.Is(err, ErrRedirectPolicy) || requests != 0 {
					t.Errorf("body-only session must be refused before transport: err=%v requests=%d", err, requests)
				}
			} else if err != nil || requests != 1 {
				t.Errorf("ordinary/trusted POST must pass: err=%v requests=%d", err, requests)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
