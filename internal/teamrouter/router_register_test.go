package teamrouter

// Ported 1:1 from router.test.ts describe('the mention-only register
// prerequisite'). The TS suite asserted the register query's argv carried no
// ownerRef; in this port the query is a typed genqlient operation, so that
// assertion lives in the operation authorship (§10 review), not here.

import (
	"regexp"
	"testing"
)

func TestRegisterRowMissingHoldsAndReports(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.mentionOnly = map[string]bool{}
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{mentions: ip(230)})}
	out := f.poll(t, c)
	if !regexp.MustCompile(`mention-only register row`).MatchString(out.Held["Eli"]) {
		t.Fatalf("hold = %q, want the register-row reason", out.Held["Eli"])
	}
	wantNoSend(t, f)
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
	f.mentionOnly = map[string]bool{"w-eli": true}
	out = f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli] once the row exists", out.Nudged)
	}
}

func TestRegisterWinningRowNotMentionOnlyDoesNotCount(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.registerRowFor = func(string) *RegisterRow {
		return &RegisterRow{NamedBy: "WORKER", ChannelID: channel, MentionOnly: false}
	}
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if !regexp.MustCompile(`mention-only`).MatchString(out.Held["Eli"]) {
		t.Fatalf("hold = %q, want the mention-only refusal", out.Held["Eli"])
	}
}

func TestRegisterEveryoneRowDoesNotCount(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.registerRowFor = func(string) *RegisterRow {
		return &RegisterRow{NamedBy: "EVERYONE", ChannelID: channel, MentionOnly: true}
	}
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if !regexp.MustCompile(`mention-only`).MatchString(out.Held["Eli"]) {
		t.Fatalf("hold = %q, want the mention-only refusal", out.Held["Eli"])
	}
}

func TestRegisterOrganizationOwnedRowCounts(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.registerRowFor = func(string) *RegisterRow {
		return &RegisterRow{NamedBy: "WORKER", ChannelID: channel, MentionOnly: true, OwnerType: "ORGANIZATION"}
	}
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli] — the check reads the EFFECTIVE registration (@codex P2, #1490)", out.Nudged)
	}
}

func TestRegisterFailedQueryThrowsAndChangesNothing(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.registerFails["w-eli"] = true
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("a failed register query must fail the poll")
	}
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
}
