package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// chatMsg builds one findNodes hit for a ChatMessages response (the legacy
// data-only shape — the academy dialect the read side must keep accepting).
func chatMsg(loc string, seq int, data string) string {
	return `{"node":{"loc":"` + loc + `","seq":` + itoa(seq) + `,"data":` + data + `}}`
}

// chatMsgCanonical builds a hit in the canonical shape (D-2026-08-07-004):
// body in content, envelope in data.
func chatMsgCanonical(loc string, seq int, content, data string) string {
	c, _ := json.Marshal(content)
	return `{"node":{"loc":"` + loc + `","seq":` + itoa(seq) + `,"content":` + string(c) + `,"data":` + data + `}}`
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func chatMessagesResp(hits ...string) string {
	return `{"data":{"findNodes":{"hits":[` + strings.Join(hits, ",") + `]}}}`
}

// The server now emits flat grammar-v2 node URNs (#697); a user pasting one
// back as --node must resolve to the same (memory, prefix) as the legacy v1
// form — the memory canonicalizing to the flat hrn:mem: URN.
func TestChatReadAcceptsV2NodeURN(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 2, `{"author":"iris","body":"hi"}`),
		),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "hrn:node:acme.com:tc:chats:api:messages", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Filter struct {
			MemoryIds []string `json:"memoryIds"`
			LocPrefix string   `json:"locPrefix"`
		} `json:"filter"`
	}
	_ = json.Unmarshal(captured["ChatMessages"], &vars)
	if len(vars.Filter.MemoryIds) != 1 || vars.Filter.MemoryIds[0] != "hrn:mem:acme.com:tc" {
		t.Errorf("v2 --node memory filter: %+v", vars.Filter)
	}
	if vars.Filter.LocPrefix != "chats:api:messages:" {
		t.Errorf("v2 --node loc prefix, got %q", vars.Filter.LocPrefix)
	}
}

// #412: the prefix filter returns the message-PARENT container alongside the
// messages. Parsed as a message it reads `seq: null, author: "unknown",
// body: ""` — indistinguishable from a malformed post, and off-by-one on any
// count. It must not appear in the result set.
func TestChatReadExcludesTheMessageParentContainer(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		// The container as the server returns it: the asked-about loc itself,
		// no seq, no data. Listed last, as observed.
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 1, `{"author":"iris","body":"hi"}`),
			chatMsg("chats:api:messages:t2-rufus", 2, `{"author":"rufus","body":"yo"}`),
			`{"node":{"loc":"chats:api:messages","seq":null,"data":null}}`,
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// --since 0 is the full-history read a new session does on its first turn —
	// the only one that surfaces the container (a nil seq is dropped by
	// --since <n>, which is why this was easy to miss).
	root.SetArgs([]string{"chat", "read", "--node", "hrn:node:acme.com:tc:chats:api:messages",
		"--since", "0", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Messages []struct {
			Seq    *int   `json:"seq"`
			Loc    string `json:"loc"`
			Author string `json:"author"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("read --json: %v (%s)", err, out.String())
	}
	if len(dto.Messages) != 2 {
		t.Fatalf("a 2-message thread must read back as 2 entries, got %d: %s", len(dto.Messages), out.String())
	}
	for _, m := range dto.Messages {
		if m.Seq == nil {
			t.Errorf("no entry may have a nil seq — consumers sort and cursor on it: %+v", m)
		}
		if m.Author == "unknown" || m.Loc == "chats:api:messages" {
			t.Errorf("the container leaked into the messages: %+v", m)
		}
	}
}

// The read side accepts BOTH storage shapes in one chat: canonical (body in
// content — wins even when a legacy data.body is also present) and the
// retired academy dialect (data.body only).
func TestChatReadMixedShapes(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 1, `{"author":"iris","body":"legacy body"}`),
			chatMsgCanonical("chats:api:messages:t2-rufus", 2, "canonical body", `{"author":"rufus","sessionId":"s-1"}`),
			chatMsgCanonical("chats:api:messages:t3-iris", 3, "content wins", `{"author":"iris","body":"stale copy"}`),
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com::tc::chats:api:messages", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Messages []struct {
			Body      string `json:"body"`
			SessionID string `json:"sessionId"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(dto.Messages) != 3 || dto.Messages[0].Body != "legacy body" ||
		dto.Messages[1].Body != "canonical body" || dto.Messages[2].Body != "content wins" {
		t.Errorf("mixed-shape bodies: %s", out.String())
	}
	if dto.Messages[1].SessionID != "s-1" {
		t.Errorf("envelope fields must parse from data: %s", out.String())
	}
}

func TestChatReadJSON(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 2, `{"author":"iris","role":"Backend","body":"first"}`),
			chatMsg("chats:api:messages:t2-rufus", 5, `{"author":"rufus","body":"@iris second"}`),
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com::tc::chats:api:messages", "--since", "1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The findNodes filter must scope to the memory and the derived prefix.
	var vars struct {
		Filter struct {
			MemoryIds []string `json:"memoryIds"`
			LocPrefix string   `json:"locPrefix"`
		} `json:"filter"`
	}
	_ = json.Unmarshal(captured["ChatMessages"], &vars)
	if len(vars.Filter.MemoryIds) != 1 || vars.Filter.MemoryIds[0] != "hrn:mem:acme.com:tc" {
		t.Errorf("memory filter: %+v", vars.Filter)
	}
	if vars.Filter.LocPrefix != "chats:api:messages:" {
		t.Errorf("--node's loc should become the message prefix, got %q", vars.Filter.LocPrefix)
	}
	var dto struct {
		Messages []struct {
			Seq    int    `json:"seq"`
			Author string `json:"author"`
			Body   string `json:"body"`
		} `json:"messages"`
		NextSince int `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json invalid: %v\n%s", err, out.String())
	}
	if len(dto.Messages) != 2 || dto.Messages[0].Author != "iris" || dto.Messages[1].Body != "@iris second" {
		t.Errorf("messages: %+v", dto.Messages)
	}
	if dto.NextSince != 5 {
		t.Errorf("nextSince should be the max seq, got %d", dto.NextSince)
	}
}

// --since filters out messages at or below the given seq (client-side).
func TestChatReadSinceFilter(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 2, `{"author":"iris","body":"old"}`),
			chatMsg("chats:api:messages:t2-rufus", 5, `{"author":"rufus","body":"new"}`),
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com::tc::chats:api:messages", "--since", "3", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "old") || !strings.Contains(out.String(), "new") {
		t.Errorf("--since 3 should drop seq<=3, got %s", out.String())
	}
}

// The human transcript renders "[seq] author (role): body".
func TestChatReadTranscript(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"ChatMessages": chatMessagesResp(
			chatMsg("chats:api:messages:t1-iris", 2, `{"author":"iris","role":"Backend","body":"hello"}`),
		),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com::tc::chats:api:messages", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "[2] iris (Backend): hello") {
		t.Errorf("transcript format wrong: %s", out.String())
	}
}

const channelMsgJSON = `{"seq":7,"at":"2026-09-17T00:00:00Z","body":"@rufus schema looks good",
	"authorName":"Iris","authorWorkerId":"w1","authorUserId":null,"authorAppId":null,
	"sessionId":"s1","replyToSeq":null,"mentions":["rufus"],"nodeId":"n1"}`

// TestChatPostWritesThroughTheChannelAPI is the contract of #367.
//
// `chat post` must stop hand-rolling a node and call createChannelMessage. The
// old tests asserted the loc format, the nodeType and the data envelope — the
// CLI's own invention, which is precisely what #367 says the CLI should not
// own. Asserting them now would pin the defect.
//
// What replaces them: the ref sent is the CHAT ROOT (the parent of the
// messages container), derived not looked up.
func TestChatPostWritesThroughTheChannelAPI(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "@rufus schema looks good", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["CreateNode"]; ok {
		t.Error("the CLI must no longer hand-roll the message node")
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateChannelMessage"], &vars)
	// The chat ROOT, not the messages container: a channelRef names the chat
	// root's node (hadron-server#1172), which is the container's parent.
	if got := vars["channelRef"]; got != "hrn:node:acme.com:tc:chats:api" {
		t.Errorf("channelRef = %v, want the chat ROOT ref", got)
	}
	if vars["body"] != "@rufus schema looks good" {
		t.Errorf("body = %v", vars["body"])
	}
	// The author the SERVER recorded reaches --json, so an agent can verify
	// who it posted as rather than trusting its own request.
	var dto struct {
		Author *string `json:"author"`
		Seq    *int    `json:"seq"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("decode: %v — %s", err, out.String())
	}
	if dto.Author == nil || *dto.Author != "Iris" {
		t.Errorf("author = %v, want the server's value", dto.Author)
	}
}

// TestChatPostResolvesAnOpaqueMemoryID — the second half of @codex's P2.
//
// An opaque memory id has NO node-ref spelling to compose, so it cannot be
// handled by string work at all. It used to reach the server untouched through
// CreateNodeInput.MemoryId, so refusing it locally was a regression; it now
// costs exactly ONE read to become a canonical URN, and only this case pays it.
func TestChatPostResolvesAnOpaqueMemoryID(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory":            `{"data":{"memory":{"id":"01a0ba2e49647287a8d6981492bf7188","urn":"hrn:mem:acme.com:tc","name":"tc"}}}`,
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post",
		"-m", "01a0ba2e49647287a8d6981492bf7188", "--messages-loc", "chats:api:messages",
		"--body", "hi", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an opaque memory id must still post: %v", err)
	}
	if _, ok := captured["GetMemory"]; !ok {
		t.Fatal("an opaque memory id must be resolved to its URN, not refused locally")
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateChannelMessage"], &vars)
	// Composed from the RESOLVED urn, not from the id.
	if got := vars["channelRef"]; got != "hrn:node:acme.com:tc:chats:api" {
		t.Errorf("channelRef = %v, want it composed from the resolved memory URN", got)
	}
}

// ...and a named memory must NOT pay that read.
func TestChatPostDoesNotResolveANamedMemory(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "hi", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["GetMemory"]; ok {
		t.Error("a composable memory ref must not cost a resolve round trip")
	}
}

// TestChatPostConvergesOnAConcurrentChannelCreate — the create RACE.
//
// Two clients making the first post both see CHANNEL_NOT_FOUND and both try to
// create. One wins; the other is told LOC_OVERLAPS_CHANNEL. Returning that as
// the post's error loses a valid message *because someone else succeeded*,
// while the Channel the caller needs now exists. So the overlap is convergence
// and the post is retried.
//
// Found independently by @codex and Copilot on PR #618; neither the plan nor I
// anticipated it, because the single-client path this was tested on cannot
// reach it.
func TestChatPostConvergesOnAConcurrentChannelCreate(t *testing.T) {
	posts := 0
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "CreateChannelMessage":
			posts++
			if posts == 1 {
				return `{"errors":[{"message":"no channel","extensions":{"code":"CHANNEL_NOT_FOUND"}}]}`
			}
			return `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`
		case "CreateChannel":
			// The other client got there first.
			return `{"errors":[{"message":"address taken","extensions":{"code":"LOC_OVERLAPS_CHANNEL"}}]}`
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "hi", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a lost race must not fail the post: %v", err)
	}
	if posts != 2 {
		t.Errorf("the post must be retried after the winner created the Channel, posts=%d", posts)
	}
	if !strings.Contains(out.String(), `"seq": 7`) {
		t.Errorf("the message must actually be posted, got %s", out.String())
	}
}

// TestChatPostCreateFailureIsStillThePostsFailure — the other half of the
// convergence rule above, which is what stops it becoming "swallow create
// errors". An access refusal must still fail the post rather than falling
// through to a second CHANNEL_NOT_FOUND.
func TestChatPostCreateFailureIsStillThePostsFailure(t *testing.T) {
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "CreateChannelMessage":
			return `{"errors":[{"message":"no channel","extensions":{"code":"CHANNEL_NOT_FOUND"}}]}`
		case "CreateChannel":
			return `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`
		}
		return ""
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "hi", "--json", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a genuine create failure must fail the post")
	}
}

// TestChatPostJSONKeepsLocAsAnExplicitNull pins the one --json shape change
// #367 forces, in the only way that distinguishes the two ways it can go wrong.
//
// The client used to mint the message's loc and report it. The server mints it
// now and answers with nodeId instead, so `loc` cannot be filled. It is kept
// and answered NULL rather than dropped: an agent selecting `.loc` must be able
// to tell "this post had no address I can give you" from "this projection
// forgot the field" — the same call internal/cmd/channel makes for chatRootUrn.
//
// Decoding into a struct cannot see the difference (both yield a nil pointer),
// so this asserts over the raw key set. Drop the field and the presence check
// fails; fill it with "" and the null check fails.
func TestChatPostJSONKeepsLocAsAnExplicitNull(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "hi", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out.String()), &raw); err != nil {
		t.Fatalf("decode: %v — %s", err, out.String())
	}
	loc, present := raw["loc"]
	if !present {
		t.Fatal("`loc` must stay in the payload — an agent selecting it needs a null, not a missing key")
	}
	if string(loc) != "null" {
		t.Errorf("`loc` = %s, want null: the server owns the loc now", loc)
	}
	// nodeId is the address that replaces it, so it must actually carry the
	// server's value rather than being an empty placeholder.
	if got := string(raw["nodeId"]); got != `"n1"` {
		t.Errorf("nodeId = %s, want the server's node id", got)
	}
}

// TestChatPostCreatesTheChannelIfMissing — create-if-missing, and NOT
// best-effort. The node materialization this replaces swallowed every error;
// here a failure to create is the post's failure, because without a Channel
// there is nothing to post to.
func TestChatPostCreatesTheChannelIfMissing(t *testing.T) {
	calls := 0
	gql, captured := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "CreateChannelMessage":
			calls++
			if calls == 1 {
				return `{"errors":[{"message":"no channel","extensions":{"code":"CHANNEL_NOT_FOUND"}}]}`
			}
			return `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`
		case "CreateChannel":
			return `{"data":{"createChannel":{"id":"c1","name":"api","description":null,"kind":"CHAT",
				"loc":"chats:api","chatRootUrn":"hrn:node:acme.com:tc:chats:api","chatRootNodeId":"n0",
				"memoryId":"mem1","lastSeq":0,"lastMessageAt":null,"createdAt":"2026-09-17T00:00:00Z",
				"updatedAt":null,"memory":{"id":"mem1","urn":"hrn:mem:acme.com:tc","name":"tc"}}}}`
		}
		return ""
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "hi", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["CreateChannel"]; !ok {
		t.Error("a missing Channel must be created, not reported")
	}
	if calls != 2 {
		t.Errorf("the post must be retried after creating the Channel, calls=%d", calls)
	}
}

// TestChatPostResolvesAReplyLocToASeq — --reply-to has always taken a LOC and
// createChannelMessage takes a SEQ. Both are accepted; a loc costs one read.
// Refusing locs would break configs and scripts for a server-side spelling.
func TestChatPostResolvesAReplyLocToASeq(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetNode": `{"data":{"node":{"id":"n9","memoryId":"mem1","loc":"chats:api:messages:t1-rufus",
			"name":"m","nodeType":"chat-message","tags":[],"seq":4,"isRunnable":false,
			"updatedAt":"2026-06-21T00:00:00Z"}}}`,
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "ack", "--reply-to", "chats:api:messages:t1-rufus", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateChannelMessage"], &vars)
	if vars["replyToSeq"] != float64(4) {
		t.Errorf("replyToSeq = %v, want the target's seq (4)", vars["replyToSeq"])
	}
}

// TestChatPostAcceptsABareReplySeq — the native form must not cost a read.
func TestChatPostAcceptsABareReplySeq(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--body", "ack", "--reply-to", "4", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := captured["GetNode"]; ok {
		t.Error("a bare seq is already a seq — it must not cost a resolution")
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateChannelMessage"], &vars)
	if vars["replyToSeq"] != float64(4) {
		t.Errorf("replyToSeq = %v", vars["replyToSeq"])
	}
}

// TestChatPostDeprecatedFlagsAreAcceptedAndWarn — a live .hadron/config.json
// carrying handle/identity/role must not start failing, and "your flag did
// nothing" is only half an answer without naming what replaces it.
func TestChatPostDeprecatedFlagsAreAcceptedAndWarn(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, _, errOut := testFactoryTTY(t, "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages",
		"--handle", "iris", "--role", "Backend", "--body", "hi", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("deprecated flags must still be accepted: %v", err)
	}
	warn := errOut.String()
	if !strings.Contains(warn, "no longer affects the post") {
		t.Errorf("the deprecation must be announced, got: %q", warn)
	}
	if !strings.Contains(warn, "--session") {
		t.Errorf("the warning must name the replacement, got: %q", warn)
	}
}

func TestChatPostBodyFileMissingIsUsageError(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--handle", "iris",
		"--body-file", filepath.Join(t.TempDir(), "nope.md"), "--server", gql.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want Usage; err %v", code, err)
	}
	if len(captured) != 0 {
		t.Errorf("must refuse before any request; sent %v", captured)
	}
	if err == nil || !strings.Contains(err.Error(), "--body-file") {
		t.Errorf("message should name the flag: %v", err)
	}
}

// --body and --body-file are mutually exclusive; neither is a usage error.
func TestChatPostBodySourceExclusive(t *testing.T) {
	cases := [][]string{
		{"--body", "hi", "--body-file", "/tmp/x"}, // both
		{}, // neither
	}
	for _, extra := range cases {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := append([]string{"chat", "post", "--node", "acme.com::tc::chats:api:messages", "--handle", "iris"}, extra...)
		root.SetArgs(append(args, "--server", "http://127.0.0.1:1"))
		if err := root.Execute(); err == nil {
			t.Fatalf("expected a usage error for body args %v", extra)
		}
	}
}

// A single chat.node URN in config supplies both memory and message location.
func TestChatPostUsesNodeConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hadron"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"handle":"iris","chat":{"node":"acme.com::tc::team-chat:api:messages","role":"Backend"}}`
	if err := os.WriteFile(filepath.Join(dir, ".hadron", "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	gql, captured := captureGraphQL(t, map[string]string{
		"CreateChannelMessage": `{"data":{"createChannelMessage":` + channelMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "post", "--body", "hi", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateChannelMessage"], &vars)
	// chat.node in config supplies both memory and message location; the ref
	// sent is still the chat ROOT derived from them.
	if got := vars["channelRef"]; got != "hrn:node:acme.com:tc:team-chat:api" {
		t.Errorf("channelRef = %v, want the chat ROOT from chat.node", got)
	}
}

// --node packs memory + loc, so it's mutually exclusive with -m / --messages-loc.
func TestChatNodeExclusiveWithMemory(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "a::b::c:d", "-m", "a::b", "--server", "http://127.0.0.1:1"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected mutual-exclusivity error for --node with -m")
	}
}

func TestChatNodeRejectsAmbiguousSingleColonURN(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"chat", "read", "--node", "acme.com:tc:chats:api:messages", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "not a fully-qualified node URN") {
		t.Fatalf("expected ambiguous single-colon node URN rejection, got %v", err)
	}
}

// A test asserting that post best-effort materializes the message-parent node
// (nodeType chat) stood here. The CLI no longer materializes the chat's node
// structure by hand — the server owns it. What replaces that guarantee is
// create-if-missing, covered by TestChatPostCreatesTheChannelIfMissing. The old
// test would now pin the defect #367 exists to remove, so it is gone rather
// than adapted.

func TestChatReadRequiresCoords(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// --messages-loc without -m/--node and (in the test's cwd) no config → error.
	root.SetArgs([]string{"chat", "read", "--messages-loc", "chats:api:messages", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no chat") {
		t.Fatalf("expected missing-coordinates usage error, got %v", err)
	}
}
