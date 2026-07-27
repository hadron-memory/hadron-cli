package cmd

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// walkCommands visits cmd and every command beneath it.
func walkCommands(cmd *cobra.Command, visit func(*cobra.Command)) {
	visit(cmd)
	for _, sub := range cmd.Commands() {
		walkCommands(sub, visit)
	}
}

// The CLI mixed `ls` and `list` as the primary name — `memory list` but
// `spec ls`, `org member ls` but `node revision list` — so callers had
// to remember which group chose which. `list` is now canonical
// everywhere (#283).
func TestListIsTheCanonicalName(t *testing.T) {
	walkCommands(NewRootCmd(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		if cmd.Name() == "ls" {
			t.Errorf("%q is named \"ls\"; name it \"list\" and keep \"ls\" as an alias", cmd.CommandPath())
		}
	})
}

// `ls` stays a working alias everywhere it used to be a name: agents
// script against this surface and muscle memory is real, so dropping it
// would be a gratuitous break.
func TestLsStaysAnAlias(t *testing.T) {
	var listCommands int
	walkCommands(NewRootCmd(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		if cmd.Name() != "list" {
			return
		}
		listCommands++
		if !slices.Contains(cmd.Aliases, "ls") {
			t.Errorf("%q dropped the \"ls\" alias", cmd.CommandPath())
		}
	})
	if listCommands == 0 {
		t.Fatal("no list commands found — the walk is wrong")
	}
}

// A command reachable as `ls` must also be reachable as `list`, so the
// canonical name works even where the primary is something else
// (`object find` aliases both).
func TestEveryLsAliasHasAListSpelling(t *testing.T) {
	walkCommands(NewRootCmd(cmdutil.NewFactory()), func(cmd *cobra.Command) {
		if !slices.Contains(cmd.Aliases, "ls") {
			return
		}
		if cmd.Name() != "list" && !slices.Contains(cmd.Aliases, "list") {
			t.Errorf("%q answers to \"ls\" but not to \"list\"", cmd.CommandPath())
		}
	})
}

// Both spellings must actually resolve to the same command — the
// aliases are only worth anything if cobra dispatches on them.
func TestBothSpellingsResolve(t *testing.T) {
	paths := [][2][]string{
		{{"memory", "share", "ls"}, {"memory", "share", "list"}},
		{{"spec", "ls"}, {"spec", "list"}},
		{{"org", "member", "ls"}, {"org", "member", "list"}},
		{{"edge", "ls"}, {"edge", "list"}},
		{{"auth", "token", "ls"}, {"auth", "token", "list"}},
		{{"object", "ls"}, {"object", "list"}},
	}
	root := NewRootCmd(cmdutil.NewFactory())
	for _, pair := range paths {
		viaLs, _, err := root.Find(pair[0])
		if err != nil {
			t.Errorf("%v did not resolve: %v", pair[0], err)
			continue
		}
		viaList, _, err := root.Find(pair[1])
		if err != nil {
			t.Errorf("%v did not resolve: %v", pair[1], err)
			continue
		}
		if viaLs != viaList {
			t.Errorf("%v resolved to %q but %v resolved to %q",
				pair[0], viaLs.CommandPath(), pair[1], viaList.CommandPath())
		}
	}
}
