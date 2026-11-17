package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/coveragematrix"
)

// danglingMatrixEntries names every `command` and `flag` line of the coverage
// matrix (hadron-cli#1334, #66) that does not resolve in the command tree: a
// command path that is not a command, a flag the command does not have, or a
// positional argument its Use line does not spell.
func danglingMatrixEntries(root *cobra.Command, entries []coveragematrix.Entry) []string {
	var out []string
	for _, e := range entries {
		if e.Kind != coveragematrix.Command && e.Kind != coveragematrix.Flag {
			continue
		}
		c, rest, err := root.Find(strings.Fields(e.Command))
		if err != nil || len(rest) > 0 || c.CommandPath() != root.Name()+" "+e.Command {
			out = append(out, e.Key+": `"+e.Command+"` is not a command")
			continue
		}
		for _, fl := range e.Flags {
			if strings.HasPrefix(fl, "--") {
				if c.Flags().Lookup(fl[2:]) == nil && c.InheritedFlags().Lookup(fl[2:]) == nil {
					out = append(out, e.Key+": `"+e.Command+"` has no "+fl)
				}
			} else if !strings.Contains(" "+c.Use+" ", " "+fl+" ") {
				out = append(out, e.Key+": `"+e.Command+"` takes no "+fl+" (Use: "+c.Use+")")
			}
		}
	}
	return out
}

func TestCoverageMatrixNamesRealCommands(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", coveragematrix.Path))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := coveragematrix.Parse(string(src))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := testFactory(t)
	if dangling := danglingMatrixEntries(NewRootCmd(f), entries); len(dangling) > 0 {
		t.Errorf("%s names commands or flags that do not exist:\n  %s", coveragematrix.Path, strings.Join(dangling, "\n  "))
	}
}

// Positive control: the check above can fail, once per kind of dangling name.
func TestDanglingMatrixEntriesAreFound(t *testing.T) {
	entries, err := coveragematrix.Parse(`[Query]
agents  command agent list
ghosts  command agent haunt
[Filters]
Query.agents(filter.ownedByMe)  flag agent list --owned-by-me --json
Query.agents(filter.type)  flag agent list --kind
Query.users(filter.query)  flag user search [query]
Query.users(filter.name)  flag user search [name]
`)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := testFactory(t)
	got := danglingMatrixEntries(NewRootCmd(f), entries)
	want := []string{
		"ghosts: `agent haunt` is not a command",
		"Query.agents(filter.type): `agent list` has no --kind",
		"Query.users(filter.name): `user search` takes no [name] (Use: search [query])",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}
