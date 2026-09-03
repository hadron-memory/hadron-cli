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
	SinceSeq  *int `json:"sinceSeq"`
	BeforeSeq *int `json:"beforeSeq"`
	Limit     *int `json:"limit"`
}

// chatServer answers TeamChatMessages from a queue of pages and records the
// variables of each call.
func chatServer(t *testing.T, pages ...string) (*httptest.Server, *[]chatVars) {
	t.Helper()
	seen := &[]chatVars{}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string   `json:"operationName"`
			Variables     chatVars `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TeamChatMessages":
			*seen = append(*seen, body.Variables)
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
		case "TeamAppIdentity":
			_, _ = w.Write([]byte(teamAppIdentityJSON))
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
	for _, tc := range []struct{ name, flag, value, want string }{
		{"limit zero", "--limit", "0", "at least 1"},
		{"limit negative", "--limit", "-1", "must not be negative"},
		{"limit above the server cap", "--limit", "500", "at most 200"},
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

// The unbounded read is UNCHANGED — it still pages to exhaustion — which is
// what makes #548 additive rather than a break.
//
// Two pages of 200 then a short one; three calls, each carrying the previous
// page's last seq forward and NO beforeSeq.
func TestTeamChatReadWithoutCursorsStillExhausts(t *testing.T) {
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
		"--json", "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(*seen) != 3 {
		t.Fatalf("the unbounded read must page to exhaustion, got %d calls", len(*seen))
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

// …while a bounded FORWARD read still records, because it is a genuine prefix.
// The positive control: without it, "never record on a bounded read" would pass
// the test above and silently retire the watermark for --limit too.
func TestTeamChatReadLimitStillRecordsTheWatermark(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, _ := chatServer(t, teamChatPage(500, 1, 2, 3))
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--limit", "3", "--server", srv.URL})
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
