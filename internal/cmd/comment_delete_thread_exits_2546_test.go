package cmd

import (
	"strings"
	"testing"
)

// cli#2546: comment delete --thread has two failure exits besides the usual
// refusals (#2538, behaviour pinned in internal/cmd/comment): an answer that
// carries no result is exit 1 (outcome unknown, read the thread before
// retrying), and an explicit deleted:false is exit 5 (not deleted). Neither
// prints a receipt on stdout. The help and the agentic-usage page must name them beside
// the exit 0 receipt, because a caller that retries blind after exit 1 can
// double-act and one that treats exit 5 as transient loops.
func TestCommentDeleteThreadHelpNamesOutcomeExits2546(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	c, _, err := root.Find([]string{"comment", "delete"})
	if err != nil || c.Name() != "delete" {
		t.Fatalf("comment delete: %v", err)
	}
	long := strings.Join(strings.Fields(c.Long), " ")
	// cli#2573 item 4: fail cleanly if the marker is reworded. The --thread
	// text is the last of the verb's Long (the outcomes paragraph follows the
	// marker), so the slice runs to its end and reads no other text.
	marker := "--thread --reason <text> deletes"
	at := strings.Index(long, marker)
	if at < 0 {
		t.Fatalf("comment delete help has no paragraph starting %q", marker)
	}
	para := long[at:]
	for _, want := range []string{
		"exit 1", "outcome unknown", "no result", "comment get", "before retrying",
		"exit 5", "deleted:false",
		// cli#2573 item 1: no receipt on stdout; with --json the standard error envelope.
		"no receipt on stdout (with --json, the standard error envelope)",
		// item 2: one wording for exit 4. Item 3: one receipt rendering.
		"a comment that is missing or unreadable",
		`{"thread":"<ref>","deleted":true}`, "thread is the comment ref you named",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("the --thread paragraph of comment delete help must contain %q: %q", want, para)
		}
	}
	usage := c.Flags().Lookup("thread").Usage
	// cli#2573 item 5: the flag usage points at the verb text instead of
	// carrying a third copy of the exit semantics.
	if !strings.Contains(usage, "explained above") || strings.Contains(usage, "exit 1") {
		t.Errorf("--thread usage must point at the text above and not repeat the exits: %q", usage)
	}
}

func TestAgenticUsageCommentDeleteThreadExits2546(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var found string
	for _, p := range strings.Split(out.String(), "\n\n") {
		if strings.HasPrefix(p, "`comment delete --thread`") {
			found = strings.Join(strings.Fields(p), " ")
		}
	}
	if found == "" {
		t.Fatal("agentic-usage has no paragraph starting with `comment delete --thread`")
	}
	for _, want := range []string{
		`{"thread":`, `"deleted":true`, "exit 0",
		"exit 1", "outcome unknown", "no result", "comment get", "before retrying",
		"exit 5", "deleted:false",
		"no receipt on stdout (with `--json`, the standard error envelope)",
		"a comment that is missing or unreadable 4",
		"thread is the comment ref you named",
	} {
		if !strings.Contains(found, want) {
			t.Errorf("the comment delete --thread paragraph must contain %q: %q", want, found)
		}
	}
}
