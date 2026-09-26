package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

// A redirect to ANOTHER host must not carry the worker session (PR #732,
// @copilot): net/http forwards custom headers across hosts, and the redirect
// request never passes back through bearerDoer. A same-host redirect keeps it.
// Driven through NewClient, for both of its redirect policies (with a token
// and without).
func TestSessionHeaderDoesNotFollowACrossHostRedirect(t *testing.T) {
	for _, token := range []string{"hdr_user_x", ""} {
		for _, tc := range []struct {
			path      string
			crossHost bool
		}{{"away", true}, {"stay", false}} {
			var got []string
			record := func(w http.ResponseWriter, r *http.Request) {
				got = append(got, r.Header.Get(SessionHeader))
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
			ctx := WithSession(context.Background(), "s-1")
			var data map[string]any
			if err := c.MakeRequest(ctx, &graphql.Request{Query: "{ x }", OpName: "X"}, &graphql.Response{Data: &data}); err != nil {
				t.Fatalf("token=%q %s: %v", token, tc.path, err)
			}
			want := "s-1"
			if tc.crossHost {
				want = ""
			}
			if len(got) != 1 || got[0] != want {
				t.Errorf("token=%q %s redirect: the landing request carried %q, want %q", token, tc.path, got, want)
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
