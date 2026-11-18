package cmd

import (
	"strings"
	"testing"
)

// cli#2509 (server#2654): the overdue filter has TWO arms — a pending,
// not-started task whose start-by has passed, or a pending or started task
// whose finish-by has passed and which has not ended. The help must say so
// where a user types --overdue, and --finish-by's own help must say a passed
// finish-by lists the task under --overdue. Text-only pins: the wire request
// and --json output are unchanged (the cli#1250 tri-state pins cover those).

func helpFor2509(t *testing.T, argv ...string) string {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(argv)
	root.SetOut(out)
	root.SetErr(out)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestTaskListOverdueHelpNamesTheFinishByArm2509(t *testing.T) {
	help := helpFor2509(t, "team", "task", "list", "--help")
	// The long help states both arms and whose clock judges them.
	for _, want := range []string{"finish-by has passed", "server clock"} {
		if !strings.Contains(help, want) {
			t.Errorf("team task list --help must say %q:\n%s", want, help)
		}
	}
	// The flag line itself names both arms, so a user reading only the
	// flags learns the set.
	var flagLine string
	for _, l := range strings.Split(help, "\n") {
		if strings.Contains(l, "--overdue") {
			flagLine = l
		}
	}
	if !strings.Contains(flagLine, "start-by") || !strings.Contains(flagLine, "finish-by") {
		t.Errorf("the --overdue flag line must name both arms: %q", flagLine)
	}
}

func TestTaskCreateFinishByHelpMentionsOverdue2509(t *testing.T) {
	help := helpFor2509(t, "team", "task", "create", "--help")
	// The --finish-by PARAGRAPH must say it, not just the --start-by one
	// above it (which already names --overdue on main, and whose cli#2509
	// edit also mentions --finish-by — so scope by the paragraph's own
	// distinctive phrase, not by the flag name).
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
	para := help[start:end]
	if !strings.Contains(para, "overdue") {
		t.Errorf("the --finish-by help must say a passed finish-by lists the task under task list --overdue:\n%s", para)
	}
	// And the start-by paragraph must not read as the only arm.
	if strings.Contains(help, "only start-by") {
		t.Errorf("start-by help implies it is the only overdue arm:\n%s", help)
	}
}
