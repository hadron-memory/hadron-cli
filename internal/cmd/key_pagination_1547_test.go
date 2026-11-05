package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The short first page and deliberately wrong total distinguish hasMore-driven
// traversal from both short-page and total-driven truncation. The fake also
// serves the old field so the pre-migration failure is missing rows.
func TestKeyPagination1547(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		args        []string
	}{
		{"tokens", "myUserApiKeys", []string{"auth", "token", "list"}},
		{"hidden tokens", "myUserApiKeys", []string{"auth", "token", "list", "--include-hidden"}},
		{"metadata", "appKeys", []string{"app", "key", "list", appRead1057}},
		{"scopes", "appKeys", []string{"app", "key", "list", appRead1057, "--scopes"}},
		{"host uses", "appKeys", []string{"app", "key", "list", appRead1057, "--scopes", "--host-uses", "2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			offsets := []int{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Query     string
					Variables map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				offset := 0
				if v, ok := req.Variables["offset"].(float64); ok {
					offset = int(v)
				}
				mu.Lock()
				offsets = append(offsets, offset)
				mu.Unlock()
				paged := strings.Contains(req.Query, tc.field+"Page(")
				if paged && req.Variables["limit"] != float64(20) {
					t.Errorf("limit=%v", req.Variables["limit"])
				}
				if strings.Contains(tc.name, "hidden") && req.Variables["includeHidden"] != true {
					t.Error("includeHidden lost")
				}
				row := fmt.Sprintf(`{"id":"key-%d","keyPreview":"preview","createdAt":"2026-01-01T00:00:00Z","issuedVia":"cli","scopes":[],"hostUses":[]}`, offset)
				w.Header().Set("Content-Type", "application/json")
				if paged {
					fmt.Fprintf(w, `{"data":{"%sPage":{"items":[%s],"total":0,"hasMore":%t}}}`, tc.field, row, offset < 2)
				} else {
					fmt.Fprintf(w, `{"data":{"%s":[%s]}}`, tc.field, row)
				}
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{}, tc.args...), "--json", "--server", srv.URL))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var rows []map[string]any
			if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 {
				t.Fatalf("truncated inventory: rows=%d want3; %s", len(rows), out.String())
			}
			for i, row := range rows {
				if row["id"] != fmt.Sprintf("key-%d", i) {
					t.Errorf("row%d=%v", i, row)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(offsets, []int{0, 1, 2}) {
				t.Errorf("offsets=%v", offsets)
			}
		})
	}
}
