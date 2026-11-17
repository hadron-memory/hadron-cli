package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
)

// The fake checks the actual document and variables, and never resolves a head.
func tag936Server(t *testing.T, tags string, refuse string, override ...func(attnCall, int) string) (*httptest.Server, *[]attnCall) {
	t.Helper()
	calls := &[]attnCall{}
	var mu sync.Mutex // independent reads may arrive together (cli#1677)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string         `json:"operationName"`
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		this := attnCall{RawOp: req.OperationName, Op: req.OperationName, Vars: req.Variables, Query: req.Query, Session: r.Header.Get("X-Hadron-Session")}
		mu.Lock()
		*calls = append(*calls, this)
		pages := len(tag936Pages(*calls))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(req.OperationName, "ReadHead") || strings.Contains(req.OperationName, "ReadMetadata") {
			fmt.Fprint(w, strings.ReplaceAll(teamThreadHead, "9", "8"))
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
		if req.OperationName == "TeamDefaultChannel" {
			fmt.Fprint(w, `{"data":{"app":{"id":"capp100000000000000000000","defaultChannel":{"id":"ch1"}}}}`)
			return
		}
		if req.OperationName == "MarkOwnTeamChatRead" {
			fmt.Fprint(w, `{"data":{"markOwnTeamChatRead":{"workerId":"wkr1","channelId":"ch1","lastSeenSeq":8}}}`)
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
		if len(override) > 0 {
			if body := override[0](this, pages); body != "" {
				fmt.Fprint(w, body)
				return
			}
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
				if tc.key == "teamHeadRef" {
					if _, present := pages[0].Vars["untagged"]; present {
						t.Fatalf("head-only sent untagged: %+v", pages[0].Vars)
					}
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
				if !strings.Contains(out.String(), `"serverCode": "`+code+`"`) || !strings.Contains(out.String(), "uniform refusal") || strings.Contains(out.String(), `"messages"`) {
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
	for _, filter := range [][]string{{"--tagged"}, {"--untagged"}, {"--team-head", "stable"}, {"--team-head", "stable", "--mentions-me"}, {"--tagged", "--mentions-me"}, nil} {
		t.Run(strings.Join(filter, " "), func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := tag936Server(t, "[]", "")
			setTeamBindingServer(t, srv.URL)
			path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
			before, _ := os.ReadFile(path)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(tag936Args("team", srv.URL, filter...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(path)
			if len(filter) > 0 && string(before) != string(after) {
				t.Fatal("tag read recorded watermark")
			}
			if len(filter) == 0 && string(before) == string(after) {
				t.Fatal("positive unfiltered twin did not record watermark")
			}
			pages := tag936Pages(*calls)
			if len(pages) != 1 {
				t.Fatal("missing page")
			}
			if strings.Contains(strings.Join(filter, " "), "--mentions-me") && pages[0].Vars["mentionsRef"] == nil {
				t.Fatal("mentions-me did not compose with the tag filter")
			}
			for _, c := range pages {
				for i, flag := range filter {
					if flag == "--team-head" && c.Vars["teamHeadRef"] != filter[i+1] {
						t.Fatalf("attributed head dropped: %+v", c.Vars)
					}
					if flag == "--tagged" && c.Vars["untagged"] != false {
						t.Fatalf("attributed tagged dropped: %+v", c.Vars)
					}
					if flag == "--untagged" && c.Vars["untagged"] != true {
						t.Fatalf("attributed untagged dropped: %+v", c.Vars)
					}
				}
				if strings.Contains(strings.Join(filter, " "), "--team-head") {
					if _, present := c.Vars["untagged"]; present {
						t.Fatalf("head-only sent untagged: %+v", c.Vars)
					}
				}
				if c.Session != "s-new" || !strings.Contains(c.Query, "advanceReadState: false") {
					t.Fatalf("attribution %+v", c)
				}
			}
			for _, c := range *calls {
				if strings.Contains(c.RawOp, "Mark") && len(filter) > 0 {
					t.Fatal("tag read acknowledged")
				}
			}
		})
	}
}

func TestTeamTags936AllAndWaitKeepFilterAndNeverMark(t *testing.T) {
	for _, mode := range []string{"--all", "--wait"} {
		for _, filter := range []string{"--tagged", "--untagged"} {
			t.Run(mode+filter, func(t *testing.T) {
				writeTeamBinding(t)
				srv, calls := tag936Server(t, "[]", "", func(c attnCall, n int) string {
					if (mode == "--all" && n > 1) || (mode == "--wait" && n == 1) {
						return `{"data":{"teamChatMessages":{"items":[]}}}`
					}
					return ""
				})
				setTeamBindingServer(t, srv.URL)
				path := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
				before, _ := os.ReadFile(path)
				f, out := testFactory(t)
				args := tag936Args("team", srv.URL, filter, mode)
				if mode == "--wait" {
					args = append(args, "5s", "--poll-interval", "2s")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				for _, c := range tag936Pages(*calls) {
					if _, ok := c.Vars["untagged"]; !ok || c.Vars["untagged"] != (filter == "--untagged") {
						t.Fatalf("filter dropped %+v", c)
					}
				}
				var b map[string]any
				data, _ := os.ReadFile(path)
				_ = json.Unmarshal(data, &b)
				if b["chatSeenSeq"] != nil || string(data) != string(before) {
					t.Fatal("filtered poll/walk wrote watermark")
				}
				if !strings.Contains(out.String(), `"nextSince": 8`) {
					t.Fatal(out.String())
				}
			})
		}
	}
}
func TestTeamTags936CompatibilityNeverWidens(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		for _, message := range []string{`Unknown argument "teamHeadRef" on field "Query.teamChatMessages".`, `Cannot query field "teamTags" on type "TeamChatMessage".`} {
			t.Run(surface+message, func(t *testing.T) {
				teamGitDir(t)
				srv, calls := tag936Server(t, "[]", "", func(c attnCall, n int) string {
					response, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": "GRAPHQL_VALIDATION_FAILED"}}}})
					return string(response)
				})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(tag936Args(surface, srv.URL, "--team-head", "raw"))
				err := root.Execute()
				if exitCodeFor(err) != 2 || !strings.Contains(err.Error(), "schema versions are out of sync") {
					t.Fatalf("schema refusal %v", err)
				}
				if len(tag936Pages(*calls)) != 1 || strings.Contains(out.String(), `"messages"`) {
					t.Fatal("schema-skew widened/retried")
				}
			})
		}
	}
	t.Run("suppression retry retains false", func(t *testing.T) {
		writeTeamBinding(t)
		srv, calls := tag936Server(t, "[]", "", func(c attnCall, n int) string {
			if c.RawOp == "TeamChatTagsAttributed" {
				return missingReadSuppression
			}
			return ""
		})
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(tag936Args("team", srv.URL, "--tagged"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		pages := tag936Pages(*calls)
		if len(pages) != 2 {
			t.Fatalf("pages %+v", *calls)
		}
		for _, c := range pages {
			if v, ok := c.Vars["untagged"]; !ok || v != false {
				t.Fatal("retry lost explicit false")
			}
		}
		if pages[0].Session != "s-new" || pages[1].Session != "" {
			t.Fatalf("retry attribution %+v", pages)
		}
	})
}
func TestTeamTags936PostKeepsStoredTagsAndRefusal(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		for _, code := range []string{"", "TEAM_CHAT_TEAM_HEAD_INVALID", "TEAM_CHAT_TEAM_METADATA_FORBIDDEN"} {
			t.Run(surface+code, func(t *testing.T) {
				teamGitDir(t)
				body := `{"data":{"createTeamChatMessage":{"nodeId":"n1","seq":1,"body":"x","at":"t","mentions":[],"teamTags":[{"headId":"h1","label":"@team.old"}]}}}`
				if code != "" {
					body = gqlErrorJSON(code)
				}
				op := "CreateTeamChatMessage"
				args := []string{"team", "chat", "post", "x", "--as-me", "--app", "acme.com:eng-team"}
				if surface == "channel" {
					op = "CreateChannelMessage"
					body = strings.ReplaceAll(body, "createTeamChatMessage", "createChannelMessage")
					args = []string{"channel", "post", "channel1", "x", "--as-me"}
				}
				srv, calls := attnServer(t, map[string]string{op: body})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append(args, "--server", srv.URL, "--json"))
				err := root.Execute()
				if code != "" {
					want := 4
					if code == "TEAM_CHAT_TEAM_METADATA_FORBIDDEN" {
						want = 8
					}
					if renderError(f, err) != want || !strings.Contains(out.String(), `"serverCode": "`+code+`"`) {
						t.Fatalf("post %v %s", err, out.String())
					}
				} else if err != nil || !strings.Contains(out.String(), `"headId": "h1"`) {
					t.Fatalf("post %v %s", err, out.String())
				}
				if len(stopCalls(calls, op)) != 1 {
					t.Fatal("post retried")
				}
			})
		}
	}
}

// Regression probes adapted from Ralion’s scoped security review.
func TestTeamTags936AttributedRefusalNoRetry(t *testing.T) {
	for _, code := range []string{"TEAM_CHAT_TEAM_HEAD_INVALID", "TEAM_CHAT_TEAM_METADATA_FORBIDDEN"} {
		t.Run(code, func(t *testing.T) {
			writeTeamBinding(t)
			srv, calls := tag936Server(t, "[]", code)
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(tag936Args("team", srv.URL, "--team-head", "h1"))
			err := root.Execute()
			pages := tag936Pages(*calls)
			if err == nil || len(pages) != 1 || pages[0].Session == "" {
				t.Fatalf("err=%v pages=%+v out=%s", err, pages, out.String())
			}
		})
	}
}

func TestTeamTags936LabelControls(t *testing.T) {
	for _, surface := range []string{"team", "channel"} {
		t.Run(surface, func(t *testing.T) {
			teamGitDir(t)
			srv, _ := tag936Server(t, `[{"headId":"h","label":"a\u001b[31m\nb\u202ec"}]`, "")
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{}
			for _, a := range tag936Args(surface, srv.URL) {
				if a != "--json" {
					args = append(args, a)
				}
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			s := out.String()
			if strings.ContainsAny(s, "\x1b\u202e") || strings.Contains(s, "a\n") {
				t.Fatalf("%q", s)
			}
		})
	}
}

func TestTeamTags936ChannelDocumentRetainsFilter(t *testing.T) {
	for _, arg := range []string{"teamHeadRef: $teamHeadRef", "untagged: $untagged"} {
		if !strings.Contains(gen.ChannelTags_Operation, arg) {
			t.Fatalf("channel document dropped %s", arg)
		}
	}
}
