package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

const channelJSON = `{
	"id":"0123456789abcdef0123456789abcdef","name":"team","description":null,"kind":"CHAT",
	"loc":"chats:team","chatRootUrn":"hrn:node:acme.com:team-shared:chats:team",
	"chatRootNodeId":"n1","memoryId":"m1","lastSeq":42,"lastMessageAt":"2026-09-16T00:00:00Z",
	"createdAt":"2026-09-01T00:00:00Z","updatedAt":null,
	"memory":{"id":"m1","urn":"hrn:mem:acme.com:team-shared","name":"Team shared"}}`

// channelNoAddressJSON is the case hadron-server#1172 warns about: a host
// memory whose stored URN predates the flat grammar, for which the server
// advertises NO address rather than one it would itself reject.
const channelNoAddressJSON = `{
	"id":"ffffffffffffffffffffffffffffffff","name":"legacy","description":null,"kind":"CHAT",
	"loc":"chats:team","chatRootUrn":null,
	"chatRootNodeId":"n2","memoryId":"m2","lastSeq":1,"lastMessageAt":null,
	"createdAt":"2026-09-01T00:00:00Z","updatedAt":null,
	"memory":{"id":"m2","urn":"hrn:mem:acme.com:legacy","name":"Legacy"}}`

// TestChannelRefIsPassedThroughUnexamined is the point of the whole package.
//
// hadron-server#1172 made channelRef accept BOTH an id and the chat root's node
// ref, at every site. So this client must not classify — an id, an address, and
// any spelling the node resolver takes must all reach the wire verbatim. A
// client-side matcher would be a third copy of one addressing rule beside the
// portal's and MCP's.
func TestChannelRefIsPassedThroughUnexamined(t *testing.T) {
	refs := []string{
		"0123456789abcdef0123456789abcdef",
		"hrn:node:acme.com:team-shared:chats:team",
		"urn:node:acme.com:team-shared:chats:team",
		"acme.com::team-shared::chats:team",
		"not-a-real-ref",
	}
	for _, ref := range refs {
		t.Run(ref, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"GetChannel": `{"data":{"channel":` + channelJSON + `}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"channel", "get", ref, "--json", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["GetChannel"], &vars)
			if vars["ref"] != ref {
				t.Errorf("ref reached the wire as %v, want %q verbatim", vars["ref"], ref)
			}
		})
	}
}

// TestChannelListPrintsWhatGetAccepts — the inconsistency #593 refused to ship.
//
// The ADDRESS column must carry the same string `channel get` takes, so a
// reader can copy one into the other. Asserted by driving the round trip: the
// address printed by list is fed straight back to get.
func TestChannelListPrintsWhatGetAccepts(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Channels": `{"data":{"channels":{"total":1,"items":[` + channelJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var listed []struct {
		ChatRootURN *string `json:"chatRootUrn"`
	}
	if err := json.Unmarshal([]byte(out.String()), &listed); err != nil {
		t.Fatalf("decode: %v — %s", err, out.String())
	}
	if len(listed) != 1 || listed[0].ChatRootURN == nil {
		t.Fatalf("list must emit the address: %s", out.String())
	}

	gql2, captured := captureGraphQL(t, map[string]string{
		"GetChannel": `{"data":{"channel":` + channelJSON + `}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"channel", "get", *listed[0].ChatRootURN, "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("the address list printed must be accepted by get: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["GetChannel"], &vars)
	if vars["ref"] != *listed[0].ChatRootURN {
		t.Errorf("round trip changed the ref: %v", vars["ref"])
	}
}

// TestChannelWithoutAnAddressStillWorks — #1172's nullable case.
//
// A Channel whose host memory predates the flat grammar has NO advertised
// address. Nothing may require one: --json must report null (not omit the key,
// which is indistinguishable from "not projected"), and the human output must
// explain the gap rather than leaving a blank column.
func TestChannelWithoutAnAddressStillWorks(t *testing.T) {
	t.Run("json reports null, not a missing key", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"GetChannel": `{"data":{"channel":` + channelNoAddressJSON + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "get", "ffffffffffffffffffffffffffffffff", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
			t.Fatalf("decode: %v", err)
		}
		v, present := raw["chatRootUrn"]
		if !present {
			t.Fatal("chatRootUrn must be PRESENT and null — an absent key reads as 'not projected'")
		}
		if string(v) != "null" {
			t.Errorf("chatRootUrn = %s, want null", v)
		}
	})

	t.Run("human output explains the gap", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"Channels": `{"data":{"channels":{"total":2,"items":[` + channelJSON + `,` + channelNoAddressJSON + `]}}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "list", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		got := out.String()
		if !strings.Contains(got, "no address the server will advertise") {
			t.Errorf("a blank ADDRESS cell must be explained, got:\n%s", got)
		}
		if !strings.Contains(got, "ID column") {
			t.Errorf("the note must name the remedy, got:\n%s", got)
		}
	})
}

// TestChannelUnreadableSaysOnlyWhatIsKnown — the server answers "does not
// exist", "names nothing" and "you may not read its host memory" with the SAME
// null, so it is never an existence oracle. The message must not claim more.
func TestChannelUnreadableSaysOnlyWhatIsKnown(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"GetChannel": `{"data":{"channel":null}}`})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "get", "whatever", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "readable") {
		t.Errorf("message should assert only unreadability, got: %v", err)
	}
	for _, forbidden := range []string{"does not exist", "not found"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Errorf("must not claim non-existence the server declined to disclose, got: %v", err)
		}
	}
}

// TestChannelUpdateOmitsUnsetFields — an omitted field preserves, an explicit
// null clears, and a decode cannot tell an absent key from a null one.
func TestChannelUpdateOmitsUnsetFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateChannel": `{"data":{"updateChannel":` + channelJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{
		"channel", "update", "hrn:node:acme.com:team-shared:chats:team",
		"--name", "daily", "--json", "--server", gql.URL,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw := string(captured["UpdateChannel"])
	if !strings.Contains(raw, `"name"`) {
		t.Errorf("the field that WAS passed must be present: %s", raw)
	}
	if strings.Contains(raw, `"description"`) {
		t.Errorf("unset description must be OMITTED, not sent (it would clear the field): %s", raw)
	}
}

// TestChannelLocalValidationPrecedesCredentials — a missing flag must report the
// usage error, not AuthRequired from resolving a client it never needed.
func TestChannelLocalValidationPrecedesCredentials(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"create/no-memory", []string{"channel", "create", "x", "--loc", "chats:x"}, "hosted in a memory"},
		{"create/no-loc", []string{"channel", "create", "x", "-m", "hrn:mem:acme.com:kb"}, "--loc"},
		{"update/nothing-to-do", []string{"channel", "update", "some-ref"}, "nothing to update"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HADRON_TOKEN", "")
			f, _ := testFactory(t)
			t.Setenv("HADRON_TOKEN", "")
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a usage refusal")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("want local usage error %q, got: %v", tc.want, err)
			}
		})
	}
}
