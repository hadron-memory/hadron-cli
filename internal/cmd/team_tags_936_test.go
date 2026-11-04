package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// The fake checks the actual document and variables, and never resolves a head.
func tag936Server(t *testing.T, tags string, refuse string) (*httptest.Server, *[]attnCall) {
	t.Helper()
	calls := &[]attnCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		*calls = append(*calls, attnCall{RawOp: req.OperationName, Op: req.OperationName, Vars: req.Variables, Query: req.Query, Session: r.Header.Get("X-Hadron-Session")})
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.OperationName, "ReadHead") || strings.Contains(req.OperationName, "ReadMetadata") {
			fmt.Fprint(w, teamThreadHead)
			return
		}
		if req.OperationName == "GetBoundSessionEnd" {
			fmt.Fprint(w, `{"data":{"session":{"id":"s-new","endedAt":null}}}`)
			return
		}
		if req.OperationName == "TeamAppIdentity" {
			fmt.Fprint(w, teamAppIdentityJSON)
			return
		}
		if req.OperationName == "ChannelReadState" {
			fmt.Fprint(w, `{"data":{"channelReadState":null}}`)
			return
		}
		key := "teamChatMessages"
		if strings.HasPrefix(req.OperationName, "Channel") {
			key = "channelMessages"
		}
		if refuse != "" {
			fmt.Fprintf(w, `{"errors":[{"message":"uniform refusal","extensions":{"code":%q}}]}`, refuse)
			return
		}
		fmt.Fprintf(w, `{"data":{%q:{"total":1,"items":[{"nodeId":"n8","seq":8,"body":"body","at":"t","authorName":"Ada","mentions":[],"teamTags":%s}]}}}`, key, tags)
	}))
	t.Cleanup(srv.Close)
	return srv, calls
}
func tag936Args(surface, url string, extra ...string) []string {
	args := []string{"team", "chat", "read", "--app", "acme.com:eng-team", "--since", "0"}
	if surface == "channel" {
		args = []string{"channel", "read", "channel1", "--since", "0"}
	}
	args = append(args, "--server", url, "--json")
	return append(args, extra...)
}
func tag936Pages(calls []attnCall) []attnCall {
	var out []attnCall
	for _, c := range calls {
		if strings.Contains(c.Query, "items {") && strings.Contains(c.Query, "body") {
			out = append(out, c)
		}
	}
	return out
}
func TestTeamTags936JSONDistinctAndText(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		for _, tags := range []string{"null", "[]", `[{"headId":"stable1","label":"@team.renamed"}]`} {
			t.Run(surface+tags, func(t *testing.T) {
				teamGitDir(t)
				srv, calls := tag936Server(t, tags, "")
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(tag936Args(surface, srv.URL))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				var dto struct {
					Messages []map[string]json.RawMessage `json:"messages"`
				}
				if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
					t.Fatal(err)
				}
				got, exists := dto.Messages[0]["teamTags"]
				if !exists {
					t.Fatal("teamTags key omitted")
				}
				var actual, expected any
				_ = json.Unmarshal(got, &actual)
				_ = json.Unmarshal([]byte(tags), &expected)
				a, _ := json.Marshal(actual)
				b, _ := json.Marshal(expected)
				if string(a) != string(b) {
					t.Fatalf("tags %s want %s", a, b)
				}
				for _, c := range tag936Pages(*calls) {
					if !strings.Contains(c.Query, "teamTags") {
						t.Fatal("not selected")
					}
					if _, ok := c.Vars["teamHeadRef"]; ok {
						t.Fatal("unfiltered head sent")
					}
					if _, ok := c.Vars["untagged"]; ok {
						t.Fatal("unfiltered boolean sent")
					}
				}
				f2, o2 := testFactory(t)
				args := tag936Args(surface, srv.URL)
				for i, a := range args {
					if a == "--json" {
						args = append(args[:i], args[i+1:]...)
						break
					}
				}
				r2 := NewRootCmd(f2)
				r2.SetArgs(args)
				if err := r2.Execute(); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(tags, "renamed") && !strings.Contains(o2.String(), "@team.renamed") {
					t.Fatal("label missing from text")
				}
				if strings.Contains(o2.String(), "no team") {
					t.Fatal("unknown/empty misrepresented")
				}
			})
		}
	}
}
func TestTeamTags936WirePresenceAndNoMark(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		for _, tc := range []struct {
			args  []string
			key   string
			value any
		}{
			{[]string{"--team-head", " raw-head-id ", "--mentions", "Ada"}, "teamHeadRef", " raw-head-id "},
			{[]string{"--untagged"}, "untagged", true}, {[]string{"--tagged"}, "untagged", false},
		} {
			t.Run(surface+strings.Join(tc.args, " "), func(t *testing.T) {
				path := bindTeamThread(t)
				before, _ := os.ReadFile(path)
				srv, calls := tag936Server(t, "[]", "")
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(tag936Args(surface, srv.URL, tc.args...))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				pages := tag936Pages(*calls)
				if len(pages) != 1 {
					t.Fatalf("pages %+v", *calls)
				}
				if v, ok := pages[0].Vars[tc.key]; !ok || v != tc.value {
					t.Fatalf("variables %+v", pages[0].Vars)
				}
				if len(tc.args) > 2 && pages[0].Vars["mentionsRef"] != "Ada" {
					t.Fatal("mention not composed")
				}
				after, _ := os.ReadFile(path)
				if string(before) != string(after) {
					t.Fatal("filtered read mutated binding")
				}
				for _, c := range *calls {
					if strings.Contains(c.RawOp, "Mark") {
						t.Fatal("filtered read marked")
					}
				}
				if !strings.Contains(out.String(), `"nextSince": 8`) {
					t.Fatal(out.String())
				}
			})
		}
	}
}
func TestTeamTags936ConflictsBeforeHTTP(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		for _, extra := range [][]string{
			{"--tagged", "--untagged"}, {"--team-head", "x", "--tagged"}, {"--team-head", "x", "--untagged"},
			{"--tagged=false"}, {"--untagged=false"}, {"--thread", "1", "--team-head", "x"}, {"--thread", "1", "--tagged"}, {"--thread", "1", "--untagged"}, {"--team-head", ""},
		} {
			t.Run(surface+strings.Join(extra, " "), func(t *testing.T) {
				teamGitDir(t)
				srv, calls := tag936Server(t, "[]", "")
				f, _ := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(tag936Args(surface, srv.URL, extra...))
				err := root.Execute()
				if code := exitCodeFor(err); code != 2 {
					t.Fatalf("exit %d err%v", code, err)
				}
				if len(*calls) != 0 {
					t.Fatal("HTTP before refusal")
				}
			})
		}
	}
}
func TestTeamTags936RefusalsAreTyped(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		for code, want := range map[string]int{"TEAM_CHAT_TEAM_HEAD_INVALID": 4, "TEAM_CHAT_TEAM_METADATA_FORBIDDEN": 8, "BAD_USER_INPUT": 2} {
			t.Run(surface+code, func(t *testing.T) {
				teamGitDir(t)
				srv, calls := tag936Server(t, "null", code)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(tag936Args(surface, srv.URL, "--team-head", "bad"))
				err := root.Execute()
				if got := renderError(f, err); got != want {
					t.Fatalf("exit%d err%v", got, err)
				}
				if !strings.Contains(out.String(), `"serverCode": "`+code+`"`) || strings.Contains(out.String(), `"messages"`) {
					t.Fatal(out.String())
				}
				if len(tag936Pages(*calls)) != 1 {
					t.Fatal("retried refusal")
				}
			})
		}
	}
}
func TestTeamTags936UnfilteredDocumentsHaveNoFilterArguments(t *testing.T) {
	for _, doc := range []string{gen.TeamChatMessages_Operation, gen.TeamChatReadPage_Operation, gen.TeamChatReadPageAttributed_Operation, gen.ChannelMessages_Operation} {
		if strings.Contains(doc, "$teamHeadRef") || strings.Contains(doc, "$untagged") {
			t.Fatal("legacy read carries tag arguments")
		}
	}
}

// Real bound same-server reads exercise attribution and the persisted watermark guard.
func TestTeamTags936AttributedNoMark(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := tag936Server(t, "[]", "")
	setTeamBindingServer(t, srv.URL)
	path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	before, _ := os.ReadFile(path)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(tag936Args("team", srv.URL, "--tagged", "--mentions-me"))
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("tag read recorded watermark")
	}
	for _, c := range tag936Pages(*calls) {
		if c.Session != "s-new" || !strings.Contains(c.Query, "advanceReadState: false") || c.Vars["mentionsRef"] == nil {
			t.Fatalf("attribution/filter %+v", c)
		}
	}
}
