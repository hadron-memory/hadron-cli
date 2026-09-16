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

// TestChannelRmReportsAFailedDelete — @codex on #598.
//
// deleteChannel returns a BOOLEAN; false means nothing was deleted (an unknown
// ref, or one already gone). Discarding it reported success with exit 0, so
// automation would treat a failed delete as a done one. `schedule rm` and
// `webhook rm` check theirs — `org rm`, which I copied, does not.
func TestChannelRmReportsAFailedDelete(t *testing.T) {
	t.Run("false is a failure", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"DeleteChannel": `{"data":{"deleteChannel":false}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "rm", "whatever", "--yes", "--server", gql.URL})
		err := root.Execute()
		if err == nil {
			t.Fatal("a false delete result must not report success")
		}
		if strings.Contains(out.String(), "deleted") {
			t.Errorf("nothing was deleted, so nothing may claim it was: %s", out.String())
		}
	})

	t.Run("true is a success", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"DeleteChannel": `{"data":{"deleteChannel":true}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "rm", "whatever", "--yes", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(out.String(), "deleted") {
			t.Errorf("a true result should confirm: %s", out.String())
		}
	})
}

const messageJSON = `{"seq":631,"at":"2026-09-16T14:00:00Z","body":"hello",
	"authorName":"Jonas","authorWorkerId":"w1","authorUserId":null,"authorAppId":null,
	"sessionId":"s1","replyToSeq":null,"mentions":[],"nodeId":"n9"}`

// TestChannelPostRefusesAmbiguousAuthorship is the one that matters most here.
//
// `sessionRef` is OPTIONAL on the wire, and omitting it records the HUMAN as
// the author — no error, wrong authorship, indistinguishable from success. The
// CLI is the only place that can be caught, so authorship is made explicit:
// --session or --as-me, never a silent default.
func TestChannelPostRefusesAmbiguousAuthorship(t *testing.T) {
	t.Run("neither is refused before the round trip", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "post", "some-ref", "hi", "--server", gql.URL})
		err := root.Execute()
		if err == nil {
			t.Fatal("posting with no authorship stated must be refused")
		}
		if !strings.Contains(err.Error(), "--session") || !strings.Contains(err.Error(), "--as-me") {
			t.Errorf("the refusal must name both ways to say who is posting, got: %v", err)
		}
		if _, ok := captured["CreateChannelMessage"]; ok {
			t.Error("nothing may be posted while authorship is ambiguous")
		}
	})

	t.Run("both is refused", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "post", "r", "hi", "--session", "s1", "--as-me", "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Fatal("--session and --as-me are mutually exclusive")
		}
	})

	t.Run("--session reaches the wire", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"CreateChannelMessage": `{"data":{"createChannelMessage":` + messageJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "post", "r", "hi", "--session", "01a0a78c", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["CreateChannelMessage"], &vars)
		if vars["sessionRef"] != "01a0a78c" {
			t.Errorf("sessionRef = %v — without it the post is recorded as the human", vars["sessionRef"])
		}
	})

	t.Run("--as-me omits sessionRef rather than sending null", func(t *testing.T) {
		gql, captured := captureGraphQL(t, map[string]string{
			"CreateChannelMessage": `{"data":{"createChannelMessage":` + messageJSON + `}}`,
		})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"channel", "post", "r", "hi", "--as-me", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if strings.Contains(string(captured["CreateChannelMessage"]), `"sessionRef"`) {
			t.Errorf("--as-me must omit sessionRef: %s", captured["CreateChannelMessage"])
		}
	})
}

// TestChannelReadReportsTheNextWatermark.
//
// sinceSeq is STRICTLY GREATER, so the next watermark is the LAST seq seen, not
// one past it. Emitting it removes the one arithmetic every polling consumer
// would otherwise reinvent and get wrong at exactly that boundary.
func TestChannelReadReportsTheNextWatermark(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ChannelMessages": `{"data":{"channelMessages":{"total":1,"items":[` + messageJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "read", "r", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		NextSince *int `json:"nextSince"`
		Messages  []struct {
			Seq int `json:"seq"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.NextSince == nil || *dto.NextSince != 631 {
		t.Errorf("nextSince = %v, want 631 — the LAST seq seen, since --since is strictly greater", dto.NextSince)
	}
}

// TestChannelReadRefusesBeforeWithOffset — the server IGNORES offset when
// before is given, so accepting both would silently drop one a caller believed
// applied.
func TestChannelReadRefusesBeforeWithOffset(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "read", "r", "--before", "100", "--offset", "5", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("--before with --offset must be refused, not silently half-applied")
	}
	if _, ok := captured["ChannelMessages"]; ok {
		t.Error("the refusal should precede the round trip")
	}
}

// TestChannelReadMarksACrossAppPost — spec 049 item J marks a post that crossed
// Apps. A transcript that drops the mark misrepresents who was speaking where.
func TestChannelReadMarksACrossAppPost(t *testing.T) {
	crossApp := strings.Replace(messageJSON, `"authorAppId":null`, `"authorAppId":"app-other"`, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"ChannelMessages": `{"data":{"channelMessages":{"total":1,"items":[` + crossApp + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "read", "r", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "cross-App") {
		t.Errorf("a cross-App post must be marked, got:\n%s", out.String())
	}
}

// TestChannelMarkReadReportsTheServerCursor.
//
// advanceChannelReadState is MONOTONIC: a lower seq is a no-op returning the
// cursor unchanged. Echoing the REQUESTED seq would report a rewind that did
// not happen, so the output states what the server ended up with.
func TestChannelMarkReadReportsTheServerCursor(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"AdvanceChannelReadState": `{"data":{"advanceChannelReadState":{"channelId":"c1",
			"attendeeUrn":"hrn:worker:acme.com:team:jonas","lastSeenSeq":900,
			"updatedAt":"2026-09-16T14:00:00Z"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Ask for 100 while the server is already at 900 — the no-op case.
	root.SetArgs([]string{"channel", "mark-read", "r", "--attendee", "Jonas", "--seq", "100", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "900") {
		t.Errorf("must report the cursor the SERVER holds, got:\n%s", got)
	}
	if !strings.Contains(got, "unchanged") {
		t.Errorf("a monotonic no-op must be described as one, got:\n%s", got)
	}
}

// TestChannelReadStateAbsentIsNotAnError — "has read nothing" is a real answer;
// reporting it as an error makes it indistinguishable from "unreadable".
func TestChannelReadStateAbsentIsNotAnError(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ChannelReadState": `{"data":{"channelReadState":null}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"channel", "read-state", "r", "--attendee", "Jonas", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an absent cursor is a real answer, not an error: %v", err)
	}
	if !strings.Contains(out.String(), "seen nothing") {
		t.Errorf("say so plainly, got:\n%s", out.String())
	}
}
