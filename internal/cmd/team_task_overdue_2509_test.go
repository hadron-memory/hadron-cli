package cmd

import (
	"strings"
	"testing"
)

// cli#2509 (server#2654): the overdue filter has TWO arms — a pending,
// not-started task whose start-by has passed, or a pending or started task
// whose finish-by has passed. The help must say so where a user types
// --overdue, and --finish-by's own help must say a passed finish-by lists
// the task under --overdue; both places also name the provider the
// finish-by arm needs. Text-only pins: the wire request and --json output
// are unchanged (the cli#1250 tri-state pins cover those).

// overdueFlagLine returns the --overdue line from the Flags: section, not a
// long-help paragraph line that also names --overdue.
func overdueFlagLine(t *testing.T, help string) string {
	t.Helper()
	flags := help
	if i := strings.Index(help, "\nFlags:"); i >= 0 {
		flags = help[i:]
	}
	for _, l := range strings.Split(flags, "\n") {
		if strings.Contains(l, "--overdue") {
			return l
		}
	}
	t.Fatalf("no --overdue flag line in help:\n%s", help)
	return ""
}

func TestTaskListOverdueHelpNamesTheFinishByArm2509(t *testing.T) {
	help := cmdHelpOut(t, "team", "task", "list")
	// The long help states both arms, whose clock judges them, the
	// never-overdue set, and the provider the finish-by arm needs.
	flat := strings.Join(strings.Fields(help), " ")
	for _, want := range []string{
		"finish-by has passed",
		"server clock",
		"never overdue",
		"Blocking is ignored",
		"needs a provider containing hadron-server#2654",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("team task list --help must say %q", want)
		}
	}
	// The flag line itself names both arms, so a user reading only the
	// flags learns the set.
	flagLine := overdueFlagLine(t, help)
	if !strings.Contains(flagLine, "start-by") || !strings.Contains(flagLine, "finish-by") {
		t.Errorf("the --overdue flag line must name both arms: %q", flagLine)
	}
}

func TestTaskCreateFinishByHelpMentionsOverdue2509(t *testing.T) {
	help := cmdHelpOut(t, "team", "task", "create")
	// The --finish-by PARAGRAPH must say it, not just the --start-by one
	// above it (which already names --overdue on main) — scope by the
	// paragraph's own distinctive phrase, not by the flag name.
	i := strings.Index(help, "sets a deadline to finish")
	if i < 0 {
		t.Fatalf("no --finish-by paragraph found:\n%s", help)
	}
	start := strings.LastIndex(help[:i], "\n\n")
	if start < 0 {
		start = 0
	} else {
		start += 2
	}
	end := len(help)
	if j := strings.Index(help[i:], "\n\n"); j >= 0 {
		end = i + j
	}
	para := strings.Join(strings.Fields(help[start:end]), " ")
	for _, want := range []string{
		"overdue",
		"needs a provider containing hadron-server#2654",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("the --finish-by help must say %q:\n%s", want, para)
		}
	}
	// And the start-by paragraph names its own arm, so it cannot read as
	// the only one.
	i = strings.Index(help, "sets when the task should be started")
	if i < 0 {
		t.Fatalf("no --start-by paragraph found:\n%s", help)
	}
	start = strings.LastIndex(help[:i], "\n\n")
	if start < 0 {
		start = 0
	} else {
		start += 2
	}
	end = len(help)
	if j := strings.Index(help[i:], "\n\n"); j >= 0 {
		end = i + j
	}
	startPara := strings.Join(strings.Fields(help[start:end]), " ")
	if !strings.Contains(startPara, "start-by arm") {
		t.Errorf("the --start-by help must name its arm:\n%s", startPara)
	}
}

func TestAgenticUsagePinsOverdueFinishByArm2509(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{
		"two arms on the server clock",
		"finish-by passed and not ended",
		"finish-by arm needs a provider containing hadron-server#2654",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("agentic-usage overdue filter sentence lacks %q", want)
		}
	}
}
