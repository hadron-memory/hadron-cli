package cmd

import (
	"strings"
	"testing"
)

// cli#2371 F4: the link-defect paragraph in agentic-usage says the last line is
// the task ref in full, that control characters in it show as spaces, and that
// the ref should be quoted when passed back to a shell.
func TestAgenticUsageLinkDefectRefWording2371(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	doc := strings.Join(strings.Fields(out.String()), " ")
	start := strings.Index(doc, "The message's last line is that task ref alone, in full")
	if start < 0 {
		t.Fatal("the link-defect ref sentence is missing")
	}
	para := doc[start:]
	if end := strings.Index(para, "No retry."); end >= 0 {
		para = para[:end]
	}
	for _, want := range []string{
		"control characters in the ref show as spaces",
		"quote the ref when you pass it back to a shell",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("link-defect paragraph lacks %q:\n%s", want, para)
		}
	}
}
