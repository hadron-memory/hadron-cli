//go:build linux

package hostpoller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

type cleanupSequence2208 struct {
	pages         [][]orcaterm.Terminal
	failList      int
	lists         int
	calls, closed []string
}

func (c *cleanupSequence2208) List(context.Context) ([]orcaterm.Terminal, error) {
	c.lists++
	c.calls = append(c.calls, "list")
	if c.lists == c.failList {
		return nil, errors.New("synthetic list refusal")
	}
	if c.lists > len(c.pages) {
		return nil, errors.New("unexpected extra list")
	}
	return c.pages[c.lists-1], nil
}
func (c *cleanupSequence2208) CloseTab(_ context.Context, handle string) error {
	c.calls = append(c.calls, "close:"+handle)
	c.closed = append(c.closed, handle)
	return nil
}

type cleanupReceipt2208 struct{ handle, outcome, reason string }

func TestCleanupInitialReasonCodes2208(t *testing.T) {
	known := "claude"
	for _, tc := range []struct {
		name   string
		term   orcaterm.Terminal
		reason string
	}{
		{"tracked", orcaterm.Terminal{Handle: "new", TabID: "new-tab", WorktreePath: "/work", AgentIdentity: &known}, "TRACKED_TERMINAL"},
		{"identified", orcaterm.Terminal{Handle: "old", TabID: "old-tab", WorktreePath: "/work", AgentIdentity: &known}, "IDENTIFIED_TERMINAL"},
		{"connected", orcaterm.Terminal{Handle: "old", TabID: "old-tab", WorktreePath: "/work", Connected: true}, "CONNECTED_TERMINAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &cleanupSequence2208{pages: [][]orcaterm.Terminal{{tc.term}}}
			proofs := 0
			var receipts []cleanupReceipt2208
			cleanupRelaunchTabs(context.Background(), client, "new", "/work", func(context.Context) bool { proofs++; return true }, func(h, o, r string) { receipts = append(receipts, cleanupReceipt2208{h, o, r}) })
			want := []cleanupReceipt2208{{tc.term.Handle, "skipped", tc.reason}}
			if !reflect.DeepEqual(receipts, want) || proofs != 0 || len(client.closed) != 0 || client.lists != 1 {
				t.Fatalf("receipts=%+v proofs=%d lists=%d closed=%v", receipts, proofs, client.lists, client.closed)
			}
		})
	}
}

func cleanupTerms2208() []orcaterm.Terminal {
	known := "claude"
	return []orcaterm.Terminal{{Handle: "new", TabID: "new-tab", WorktreePath: "/work", AgentIdentity: &known}, {Handle: "old", TabID: "old-tab", WorktreePath: "/work"}}
}

func TestCleanupFreshDefencesAndListFailure2208(t *testing.T) {
	known := "codex"
	for _, tc := range []struct {
		name     string
		change   func([]orcaterm.Terminal) []orcaterm.Terminal
		failList int
		reason   string
	}{
		{name: "second list fails", failList: 2, reason: "TERMINAL_LIST_UNAVAILABLE"},
		{name: "tracked tab now shared", reason: "TAB_UNPROVEN", change: func(rows []orcaterm.Terminal) []orcaterm.Terminal {
			rows[0].TabID = "old-tab"
			rows[0].AgentIdentity = nil
			return rows
		}},
		{name: "tracked tab id missing", reason: "TAB_UNPROVEN", change: func(rows []orcaterm.Terminal) []orcaterm.Terminal { rows[0].TabID = ""; return rows }},
		{name: "tracked worktree changed", reason: "TAB_UNPROVEN", change: func(rows []orcaterm.Terminal) []orcaterm.Terminal { rows[0].WorktreePath = "/elsewhere"; return rows }},
		{name: "fresh candidate identified", reason: "TAB_UNPROVEN", change: func(rows []orcaterm.Terminal) []orcaterm.Terminal { rows[1].AgentIdentity = &known; return rows }},
		{name: "connected sibling pane", reason: "TAB_UNPROVEN", change: func(rows []orcaterm.Terminal) []orcaterm.Terminal {
			return append(rows, orcaterm.Terminal{Handle: "sibling", TabID: "old-tab", WorktreePath: "/work", Connected: true})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			initial := cleanupTerms2208()
			fresh := cleanupTerms2208()
			if tc.change != nil {
				fresh = tc.change(fresh)
			}
			client := &cleanupSequence2208{pages: [][]orcaterm.Terminal{initial, fresh}, failList: tc.failList}
			proofs := 0
			var receipts []cleanupReceipt2208
			cleanupRelaunchTabs(context.Background(), client, "new", "/work", func(context.Context) bool { proofs++; client.calls = append(client.calls, "proof"); return true }, func(h, o, r string) {
				if h == "old" {
					receipts = append(receipts, cleanupReceipt2208{h, o, r})
				}
			})
			want := []cleanupReceipt2208{{"old", "skipped", tc.reason}}
			if !reflect.DeepEqual(receipts, want) || proofs != 1 || len(client.closed) != 0 || client.lists != 2 {
				t.Fatalf("receipts=%+v proofs=%d lists=%d closed=%v", receipts, proofs, client.lists, client.closed)
			}
			if !reflect.DeepEqual(client.calls, []string{"list", "proof", "list"}) {
				t.Fatalf("final list preceded census: %v", client.calls)
			}
		})
	}
}

func TestCleanupEachTabGetsFreshProof2208(t *testing.T) {
	for _, refuseSecond := range []bool{false, true} {
		t.Run(map[bool]string{false: "two eligible tabs", true: "second proof refused"}[refuseSecond], func(t *testing.T) {
			terms := append(cleanupTerms2208(), orcaterm.Terminal{Handle: "other", TabID: "other-tab", WorktreePath: "/work"})
			client := &cleanupSequence2208{pages: [][]orcaterm.Terminal{terms, terms, terms}}
			proofs := 0
			receipts := map[string]cleanupReceipt2208{}
			cleanupRelaunchTabs(context.Background(), client, "new", "/work", func(context.Context) bool {
				proofs++
				client.calls = append(client.calls, "proof")
				return !refuseSecond || proofs != 2
			}, func(h, o, r string) { receipts[h] = cleanupReceipt2208{h, o, r} })
			wantClosed := []string{"old", "other"}
			wantCalls := []string{"list", "proof", "list", "close:old", "proof", "list", "close:other"}
			wantOther := cleanupReceipt2208{"other", "applied", "DEAD_TAB_CLOSED"}
			if refuseSecond {
				wantClosed = []string{"old"}
				wantCalls = wantCalls[:5]
				wantOther = cleanupReceipt2208{"other", "skipped", "CENSUS_UNPROVEN"}
			}
			if proofs != 2 || !reflect.DeepEqual(client.closed, wantClosed) || !reflect.DeepEqual(client.calls, wantCalls) {
				t.Fatalf("proofs=%d closed=%v calls=%v", proofs, client.closed, client.calls)
			}
			if receipts["old"] != (cleanupReceipt2208{"old", "applied", "DEAD_TAB_CLOSED"}) || receipts["other"] != wantOther {
				t.Fatalf("receipts=%+v", receipts)
			}
		})
	}
}

func TestCleanupDeduplicatesPanesWithinOneTab2208(t *testing.T) {
	terms := append(cleanupTerms2208(), orcaterm.Terminal{Handle: "sibling", TabID: "old-tab", WorktreePath: "/work"})
	client := &cleanupSequence2208{pages: [][]orcaterm.Terminal{terms, terms, terms}}
	proofs := 0
	cleanupRelaunchTabs(context.Background(), client, "new", "/work", func(context.Context) bool { proofs++; return true }, func(string, string, string) {})
	if proofs != 1 || client.lists != 2 || !reflect.DeepEqual(client.closed, []string{"old"}) {
		t.Fatalf("proofs=%d lists=%d closed=%v", proofs, client.lists, client.closed)
	}
}
