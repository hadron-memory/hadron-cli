package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"strings"
	"testing"
)

// These are deliberate refusals, never a transient retry signal. Exercise
// the shared mapper through posts, replies and the generic Channel door.
func TestC1306PostRefusalExitAndJSON(t *testing.T) {
	for _, tc := range []struct {
		code, message string
		exit          int
	}{
		{"TEAM_CHAT_DUPLICATE", "Duplicate message: earlier seq #42 in channel c1.", exitcode.Conflict},
		{"TEAM_CHAT_POST_RATE_LIMITED", "Post limit 10 reached; resets at 2026-10-30T14:01:00Z.", exitcode.Conflict},
		{"TEAM_CHAT_BODY_TOO_LARGE", "Body exceeds the team-chat cap.", exitcode.Usage},
	} {
		for _, surface := range []struct {
			name, op string
			args     []string
		}{
			{"post", "CreateTeamChatMessage", []string{"team", "chat", "post", "message"}},
			{"reply", "CreateTeamChatMessage", []string{"team", "chat", "post", "message", "--reply-to", "42"}},
			{"channel", "CreateChannelMessage", []string{"channel", "post", "c1", "message", "--session", "s-new"}},
		} {
			for _, j := range []bool{false, true} {
				t.Run(tc.code+"/"+surface.name+map[bool]string{false: "/text", true: "/json"}[j], func(t *testing.T) {
					writeTeamBinding(t)
					raw, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": tc.message, "extensions": map[string]any{"code": tc.code, "earlierSeq": 42, "limit": 10, "resetsAt": "2026-10-30T14:01:00Z"}}}})
					srv, calls := attnServer(t, map[string]string{surface.op: string(raw)})
					setTeamBindingServer(t, srv.URL)
					f, out := testFactory(t)
					stderr := f.IOStreams.ErrOut.(*strings.Builder)
					args := append([]string{"--server", srv.URL}, surface.args...)
					if j {
						args = append(args, "--json")
					}
					root := NewRootCmd(f)
					root.SetArgs(args)
					err := root.Execute()
					if err == nil {
						t.Fatal("refusal succeeded")
					}
					code := renderError(f, err)
					if code != tc.exit {
						t.Errorf("exit %d want %d", code, tc.exit)
					}
					if n := len(stopCalls(calls, surface.op)); n != 1 {
						t.Errorf("post calls %d want exactly one", n)
					}
					if j {
						var dto struct {
							Error struct {
								Code       int    `json:"code"`
								Message    string `json:"message"`
								ServerCode string `json:"serverCode"`
							} `json:"error"`
						}
						if e := json.Unmarshal([]byte(out.String()), &dto); e != nil {
							t.Fatal(e, out.String())
						}
						if dto.Error.Code != tc.exit || dto.Error.Message != tc.message {
							t.Errorf("envelope %s", out.String())
						}
						if tc.exit == exitcode.Conflict && dto.Error.ServerCode != tc.code {
							t.Errorf("server code lost: %s", out.String())
						}
						if stderr.Len() != 0 {
							t.Errorf("JSON stderr %q", stderr.String())
						}
					} else {
						if out.Len() != 0 || stderr.String() != "hadron: "+tc.message+"\n" {
							t.Errorf("streams stdout%q stderr%q", out.String(), stderr.String())
						}
					}
				})
			}
		}
	}
}
