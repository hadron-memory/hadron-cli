package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#727 — the server's LOC_PROTECTED refusal exits 2 in all three of its
// remedy classes (Holger, team chat #1697). The envelopes are LocProtectedError's
// (hadron-server src/lib/protectedLoc.ts at origin/main c226af5), rebuilt from
// its message template and extensions, not captured live. Each keeps its own
// remedy in the message, which must survive the mapping.

func locProtectedJSON(message, op string, live bool) string {
	liveJSON := "true"
	if !live {
		liveJSON = "false"
	}
	return `{"errors":[{"message":"` + message + `","extensions":{"code":"LOC_PROTECTED","loc":"chats:team:messages:m1","protectedLoc":"chats:team","channelLoc":"chats:team","channelId":"ch1","owner":"createTeamChatMessage / hadron_team_chat_post","channelLive":` + liveJSON + `,"op":"` + op + `"}}]}`
}

func TestLocProtectedRefusalExitsTwo(t *testing.T) {
	cases := []struct {
		name      string
		responses map[string]string
		args      []string
		keep      string
	}{
		{
			// A live Channel, a Channel-backed op: the remedy is a different tool.
			name: "live Channel, wrong tool (node add)",
			responses: map[string]string{
				"CreateNode": locProtectedJSON(`\"chats:team:messages:m1\" is protected: it is the address of Channel \"team\" (chats:team), which only its own operations may create. Post with createTeamChatMessage / hadron_team_chat_post; the generic node surface does not reach it.`, "create", true),
			},
			args: []string{"node", "add", "-m", "acme.com::kb", "--loc", "chats:team:messages:m1", "--name", "X"},
			keep: "createTeamChatMessage",
		},
		{
			// A live Channel, any other op: there is no route at all.
			name: "live Channel, no route (node rm)",
			responses: map[string]string{
				"ResolveUrn": resolveNodeJSON,
				"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
				"DeleteNode": locProtectedJSON(`\"chats:team:messages:m1\" is protected: it is the address of Channel \"team\" (chats:team), which only its own operations may delete. There is no operation that may delete here — the Channel's own operation posts messages, so this operation has no bypass.`, "delete", true),
			},
			args: []string{"node", "rm", nodeURN, "--yes"},
			keep: "no operation that may delete here",
		},
		{
			// A deleted Channel's reservation: the remedy is recreating it.
			name: "deleted Channel reservation (node update)",
			responses: map[string]string{
				"ResolveUrn": resolveNodeJSON,
				"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
				"UpdateNode": locProtectedJSON(`\"chats:team:messages:m1\" is protected: it is the address of the DELETED Channel \"team\" (chats:team). The Channel is DELETED, so none of its operations can be reached — the address stays reserved so it cannot be occupied and then restored over. Recreate the Channel (createChannel) to write here, or delete what it orphaned.`, "update", false),
			},
			args: []string{"node", "update", nodeURN, "--description", "d"},
			keep: "Recreate the Channel",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, tc.responses)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--server", gql.URL))
			err := root.Execute()
			if got := exitCodeFor(err); got != exitcode.Usage {
				t.Fatalf("LOC_PROTECTED must exit %d, got %d (%v)", exitcode.Usage, got, err)
			}
			if !strings.Contains(err.Error(), tc.keep) {
				t.Errorf("the server's remedy must survive the mapping (want %q): %v", tc.keep, err)
			}
		})
	}
}

// `hadron api` shares the mapper, so the raw path exits 2 as well and prints
// the envelope with its extensions, channelLive included.
func TestRawAPILocProtectedExitsTwo(t *testing.T) {
	gql := graphQLAlways(t, http.StatusOK, locProtectedJSON(`\"chats:team:messages:m1\" is protected: it is the address of the DELETED Channel \"team\" (chats:team). The Channel is DELETED, so none of its operations can be reached — the address stays reserved so it cannot be occupied and then restored over. Recreate the Channel (createChannel) to write here, or delete what it orphaned.`, "update", false))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"api", "{ __typename }", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Errorf("`hadron api` must exit %d on LOC_PROTECTED, got %d", exitcode.Usage, got)
	}
}
