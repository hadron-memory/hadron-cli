package teamrouter

// Ported 1:1 from router.test.ts describe('the happy path') — the acceptance
// spec for the scan→gate→nudge cycle (cli#829: port the tests first).

import (
	"strings"
	"testing"
)

func TestHappyNudgesIdleRosterWorkerOnce(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(2), unread: ip(5)})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" || !out.Adopted {
		t.Fatalf("outcome = %+v, want nudged [Eli] adopted", out)
	}
	if got := adoptedToken(t, c); got != token1 {
		t.Fatalf("adopted token = %q, want %q", got, token1)
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent = %v, want one", f.sent)
	}
	if f.sent[0].handle != "term_eli" {
		t.Fatalf("send handle = %q, want term_eli", f.sent[0].handle)
	}
	for _, want := range []string{"5 relevant unread (2 mentions", "unread #100–#120", "latest mention #110", "as of send", "end with one short line", "do not post to team chat"} {
		if !strings.Contains(f.sent[0].text, want) {
			t.Fatalf("nudge text missing %q:\n%s", want, f.sent[0].text)
		}
	}
	waitIdx, readIdx, sendIdx := -1, -1, -1
	for i, c := range f.calls {
		switch {
		case strings.HasPrefix(c, "terminal wait") && waitIdx == -1:
			waitIdx = i
		case strings.HasPrefix(c, "terminal read") && readIdx == -1:
			readIdx = i
		case strings.HasPrefix(c, "terminal send") && sendIdx == -1:
			sendIdx = i
		}
	}
	if waitIdx >= readIdx || readIdx >= sendIdx {
		t.Fatalf("verbs out of order: wait=%d read=%d send=%d", waitIdx, readIdx, sendIdx)
	}
}

func TestHappyPassesAdoptedTokenBack(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	f.poll(t, c)
	f.attention.token = token2
	f.attention.workers = nil // nothing relevant since the adopted token
	f.poll(t, c)
	var withSince []AttentionRequest
	for _, p := range f.polls {
		if p.Since != "" {
			withSince = append(withSince, p)
		}
		if strings.Contains(p.Since, freshTok) {
			t.Fatalf("fresh token adopted or sent: %+v", p)
		}
	}
	if len(f.polls) == 0 || f.polls[0].Since != "" {
		t.Fatalf("first main scan must be bare (no token yet): %+v", f.polls)
	}
	if len(withSince) != 1 || withSince[0].Since != token1 {
		t.Fatalf("adopted-token polls = %+v, want exactly one s=%s", withSince, token1)
	}
	if f.freshN != 1 {
		t.Fatalf("fresh re-reads = %d, want 1 (poll 1's single nudge)", f.freshN)
	}
}

func TestHappyLeavesOthersAlone(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.attention.workers = []AttentionItem{worker("w-ada", "Ada", workerOpts{}), worker("w-eli", "Eli", workerOpts{mentions: ip(0)})}
	out := f.poll(t, c)
	if !out.Adopted {
		t.Fatal("want adopted")
	}
	if len(f.sent) != 0 {
		t.Fatalf("sent = %v, want none", f.sent)
	}
}
