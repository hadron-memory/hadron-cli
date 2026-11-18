package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Capture every page's real wire filter, not only the final request. Responses
// deliberately include an untagged role-only node; the provider owns matching.
func runSpecListRole684(t *testing.T, role string, flags []string, failure string) (string, []map[string]any, int32, error) {
	t.Helper()
	var mu sync.Mutex
	var filters []map[string]any
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var q struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Filter map[string]any `json:"filter"`
				Offset int            `json:"offset"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			t.Error(err)
			http.Error(w, "decode", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if q.OperationName == "FindNodes" {
			mu.Lock()
			filters = append(filters, q.Variables.Filter)
			mu.Unlock()
			if failure != "" {
				_, _ = w.Write([]byte(failure))
				return
			}
			nodes := ""
			if q.Variables.Offset == 0 {
				// Two rows require paging before applying the display window. A fake
				// role outside spec.* proves the CLI does not reimplement role matching.
				node := func(loc string) string {
					base := specNodeList(loc, `[]`)
					b, _ := json.Marshal(role)
					return strings.Replace(base, `"updatedAt":`, `"role":`+string(b)+`,"updatedAt":`, 1)
				}
				nodes = node("msg:010:01") + "," + node("msg:010:02")
			}
			_, _ = w.Write([]byte(translateFindNodes("FindNodes", `{"data":{"nodes":[`+nodes+`]}}`)))
			return
		}
		if q.OperationName == "Memories" {
			_, _ = w.Write([]byte(memListJSON))
			return
		}
		if body, ok := unstubbedDefault(q.OperationName); ok {
			_, _ = w.Write([]byte(body))
			return
		}
		t.Errorf("unexpected operation %s", q.OperationName)
		http.Error(w, "unexpected", 500)
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := []string{"spec", "ls", "--role", role, "--json", "--server", srv.URL}
	root.SetArgs(append(args, flags...))
	err := root.Execute()
	mu.Lock()
	defer mu.Unlock()
	return out.String(), append([]map[string]any(nil), filters...), requests.Load(), err
}

func TestSpecListRolePassThrough684(t *testing.T) {
	for _, tc := range []struct {
		name, role string
		flags      []string
		approval   any
		minted     any
		streams    int
	}{
		{"plain", "spec.rule", nil, nil, nil, 1},
		{"verbatim", "CUSTOM.Role", nil, nil, nil, 1},
		{"approved", "spec.rule", []string{"--approved"}, "APPROVED", nil, 1},
		{"unapproved", "spec.rule", []string{"--unapproved"}, nil, nil, 2},
		{"minted", "spec.rule", []string{"--minted"}, nil, true, 1},
		{"unminted", "spec.rule", []string{"--unminted"}, nil, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flags := append([]string{"-m", specMem, "--prefix", "msg:010", "--limit", "1", "--offset", "1"}, tc.flags...)
			out, filters, _, err := runSpecListRole684(t, tc.role, flags, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(filters) != 2*tc.streams {
				t.Fatalf("filters=%v, want page+empty probe per status stream", filters)
			}
			states := map[string]bool{}
			for _, f := range filters {
				if f["role"] != tc.role || f["locPrefix"] != "msg:010" {
					t.Fatalf("lost selectors: %v", f)
				}
				if ids, ok := f["memoryIds"].([]any); !ok || len(ids) != 1 || ids[0] != specMem {
					t.Fatalf("lost memory scope: %v", f)
				}
				if _, ok := f["tags"]; ok {
					t.Fatalf("explicit role must not use legacy tags: %v", f)
				}
				if tc.approval != nil && f["approvalState"] != tc.approval {
					t.Fatalf("approval lost: %v", f)
				}
				if tc.minted != nil && f["minted"] != tc.minted {
					t.Fatalf("mint lost: %v", f)
				}
				if state, ok := f["approvalState"].(string); ok {
					states[state] = true
				}
			}
			if tc.name == "unapproved" && (!states["NOT_APPROVED"] || !states["SUPERSEDED"] || len(states) != 2) {
				t.Fatalf("unapproved states=%v", states)
			}
			var rows []map[string]any
			if err := json.Unmarshal([]byte(out), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0]["citation"] != "msg:010:02" {
				t.Fatalf("provider results/window changed: %s", out)
			}
			for _, key := range []string{"memoryId", "memoryUrn", "governed", "replaced", "replacedBy", "tags"} {
				if _, ok := rows[0][key]; !ok {
					t.Fatalf("JSON lost %s: %s", key, out)
				}
			}
		})
	}
}

func TestSpecListRoleEmptyAndNoFallback684(t *testing.T) {
	_, _, requests, err := runSpecListRole684(t, "", nil, "")
	if exitcode.FromError(err) != exitcode.Usage || requests != 0 || !strings.Contains(err.Error(), "--role is blank") {
		t.Fatalf("empty role: err=%v requests=%d", err, requests)
	}
	skew := `{"errors":[{"message":"Field \"role\" is not defined by type \"NodeFilter\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	out, filters, _, err := runSpecListRole684(t, "spec.rule", nil, skew)
	if err == nil || len(filters) != 1 || out != "" {
		t.Fatalf("explicit role fallback/partial success: err=%v filters=%v out=%q", err, filters, out)
	}
	if filters[0]["role"] != "spec.rule" {
		t.Fatalf("missing role: %v", filters)
	}
}
