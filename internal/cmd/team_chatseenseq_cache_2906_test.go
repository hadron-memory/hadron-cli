package cmd

import (
	"strings"
	"testing"
)

// cli#2906 (decision (b), the issue body): the binding's chatSeenSeq is kept as an explicit
// per-worktree RENDERING CACHE. The consumer is `chat read`'s contiguity check (whether a read
// counts as read-through, and so whether its server-side mark counts); it is never an unread
// position and never read state: every unread claim derives from the server read cursor
// (cor:dmo:090:06: unread is derived from the attendee's server-side read state, and a per-worktree
// hint is not the attendee's read state: one worker bound in two worktrees holds two). The
// existing pin TestSessionLogUnreadCountsFromTheServerCursor2858 keeps the unread claim honest;
// these pins keep the two places a reader meets the key (whoami's help and the agent contract)
// from calling the cache by the wrong name.

func flat2906(s string) string { return strings.Join(strings.Fields(s), " ") }

// whoami's help names the key and says what it is.
func TestWhoamiHelpDescribesChatSeenSeqAsARenderingCache2906(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "whoami", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := flat2906(out.String())
	for _, want := range []string{
		"chatSeenSeq",
		"how far this worktree has rendered the team chat",
		"decides whether a chat read counts as read through",
		"never an unread position",
		"the server read cursor",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("team session whoami --help must say %q:\n%s", want, help)
		}
	}
}

// The agent contract describes the key as a cache and no longer says session log compares against it.
func TestAgenticUsageDescribesChatSeenSeqAsARenderingCache2906(t *testing.T) {
	contract := flat2906(string(readContract(t)))
	for _, want := range []string{
		"per-worktree rendering cache",
		"only `chat read` reads it",
		"never an unread position",
		"cor:dmo:090:06",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("agentic-usage must say %q about chatSeenSeq", want)
		}
	}
	for _, stale := range []string{"`session log` compares against it", "session log compares against the watermark"} {
		if strings.Contains(contract, stale) {
			t.Errorf("agentic-usage still says %q", stale)
		}
	}
}
