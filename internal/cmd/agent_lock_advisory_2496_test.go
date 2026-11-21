package cmd

import (
	"strings"
	"testing"
)

// cli#2496: Holger's ruling of 2026-11-18 (verbatim "a") makes the editor lock
// advisory — a coordination signal for an Agent's system memory; an edit from
// someone who does not hold it is not refused. Every `agent lock` help page
// states that, replacing the earlier "the server's decision" pointer, which no
// longer matches the ruled semantics. Each page is judged alone (help rubric:
// the reader has that page and nothing else), so parent and both verbs carry
// the statement.
func TestAgentLockHelpStatesAdvisory2496(t *testing.T) {
	help := func(path ...string) string {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(path, "--help"))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(out.String()), " ")
	}
	for _, path := range [][]string{
		{"agent", "lock"},
		{"agent", "lock", "acquire"},
		{"agent", "lock", "release"},
	} {
		name := strings.Join(path, " ")
		h := help(path...)
		if !strings.Contains(h, "The lock is advisory") {
			t.Errorf("%s: help must say the lock is advisory (a coordination signal), not a guard: %s", name, h)
		}
		if !strings.Contains(h, "is not refused") {
			t.Errorf("%s: help must say an edit from someone who does not hold the lock is not refused: %s", name, h)
		}
		if strings.Contains(h, "the server's decision") {
			t.Errorf("%s: the stale \"the server's decision\" wording is replaced by the ruled semantics (cli#2496): %s", name, h)
		}
	}
}
