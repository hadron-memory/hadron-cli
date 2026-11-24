package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// ownRequestServer is a live test server that counts only the requests the command under test sent
// (cli#3112). Loopback ports are recycled: under port churn another process on the host can dial the
// port this server was just given, and counting EVERY request then reports a second request the CLI
// never sent (question_cmd_test.go saw requests=2 on a leg that expects exactly one). The command's own
// requests are the ones carrying its bearer token, which the test sets per leg (HADRON_TOKEN), so a
// stranger's request is answered 404 without reaching the handler and is counted apart in strangers.
func ownRequestServer(t *testing.T, token string, h func(w http.ResponseWriter, r *http.Request)) (srv *httptest.Server, own, strangers *int32) {
	t.Helper()
	own, strangers = new(int32), new(int32)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			atomic.AddInt32(strangers, 1)
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(own, 1)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, own, strangers
}

// cli#3112: a request that does not carry the command's token is not the CLI's. It must reach neither
// the handler nor the own-request count.
func TestOwnRequestServerCountsOnlyTheCommandsOwnRequests3112(t *testing.T) {
	const token = "hdr_user_leg-1"
	handled := int32(0)
	srv, own, strangers := ownRequestServer(t, token, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&handled, 1)
		w.WriteHeader(http.StatusOK)
	})
	do := func(auth string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL, nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := do(""); code != http.StatusNotFound {
		t.Errorf("a request with no token is a stranger's: want 404, got %d", code)
	}
	if code := do("Bearer someone-elses"); code != http.StatusNotFound {
		t.Errorf("a request with another token is a stranger's: want 404, got %d", code)
	}
	if got := atomic.LoadInt32(own); got != 0 {
		t.Errorf("strangers must not count as the command's own requests: own=%d", got)
	}
	if got := atomic.LoadInt32(strangers); got != 2 {
		t.Errorf("strangers are counted apart: want 2, got %d", got)
	}
	if got := atomic.LoadInt32(&handled); got != 0 {
		t.Errorf("a stranger's request must not reach the handler: handled=%d", got)
	}
	if code := do("Bearer " + token); code != http.StatusOK || atomic.LoadInt32(own) != 1 || atomic.LoadInt32(&handled) != 1 {
		t.Errorf("the command's own request counts once and is handled: code=%d own=%d handled=%d", code, atomic.LoadInt32(own), atomic.LoadInt32(&handled))
	}
}
