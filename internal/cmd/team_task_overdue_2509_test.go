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
	// No silent fallback to the whole help (cli#2520): a help with no Flags:
	// heading would otherwise let a long-help line stand in for the flag line.
	i := strings.Index(help, "\nFlags:")
	if i < 0 {
		t.Fatalf("no Flags: section in help:\n%s", help)
	}
	for _, l := range strings.Split(help[i:], "\n") {
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
	// The never-overdue set (backlog, ended, no time) stays in the long help only
	// (cli#2520, cor:agt:050:14), so the flag line must not read as the whole
	// definition: each arm keeps its qualifier.
	if !strings.Contains(flagLine, "not started") || !strings.Contains(flagLine, "not ended") {
		t.Errorf("the --overdue flag line must keep each arm's qualifier (not started / not ended), so it does not claim to be complete: %q", flagLine)
	}
}

// paragraphAround2509 returns the blank-line-delimited paragraph of help that
// contains phrase, with its original line breaks (cli#2520: one extraction for
// every paragraph pin, scoped by the paragraph's own distinctive phrase).
func paragraphAround2509(t *testing.T, help, phrase string) string {
	t.Helper()
	i := strings.Index(help, phrase)
	if i < 0 {
		t.Fatalf("no paragraph containing %q found:\n%s", phrase, help)
	}
	start := 0
	if j := strings.LastIndex(help[:i], "\n\n"); j >= 0 {
		start = j + 2
	}
	end := len(help)
	if j := strings.Index(help[i:], "\n\n"); j >= 0 {
		end = i + j
	}
	return help[start:end]
}

func TestTaskCreateFinishByHelpMentionsOverdue2509(t *testing.T) {
	help := cmdHelpOut(t, "team", "task", "create")
	// The --finish-by PARAGRAPH must say it, not just the --start-by one
	// above it (which already names --overdue on main).
	para := strings.Join(strings.Fields(paragraphAround2509(t, help, "sets a deadline to finish")), " ")
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
	startPara := strings.Join(strings.Fields(paragraphAround2509(t, help, "sets when the task should be started")), " ")
	if !strings.Contains(startPara, "start-by arm") {
		t.Errorf("the --start-by help must name its arm:\n%s", startPara)
	}
}

// cli#2520: the overdue paragraphs wrap evenly: every line but the last is a
// full line, and the last is not an orphan word or two.
func assertEvenWrap2509(t *testing.T, name, para string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(para, "\n"), "\n")
	for i, l := range lines {
		n := len([]rune(l))
		switch {
		case n > 80:
			t.Errorf("%s line %d is %d columns (over 80): %q", name, i+1, n, l)
		case i < len(lines)-1 && n < 60:
			t.Errorf("%s line %d is short (%d columns) before the paragraph ends: %q", name, i+1, n, l)
		case i == len(lines)-1 && n < 25:
			t.Errorf("%s ends in an orphan line (%d columns): %q", name, n, l)
		}
	}
}

func TestOverdueHelpParagraphsWrapEvenly2520(t *testing.T) {
	list := cmdHelpOut(t, "team", "task", "list")
	i := strings.Index(list, "--overdue lists tasks overdue")
	j := strings.Index(list[i:], "\n--created-/")
	if i < 0 || j < 0 {
		t.Fatalf("no --overdue paragraph followed by the range-bound one in:\n%s", list)
	}
	assertEvenWrap2509(t, "task list --overdue paragraph", list[i:i+j])
	// The range-bound sentences next to it (the same wrap pass).
	k := strings.Index(list[i+j:], "\n\n")
	assertEvenWrap2509(t, "task list range-bound paragraph", strings.TrimPrefix(list[i+j:i+j+k], "\n"))
	create := cmdHelpOut(t, "team", "task", "create")
	assertEvenWrap2509(t, "task create --finish-by paragraph", paragraphAround2509(t, create, "sets a deadline to finish"))
}

func TestAgenticUsagePinsOverdueFinishByArm2509(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Wired like the other help pins, so cobra output cannot leak to the real
	// stdout/stderr or be missed here (cli#2520).
	root.SetOut(out)
	root.SetErr(f.IOStreams.ErrOut)
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
