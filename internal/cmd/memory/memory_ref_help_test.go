package memory

import (
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

// `memory share ls` had no Long at all, so annotating it must promote
// Short rather than leave the command described only by the boilerplate
// (this is the exact command from #282).
func TestMemoryRefHelpPromotesShortWhenLongIsEmpty(t *testing.T) {
	var ls *cobra.Command
	walk(NewCmdMemory(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		if cmd.CommandPath() == "memory share ls" {
			ls = cmd
		}
	})
	if ls == nil {
		t.Fatal("memory share ls not found")
	}
	if !strings.HasPrefix(ls.Long, ls.Short) {
		t.Errorf("Long = %q does not lead with Short = %q", ls.Long, ls.Short)
	}
	if !strings.Contains(ls.Use, memoryRefToken) {
		t.Errorf("Use = %q does not take a %s", ls.Use, memoryRefToken)
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
