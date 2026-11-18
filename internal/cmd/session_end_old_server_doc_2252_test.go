package cmd

import (
	"strings"
	"testing"
)

// cli#2252: the session-end paragraph in agentic-usage documents the
// old-server exception that TestSessionEndOnAServerWithoutContinuityIsToday1720
// pins: a server without Worker.continuity prints no handoff line. Spec FIT
// wording condition: it says only that no line is printed, never that the
// handoff was not saved (cor:agt:020:10 still wrote it before the end).
func TestAgenticUsageSessionEndOldServerException2252(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	doc := out.String()
	start := strings.Index(doc, "handoff saved; URN not read")
	if start < 0 {
		t.Fatal("session-end read-back paragraph is missing")
	}
	para := doc[start:]
	if end := strings.Index(para, "Passing it on a session with no"); end >= 0 {
		para = para[:end]
	}
	para = strings.Join(strings.Fields(para), " ")
	for _, want := range []string{
		"Worker.continuity",
		"prints no handoff line",
		"does not mean the handoff was lost",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("session-end paragraph lacks %q:\n%s", want, para)
		}
	}
	for _, bad := range []string{"not saved", "was not written", "is lost"} {
		if strings.Contains(para, bad) {
			t.Errorf("session-end paragraph must not imply the handoff went unsaved (%q):\n%s", bad, para)
		}
	}
}
