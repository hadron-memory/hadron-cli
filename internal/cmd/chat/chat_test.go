package chat

import (
	"context"
	"testing"
)

// TestChatRootRefPreservesEveryComposableMemorySpelling — one case per memory
// spelling `chat post` accepts, because a hand-rolled composer that handles
// only the common one narrows the command SILENTLY.
//
// The regression @codex caught on PR #618: chatRootRef called cmdutil.NodeURN,
// which composes only a flat-v2 <root>:<slug> memory and returns "" otherwise.
// A COMPOUND app-mem memory cannot be a fixed-arity flat node URN at all, so
// `chat post` began refusing one locally — with no request made — that had
// previously passed straight through to the server in CreateNodeInput.MemoryId.
// cmdutil.BatchNodeRef is the shared composer that already knew this.
//
// An opaque memory id is NOT here: it has no spelling to compose and costs a
// server read, which is covered at the command level instead.
func TestChatRootRefPreservesEveryComposableMemorySpelling(t *testing.T) {
	cases := []struct {
		name   string
		memory string
		want   string
	}{
		{"flat v2 URN", "hrn:mem:acme.com:tc", "hrn:node:acme.com:tc:chats:api"},
		{"legacy double-colon pair", "acme.com::tc", "hrn:node:acme.com:tc:chats:api"},
		{"single-colon pair", "acme.com:tc", "hrn:node:acme.com:tc:chats:api"},
		{
			// The one NodeURN cannot express; BatchNodeRef joins the legacy
			// <memory>::<loc> form, which the server accepts forever (#239).
			"compound app-mem",
			"acme.com::myagent:app-mem:slug",
			"hrn:node:acme.com::myagent:app-mem:slug::chats:api",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := chatRootRef(context.Background(), nil,
				Coords{Memory: c.memory, MessagesLoc: "chats:api:messages"})
			if err != nil {
				t.Fatalf("%s must still compose, got %v", c.memory, err)
			}
			if got != c.want {
				t.Errorf("chatRootRef(%q) = %q, want %q", c.memory, got, c.want)
			}
		})
	}
}

// A messages loc with no parent has no chat root, and guessing one would create
// a Channel at the wrong address — so it is refused rather than defaulted.
func TestChatRootRefRefusesAMessagesLocWithNoParent(t *testing.T) {
	if _, err := chatRootRef(context.Background(), nil,
		Coords{Memory: "hrn:mem:acme.com:tc", MessagesLoc: "messages"}); err == nil {
		t.Error("a messages loc with no parent must be refused")
	}
}

// TestAuthorFromLocReadsAllThreeLocDialects pins the fallback author parser
// against every loc shape `chat read` can meet, and exists because #367 made
// one of them reachable for the first time.
//
// Moving the write path onto Channels means the SERVER mints the loc, as
// "<ordinal>-<8hex>-<handle>", and writes no `data.author` — so this fallback
// now runs for every newly posted message rather than only for legacy rows.
// The pre-#367 last-dash fallback reads 002-279bba33-mary-jane as "jane":
// a message attributed to a person who does not exist, with nothing to
// indicate it. The client-minted dialect never had that failure because its
// "Z-" terminator bounded the handle.
//
// Dashed handles are ordinary data, not a contrived case — a worker name with
// a space slugs to one ("Mary Jane" → mary-jane), which is the same
// mentionTokenOf rule the team chat uses.
func TestAuthorFromLocReadsAllThreeLocDialects(t *testing.T) {
	cases := []struct {
		name string
		loc  string
		want string
	}{
		{
			name: "server-minted, plain handle",
			loc:  "chats:api:messages:002-279bba33-jonas",
			want: "jonas",
		},
		{
			// The case the last-dash fallback gets wrong.
			name: "server-minted, dashed handle",
			loc:  "chats:api:messages:002-279bba33-mary-jane",
			want: "mary-jane",
		},
		{
			name: "server-minted, three-digit ordinal",
			loc:  "chats:api:messages:1024-0a1b2c3d-iris",
			want: "iris",
		},
		{
			name: "client-minted academy dialect, dashed handle",
			loc:  "chats:api:messages:2026-09-19T150000000Z-mary-jane",
			want: "mary-jane",
		},
		{
			name: "legacy stamp with no Z terminator",
			loc:  "chats:api:messages:20260919150000-iris",
			want: "iris",
		},
		{
			name: "nothing parseable",
			loc:  "chats:api:messages:opaque",
			want: "unknown",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := authorFromLoc(c.loc); got != c.want {
				t.Errorf("authorFromLoc(%q) = %q, want %q", c.loc, got, c.want)
			}
		})
	}
}

// A client-minted timestamp must not be mistaken for a server-minted loc: the
// two share a leading run of digits, and only the 8-hex middle tells them
// apart. Were the ordinal pattern loosened to accept the timestamp, the handle
// would come back with the rest of the stamp glued to its front.
func TestServerMintedLocPatternDoesNotSwallowATimestamp(t *testing.T) {
	if serverMintedLocRE.MatchString("2026-09-19T150000000Z-mary-jane") {
		t.Error("the client-minted timestamp dialect must not match the server-minted pattern")
	}
}
