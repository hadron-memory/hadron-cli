package cmd

import (
	"strings"
	"testing"
)

// cli#2546: comment delete --thread has two failure exits besides the usual
// refusals (#2538, behaviour pinned in internal/cmd/comment): an answer that
// carries no result is exit 1 (outcome unknown, read the thread before
// retrying), and an explicit deleted:false is exit 5 (not deleted). Both print
// nothing to stdout. The help and the agentic-usage page must name them beside
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
	para := long[strings.Index(long, "--thread --reason <text> deletes"):]
	for _, want := range []string{
		"exit 1", "outcome unknown", "comment get", "before retrying",
		"exit 5", "deleted:false", "prints nothing",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("the --thread paragraph of comment delete help must contain %q: %q", want, para)
		}
	}
	usage := c.Flags().Lookup("thread").Usage
	for _, want := range []string{"exit 1", "exit 5"} {
		if !strings.Contains(usage, want) {
			t.Errorf("--thread usage must name %q: %q", want, usage)
		}
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
		"exit 1", "outcome unknown", "comment get", "before retrying",
		"exit 5", "deleted:false", "no stdout",
	} {
		if !strings.Contains(found, want) {
			t.Errorf("the comment delete --thread paragraph must contain %q: %q", want, found)
		}
	}
}
