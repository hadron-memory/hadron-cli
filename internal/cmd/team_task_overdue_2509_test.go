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

// helpParagraph returns the blank-line-delimited paragraph of help that
// contains phrase, with its original line breaks (cli#2520: one extraction for
// every paragraph pin, scoped by the paragraph's own distinctive phrase).
func helpParagraph(t *testing.T, help, phrase string) string {
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
	para := strings.Join(strings.Fields(helpParagraph(t, help, "sets a deadline to finish")), " ")
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
	startPara := strings.Join(strings.Fields(helpParagraph(t, help, "sets when the task should be started")), " ")
	if !strings.Contains(startPara, "start-by arm") {
		t.Errorf("the --start-by help must name its arm:\n%s", startPara)
	}
}

// The wrap shape of the overdue paragraphs (cli#2520): every line but the last
// is a full line, and the last is not an orphan word or two. The numbers are
// named so a deliberate help edit changes one constant, not a scattered literal.
const (
	wrapMaxRunes     = 80 // no line is longer than this many runes
	wrapMinFullRunes = 60 // every line but the last is at least this long
	wrapMinLastRunes = 25 // the last line is at least this long (no orphan)
)

func assertEvenWrap(t *testing.T, name, para string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(para, "\n"), "\n")
	for i, l := range lines {
		n := len([]rune(l)) // runes, not display columns: the help is ASCII apart from dashes
		switch {
		case n > wrapMaxRunes:
			t.Errorf("%s line %d is %d runes (over %d): %q", name, i+1, n, wrapMaxRunes, l)
		case i < len(lines)-1 && n < wrapMinFullRunes:
			t.Errorf("%s line %d is short (%d runes) before the paragraph ends: %q", name, i+1, n, l)
		case i == len(lines)-1 && n < wrapMinLastRunes:
			t.Errorf("%s ends in an orphan line (%d runes): %q", name, n, l)
		}
	}
}

// textBetween returns help from the line starting with from up to (not
// including) the line starting with to, or to the end of help when to is "" or
// absent; it fails the test, never panics, when from is missing.
func textBetween(t *testing.T, help, from, to string) string {
	t.Helper()
	i := strings.Index(help, from)
	if i < 0 {
		t.Fatalf("no text starting %q in:\n%s", from, help)
	}
	rest := help[i:]
	end := len(rest)
	if to != "" {
		if j := strings.Index(rest, to); j >= 0 {
			end = j
		}
	}
	return strings.TrimRight(rest[:end], "\n")
}

func TestOverdueHelpParagraphsWrapEvenly2520(t *testing.T) {
	list := cmdHelpOut(t, "team", "task", "list")
	// The --overdue paragraph runs straight into the range-bound one (no blank
	// line between them), then a blank line ends the range-bound one.
	assertEvenWrap(t, "task list --overdue paragraph", textBetween(t, list, "--overdue lists tasks overdue", "\n--created-/"))
	rangeBound := textBetween(t, list, "--created-/--started-/--finished-after", "\n\n")
	assertEvenWrap(t, "task list range-bound paragraph", rangeBound)
	create := cmdHelpOut(t, "team", "task", "create")
	assertEvenWrap(t, "task create --finish-by paragraph", helpParagraph(t, create, "sets a deadline to finish"))
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
	if errOut := f.IOStreams.ErrOut.(*strings.Builder).String(); errOut != "" {
		t.Errorf("agentic-usage wrote to stderr: %q", errOut)
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
