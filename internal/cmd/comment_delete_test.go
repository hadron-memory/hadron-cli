package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommentDeleteContract(t *testing.T) {
	for _, stub := range []bool{false, true} {
		t.Run(fmt.Sprint(stub), func(t *testing.T) {
			writeTeamBinding(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Op        string                     `json:"operationName"`
					Query     string                     `json:"query"`
					Variables map[string]json.RawMessage `json:"variables"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				calls++
				if req.Op != "DeleteComment" || string(req.Variables["expected"]) != "4" {
					t.Errorf("wrong delete request: %+v", req)
				}
				for _, old := range []string{"retractComment", "hideComment", "retracted", "hidden", "revisions"} {
					if strings.Contains(req.Query, old) {
						t.Errorf("obsolete selection %s", old)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":{"deleteComment":{"id":"c1","urn":"hrn:node:acme.com:kb:comments:c1","deleted":true,"deletedAt":"2026-10-11T00:00:00Z","deletedBy":{"id":"u1","name":"Manager"},"revSeq":5,"isTopLevel":%t,"body":null,"quote":null,"createdAt":"2026-10-05T00:00:00Z"}}}`, stub)
			}))
			defer srv.Close()
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"comment", "delete", "--yes", "hrn:node:acme.com:kb:comments:c1", "--expected-revision", "4", "--json", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || dto["deleted"] != true || dto["revSeq"] != float64(5) || dto["body"] != nil || dto["deletedBy"].(map[string]any)["name"] != "Manager" {
				t.Fatal(out.String())
			}
			for _, key := range []string{"revision", "retracted", "retractedAt", "hidden", "hiddenAt", "revisions"} {
				if _, ok := dto[key]; ok {
					t.Errorf("obsolete key %s", key)
				}
			}
		})
	}
}

func TestCommentRemovedVerbs(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	group, _, err := root.Find([]string{"comment"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range group.Commands() {
		for _, name := range append([]string{cmd.Name()}, cmd.Aliases...) {
			if name == "hide" || name == "retract" {
				t.Errorf("obsolete verb registered: %s", name)
			}
		}
	}
}

func TestCommentEditedMetadataAndCounter(t *testing.T) {
	for _, edited := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("edited=%t/json=%t", edited, asJSON), func(t *testing.T) {
				stamp := `"editedAt":null,"editedBy":null`
				if edited {
					stamp = `"editedAt":"2026-10-11T00:00:00Z","editedBy":{"id":"w1","name":"Jane"}`
				}
				response := `{"data":{"comment":` + strings.TrimSuffix(commentResult, "}") + `,` + stamp + `,"updatedAt":"2026-10-11T01:00:00Z"}}}`
				gql := fakeGraphQL(t, map[string]string{"GetComment": response})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"comment", "get", "hrn:node:acme.com:kb:comments:c1", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if asJSON {
					var dto map[string]any
					_ = json.Unmarshal([]byte(out.String()), &dto)
					if dto["revSeq"] != float64(4) {
						t.Fatal(out.String())
					}
					if _, ok := dto["revision"]; ok {
						t.Fatal("comment history revision printed")
					}
					if edited && (dto["editedAt"] != "2026-10-11T00:00:00Z" || dto["editedBy"].(map[string]any)["name"] != "Jane") {
						t.Fatal(out.String())
					}
					if !edited && (dto["editedAt"] != nil || dto["editedBy"] != nil) {
						t.Fatal(out.String())
					}
				} else {
					if !strings.Contains(out.String(), "write counter 4") {
						t.Fatal(out.String())
					}
					if strings.Contains(out.String(), "Edited:") != edited {
						t.Fatal(out.String())
					}
					if edited && !strings.Contains(out.String(), "Jane") {
						t.Fatal(out.String())
					}
				}
			})
		}
	}
}

func TestCommentDeletedRefusalNeverRetries(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"errors":[{"message":"This comment is deleted.","extensions":{"code":"COMMENT_DELETED"}}]}`)
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"comment", "edit", "hrn:node:acme.com:kb:comments:c1", "--body", "new", "--expected-revision", "4", "--server", srv.URL})
	if code := exitCodeFor(root.Execute()); code != 5 || calls != 1 || out.Len() != 0 {
		t.Fatalf("code=%d calls=%d out=%s", code, calls, out.String())
	}
}
