package memory

import (
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// walk visits cmd and every command beneath it.
func walk(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, sub := range cmd.Commands() {
		walk(sub, visit)
	}
}

// Every command that acts on a memory must name the positional
// <memoryRef> — not <memory>, <memory-id-or-urn>, or any other spelling
// this group used to mix (#282).
func TestMemoryRefPositionalIsNamedConsistently(t *testing.T) {
	stale := []string{"<memory>", "<memory-id-or-urn>", "<memory-urn-or-id>"}
	walk(NewCmdMemory(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		for _, s := range stale {
			if strings.Contains(cmd.Use, s) {
				t.Errorf("%q uses the stale placeholder %s; use %s", cmd.CommandPath(), s, memoryRefToken)
			}
		}
	})
}

// A <memoryRef> positional is useless without knowing what it accepts,
// which is exactly what `memory share ls --help` failed to say.
func TestMemoryRefCommandsDocumentAcceptedForms(t *testing.T) {
	var annotated int
	walk(NewCmdMemory(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		if !strings.Contains(cmd.Use, memoryRefToken) {
			// Commands that take something else must not claim to take
			// a memory ref — `memory extract <parentRef>` takes a node.
			if strings.Contains(cmd.Long, memoryRefHelp) {
				t.Errorf("%q documents <memoryRef> but does not take one", cmd.CommandPath())
			}
			return
		}
		annotated++
		if !strings.Contains(cmd.Long, memoryRefHelp) {
			t.Errorf("%q takes a %s but its help does not say what that accepts", cmd.CommandPath(), memoryRefToken)
		}
		// The annotation must not swallow the command's own description:
		// cobra shows Long in place of Short once Long is set, so
		// something has to sit above the appended paragraph.
		if desc := strings.TrimSpace(strings.TrimSuffix(cmd.Long, memoryRefHelp)); desc == "" {
			t.Errorf("%q has no description above the <memoryRef> help", cmd.CommandPath())
		}
	})
	if annotated == 0 {
		t.Fatal("no command took a <memoryRef> — the walk or the token is wrong")
	}
}

// The share-listing command had no Long at all, so annotating it must
// promote Short rather than leave the command described only by the
// boilerplate (this is the exact command from #282 — reported against
// its `ls` spelling, renamed to `list` in #283, still reachable as both).
func TestMemoryRefHelpPromotesShortWhenLongIsEmpty(t *testing.T) {
	const path = "memory share list"
	var shareList *cobra.Command
	walk(NewCmdMemory(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		if cmd.CommandPath() == path {
			shareList = cmd
		}
	})
	if shareList == nil {
		t.Fatalf("%q not found", path)
	}
	if !strings.HasPrefix(shareList.Long, shareList.Short) {
		t.Errorf("Long = %q does not lead with Short = %q", shareList.Long, shareList.Short)
	}
	if !strings.Contains(shareList.Use, memoryRefToken) {
		t.Errorf("Use = %q does not take a %s", shareList.Use, memoryRefToken)
	}
	if !slices.Contains(shareList.Aliases, "ls") {
		t.Errorf("%q lost the `ls` alias the issue was reported against", path)
	}
}

// The help paragraph presents itself as exhaustive, so every form it
// names must actually resolve, and every form the parser accepts must be
// named. The first version claimed "any URN spelling" while listing only
// the two `hrn:` forms and calling the root atom `<org>` — misleading for
// anyone holding a `urn:` reference or an --owner-me memory, which is
// minted under a user handle (review on #296).
func TestMemoryRefHelpMatchesTheParser(t *testing.T) {
	// One representative per spelling the help documents, in both the
	// org-domain and user-handle shapes.
	accepted := []struct{ ref, wantRoot, wantSlug string }{
		{"hrn:mem:acme.com:kb", "acme.com", "kb"},
		{"hrn:memory:acme.com::kb", "acme.com", "kb"},
		{"urn:mem:acme.com:kb", "acme.com", "kb"},
		{"urn:memory:acme.com::kb", "acme.com", "kb"},
		{"acme.com:kb", "acme.com", "kb"},
		{"acme.com::kb", "acme.com", "kb"},
		{"hrn:mem:jane:kb", "jane", "kb"},
		{"urn:mem:jane:kb", "jane", "kb"},
		{"jane:kb", "jane", "kb"},
	}
	for _, tc := range accepted {
		root, slug, ok := cmdutil.MemoryParts(tc.ref)
		if !ok || root != tc.wantRoot || slug != tc.wantSlug {
			t.Errorf("MemoryParts(%q) = (%q, %q, %v); the help documents this form as accepted",
				tc.ref, root, slug, ok)
		}
	}

	// Every documented token must appear in the paragraph — this is what
	// catches the omission the reviewers found.
	for _, want := range []string{
		"hrn:mem:<root>:<slug>",
		"hrn:memory:<root>::<slug>",
		"<root>:<slug>",
		"<root>::<slug>",
		"urn:",
		"user handle",
		"id",
	} {
		if !strings.Contains(memoryRefHelp, want) {
			t.Errorf("memoryRefHelp does not mention %q", want)
		}
	}

	// The root atom is not necessarily an org, so the help must not call
	// it one.
	if strings.Contains(memoryRefHelp, "<org>") {
		t.Error("memoryRefHelp calls the root atom <org>, but it may be a user handle")
	}
}

// The annotation is idempotent per tree, but a second NewCmdMemory must
// not compound onto shared state either.
func TestMemoryRefHelpAppendedOnce(t *testing.T) {
	for i := 0; i < 2; i++ {
		walk(NewCmdMemory(cmdutil.NewFactory()), func(cmd *cobra.Command) {
			if n := strings.Count(cmd.Long, memoryRefHelp); n > 1 {
				t.Errorf("%q repeats the <memoryRef> help %d times", cmd.CommandPath(), n)
			}
		})
	}
}
