package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// Instructed previews write a claim on the worker-session path even though
// their GraphQL document is a query. A redirect must not replay that claim.
func TestCastInstructedPreviewRedirect1755(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			writeTeamBinding(t)
			responses := responses1594()
			backend, _ := attnServerHook(t, responses, nil)
			target, err := url.Parse(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			var first, second atomic.Int32
			var front *httptest.Server
			front = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/landed" {
					second.Add(1)
					_, _ = w.Write([]byte(responses["CastWorkerPreviewInstructed"]))
					return
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					http.Error(w, "read", http.StatusBadRequest)
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
				var q struct {
					OperationName string                     `json:"operationName"`
					Variables     map[string]json.RawMessage `json:"variables"`
				}
				if json.Unmarshal(raw, &q) == nil && q.OperationName == "CastWorkerPreviewInstructed" {
					first.Add(1)
					if r.Header.Get("X-Hadron-Session") == "" {
						t.Error("preview must exercise the claim-writing worker session")
					}
					if !bytes.Contains(q.Variables["instruction"], []byte(taskRef1592)) {
						t.Error("missing instruction")
					}
					http.Redirect(w, r, front.URL+"/landed", status)
					return
				}
				proxy.ServeHTTP(w, r)
			}))
			t.Cleanup(front.Close)
			setTeamBindingServer(t, front.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "worker", "cast", "--app", app1594, "--name", "Uma", "--agent", agent1594, "--no-team", "--dry-run", "--instruction", instrTask1592, "--server", front.URL})
			err = root.Execute()
			if first.Load() != 1 || second.Load() != 0 {
				t.Errorf("want one preview and zero second-hop sends, got %d/%d", first.Load(), second.Load())
			}
			if err == nil || out.String() != "" {
				t.Errorf("want failure with no stdout, got err=%v out=%q", err, out.String())
			}
			if err != nil && !strings.Contains(err.Error(), "redirect refused by policy") {
				t.Errorf("want redirect policy refusal, got %v", err)
			}
		})
	}
}
