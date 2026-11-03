package cmd

import (
	"strings"
	"testing"
)

// #1170: the served-prompt help says that a null record is a successful read
// (unlike node provenance's exit 4), and what provenance.limit and .offset
// mean once --all has aggregated every page. node provenance names the
// difference from its side.
func TestServedPromptHelpExplainsNullSuccessAndAllPaging(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	for _, argv := range [][]string{{"team", "worker", "served-prompt"}, {"run", "served-prompt"}} {
		help := flat(cmdHelpOut(t, argv...))
		for _, want := range []string{
			"It is still a successful read: exit 0 with {servedPrompt: null}.",
			"(node provenance differs: an unavailable compiled revision there exits 4.)",
			"With --all, provenance.items holds every page, while provenance.limit and .offset still describe the requests: the page size each one asked for, and offset 0.",
		} {
			if !strings.Contains(help, want) {
				t.Errorf("%s help lacks %q", strings.Join(argv, " "), want)
			}
		}
	}
	if help := flat(cmdHelpOut(t, "team", "worker", "served-prompt")); !strings.Contains(help, "it is not necessarily the worker's latest record overall") {
		t.Error("worker served-prompt help lacks the newest-started vs latest-record note")
	}
	if help := flat(cmdHelpOut(t, "node", "provenance")); !strings.Contains(help, "(The served-prompt reads differ: an unavailable served record is a successful null, exit 0.)") {
		t.Error("node provenance help lacks the served-prompt contrast")
	}
}
