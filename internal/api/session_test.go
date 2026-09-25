package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
