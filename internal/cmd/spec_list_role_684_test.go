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
	return runSpecListRole684State(t, role, flags, failure, "")
}

// runSpecListRole684State is runSpecListRole684 with the failure confined to the
// requests whose approvalState is failState (empty: every request fails).
func runSpecListRole684State(t *testing.T, role string, flags []string, failure, failState string) (string, []map[string]any, int32, error) {
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
			if failure != "" && (failState == "" || q.Variables.Filter["approvalState"] == failState) {
				_, _ = w.Write([]byte(failure))
				return
			}
			nodes := ""
			if q.Variables.Offset == 0 || q.Variables.Offset == 2 {
				// The fixture serves two pages (the first out of order, the second
				// repeating msg:010:02 and adding msg:010:03) so the CLI must page and
				// deduplicate before it applies the display window. A fake role
				// outside spec.* proves the CLI does not reimplement role matching.
				node := func(loc string) string {
					base := specNodeList(loc, `[]`)
					b, _ := json.Marshal(role)
					return strings.Replace(base, `"updatedAt":`, `"role":`+string(b)+`,"updatedAt":`, 1)
				}
				nodes = node("msg:010:02") + "," + node("msg:010:01")
				if q.Variables.Offset == 2 {
					nodes = node("msg:010:02") + "," + node("msg:010:03") // Repeated ID plus progress.
				}
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
		{"verbatim-spacing", " spec.rule ", nil, nil, nil, 1},
		{"unapproved-verbatim", "CUSTOM.Role", []string{"--unapproved"}, nil, nil, 2},
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
			// Each status stream is paged, so it asks at least once. How many pages
			// the pager takes is its own contract and is not pinned here (#2474 F8);
			// which streams ran is, through the per-state check below.
			if len(filters) < tc.streams {
				t.Fatalf("filters=%v, want at least one request per status stream (%d)", filters, tc.streams)
			}
			states := map[string]bool{}
			for _, f := range filters {
				if f["role"] != tc.role || f["locPrefix"] != "msg:010" {
					t.Fatalf("lost selectors: %v", f)
				}
				if ids, ok := f["memoryRefs"].([]any); !ok || len(ids) != 1 || ids[0] != specMem {
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
			if strings.HasPrefix(tc.name, "unapproved") && (!states["NOT_APPROVED"] || !states["SUPERSEDED"] || len(states) != 2) {
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
	for _, role := range []string{"", " \t"} {
		_, _, requests, err := runSpecListRole684(t, role, nil, "")
		if exitCodeFor(err) != exitcode.Usage || requests != 0 || !strings.Contains(err.Error(), "--role is blank") {
			t.Fatalf("empty role %q: err=%v requests=%d", role, err, requests)
		}
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

func TestSpecListRoleDeduplicates684(t *testing.T) {
	for _, flags := range [][]string{nil, {"--approved"}, {"--minted"}, {"--unminted"}, {"--unapproved"}} {
		out, _, _, err := runSpecListRole684(t, "CUSTOM.Role", flags, "")
		if err != nil {
			t.Fatal(err)
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 3 || rows[0]["citation"] != "msg:010:01" || rows[1]["citation"] != "msg:010:02" || rows[2]["citation"] != "msg:010:03" {
			t.Fatalf("flags=%v: expected three unique rows in canonical order: %s", flags, out)
		}
	}
}
