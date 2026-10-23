package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const reembedMemID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// opLog serves canned responses per operation and records every call in order,
// so a test can assert the EXACT number of mutations the command sent.
func opLog(t *testing.T, responses map[string]string) (*httptest.Server, func() []string, func(op string) []json.RawMessage) {
	t.Helper()
	var mu sync.Mutex
	var ops []string
	vars := map[string][]json.RawMessage{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		ops = append(ops, body.OperationName)
		vars[body.OperationName] = append(vars[body.OperationName], body.Variables)
		mu.Unlock()
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv,
		func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), ops...) },
		func(op string) []json.RawMessage { mu.Lock(); defer mu.Unlock(); return vars[op] }
}

func countOp(ops []string, op string) int {
	n := 0
	for _, o := range ops {
		if o == op {
			n++
		}
	}
	return n
}

func TestMemoryReembedFailed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		count    string
		wantText string
	}{
		{"positive", "5", "re-stamped 5 node(s)"},
		{"zero is legitimate", "0", "re-stamped 0 node(s)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ops, vars := opLog(t, map[string]string{
				"RetryFailedEmbeddings": `{"data":{"retryFailedEmbeddings":` + tc.count + `}}`,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "reembed-failed", reembedMemID, "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if n := countOp(ops(), "RetryFailedEmbeddings"); n != 1 {
				t.Fatalf("exactly one mutation expected, got %d", n)
			}
			var v map[string]any
			_ = json.Unmarshal(vars("RetryFailedEmbeddings")[0], &v)
			if v["memoryRef"] != reembedMemID {
				t.Errorf("memoryRef = %v, want %s", v["memoryRef"], reembedMemID)
			}
			if !strings.Contains(out.String(), tc.wantText) || !strings.Contains(out.String(), "not a count of indexed") {
				t.Errorf("text should say %q and never claim indexed, got %q", tc.wantText, out.String())
			}
		})
	}
}

// A URN ref is resolved with one read, then exactly one mutation, and the JSON
// DTO is the stable shape with `restamped` (never "indexed").
func TestMemoryReembedFailedJSONResolvesURN(t *testing.T) {
	srv, ops, vars := opLog(t, map[string]string{
		"GetMemory": `{"data":{"memory":{"id":"` + reembedMemID + `","urn":"hrn:mem:acme.com:kb","name":"KB",
			"shortDescription":null,"description":null,"class":"shared","visibility":"private",
			"organizationId":"org1","isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-08-05T00:00:00Z"}}}`,
		"RetryFailedEmbeddings": `{"data":{"retryFailedEmbeddings":3}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "reembed-failed", "hrn:mem:acme.com:kb", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := ops(); len(got) != 2 || got[0] != "GetMemory" || got[1] != "RetryFailedEmbeddings" {
		t.Fatalf("want one read then one mutation, got %v", got)
	}
	var v map[string]any
	_ = json.Unmarshal(vars("RetryFailedEmbeddings")[0], &v)
	if v["memoryRef"] != reembedMemID {
		t.Errorf("the mutation should carry the resolved id, got %v", v["memoryRef"])
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("stdout is not JSON: %v: %q", err, out.String())
	}
	if dto["restamped"] != float64(3) || dto["memoryId"] != reembedMemID || dto["memory"] != "hrn:mem:acme.com:kb" || len(dto) != 3 {
		t.Errorf("unexpected DTO %v", dto)
	}
}

// A refused call (the server's owner/ADMIN gate) maps through the error
// mapper, prints no success, and is NOT sent a second time.
func TestMemoryReembedFailedRefusedAndErrorsAreNotRetried(t *testing.T) {
	for _, tc := range []struct {
		name, resp string
	}{
		{"refused", `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}],"data":null}`},
		{"server error", `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, ops, _ := opLog(t, map[string]string{"RetryFailedEmbeddings": tc.resp})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"memory", "reembed-failed", reembedMemID, "--server", srv.URL})
			if err := root.Execute(); err == nil {
				t.Fatal("expected an error")
			}
			if n := countOp(ops(), "RetryFailedEmbeddings"); n != 1 {
				t.Errorf("a failed mutation must not be retried; sent %d times", n)
			}
			if strings.Contains(out.String(), "re-stamped") {
				t.Errorf("no success line on failure, got %q", out.String())
			}
		})
	}
}

// An ambiguous transport failure (the connection drops after the request) is
// reported, never retried: the outcome is unknown.
func TestMemoryReembedFailedTransportFailureSentOnce(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"capabilities":[]}`))
			return
		}
		mu.Lock()
		calls++
		mu.Unlock()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"memory", "reembed-failed", reembedMemID, "--server", srv.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("expected a transport error")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("an ambiguous transport failure must not be retried; request sent %d times", calls)
	}
}
