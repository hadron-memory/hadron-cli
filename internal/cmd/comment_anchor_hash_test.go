package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCommentAnchorHashCommandOutput(t *testing.T) {
	hash := strings.Repeat("0123456789abcdef", 4)
	for _, value := range []string{"null", `"` + hash + `"`} {
		for _, tc := range []struct {
			op, field string
			args      []string
		}{
			{"GetComment", "comment", []string{"get"}},
			{"CommentThreads", "commentThreads", []string{"list"}},
			{"CreateComment", "createComment", []string{"create", "--body", "new"}},
			{"ReplyToComment", "replyToComment", []string{"reply", "--body", "new"}},
			{"EditComment", "editComment", []string{"edit", "--body", "new", "--expected-revision", "4"}},
			{"DeleteComment", "deleteComment", []string{"delete", "--yes", "--expected-revision", "4"}},
			{"ResolveCommentThread", "resolveCommentThread", []string{"resolve", "--expected-revision", "4"}},
			{"ReopenCommentThread", "reopenCommentThread", []string{"resolve", "--reopen", "--expected-revision", "4"}},
		} {
			for _, asJSON := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/json=%t", tc.op, value, asJSON), func(t *testing.T) {
					result := strings.Replace(commentResult, `"anchorApprovalHash":"hash"`, `"anchorApprovalHash":`+value, 1)
					result = strings.Replace(result, `"body":"body"`, `"body":null`, 1)
					payload := result
					if tc.op == "CommentThreads" {
						payload = `{"total":1,"items":[{"root":` + result + `,"replies":[` + result + `],"replyCount":1}]}`
					}
					gql := fakeGraphQL(t, map[string]string{tc.op: `{"data":{"` + tc.field + `":` + payload + `}}`})
					f, out := testFactory(t)
					root := NewRootCmd(f)
					args := []string{"comment", tc.args[0], "hrn:node:acme.com:kb:target", "--server", gql.URL}
					args = append(args, tc.args[1:]...)
					if asJSON {
						args = append(args, "--json")
					}
					root.SetArgs(args)
					if err := root.Execute(); err != nil {
						t.Fatal(err)
					}
					if !asJSON {
						line := "hrn:node:acme.com:kb:comments:c1 — RESOLVED; author Jane (worker); write counter 4; on target revision 2 (older target text)\n  session bound by @holger (provenance)\n"
						want := line
						if tc.op == "CommentThreads" {
							want = "Threads: 1 (showing 1 from offset 0)\n" + line + line
						}
						if out.String() != want {
							t.Fatalf("text=%q want=%q", out.String(), want)
						}
						return
					}
					var dto map[string]json.RawMessage
					if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
						t.Fatal(err)
					}
					comments := []map[string]json.RawMessage{dto}
					if tc.op == "CommentThreads" {
						var page struct {
							Items []struct {
								Root    map[string]json.RawMessage
								Replies []map[string]json.RawMessage
							}
						}
						if err := json.Unmarshal([]byte(out.String()), &page); err != nil {
							t.Fatal(err)
						}
						if len(page.Items) != 1 || len(page.Items[0].Replies) != 1 {
							t.Fatal(out.String())
						}
						comments = append([]map[string]json.RawMessage{page.Items[0].Root}, page.Items[0].Replies...)
					}
					for _, c := range comments {
						if string(c["anchorApprovalHash"]) != value {
							t.Errorf("anchorApprovalHash=%s want=%s", c["anchorApprovalHash"], value)
						}
						for key, want := range map[string]string{"anchorRevision": "2", "anchorIsCurrent": "false", "body": "null", "quote": "null", "revSeq": "4", "state": `"RESOLVED"`} {
							if string(c[key]) != want {
								t.Errorf("%s=%s want=%s", key, c[key], want)
							}
						}
					}
				})
			}
		}
	}
}
