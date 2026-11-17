package hostpoller

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

type cleanupClient2193 struct {
	terms             []orcaterm.Terminal
	fresh             []orcaterm.Terminal
	lists             int
	closes            []string
	listErr, closeErr error
}

func (c *cleanupClient2193) List(context.Context) ([]orcaterm.Terminal, error) {
	c.lists++
	if c.lists > 1 && c.fresh != nil {
		return c.fresh, c.listErr
	}
	return c.terms, c.listErr
}
func (c *cleanupClient2193) CloseTab(_ context.Context, h string) error {
	c.closes = append(c.closes, h)
	return c.closeErr
}

func TestRelaunchTabProofBoundary2193(t *testing.T) {
	known := "codex"
	tracked := orcaterm.Terminal{Handle: "new", WorktreePath: "/work", TabID: "new-tab", AgentIdentity: &known}
	shell := orcaterm.Terminal{Handle: "old", WorktreePath: "/work", TabID: "old-tab", Connected: false}
	for _, tc := range []struct {
		name   string
		mutate func(*cleanupClient2193)
		proof  bool
		want   int
		reason string
	}{
		{name: "connected bare shell retained", proof: true, want: 0, reason: "CONNECTED_TERMINAL", mutate: func(c *cleanupClient2193) { c.terms[1].Connected = true }},
		{name: "disconnected bare shell with own-agent proof", proof: true, want: 1, reason: "DEAD_TAB_CLOSED", mutate: func(c *cleanupClient2193) { c.terms[1].Connected = false }},
		{name: "unreadable or ambiguous census", reason: "CENSUS_UNPROVEN"},
		{name: "disconnected alone proves nothing", reason: "CENSUS_UNPROVEN", mutate: func(c *cleanupClient2193) { c.terms[1].Connected = false }},
		{name: "tracked unknown", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) { c.terms[0].AgentIdentity = nil; c.terms[1].TabID = "new-tab" }},
		{name: "known pane in same tab", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) {
			c.terms = append(c.terms, orcaterm.Terminal{Handle: "pane", TabID: "old-tab", WorktreePath: "/work", AgentIdentity: &known})
		}},
		{name: "other worktree pane shares tab", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) {
			c.terms = append(c.terms, orcaterm.Terminal{Handle: "pane", TabID: "old-tab", WorktreePath: "/elsewhere"})
		}},
		{name: "missing tracked row", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) { c.terms = c.terms[1:] }},
		{name: "missing tab id", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) { c.terms[1].TabID = "" }},
		{name: "connected on fresh list", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) {
			c.fresh = append([]orcaterm.Terminal{}, c.terms...)
			c.fresh[1].Connected = true
		}},
		{name: "changed identity before close", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) {
			c.fresh = append([]orcaterm.Terminal{}, c.terms...)
			c.fresh[1].AgentIdentity = &known
		}},
		{name: "duplicate handle in list", proof: true, reason: "TAB_UNPROVEN", mutate: func(c *cleanupClient2193) { c.terms = append(c.terms, c.terms[1]) }},
		{name: "failed list", proof: true, reason: "TERMINAL_LIST_UNAVAILABLE", mutate: func(c *cleanupClient2193) { c.listErr = errors.New("unavailable") }},
		{name: "close refused", proof: true, want: 1, reason: "TERMINAL_CLOSE_FAILED", mutate: func(c *cleanupClient2193) { c.closeErr = errors.New("refused") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &cleanupClient2193{terms: []orcaterm.Terminal{tracked, shell}}
			if tc.mutate != nil {
				tc.mutate(c)
			}
			reasons := map[string]bool{}
			cleanupRelaunchTabs(context.Background(), c, "new", "/work", func(context.Context) bool { return tc.proof }, func(h, outcome, reason string) {
				reasons[reason] = true
				if h == "new" && outcome == "applied" {
					t.Error("tracked tab closed")
				}
			})
			want := tc.want
			if runtime.GOOS != "linux" {
				want = 0
			}
			if len(c.closes) != want {
				t.Fatalf("closed=%v want %d", c.closes, want)
			}
			if runtime.GOOS == "linux" && !reasons[tc.reason] {
				t.Fatalf("receipts=%v want %s", reasons, tc.reason)
			}
		})
	}
}

func TestUnprovenPlatformCensusNeverGrantsAbsence2193(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("non-Linux stub pin")
	}
	proof, err := ProbeWorktreeAgents(context.Background(), t.TempDir())
	if err == nil || proof.Proven || len(proof.PIDs) != 0 {
		t.Fatalf("non-Linux proof=%+v err=%v", proof, err)
	}
}

func TestCleanupClientRejectsFailedReply2193(t *testing.T) {
	for _, code := range []int{0, 1} {
		t.Run(string(rune('0'+code)), func(t *testing.T) {
			client := relaunchTerminalClientFor(func(context.Context, []string) (int, string, string) {
				return code, `{"ok":true,"result":{"terminals":[]}}`, ""
			})
			_, listErr := client.List(context.Background())
			closeErr := client.CloseTab(context.Background(), "old")
			if (listErr != nil) != (code != 0) || (closeErr != nil) != (code != 0) {
				t.Fatalf("code=%d list=%v close=%v", code, listErr, closeErr)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := relaunchTerminalClientFor(func(context.Context, []string) (int, string, string) {
		return 0, `{"ok":true,"result":{"terminals":[]}}`, ""
	})
	if _, err := client.List(ctx); err == nil {
		t.Fatal("cancelled reply trusted")
	}
}
