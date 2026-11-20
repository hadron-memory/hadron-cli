package cmd

import (
	"strings"
	"testing"
)

// cli#1051 (replace and comment rows): the replace text page must let a reader
// anticipate a refusal and recognise the no-match outcome. Each pin is a claim
// the command makes in code (replacecmd/replace.go): the refusals before a write
// are usage errors (exit 2) that change nothing, a real write needs write access,
// and a run with no matches says so and writes nothing.
func TestReplaceTextHelpStatesPrerequisitesAndRefusals1051(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	c, _, err := root.Find([]string{"replace", "text"})
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(c.Long), " ")
	for _, want := range []string{
		"Needs write access to every memory it writes in.",
		"A refusal before any write exits 2 and changes nothing: no --yes without a terminal, more nodes than --max-nodes, an empty <old>, a missing --field, --node or --memory, or --prefix without --memory.",
		`If nothing matches it prints "No matches — nothing to replace." on stderr and writes nothing (exit 0).`,
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("replace text help lacks %q", want)
		}
	}
	// The --prefix paragraph must start on its own line, not run on from the
	// sentence about skipped nodes.
	if !strings.Contains(c.Long, "\n\n--prefix narrows") {
		t.Error("the --prefix paragraph does not start its own paragraph")
	}
}

// cli#1051 (evaluator Gemma, AT0.8437): replace text named the nodes it skips
// (governed nodes, compiled boot prompts, protected addresses) without defining
// them, and gave no way to learn that the old term is gone everywhere. The
// definitions come from the server's skip rules (comments, governed nodes,
// compiled boot prompts, a Channel's reserved address). The command cannot
// report the skipped count (the CLI does not select nodesSkipped; filed as a
// follow-up), so the page gives the user a way to check instead.
func TestReplaceTextHelpDefinesSkippedNodesAndHowToCheck1051(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	c, _, err := root.Find([]string{"replace", "text"})
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(c.Long), " ")
	for _, want := range []string{
		"A skipped node still has the old text:",
		"comments (change them with comment edit)",
		"governed nodes: specs, reviews and runnable tasks (spec edit and spec replace change specs)",
		"compiled boot prompts (a compile writes them: team worker compile)",
		"nodes under a Channel's reserved address (channel list prints it)",
		`To check that the old text is gone, search for it afterwards: hadron search "<old>" -m <memory> lists the notes that still mention it (search also matches by meaning, so read the hits).`,
		"Pass --reason so the revision is easy to find; without it the revision carries no label of yours.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("replace text help lacks %q", want)
		}
	}
}
