package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#2601 item 1: the walks' one allowed local refusal is the confirmation
// prompt's, matched exactly, not any error that mentions --yes.
func TestConfirmRefusalCarveOutIsExact2601(t *testing.T) {
	io, _, _ := output.Test() // no terminal
	for name, err := range map[string]error{
		"Confirm":         cmdutil.Confirm(io, false, "go?"),
		"ConfirmDeletion": cmdutil.ConfirmDeletion(io, false, "thing"),
	} {
		if !isConfirmRefusal(err) {
			t.Errorf("%s: the real confirmation refusal must match: %v", name, err)
		}
	}
	for name, err := range map[string]error{
		"nil":               nil,
		"flag error":        exitcode.Newf(exitcode.Usage, "unknown flag: --yes-please"),
		"mentions --yes":    exitcode.Newf(exitcode.Usage, "--yes cannot be combined with --dry-run"),
		"right words, exit": exitcode.Newf(exitcode.Error, "refusing to proceed without --yes in non-interactive mode"),
		"plain error":       errors.New("refusing to proceed without --yes in non-interactive mode"),
	} {
		if isConfirmRefusal(err) {
			t.Errorf("%s must not pass the carve-out: %v", name, err)
		}
	}
}

// cli#2601 item 3: a known panic matches the resolved command AND the tracked
// signature.
func TestKnownPanicMatchesPathAndSignature2601(t *testing.T) {
	known := map[string]knownPanic{"usage events": {Issue: "#1", Signature: "nil pointer dereference"}}
	if issue, ok := matchKnownPanic(known, "usage events", "runtime error: invalid memory address or nil pointer dereference"); !ok || issue != "#1" {
		t.Errorf("the tracked panic of the tracked command must match: %q %v", issue, ok)
	}
	if _, ok := matchKnownPanic(known, "usage events", "index out of range"); ok {
		t.Error("another panic of the same command must not match")
	}
	if _, ok := matchKnownPanic(known, "usage activity", "nil pointer dereference"); ok {
		t.Error("the same panic of another command must not match")
	}
	if _, ok := matchKnownPanic(map[string]knownPanic{"x": {Issue: "#2"}}, "x", "anything"); ok {
		t.Error("an entry with no signature must never match")
	}
	f, _ := testFactory(t)
	if got := resolvedPath(NewRootCmd(f), []string{"usage", "events", "--limit", "3"}); got != "usage events" {
		t.Errorf("resolved path = %q", got)
	}
}

// cli#2601 item 4: "reached the server" needs a named operation.
func TestWalkFixtureCountsOnlyNamedOperations2601(t *testing.T) {
	fx := newWalkFixture(t)
	post := func(body string) {
		resp, err := http.Post(fx.srv.URL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	n := fx.mark()
	post(`{"query":"{ x }"}`)
	if got := fx.since(n); len(got) != 0 {
		t.Errorf("an unnamed request must not count as reaching the server: %v", got)
	}
	post(`{"operationName":"Foo","query":"query Foo { x }"}`)
	if got := fx.since(n); len(got) != 1 || got[0] != "Foo" {
		t.Errorf("a named operation counts: %v", got)
	}
}

// cli#2601 item 2: every command a touched help page names resolves in the tree.
func TestHelpPagesNameCommandsThatExist2601(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	for _, path := range [][]string{
		{"automation", "approval"}, {"automation", "approve"}, {"schedule", "list"}, {"webhook", "list"},
	} {
		if c, _, err := root.Find(path); err != nil || c.Name() != path[len(path)-1] {
			t.Errorf("a help page names %q, which is not a command: %v", strings.Join(path, " "), err)
		}
	}
	// And the pages really name them: a rename of the text must not silently
	// drop the pointer this pin is about.
	for _, tc := range []struct {
		page []string
		want []string
	}{
		{[]string{"automation", "preview"}, []string{"hadron automation approval", "hadron automation approve", "hadron schedule list", "hadron webhook list"}},
		{[]string{"automation", "re-adopt"}, []string{"hadron automation approval", "hadron automation approve", "hadron schedule list", "hadron webhook list"}},
	} {
		long, _ := page2579(t, tc.page...)
		for _, w := range tc.want {
			if !strings.Contains(long, w) {
				t.Errorf("%v no longer names %q", tc.page, w)
			}
		}
	}
}

// cli#2601 item 7: team-snapshot-grant set's --trigger-id flag line carries the
// same pointer grant set's does.
func TestTeamSnapshotGrantTriggerIDFlagPointsAtTheListings2601(t *testing.T) {
	f, _ := testFactory(t)
	for _, path := range [][]string{{"automation", "grant", "set"}, {"automation", "team-snapshot-grant", "set"}} {
		c, _, err := NewRootCmd(f).Find(path)
		if err != nil {
			t.Fatal(err)
		}
		usage := c.Flags().Lookup("trigger-id").Usage
		for _, want := range []string{"hadron schedule list", "hadron webhook list"} {
			if !strings.Contains(usage, want) {
				t.Errorf("%v --trigger-id flag line must name %q: %q", path, want, usage)
			}
		}
	}
}

// cli#2601 item 8: re-adopt says when you need it, and what a MANUAL trigger is.
func TestReAdoptSaysWhenYouNeedIt2601(t *testing.T) {
	long, _ := page2579(t, "automation", "re-adopt")
	for _, want := range []string{"was refused", "changed since you last approved", "MANUAL"} {
		if !strings.Contains(long, want) {
			t.Errorf("re-adopt must say when it is needed (missing %q): %q", want, long)
		}
	}
}
