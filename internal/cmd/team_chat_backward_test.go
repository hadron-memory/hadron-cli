package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #548 — `team chat read` gains a BACKWARD cursor.
//
// `beforeSeq` shipped server-side in hadron-server#1116 on three surfaces and
// the CLI had none of it, so the whole history was reachable only by asking for
// all of it at once. On a team chat with real history that is the difference
// between a read you can hold and one you cannot — @Ada's `sinceSeq: 0` read
// exceeded the MCP result ceiling and had to be paged out of a file.

// chatMsg builds a message fixture at a given seq.
func teamChatMsgAt(seq int) string {
	return fmt.Sprintf(`{"nodeId":"n%d","seq":%d,"body":"m%d","at":"2026-08-12T10:00:00Z",
		"authorUserId":null,"authorWorkerId":"wkr1","authorName":"Iris","sessionId":"s-new",
		"replyToSeq":null,"mentions":[]}`, seq, seq, seq)
}

// chatPage renders a page. `total` is deliberately a value the test CHOOSES
// rather than len(items): the server scopes it to the cursor under beforeSeq
// (hadron-server#1121), so a fixture that always made it agree with the page
// could not express the shape a client must not trust.
func teamChatPage(total int, seqs ...int) string {
	items := make([]string, 0, len(seqs))
	for _, s := range seqs {
		items = append(items, teamChatMsgAt(s))
	}
	return fmt.Sprintf(`{"data":{"teamChatMessages":{"total":%d,"items":[%s]}}}`,
		total, strings.Join(items, ","))
}

// chatVars records the paging arguments of every TeamChatMessages call, in
// order, so a test can assert what was ASKED rather than only what came back.
type chatVars struct {
	SinceSeq        *int   `json:"sinceSeq"`
	BeforeSeq       *int   `json:"beforeSeq"`
	Limit           *int   `json:"limit"`
	SinceSeqPresent bool   `json:"-"`
	Session         string `json:"-"`
	Query           string `json:"-"`
}

// chatServer answers TeamChatMessages from a queue of pages and records the
// variables of each call.
func chatServer(t *testing.T, pages ...string) (*httptest.Server, *[]chatVars) {
	t.Helper()
	seen := &[]chatVars{}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
			Query         string          `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		body.OperationName = legacyChatReadOperation(body.OperationName)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TeamChatMessages":
			var vars chatVars
			_ = json.Unmarshal(body.Variables, &vars)
			vars.Session = r.Header.Get("X-Hadron-Session")
			vars.Query = body.Query
			var raw map[string]json.RawMessage
			_ = json.Unmarshal(body.Variables, &raw)
			_, vars.SinceSeqPresent = raw["sinceSeq"]
			*seen = append(*seen, vars)
			if i < len(pages) {
				_, _ = w.Write([]byte(pages[i]))
				i++
				return
			}
			// Running off the end is a TEST bug — an exhaustive loop that did
			// not terminate — so say so rather than quietly answering empty and
			// letting the loop look bounded.
			t.Errorf("TeamChatMessages called %d times, only %d pages queued", i+1, len(pages))
			_, _ = w.Write([]byte(teamChatPage(0)))
		case "TeamChatReadHead", "TeamChatReadMetadata":
			_, _ = w.Write([]byte(teamReadHeadFixture(pages...)))
		case "ChannelReadState":
			resp, _ := unstubbedDefault(body.OperationName)
			_, _ = w.Write([]byte(resp))
		case "TeamAppIdentity":
			_, _ = w.Write([]byte(teamAppIdentityJSON))
		case "TeamDefaultChannel", "MarkOwnTeamChatRead":
			resp, _ := unstubbedDefault(body.OperationName)
			_, _ = w.Write([]byte(resp))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, seen
}

// --before forwards the cursor and returns ONE page, and --json hands back
// prevBefore — the lowest seq seen — as the cursor for the page before it.
func TestTeamChatReadBeforeWalksBackOnePage(t *testing.T) {
	srv, seen := chatServer(t, teamChatPage(399, 397, 398, 399))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--before", "400", "--limit", "3", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("a bounded read is ONE page, got %d calls", len(*seen))
	}
	if got := (*seen)[0].BeforeSeq; got == nil || *got != 400 {
		t.Errorf("beforeSeq must ride to the server, got %v", got)
	}
	if (*seen)[0].SinceSeqPresent {
		t.Errorf("--before without --since must omit sinceSeq, got %+v", (*seen)[0])
	}
	if got := (*seen)[0].Limit; got == nil || *got != 3 {
		t.Errorf("--limit must set the page size, got %v", got)
	}
	var dto struct {
		Messages   []struct{ Seq int } `json:"messages"`
		NextSince  int                 `json:"nextSince"`
		PrevBefore *int                `json:"prevBefore"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	// The LOWEST seq, not the highest: it is the cursor for the page BEFORE
	// this one, and nextSince (the highest) already answers the other
	// direction. Getting these the same way round is the whole bug class.
	if dto.PrevBefore == nil || *dto.PrevBefore != 397 {
		t.Errorf("prevBefore must be the lowest seq returned, got %v", dto.PrevBefore)
	}
	if dto.NextSince != 399 {
		t.Errorf("nextSince must still be the highest, got %d", dto.NextSince)
	}
}

// --before ALONE bounds the read, and this is the only case that proves it.
//
// Found by a mutation that came back GREEN: dropping `--before` from the
// bounded condition changed nothing, because every other backward test either
// passes --limit as well (so `bounded` is still true) or returns a SHORT page
// (so the exhaustion loop breaks on its own). Neither can tell the bounded rule
// from the short-page rule.
//
// A FULL page under `--before` is where they come apart — and it is the
// ordinary case for a reader walking back through real history, not an edge
// one. Without the bound, this walks the whole chat: the exact behaviour #548
// exists to stop.
func TestTeamChatReadBeforeAloneBoundsAFullPage(t *testing.T) {
	full := make([]int, 0, team.TeamChatPageSize)
	for i := 1; i <= team.TeamChatPageSize; i++ {
		full = append(full, i)
	}
	srv, seen := chatServer(t, teamChatPage(9999, full...))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--before", "500", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// ONE call. A second would mean the read fell through to the exhaustion
	// loop — and chatServer fails loudly on a page it has not queued, so this
	// cannot pass by the fixture quietly running dry.
	if len(*seen) != 1 {
		t.Fatalf("--before alone must bound the read to one page even when it is FULL, got %d calls", len(*seen))
	}
}

// An EMPTY page is the end of history, and prevBefore goes null to say so.
//
// This is the ONLY signal offered, deliberately: the server's `total` is scoped
// to the cursor under beforeSeq, so the fixture here reports a total far larger
// than the page — the exact shape that would make a `len(items) < total`
// reader keep walking, or a `total`-based "is there more" reader stop early.
func TestTeamChatReadBeforeEndsOnAnEmptyPage(t *testing.T) {
	srv, _ := chatServer(t, teamChatPage(9999))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--before", "1", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Messages   []struct{ Seq int } `json:"messages"`
		PrevBefore *int                `json:"prevBefore"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	if len(dto.Messages) != 0 {
		t.Errorf("the page is empty: %s", out.String())
	}
	if dto.PrevBefore != nil {
		t.Errorf("nothing older means no cursor to continue from, got %v", *dto.PrevBefore)
	}
}

// A CURSOR THAT CAN ONLY RETURN NOTHING IS REFUSED, before the query
// (@codex P2, @copilot).
//
// Every value here yields an EMPTY page — and an empty page is this command's
// end-of-history signal, so a permissive parse answers "you have reached the
// beginning" to a caller with the whole chat still ahead of them. The contract's
// one guarantee, broken by a typo.
//
// `--limit 0` is the sharpest, and the reason it is not merely a nonsense value
// the server would reject: the SDL gives it a MEANING — return only `total` —
// so it SUCCEEDS, returns nothing, and is indistinguishable from the end.
func TestTeamChatReadRefusesCursorsThatCanOnlyBeEmpty(t *testing.T) {
	// The cap comes from the CONSTANT, not from a literal (@copilot). The
	// message is formatted from team.TeamChatPageSize, so a hard-coded "200"
	// would red this test the day the cap moves while the behaviour stayed
	// correct — and the constant was exported in this very PR to stop exactly
	// that in the exhaustion test.
	overCap := fmt.Sprintf("at most %d", team.TeamChatPageSize)
	for _, tc := range []struct{ name, flag, value, want string }{
		{"limit zero", "--limit", "0", "at least 1"},
		{"limit negative", "--limit", "-1", "must not be negative"},
		{"limit above the server cap", "--limit", fmt.Sprint(team.TeamChatPageSize + 1), overCap},
		{"before zero", "--before", "0", "at least 1"},
		{"before negative", "--before", "-5", "at least 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No pages queued: reaching the server at all is the failure, and
			// chatServer says so loudly rather than answering empty.
			srv, seen := chatServer(t)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
				tc.flag, tc.value, "--server", srv.URL})
			err := root.Execute()
			if code := exitCodeFor(err); code != exitcode.Usage {
				t.Fatalf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal must say %q: %v", tc.want, err)
			}
			if len(*seen) != 0 {
				t.Errorf("a refused cursor must not reach the server, got %d calls", len(*seen))
			}
		})
	}
}

// …AND A SIGNED-OUT CALLER GETS THE SAME ANSWER (@codex P2).
//
// The validation used to run AFTER `f.GraphQLClient()`, so somebody who typed
// `--limit 0` without credentials was told AuthRequired — an answer about their
// session for a mistake in their arguments, sending them to fix the wrong
// thing. These flags are wrong whatever your credentials are.
//
// This is the mirror of the reasoning `alreadyBoundError` records in
// session.go, where hoisting the client ABOVE a guard would have replaced a
// documented conflict with an auth error. A guard that can answer before a
// client exists must.
func TestTeamChatReadRefusesBadCursorsWhenSignedOut(t *testing.T) {
	t.Setenv("HADRON_TOKEN", "")
	srv, seen := chatServer(t)
	f, _ := testFactory(t)
	// testFactory sets a token; clear it AFTER, since it is what makes this
	// caller signed out and the fixture would otherwise mask the branch.
	t.Setenv("HADRON_TOKEN", "")
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--limit", "0", "--server", srv.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("a bad flag is a USAGE error signed out too, got exit %d: %v", code, err)
	}
	if err == nil || !strings.Contains(err.Error(), "--limit must be at least 1") {
		t.Errorf("the refusal must be about the flag, not the session: %v", err)
	}
	if len(*seen) != 0 {
		t.Errorf("nothing should reach the server, got %d calls", len(*seen))
	}
}

// …and `--before 1` STAYS LEGAL. It means "nothing older", which is the honest
// end-of-history answer rather than a mistake — and it is what a reader walking
// back arrives at naturally. A guard that refused it would break the last step
// of every backward walk.
func TestTeamChatReadBeforeOneIsLegal(t *testing.T) {
	srv, seen := chatServer(t, teamChatPage(0))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--before", "1", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--before 1 is the end of a walk, not a usage error: %v", err)
	}
	if len(*seen) != 1 {
		t.Errorf("it must still ask the server, got %d calls", len(*seen))
	}
}

// A cursorless read must omit sinceSeq to ask the new server for its newest
// page. A full page is still ONE request; fetching the next page would turn a
// harmless default read into the historical whole-chat dump (#1536).
func TestTeamChatReadWithoutCursorTakesOneTailPage(t *testing.T) {
	full := make([]int, 0, team.TeamChatPageSize)
	for i := 401; i <= 400+team.TeamChatPageSize; i++ {
		full = append(full, i)
	}
	srv, seen := chatServer(t, teamChatPage(600, full...))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("default read must take one page, got %d calls", len(*seen))
	}
	if (*seen)[0].SinceSeqPresent {
		t.Errorf("cursorless tail must omit sinceSeq, got %+v", (*seen)[0])
	}
	var dto struct {
		Messages   []struct{ Seq int } `json:"messages"`
		NextSince  int                 `json:"nextSince"`
		PrevBefore *int                `json:"prevBefore"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Messages) != team.TeamChatPageSize || dto.NextSince != 600 || dto.PrevBefore == nil || *dto.PrevBefore != 401 {
		t.Errorf("tail cursors and page: %+v", dto)
	}
}

// --all is the only way to walk to exhaustion. It sends an EXPLICIT zero to
// ask for the oldest page even after the server flips its cursorless default.
func TestTeamChatReadAllWalksForwardToExhaustion(t *testing.T) {
	full := make([]int, 0, team.TeamChatPageSize)
	for i := 1; i <= team.TeamChatPageSize; i++ {
		full = append(full, i)
	}
	second := make([]int, 0, team.TeamChatPageSize)
	for i := team.TeamChatPageSize + 1; i <= 2*team.TeamChatPageSize; i++ {
		second = append(second, i)
	}
	srv, seen := chatServer(t,
		teamChatPage(len(full), full...),
		teamChatPage(len(second), second...),
		teamChatPage(1, 2*team.TeamChatPageSize+1))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--all", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*seen) != 3 {
		t.Fatalf("--all must page to exhaustion, got %d calls", len(*seen))
	}
	if got := (*seen)[0].SinceSeq; got == nil || *got != 0 || !(*seen)[0].SinceSeqPresent {
		t.Errorf("--all must start with explicit sinceSeq:0, got %+v", (*seen)[0])
	}
	for i, v := range *seen {
		if v.BeforeSeq != nil {
			t.Errorf("call %d must send no beforeSeq, got %v", i, *v.BeforeSeq)
		}
	}
	if got := (*seen)[1].SinceSeq; got == nil || *got != team.TeamChatPageSize {
		t.Errorf("the second page must resume at the first page's last seq, got %v", got)
	}
}

func TestTeamChatReadSinceZeroTakesOneOldestPage(t *testing.T) {
	full := make([]int, 0, team.TeamChatPageSize)
	for i := 1; i <= team.TeamChatPageSize; i++ {
		full = append(full, i)
	}
	srv, seen := chatServer(t, teamChatPage(600, full...))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--since", "0", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*seen) != 1 || !(*seen)[0].SinceSeqPresent || (*seen)[0].SinceSeq == nil || *(*seen)[0].SinceSeq != 0 {
		t.Fatalf("--since 0 must request one oldest page, got %+v", *seen)
	}
	var dto struct {
		NextSince int `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.NextSince != team.TeamChatPageSize {
		t.Errorf("nextSince = %d, want %d", dto.NextSince, team.TeamChatPageSize)
	}
}

// A cursorless newest page never proves a contiguous prefix: the server's
// count and fetch can race even when the page looks complete (#1538).
func TestTeamChatReadTailNeverMarksTheWatermark(t *testing.T) {
	for _, tc := range []struct {
		name, binding string
		seqs          []int
		total         int
		want          *int
	}{
		{"unseen prefix", bindingWithTeamFixture, []int{401, 402}, 402, nil},
		{"apparently complete page", bindingWithTeamFixture, []int{1, 2}, 2, nil},
		{"tail appears to join prior cursor", bindingChatSeenFixture, []int{91, 92}, 92, intPtr(90)},
		{"tail skips prior cursor", bindingChatSeenFixture, []int{101, 102}, 102, intPtr(90)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			path := filepath.Join(dir, "hadron-team-session.json")
			if err := os.WriteFile(path, []byte(tc.binding), 0o600); err != nil {
				t.Fatal(err)
			}
			srv, seen := chatServer(t, teamChatPage(tc.total, tc.seqs...))
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--limit", "2", "--server", srv.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if len(*seen) != 1 || (*seen)[0].SinceSeqPresent {
				t.Errorf("tail must be a single cursorless request: %+v", *seen)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var bound struct {
				ChatSeenSeq *int `json:"chatSeenSeq"`
			}
			if err := json.Unmarshal(data, &bound); err != nil {
				t.Fatal(err)
			}
			if (bound.ChatSeenSeq == nil) != (tc.want == nil) ||
				(bound.ChatSeenSeq != nil && *bound.ChatSeenSeq != *tc.want) {
				t.Errorf("watermark = %v, want %v", bound.ChatSeenSeq, tc.want)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

func TestTeamChatReadAllAndOnePageFlagsDoNotCompose(t *testing.T) {
	for _, flag := range []string{"--before", "--limit"} {
		t.Run(flag, func(t *testing.T) {
			srv, seen := chatServer(t)
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--all", flag, "2", "--server", srv.URL})
			if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
				t.Errorf("--all %s must be a usage error, got %d", flag, code)
			}
			if len(*seen) != 0 {
				t.Errorf("invalid flag pair reached the server: %+v", *seen)
			}
		})
	}
}

// --limit ALONE also bounds the read, without a backward cursor. That is what
// keeps it a flag with its own meaning rather than a modifier on --before:
// there is no observable page size in an exhaustive read, so a --limit that
// only sized invisible pages would be a flag with no effect.
func TestTeamChatReadLimitAloneBoundsTheRead(t *testing.T) {
	srv, seen := chatServer(t, teamChatPage(500, 1, 2))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--limit", "2", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// A full page under an exhaustive loop would have fetched again; the
	// fixture's total says 500 remain, so only the bound stops it.
	if len(*seen) != 1 {
		t.Fatalf("--limit must bound the read to one page, got %d calls", len(*seen))
	}
	if got := (*seen)[0].Limit; got == nil || *got != 2 {
		t.Errorf("limit: %v", got)
	}
	if (*seen)[0].SinceSeqPresent {
		t.Errorf("--limit without a cursor must use the server tail, got %+v", (*seen)[0])
	}
}

// A --before READ MUST NOT ADVANCE THE WATERMARK, and the reason is not the one
// the existing contiguity check tests.
//
// The read below STARTS contiguously — no --since at all, and the binding has
// no watermark — so every guard already in place is satisfied. What makes it
// unrecordable is that a backward page is the NEWEST messages before a cursor:
// everything between the start and that page is unread, and the hole is in the
// MIDDLE where a start-of-read check cannot see it. "A window is not a prefix",
// on the first surface that can produce a window with a contiguous-looking start.
func TestTeamChatReadBeforeNeverRecordsTheWatermark(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := chatServer(t, teamChatPage(399, 397, 398, 399))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--before", "400", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	var b struct {
		ChatSeenSeq *int `json:"chatSeenSeq"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("binding: %v", err)
	}
	if b.ChatSeenSeq != nil {
		t.Errorf("a backward page skips everything before it — the watermark must stay unset, got %d", *b.ChatSeenSeq)
	}
}

// …while a bounded EXPLICIT FORWARD read still records: it is a genuine prefix.
// The positive control distinguishes it from a cursorless tail with --limit.
func TestTeamChatReadLimitStillRecordsTheWatermark(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := chatServer(t, teamChatPage(500, 1, 2, 3))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--since", "0", "--limit", "3", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	var b struct {
		ChatSeenSeq *int `json:"chatSeenSeq"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("binding: %v", err)
	}
	if b.ChatSeenSeq == nil || *b.ChatSeenSeq != 3 {
		t.Errorf("a bounded forward read from the start IS a prefix and must record it, got %v", b.ChatSeenSeq)
	}
}

// The two cursors COMPOSE, per the SDL — a bounded slice in the middle.
func TestTeamChatReadComposesBothCursors(t *testing.T) {
	srv, seen := chatServer(t, teamChatPage(40, 301, 302))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team",
		"--since", "300", "--before", "340", "--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("one page, got %d", len(*seen))
	}
	v := (*seen)[0]
	if v.SinceSeq == nil || *v.SinceSeq != 300 || v.BeforeSeq == nil || *v.BeforeSeq != 340 {
		t.Errorf("both cursors must ride together, got since=%v before=%v", v.SinceSeq, v.BeforeSeq)
	}
}
