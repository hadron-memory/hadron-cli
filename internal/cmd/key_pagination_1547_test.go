package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"net/http"
	"net/http/httptest"
	"os"
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
		{"instructed metadata", "appKeys", []string{"app", "key", "list", appRead1057, "--instruction", "task:" + taskRef1592}},
		{"instructed scopes", "appKeys", []string{"app", "key", "list", appRead1057, "--scopes", "--instruction", "task:" + taskRef1592}},
		{"instructed host uses", "appKeys", []string{"app", "key", "list", appRead1057, "--scopes", "--host-uses", "2", "--instruction", "task:" + taskRef1592}},
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
				if strings.HasPrefix(tc.name, "instructed") {
					want := map[string]any{"kind": "TASK", "ref": taskRef1592}
					if !reflect.DeepEqual(req.Variables["instruction"], want) || r.Header.Get("X-Hadron-Session") != "s-new" {
						t.Error("page lost instruction/session")
					}
				} else if _, ok := req.Variables["instruction"]; ok || r.Header.Get("X-Hadron-Session") != "" {
					t.Error("ordinary page acquired instruction/session")
				}
				if strings.Contains(tc.name, "host uses") && req.Variables["first"] != float64(2) {
					t.Error("host-use bound lost")
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
				if offset > 2 {
					t.Error("read beyond hasMore=false")
					fmt.Fprint(w, `{"errors":[{"message":"fixture exhausted"}]}`)
					return
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
			if strings.HasPrefix(tc.name, "instructed") {
				teamGitDir(t)
				writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
			}
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

func TestKeyPagination1547FailsWithoutPartialOutput(t *testing.T) {
	for _, args := range [][]string{
		{"auth", "token", "list"},
		{"app", "key", "list", appRead1057},
		{"app", "key", "list", appRead1057, "--scopes"},
		{"app", "key", "list", appRead1057, "--scopes", "--host-uses", "2"},
		{"app", "key", "list", appRead1057, "--instruction", "task:" + taskRef1592},
		{"app", "key", "list", appRead1057, "--scopes", "--instruction", "task:" + taskRef1592},
		{"app", "key", "list", appRead1057, "--scopes", "--host-uses", "2", "--instruction", "task:" + taskRef1592},
	} {
		for _, failure := range []string{"error", "missing page", "no progress", "insertion", "revocation", "equal-count duplicate"} {
			t.Run(strings.Join(args, "/")+"/"+failure, func(t *testing.T) {
				var mu sync.Mutex
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					calls++
					n := calls
					mu.Unlock()
					w.Header().Set("Content-Type", "application/json")
					field := "appKeysPage"
					if args[0] == "auth" {
						field = "myUserApiKeysPage"
					}
					if n > 3 {
						t.Error("pagination did not stop")
						fmt.Fprint(w, `{"errors":[{"message":"loop"}]}`)
						return
					}
					if n == 1 {
						total := 0
						if failure == "insertion" || failure == "revocation" || failure == "equal-count duplicate" {
							total = 3
						}
						fmt.Fprintf(w, `{"data":{"%s":{"items":[{"id":"first","createdAt":"2026-01-01T00:00:00Z"}],"total":%d,"hasMore":true}}}`, field, total)
						return
					}
					switch failure {
					case "error":
						fmt.Fprint(w, `{"errors":[{"message":"later page refused","extensions":{"code":"FORBIDDEN"}}]}`)
					case "insertion", "revocation", "equal-count duplicate":
						// Starting [first, middle, last], inserting a newer key shifts
						// first onto page two; revoking first shifts middle before offset.
						total, id := 3, "first"
						if failure == "insertion" {
							total = 4
						}
						if failure == "revocation" {
							total, id = 2, "last"
						}
						fmt.Fprintf(w, `{"data":{"%s":{"items":[{"id":%q,"createdAt":"2026-01-01T00:00:00Z"}],"total":%d,"hasMore":false}}}`, field, id, total)
					case "missing page":
						fmt.Fprintf(w, `{"data":{"%s":null}}`, field)
					default:
						fmt.Fprintf(w, `{"data":{"%s":{"items":[],"total":0,"hasMore":true}}}`, field)
					}
				}))
				defer srv.Close()
				instructed := false
				for _, arg := range args {
					if arg == "--instruction" {
						instructed = true
					}
				}
				if instructed {
					teamGitDir(t)
					writeTeamBindingIn(t, os.Getenv(team.GitDirEnv), srv.URL)
				}
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(append([]string{}, args...), "--json", "--server", srv.URL))
				err := root.Execute()
				if err == nil || out.Len() != 0 {
					t.Fatalf("error=%v partial output=%s", err, out.String())
				}
				wantCode := 1
				if failure == "error" {
					wantCode = 8
				}
				if code := renderFailure(f, append(args, "--json"), err); code != wantCode {
					t.Errorf("exit=%d want%d", code, wantCode)
				}
				var envelope struct {
					Error struct {
						Code       int    `json:"code"`
						ServerCode string `json:"serverCode"`
					} `json:"error"`
				}
				if e := json.Unmarshal([]byte(out.String()), &envelope); e != nil {
					t.Fatal(e)
				}
				if envelope.Error.Code != wantCode {
					t.Errorf("error envelope=%s", out.String())
				}
				if instructed && failure == "error" && envelope.Error.ServerCode != "FORBIDDEN" {
					t.Errorf("lost serverCode: %s", out.String())
				}
				mu.Lock()
				defer mu.Unlock()
				if calls != 2 {
					t.Errorf("requests=%d want2", calls)
				}
			})
		}
	}
}

func TestKeyPagination1547HelpWarnsAboutConcurrentChanges(t *testing.T) {
	for _, args := range [][]string{{"auth", "token", "list", "--help"}, {"app", "key", "list", "--help"}} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"not a snapshot", "equal-count", "partial inventory"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%v help lacks %q", args, want)
			}
		}
	}
}
