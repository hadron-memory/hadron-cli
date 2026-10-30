package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const (
	searchURN1 = "hrn:node:acme.com:team-shared:chats:team:m-142"
	searchURN2 = "hrn:node:acme.com:team-shared:chats:team:m-97"
)

func searchPage(complete bool, cursor string, items ...string) string {
	cur := "null"
	if cursor != "" {
		cur = `"` + cursor + `"`
	}
	return `{"channelId":"c1","channelIncarnation":1,"complete":` + map[bool]string{true: "true", false: "false"}[complete] +
		`,"cursor":` + cur + `,"items":[` + strings.Join(items, ",") + `]}`
}

func searchMatch(seq int, urn string) string {
	u := "null"
	if urn != "" {
		u = `"` + urn + `"`
	}
	return `{"channelId":"c1","channelIncarnation":1,"nodeId":"n1","seq":` + itoa(seq) + `,"urn":` + u + `}`
}

type searchSurface struct {
	name, op, field string
	args            func(extra ...string) []string
	refVar, refWant string
	hint            string
}

func searchSurfaces() []searchSurface {
	return []searchSurface{
		{
			name: "team chat", op: "TeamChatSearch", field: "teamChatSearch",
			args: func(extra ...string) []string {
				return append([]string{"team", "chat", "search", "--app", "hrn:app:acme.com:team"}, extra...)
			},
			refVar: "appRef", refWant: "hrn:app:acme.com:team",
			hint: "hadron team chat read --server SRV --app hrn:app:acme.com:team --since 141 --limit 1",
		},
		{
			name: "channel", op: "ChannelMessageSearch", field: "channelMessageSearch",
			args: func(extra ...string) []string {
				return append([]string{"channel", "search", "c1"}, extra...)
			},
			refVar: "channelRef", refWant: "c1",
			hint: "hadron channel read --server SRV c1 --since 141 --limit 1",
		},
	}
}

func runSearch(t *testing.T, s searchSurface, response string, extra ...string) (map[string]any, string, string, error) {
	t.Helper()
	srv, captured := captureGraphQL(t, map[string]string{s.op: response})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	args := append([]string{"--server", srv.URL}, s.args(extra...)...)
	root.SetArgs(args)
	err := root.Execute()
	vars := map[string]any{}
	if raw := captured[s.op]; raw != nil {
		_ = json.Unmarshal(raw, &vars)
	}
	return vars, strings.ReplaceAll(out.String(), srv.URL, "SRV"), f.IOStreams.ErrOut.(*strings.Builder).String(), err
}

func TestC1327SearchRequestAndRendering(t *testing.T) {
	for _, s := range searchSurfaces() {
		t.Run(s.name+"/request omits unset limit and cursor", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(true, "") + `}}`
			vars, _, _, err := runSearch(t, s, resp, "deploy")
			if err != nil {
				t.Fatal(err)
			}
			if vars["query"] != "deploy" || vars[s.refVar] != s.refWant {
				t.Errorf("vars %v", vars)
			}
			if _, ok := vars["limit"]; ok {
				t.Errorf("limit sent when unset: %v", vars)
			}
			if _, ok := vars["cursor"]; ok {
				t.Errorf("cursor sent when unset: %v", vars)
			}
		})
		t.Run(s.name+"/limit and cursor pass through verbatim", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(true, "") + `}}`
			vars, _, _, err := runSearch(t, s, resp, "deploy", "--limit", "500", "--cursor", "opaque-c")
			if err != nil {
				t.Fatal(err)
			}
			if vars["limit"] != float64(500) || vars["cursor"] != "opaque-c" {
				t.Errorf("not passed through (the server clamps): %v", vars)
			}
		})
		t.Run(s.name+"/explicit empty cursor is sent, not dropped", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(true, "") + `}}`
			vars, _, _, err := runSearch(t, s, resp, "deploy", "--cursor", "")
			if err != nil {
				t.Fatal(err)
			}
			if c, ok := vars["cursor"]; !ok || c != "" {
				t.Errorf("explicit empty cursor not passed through: %v", vars)
			}
		})
		t.Run(s.name+"/limit below one is sent, not fixed up", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(true, "") + `}}`
			vars, _, _, err := runSearch(t, s, resp, "deploy", "--limit", "0")
			if err != nil {
				t.Fatal(err)
			}
			if vars["limit"] != float64(0) {
				t.Errorf("limit 0 not sent: %v", vars)
			}
		})
		t.Run(s.name+"/complete page text", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(true, "", searchMatch(142, searchURN1), searchMatch(97, searchURN2)) + `}}`
			_, out, errOut, err := runSearch(t, s, resp, "deploy")
			if err != nil {
				t.Fatal(err)
			}
			want := "#142  " + searchURN1 + "\n#97  " + searchURN2 + "\nread a hit: " + s.hint + "\ncomplete\n"
			if out != want || errOut != "" {
				t.Errorf("stdout %q\nwant %q\nstderr %q", out, want, errOut)
			}
		})
		t.Run(s.name+"/incomplete page prints the cursor", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(false, "next-c", searchMatch(142, searchURN1)) + `}}`
			_, out, _, err := runSearch(t, s, resp, "deploy")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "incomplete:") || !strings.HasSuffix(out, "--cursor next-c\n") || strings.Contains(out, "\ncomplete\n") {
				t.Errorf("stdout %q", out)
			}
		})
		t.Run(s.name+"/empty complete page is no matches", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(true, "") + `}}`
			_, out, _, err := runSearch(t, s, resp, "deploy")
			if err != nil {
				t.Fatal(err)
			}
			if out != "no matches\ncomplete\n" {
				t.Errorf("stdout %q", out)
			}
		})
		t.Run(s.name+"/empty incomplete page is not no matches", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(false, "scan-c") + `}}`
			_, out, _, err := runSearch(t, s, resp, "deploy")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "no matches") || !strings.Contains(out, "--cursor scan-c") {
				t.Errorf("an unfinished scan was shown as no matches: %q", out)
			}
		})
		t.Run(s.name+"/json shape", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(false, "next-c", searchMatch(142, searchURN1), searchMatch(7, "")) + `}}`
			_, out, errOut, err := runSearch(t, s, resp, "deploy", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var dto struct {
				ChannelID *string          `json:"channelId"`
				Complete  bool             `json:"complete"`
				Cursor    *string          `json:"cursor"`
				Items     []map[string]any `json:"items"`
			}
			if e := json.Unmarshal([]byte(out), &dto); e != nil {
				t.Fatal(e, out)
			}
			if dto.ChannelID == nil || *dto.ChannelID != "c1" || dto.Complete || dto.Cursor == nil || *dto.Cursor != "next-c" || len(dto.Items) != 2 {
				t.Errorf("dto %s", out)
			}
			if dto.Items[0]["seq"] != float64(142) || dto.Items[0]["urn"] != searchURN1 || dto.Items[1]["urn"] != nil {
				t.Errorf("items %v", dto.Items)
			}
			for _, key := range []string{"seq", "urn", "nodeId", "channelId", "channelIncarnation"} {
				if _, ok := dto.Items[0][key]; !ok {
					t.Errorf("item lacks %q: %v", key, dto.Items[0])
				}
			}
			if dto.Items[0]["nodeId"] != "n1" || dto.Items[0]["channelId"] != "c1" || dto.Items[0]["channelIncarnation"] != float64(1) {
				t.Errorf("item identity %v", dto.Items[0])
			}
			var page map[string]any
			_ = json.Unmarshal([]byte(out), &page)
			if page["channelIncarnation"] != float64(1) || len(page) != 5 {
				t.Errorf("page keys %v", page)
			}
			if errOut != "" {
				t.Errorf("stderr under --json: %q", errOut)
			}
		})
		t.Run(s.name+"/json empty items is [] not null", func(t *testing.T) {
			resp := `{"data":{"` + s.field + `":` + searchPage(true, "") + `}}`
			_, out, _, err := runSearch(t, s, resp, "deploy", "--json")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, `"items": []`) || !strings.Contains(out, `"cursor": null`) {
				t.Errorf("out %s", out)
			}
		})
	}
}

func TestC1327SearchRefusalsKeepTheServerCode(t *testing.T) {
	for _, s := range searchSurfaces() {
		for _, tc := range []struct {
			code string
			exit int
		}{
			{"TEAM_CHAT_SEARCH_QUERY_LENGTH", exitcode.Usage},
			{"TEAM_CHAT_SEARCH_CURSOR_INVALID", exitcode.Usage},
			{"TEAM_CHAT_SEARCH_ENCRYPTED", exitcode.Conflict},
			{"BAD_USER_INPUT", exitcode.Usage},
		} {
			t.Run(s.name+"/"+tc.code, func(t *testing.T) {
				raw, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": "refused: " + tc.code, "extensions": map[string]any{"code": tc.code}}}})
				srv, _ := captureGraphQL(t, map[string]string{s.op: string(raw)})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				root.SetArgs(append([]string{"--server", srv.URL, "--json"}, s.args("x")...))
				err := root.Execute()
				if err == nil {
					t.Fatal("refusal succeeded")
				}
				if code := renderError(f, err); code != tc.exit {
					t.Errorf("exit %d want %d", code, tc.exit)
				}
				var env struct {
					Error struct {
						Code       int    `json:"code"`
						ServerCode string `json:"serverCode"`
					} `json:"error"`
				}
				if e := json.Unmarshal([]byte(out.String()), &env); e != nil {
					t.Fatal(e, out.String())
				}
				if env.Error.ServerCode != tc.code {
					t.Errorf("server code %q not kept verbatim: %s", env.Error.ServerCode, out.String())
				}
			})
		}
	}
}

// An App with no team chat yet answers the complete empty page with a null
// channel identity; the DTO keeps the nulls rather than inventing values.
func TestC1327SearchNoTeamChatYetKeepsNullIdentity(t *testing.T) {
	for _, s := range searchSurfaces() {
		resp := `{"data":{"` + s.field + `":{"channelId":null,"channelIncarnation":null,"complete":true,"cursor":null,"items":[]}}}`
		_, out, _, err := runSearch(t, s, resp, "deploy", "--json")
		if err != nil {
			t.Fatal(err)
		}
		var page map[string]any
		if e := json.Unmarshal([]byte(out), &page); e != nil {
			t.Fatal(e, out)
		}
		v, ok := page["channelId"]
		if !ok || v != nil || page["channelIncarnation"] != nil || page["complete"] != true {
			t.Errorf("%s page %v", s.name, page)
		}
	}
}

// The hint must not invent --app when only the worktree binding selected the
// team, but still repeats the --server that was typed.
func TestC1327TeamHintOmitsAppWhenOnlyBound(t *testing.T) {
	writeTeamBinding(t)
	resp := `{"data":{"teamChatSearch":` + searchPage(true, "", searchMatch(142, searchURN1)) + `}}`
	srv, calls := attnServer(t, map[string]string{"TeamChatSearch": resp})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "chat", "search", "deploy"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if n := len(stopCalls(calls, "TeamChatSearch")); n != 1 {
		t.Fatalf("search calls %d", n)
	}
	want := "hadron team chat read --server " + srv.URL + " --since 141 --limit 1"
	if !strings.Contains(out.String(), want) || strings.Contains(out.String(), "--app") {
		t.Errorf("hint %q want %q without --app", out.String(), want)
	}
}
