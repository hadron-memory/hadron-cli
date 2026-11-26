package cmd

import (
	"strings"
	"testing"
)

// cli#3240 (ruling A: keep exit 1): an INTERNAL_SERVER_ERROR or a codeless server error on
// `memory sync push` exits 1 (the cli#898 contract), and the push may ALSO have happened. The
// help and the agentic-usage push entry say so, and neither reads as if exit 7 were the only
// outcome after which the push may have happened. The exit-1 pins live in
// memory_sync_push_2513_test.go and stay.
func TestSyncPushTextSaysAnInternalErrorExitsOneAndMayHaveHappened3240(t *testing.T) {
	help := strings.Join(strings.Fields(cmdHelpOut(t, "memory", "sync", "push")), " ")
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	usage := strings.Join(strings.Fields(out.String()), " ")
	i := strings.Index(usage, "### Push a memory to its repository")
	if i < 0 {
		t.Fatal("agentic-usage has no push entry")
	}
	entry := usage[i:]
	if j := strings.Index(entry[5:], "###"); j >= 0 {
		entry = entry[:j+5]
	}
	for name, text := range map[string]string{"memory sync push --help": help, "agentic-usage push entry": entry} {
		for _, want := range []string{"INTERNAL_SERVER_ERROR", "no code", "exits 1", "may also have happened", "check the repository before pushing again"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s does not say %q:\n%s", name, want, text)
			}
		}
		if !strings.Contains(text, "exit 7") && !strings.Contains(text, "exits 7") {
			t.Errorf("%s lost the exit 7 text for a missing, false or empty answer:\n%s", name, text)
		}
	}
}
