package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"
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
	raw := out.String()
	doc := strings.Join(strings.Fields(raw), " ")
	const anchor = "The message's last line is that task ref alone, in full"
	// Exactly once (cli#2417 F2): a second occurrence would let the slice below
	// read the wrong paragraph.
	if n := strings.Count(doc, anchor); n != 1 {
		t.Fatalf("the link-defect ref sentence must occur exactly once, found %d", n)
	}
	start := strings.Index(doc, anchor)
	para := doc[start:]
	if end := strings.Index(para, "No retry."); end >= 0 {
		para = para[:end]
	}
	for _, want := range []string{
		"control characters in the ref show as spaces",
		"quote the ref when you pass it back to a POSIX shell",
		// cli#2417 F4: name a safe form. A ref can hold a single quote and
		// double quotes still expand $(...) and backticks.
		"Single-quote it, writing each embedded `'` as `'\\''`",
		"double quotes still expand `$(...)` and backticks",
	} {
		if !strings.Contains(para, want) {
			t.Errorf("link-defect paragraph lacks %q:\n%s", want, para)
		}
	}
}

// cli#2417 F3: the paragraph keeps the file's ~78-column wrap (no line past 80).
func TestAgenticUsageLinkDefectParagraphIsWrapped2417(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	first := -1
	for i, l := range lines {
		if strings.Contains(l, "The message's last line is that task ref alone") {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatal("the link-defect ref paragraph is missing")
	}
	ended := false
	for i := first; i < len(lines) && !ended; i++ {
		ended = strings.Contains(lines[i], "No retry.")
		if n := utf8.RuneCountInString(lines[i]); n > 80 {
			t.Errorf("agentic-usage line %d is %d columns, want at most 80: %q", i+1, n, lines[i])
		}
	}
	if !ended {
		t.Fatal("the paragraph never reaches its 'No retry.' sentence")
	}
}
