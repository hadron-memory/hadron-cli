package chat

import (
	"context"
	"encoding/json"
	"testing"
)

// TestParseMessageAuthorPrecedence pins #630: the author comes from the
// envelope the PRODUCER writes, and the loc is the last resort.
//
// The defect this replaces was not the dashed-handle edge case — that was the
// symptom loud enough to notice. `createChannelMessage` writes
// `data.authorName`; this reader decoded only `data.author`, the retired
// academy key, so EVERY server-minted message fell through to the loc and was
// rendered as its lowercase mention token instead of the recorded name.
// Measured on a production node: `authorName: "Jonas"`, rendered `jonas`.
func TestParseMessageAuthorPrecedence(t *testing.T) {
	raw := func(s string) *json.RawMessage { m := json.RawMessage(s); return &m }
	body := "hi"
	cases := []struct {
		name string
		loc  string
		data *json.RawMessage
		want string
	}{
		{
			// The case that was wrong on every message: the envelope has the
			// real name and the loc has a slug. Capital J is the whole point.
			name: "envelope authorName wins over the loc slug",
			loc:  "chats:api:messages:002-279bba33-jonas",
			data: raw(`{"authorName":"Jonas","authorWorkerId":"w1","sessionId":"s1"}`),
			want: "Jonas",
		},
		{
			// And it wins even where the loc would have produced a DIFFERENT
			// person, which is the dashed-handle bug rendered harmless.
			name: "envelope authorName wins over a misparsable loc",
			loc:  "chats:api:messages:002-279bba33-mary-jane",
			data: raw(`{"authorName":"Mary Jane"}`),
			want: "Mary Jane",
		},
		{
			// The retired academy dialect still reads.
			name: "legacy data.author when there is no authorName",
			loc:  "chats:api:messages:2026-09-19T150000000Z-rufus",
			data: raw(`{"author":"rufus","role":"Backend Engineer"}`),
			want: "rufus",
		},
		{
			// authorName outranks a stale academy `author` on the same node.
			name: "authorName outranks data.author",
			loc:  "chats:api:messages:001-abcdef12-x",
			data: raw(`{"authorName":"Vera","author":"stale"}`),
			want: "Vera",
		},
		{
			// The last resort, doing the job it was written for.
			name: "loc only when the envelope has no author at all",
			loc:  "chats:api:messages:2026-09-19T150000000Z-mary-jane",
			data: raw(`{"role":"Backend Engineer"}`),
			want: "mary-jane",
		},
		{
			name: "no data at all",
			loc:  "chats:api:messages:001-abcdef12-iris",
			data: nil,
			want: "iris",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseMessage(c.loc, nil, &body, c.data)
			if got.Author != c.want {
				t.Errorf("Author = %q, want %q", got.Author, c.want)
			}
		})
	}
}

// The envelope's identity fields reach --json, so a consumer can tell a WORKER
// post from a human one — the distinction the Worker model exists to record,
// and which this reader dropped entirely.
func TestParseMessageCarriesTheEnvelopeIdentity(t *testing.T) {
	body := "hi"
	d := json.RawMessage(`{"authorName":"Jonas","authorWorkerId":"wkr1",
		"authorAppId":"app1","sessionId":"s1"}`)
	m := parseMessage("chats:api:messages:002-279bba33-jonas", nil, &body, &d)
	for _, f := range []struct{ name, got, want string }{
		{"AuthorWorkerID", m.AuthorWorkerID, "wkr1"},
		{"AuthorAppID", m.AuthorAppID, "app1"},
		{"SessionID", m.SessionID, "s1"},
	} {
		if f.got != f.want {
			t.Errorf("%s = %q, want %q", f.name, f.got, f.want)
		}
	}
	// A human post carries authorUserId instead; neither is invented.
	if m.AuthorUserID != "" {
		t.Errorf("AuthorUserID = %q, want empty — the node carries none", m.AuthorUserID)
	}
}

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
// "<ordinal>-<8hex>-<handle>".
//
// CORRECTED by #630: the sentence that stood here — "writes no `data.author`,
// so this fallback now runs for every newly posted message" — was true about
// `author` and false about what it implied. The server writes
// `data.authorName`, and the reader simply did not decode it. So this fallback
// runs for the legacy rows it was always for, and the cases below are
// defence-in-depth rather than the live path. Left pinned because those rows
// exist and still reach it.
//
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
