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

// whoami's help names the key and says what it is.
func TestWhoamiHelpDescribesChatSeenSeqAsARenderingCache2906(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "whoami", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	help := flat(out.String())
	for _, want := range []string{
		"chatSeenSeq",
		// cli#2976: absent vs 0 are different claims; swapping them must fail here.
		"absent when it never read or the binding predates the field, 0 when it read an empty chat",
		"how far this worktree has rendered the team chat",
		"only `chat read` consults it",
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
	contract := flat(string(readContract(t)))
	for _, want := range []string{
		"per-worktree rendering cache",
		"only `chat read` consults it",
		// cli#2976: the same absent-vs-0 claim as whoami's help; the meaning matches, the wording is
		// adapted to each text (cli#3146).
		"absent on a binding that has never read or that predates the field, `0` on one that read an empty chat",
		"never an unread position",
		"cor:dmo:090:06",
	} {
		if !strings.Contains(contract, want) {
			t.Errorf("agentic-usage must say %q about chatSeenSeq", want)
		}
	}
	// cli#2976: "reads it" was wrong (whoami --json reports the field too), and "hint" /
	// "local watermark" were two more names for the one rendering cache.
	for _, stale := range []string{
		"only `chat read` reads it",
		"`chatSeenSeq` hint",
		"local watermark",
		"`session log` compares against it",
		"only `chat read` acts on it",
		"session log compares against the watermark",
	} {
		if strings.Contains(contract, stale) {
			t.Errorf("agentic-usage still says %q", stale)
		}
	}
}
