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
	if strings.Contains(c.Long, "skipped.\n--prefix") {
		t.Error("the --prefix paragraph runs on from the skipped-nodes paragraph")
	}
}
