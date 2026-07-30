package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
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

// The tree-walk tests above can't see documentation, which is how the
// shipped Claude Code plugin kept teaching `hadron edge ls` after the
// command surface went canonical (review on #297). Agents generate
// commands from these files, so a stale `ls` here quietly defeats the
// standardization even though the alias keeps the example runnable.
//
// docs/plans/ is deliberately excluded: those are design-as-built
// records of what shipped at the time, not live instructions.
func TestShippedDocsUseTheCanonicalSpelling(t *testing.T) {
	// Test binaries run with cwd set to their package directory.
	const repoRoot = "../.."
	roots := []string{
		filepath.Join(repoRoot, "plugins"),
		filepath.Join(repoRoot, "docs", "how-to"),
	}
	files := []string{filepath.Join(repoRoot, "README.md")}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".md") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", root, err)
		}
	}
	if len(files) < 2 {
		t.Fatal("found almost no docs to check — the paths are wrong")
	}

	bareLs := regexp.MustCompile(`\bls\b`)
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if bareLs.MatchString(line) {
				t.Errorf("%s:%d teaches the non-canonical `ls`; use `list`:\n\t%s",
					path, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// The embedded agent-facing contract is the highest-traffic doc of all.
func TestAgenticUsageUsesTheCanonicalSpelling(t *testing.T) {
	bareLs := regexp.MustCompile(`\bls\b`)
	for i, line := range strings.Split(agentic.Doc(), "\n") {
		if bareLs.MatchString(line) {
			t.Errorf("agentic-usage.md:%d teaches the non-canonical `ls`; use `list`:\n\t%s",
				i+1, strings.TrimSpace(line))
		}
	}
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
		{{"user", "ls"}, {"user", "list"}},
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
