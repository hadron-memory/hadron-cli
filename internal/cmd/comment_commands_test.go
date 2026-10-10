package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api"
)

const commentResult = `{"id":"c1","urn":"hrn:node:acme.com:kb:comments:c1","portalUrl":null,"target":{"id":"n1","urn":"hrn:node:acme.com:kb:target","name":"Target","nodeType":"info","revSeq":9},"revSeq":4,"anchorRevision":2,"anchorApprovalHash":"hash","anchorIsCurrent":false,"isTopLevel":true,"author":{"kind":"WORKER","worker":{"id":"w1","name":"Jane","urn":"hrn:worker:acme.com:team:jane"}},"provenanceUser":{"id":"u1","handle":"holger"},"body":"body","quote":null,"state":"RESOLVED","createdAt":"2026-10-05T00:00:00Z","viewerIsAuthor":true}`

func TestCommentGeneratedMutationVariablesAndAttribution(t *testing.T) {
	for _, tc := range []struct {
		name, op string
		args     []string
		check    func(*testing.T, map[string]json.RawMessage)
	}{
		{"create defaults", "CreateComment", []string{"create", "--body", "new"}, func(t *testing.T, v map[string]json.RawMessage) {
			if _, ok := v["quote"]; ok {
				t.Fatal("omitted quote sent")
			}
			if _, ok := v["anchor"]; ok {
				t.Fatal("omitted anchor sent")
			}
		}},
		{"create empty quote", "CreateComment", []string{"create", "--body", "new", "--quote", ""}, func(t *testing.T, v map[string]json.RawMessage) {
			if _, ok := v["quote"]; ok {
				t.Fatal("empty create quote must be omitted")
			}
		}},
		{"create explicit", "CreateComment", []string{"create", "--body", "new", "--quote", "text", "--anchor-revision", "2"}, func(t *testing.T, v map[string]json.RawMessage) {
			if string(v["quote"]) != `"text"` || string(v["anchor"]) != "2" {
				t.Fatal(v)
			}
		}},
		{"reply resolved", "ReplyToComment", []string{"reply", "--body", "reply"}, nil},
		{"quote clear", "EditComment", []string{"edit", "--quote", "", "--expected-revision", "4"}, func(t *testing.T, v map[string]json.RawMessage) {
			if _, ok := v["body"]; ok {
				t.Fatal("omitted body sent")
			}
			if string(v["quote"]) != `""` || string(v["expected"]) != "4" {
				t.Fatal(v)
			}
		}},
		{"body edit", "EditComment", []string{"edit", "--body", "new", "--expected-revision", "4"}, func(t *testing.T, v map[string]json.RawMessage) {
			if _, ok := v["quote"]; ok {
				t.Fatal("omitted quote sent")
			}
		}},
		{"retract", "RetractComment", []string{"retract", "--expected-revision", "4"}, nil},
		{"resolve", "ResolveCommentThread", []string{"resolve", "--expected-revision", "4"}, nil},
		{"reopen", "ReopenCommentThread", []string{"resolve", "--reopen", "--expected-revision", "4"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					OperationName string                     `json:"operationName"`
					Variables     map[string]json.RawMessage `json:"variables"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				calls++
				if req.OperationName != tc.op {
					t.Errorf("op=%s", req.OperationName)
				}
				if r.Header.Get(api.SessionHeader) == "" {
					t.Error("authored write lacks bound session")
				}
				if tc.check != nil {
					tc.check(t, req.Variables)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":{"%s":%s}}`, strings.ToLower(tc.op[:1])+tc.op[1:], commentResult)
			}))
			defer srv.Close()
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"comment", tc.args[0], "hrn:node:acme.com:kb:target", "--json", "--server", srv.URL}
			args = append(args, tc.args[1:]...)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if dto["revision"] != float64(4) || dto["anchorRevision"] != float64(2) || calls != 1 {
				t.Fatalf("dto=%s calls=%d", out.String(), calls)
			}
		})
	}
}
func TestCommentThreadFixtureAndHeaderlessReads(t *testing.T) {
	raw, err := os.ReadFile("testdata/comments/comment.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Thread json.RawMessage `json:"thread"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	writeTeamBinding(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Op        string                     `json:"operationName"`
			Variables map[string]json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Op != "CommentThreads" {
			t.Errorf("op=%s", req.Op)
		}
		if r.Header.Get(api.SessionHeader) != "" {
			t.Error("query attributed as write")
		}
		if _, ok := req.Variables["state"]; ok {
			t.Error("omitted state sent")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"commentThreads":{"total":1,"items":[%s]}}}`, fixture.Thread)
	}))
	defer srv.Close()
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"comment", "list", "target", "-m", "hrn:mem:acme.com:kb", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Items []struct {
			Root struct {
				State          string
				AnchorRevision int
				Author         struct{ Kind string }
			}
			Replies []struct {
				Body      *string
				Retracted bool
			}
		}
	}
	if err = json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 1 || dto.Items[0].Root.State != "RESOLVED" || dto.Items[0].Root.Author.Kind != "WORKER" || !dto.Items[0].Replies[0].Retracted || dto.Items[0].Replies[0].Body != nil {
		t.Fatal(out.String())
	}
}
func TestCommentTypedRefusalsNeverRetry(t *testing.T) {
	cases := map[string]int{"COMMENT_NOT_FOUND": 4, "COMMENT_TARGET_NOT_FOUND": 4, "COMMENT_FORBIDDEN": 8, "COMMENT_NOT_AUTHOR": 8, "COMMENT_ADMIN_REQUIRED": 8, "COMMENT_IMPERSONATION_REFUSED": 8, "COMMENT_TARGET_IS_COMMENT": 2, "COMMENT_MEMORY_NOT_COMMENTABLE": 2, "COMMENT_ANCHOR_REVISION_INVALID": 2, "COMMENT_NOT_TOP_LEVEL": 2, "COMMENT_BODY_INVALID": 2, "COMMENT_MOVE_UNCOMMENTABLE": 2, "COMMENT_MERGE_FOLDS_THREADS": 2, "COMMENT_NOT_APPROVABLE": 2, "COMMENT_OPEN_THREAD_EXISTS": 5, "COMMENT_ANCHOR_REVISION_UNAVAILABLE": 5, "COMMENT_RETRACTED": 5, "COMMENT_HIDDEN": 5, "COMMENT_THREAD_STATE": 5, "NODE_WRITE_CONFLICT": 5, "ROLE_GOVERNED": 2}
	for code, want := range cases {
		t.Run(code, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"errors":[{"message":"refused","extensions":{"code":%q,"kind":"comment"}}]}`, code)
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"comment", "edit", "hrn:node:acme.com:kb:comments:one", "--body", "new", "--expected-revision", "2", "--server", srv.URL})
			err := root.Execute()
			if exitCodeFor(err) != want || calls != 1 || out.Len() != 0 {
				t.Fatalf("err=%v calls=%d out=%s", err, calls, out.String())
			}
			if code == "ROLE_GOVERNED" && !strings.Contains(err.Error(), "hadron comment") {
				t.Fatal(err)
			}
		})
	}
}
func TestCommentMissingMutationResultFailsUnknown(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{"CreateComment": `{"data":{"createComment":null}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"comment", "create", "hrn:node:acme.com:kb:target", "--body", "new", "--server", gql.URL})
	if code := exitCodeFor(root.Execute()); code != 7 || out.Len() != 0 {
		t.Fatalf("code=%d out=%s", code, out.String())
	}
}
func TestNodeGetCommentCueAndOldServer(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		known          bool
	}{{"current", `{"data":{"nodeBatch":{"nodes":[{"id":"n1","commentSummary":{"openThreads":2,"resolvedThreads":1,"comments":7,"viewerOpenThreadId":"c1"}}],"truncated":false,"omitted":[],"unavailable":[]}}}`, true}, {"old", missingFieldJSON("commentSummary"), false}} {
		t.Run(tc.name, func(t *testing.T) {
			gql := fakeGraphQL(t, map[string]string{"ResolveUrn": resolveNodeJSON, "GetNode": `{"data":{"node":` + nodeJSON + `}}`, "NodeCommentSummaries": tc.response})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "get", "hrn:node:acme.com:kb:findings:flaky-ci", "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
				t.Fatal(err)
			}
			if tc.known {
				summary, ok := dto["commentSummary"].(map[string]any)
				if !ok {
					t.Fatalf("summary absent: %s", out.String())
				}
				if summary["openThreads"] != float64(2) || summary["comments"] != float64(7) {
					t.Fatal(summary)
				}
			} else if dto["commentSummary"] != nil {
				t.Fatal(out.String())
			}
		})
	}
}

func TestCommentRecoveryDetailsReachRenderedOutput(t *testing.T) {
	for _, tc := range []struct {
		code, key string
		value     any
		wantExit  int
	}{
		{"NODE_WRITE_CONFLICT", "currentRevision", float64(7), 5},
		{"COMMENT_OPEN_THREAD_EXISTS", "threadId", "existing-thread", 5},
		{"COMMENT_MEMORY_NOT_COMMENTABLE", "class", "PRIVATE", 2},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.code, asJSON), func(t *testing.T) {
				response, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": "refused", "extensions": map[string]any{"code": tc.code, tc.key: tc.value, "unrelated": "must not be forwarded"}}}})
				gql := fakeGraphQL(t, map[string]string{"EditComment": string(response)})
				f, out := testFactory(t)
				stderr := &strings.Builder{}
				f.IOStreams.ErrOut = stderr
				root := NewRootCmd(f)
				args := []string{"comment", "edit", "hrn:node:acme.com:kb:comments:c1", "--body", "new", "--expected-revision", "2", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("refusal succeeded")
				}
				if code := renderFailure(f, args, err); code != tc.wantExit {
					t.Fatalf("exit=%d", code)
				}
				if asJSON {
					var envelope struct {
						Error struct {
							Code       int            `json:"code"`
							Extensions map[string]any `json:"extensions"`
						} `json:"error"`
					}
					if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.Error.Extensions[tc.key] != tc.value || envelope.Error.Extensions["code"] != tc.code {
						t.Fatalf("recovery lost: %s", out.String())
					}
					if _, ok := envelope.Error.Extensions["unrelated"]; ok {
						t.Fatal("arbitrary extension leaked")
					}
				} else {
					if !strings.Contains(stderr.String(), fmt.Sprintf("%s: %v", tc.key, tc.value)) {
						t.Fatalf("recovery lost: %s", stderr.String())
					}
					if strings.Contains(stderr.String(), "unrelated") {
						t.Fatal("arbitrary extension leaked")
					}
				}
			})
		}
	}
}

func TestCommentHelpUsesTheAddressedEntity(t *testing.T) {
	for _, verb := range []string{"get", "reply", "edit", "retract", "resolve", "create", "list"} {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"comment", verb, "--help"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		label := "<comment-ref>"
		if verb == "create" || verb == "list" {
			label = "<target-ref>"
		}
		if !strings.Contains(out.String(), verb+" "+label) || strings.Contains(out.String(), "<node-ref>") {
			t.Fatalf("wrong help for %s: %s", verb, out.String())
		}
	}
}
