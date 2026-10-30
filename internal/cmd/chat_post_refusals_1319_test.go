package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1319: the node-addressed door (`hadron chat post --node`) maps the same
// deliberate refusals as the team/channel doors pinned in #1306. A refusal is
// never a retry signal: exactly one CreateChannelMessage, no create-if-missing.
const nodeURN1319 = "hrn:node:acme.com:tc:chats:api:messages"

func chatPostRefusalRaw(code, message string) string {
	raw, _ := json.Marshal(map[string]any{"errors": []any{map[string]any{"message": message, "extensions": map[string]any{"code": code}}}})
	return string(raw)
}

func TestC1319NodeAddressedPostRefusalExitAndJSON(t *testing.T) {
	for _, tc := range []struct {
		code, message string
		exit          int
	}{
		{"TEAM_CHAT_DUPLICATE", "Duplicate message: earlier seq #42 in channel c1.", exitcode.Conflict},
		{"TEAM_CHAT_POST_RATE_LIMITED", "Post limit 10 reached; resets at 2026-10-30T14:01:00Z.", exitcode.Conflict},
		{"TEAM_CHAT_BODY_TOO_LARGE", "Body exceeds the team-chat cap.", exitcode.Usage},
	} {
		for _, j := range []bool{false, true} {
			t.Run(tc.code+map[bool]string{false: "/text", true: "/json"}[j], func(t *testing.T) {
				srv, calls := attnServer(t, map[string]string{"CreateChannelMessage": chatPostRefusalRaw(tc.code, tc.message)})
				f, out := testFactory(t)
				stderr := f.IOStreams.ErrOut.(*strings.Builder)
				args := []string{"--server", srv.URL, "chat", "post", "--node", nodeURN1319, "--body", "hello"}
				if j {
					args = append(args, "--json")
				}
				root := NewRootCmd(f)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("refusal succeeded")
				}
				if code := renderError(f, err); code != tc.exit {
					t.Errorf("exit %d want %d", code, tc.exit)
				}
				if n := len(stopCalls(calls, "CreateChannelMessage")); n != 1 {
					t.Errorf("post calls %d want exactly one (no retry)", n)
				}
				if n := len(stopCalls(calls, "CreateChannel")); n != 0 {
					t.Errorf("a refusal must not create a channel, got %d CreateChannel calls", n)
				}
				if !j {
					if out.Len() != 0 || stderr.String() != "hadron: "+tc.message+"\n" {
						t.Errorf("streams stdout%q stderr%q", out.String(), stderr.String())
					}
					return
				}
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
					t.Errorf("serverCode lost: %s", out.String())
				}
				if stderr.Len() != 0 {
					t.Errorf("JSON stderr %q", stderr.String())
				}
			})
		}
	}
}

// CHANNEL_NOT_FOUND is discovery, not a refusal: it creates the Channel and
// posts once more, and succeeds. If that second post is itself refused, the
// refusal maps like any other and the post is not tried a third time.
func TestC1319ChannelNotFoundRetryIsNotARefusal(t *testing.T) {
	for _, tc := range []struct {
		name, second string
		wantExit     int
	}{
		{"retry succeeds", `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`, 0},
		{"retry refused as duplicate", chatPostRefusalRaw("TEAM_CHAT_DUPLICATE", "Duplicate message: earlier seq #42."), exitcode.Conflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			posts, creates := 0, 0
			gql, _ := captureGraphQLFunc(t, func(op string) string {
				switch op {
				case "CreateChannelMessage":
					posts++
					if posts == 1 {
						return `{"errors":[{"message":"no channel","extensions":{"code":"CHANNEL_NOT_FOUND"}}]}`
					}
					return tc.second
				case "CreateChannel":
					creates++
					return `{"data":{"createChannel":{"id":"c1"}}}`
				}
				return ""
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"chat", "post", "--node", nodeURN1319, "--body", "hi", "--json", "--server", gql.URL})
			err := root.Execute()
			got := 0
			if err != nil {
				got = exitCodeFor(err)
			}
			if got != tc.wantExit {
				t.Errorf("exit %d want %d (%v)", got, tc.wantExit, err)
			}
			if posts != 2 || creates != 1 {
				t.Errorf("posts=%d creates=%d, want exactly 2 posts and 1 create", posts, creates)
			}
		})
	}
}
