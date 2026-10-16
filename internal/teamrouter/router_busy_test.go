package teamrouter

// Ported 1:1 from router.test.ts describe('busy: never typed into; hold
// the token').

import (
	"regexp"
	"strings"
	"testing"
)

func sentHandles(f *fakeCLIs) []string {
	var out []string
	for _, s := range f.sent {
		out = append(out, s.handle)
	}
	return out
}

func wantHandles(t *testing.T, f *fakeCLIs, want ...string) {
	t.Helper()
	got := sentHandles(f)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sent handles = %v, want %v", got, want)
	}
}

func TestBusyHoldsTokenThenNudgesOnceIdle(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "codex", "", true)
	f.busy["term_baloo"] = true
	f.setScreen("term_baloo", screenSpec{tail: codexBusyEmpty}) // #1924: a busy hold needs the live indicator on screen
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{}), worker("w-baloo", "Baloo", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli]", out.Nudged)
	}
	if len(out.Held) != 1 || out.Held["Baloo"] == "" {
		t.Fatalf("held = %v, want only Baloo", out.Held)
	}
	if out.Adopted {
		t.Fatal("a held poll adopts nothing")
	}
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
	wantHandles(t, f, "term_eli")

	delete(f.busy, "term_baloo")
	out = f.poll(t, c)
	if !contains(out.Covered, "Eli") || len(out.Nudged) != 1 || out.Nudged[0] != "Baloo" || !out.Adopted {
		t.Fatalf("second = %+v, want Eli covered, Baloo nudged, adopted", out)
	}
	wantHandles(t, f, "term_eli", "term_baloo")
}

func TestBusyHeldTokenNoRenudgeOnUnchangedMention(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.busy["term_baloo"] = true
	f.setScreen("term_baloo", screenSpec{tail: claudeBusyEmpty}) // #1924: a busy hold needs the live indicator on screen
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{relevant: ip(110)}), worker("w-baloo", "Baloo", workerOpts{})}
	f.poll(t, c)
	// Unrelated chat moved the head; Eli's latest mention is still 110.
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{relevant: ip(110), head: ip(140)}), worker("w-baloo", "Baloo", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Covered) != 1 || out.Covered[0] != "Eli" {
		t.Fatalf("covered = %v, want [Eli]", out.Covered)
	}
	wantHandles(t, f, "term_eli")
}

func TestBusyNewMentionRenudges(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.busy["term_baloo"] = true
	f.setScreen("term_baloo", screenSpec{tail: claudeBusyEmpty}) // #1924: a busy hold needs the live indicator on screen
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{first: ip(100), relevant: ip(110), head: ip(120)}), worker("w-baloo", "Baloo", workerOpts{})}
	f.poll(t, c)
	// Eli read only its mention: firstUnreadSeq is still 100 (at or below the
	// old head), and a new mention arrived at 125. The first version called
	// this covered (@codex P1, #1490).
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{first: ip(100), relevant: ip(125), head: ip(130)}), worker("w-baloo", "Baloo", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" {
		t.Fatalf("nudged = %v, want [Eli] re-nudged", out.Nudged)
	}
	wantHandles(t, f, "term_eli", "term_eli")
}

func TestBusyAcceptedNudgePersistedAtOnce(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"}, [2]string{"w-baloo", "Baloo"})
	f.addTerminal("eli", "claude", "", true)
	f.addTerminal("baloo", "claude", "", true)
	f.registerFails["w-baloo"] = true // Baloo's check throws AFTER Eli was nudged
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{}), worker("w-baloo", "Baloo", workerOpts{})}
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("the register failure must fail the poll")
	}
	wantHandles(t, f, "term_eli")
	delete(f.registerFails, "w-baloo")
	out := f.poll(t, c)
	if !contains(out.Covered, "Eli") || len(out.Nudged) != 1 || out.Nudged[0] != "Baloo" || !out.Adopted {
		t.Fatalf("second = %+v, want Eli covered, Baloo nudged, adopted", out)
	}
	wantHandles(t, f, "term_eli", "term_baloo")
}

func TestBusyDrainsEveryPageAdoptsFinalToken(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.pages = 3
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	out := f.poll(t, c)
	if len(out.Nudged) != 1 || out.Nudged[0] != "Eli" || !out.Adopted {
		t.Fatalf("outcome = %+v, want nudged [Eli] adopted", out)
	}
	// 3 main-scan pages + the single-page fresh re-read before the send (#1616).
	if len(f.polls) != 4 {
		t.Fatalf("attention calls = %d, want 4 (3 pages + 1 fresh)", len(f.polls))
	}
	if f.freshN != 1 {
		t.Fatalf("fresh re-reads = %d, want 1", f.freshN)
	}
	if tok := adoptedToken(t, c); tok != token1 {
		t.Fatalf("token = %q, want %q", tok, token1)
	}
}

func TestBusyIncompleteFinalPageRefused(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.incompl = true
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	if _, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil); err == nil {
		t.Fatal("an incomplete final scan must be refused")
	}
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none (a refused token never lands)", tok)
	}
}

func TestBusyFinalPageNoAdoptableTokenFailsPoll(t *testing.T) {
	root := t.TempDir()
	f := newFake(t, root)
	c := cfgEntries(root, [2]string{"w-eli", "Eli"})
	f.addTerminal("eli", "claude", "", true)
	f.dropTok = true
	f.attention.workers = []AttentionItem{worker("w-eli", "Eli", workerOpts{})}
	_, err := PollOnce(t.Context(), c, f, f.orca, func(string) {}, nil)
	if err == nil || !regexp.MustCompile("no adoptable token").MatchString(err.Error()) {
		t.Fatalf("err = %v, want 'no adoptable token'", err)
	}
	wantNoSend(t, f)
	if tok := adoptedToken(t, c); tok != "" {
		t.Fatalf("token = %q, want none", tok)
	}
}
