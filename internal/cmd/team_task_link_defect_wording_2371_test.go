package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// linkDefectParagraph2440 returns the lines of the Markdown paragraph (the
// blank-line-bounded block) that holds the link-defect ref sentence, and its
// first line number. Both tests below run against this block, so a moved or
// missing "No retry." sentence cannot send them into unrelated prose, and the
// opening lines of the paragraph are checked too (cli#2440, BLIND P3).
func linkDefectParagraph2440(t *testing.T) (lines []string, firstLine int) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	all := strings.Split(out.String(), "\n")
	at := -1
	for i, l := range all {
		if strings.Contains(l, "The message's last line is that task ref alone") {
			if at >= 0 {
				t.Fatal("the link-defect ref sentence must occur exactly once (cli#2417 F2)")
			}
			at = i
		}
	}
	if at < 0 {
		t.Fatal("the link-defect ref paragraph is missing")
	}
	start, end := at, at
	for start > 0 && strings.TrimSpace(all[start-1]) != "" {
		start--
	}
	for end+1 < len(all) && strings.TrimSpace(all[end+1]) != "" {
		end++
	}
	para := all[start : end+1]
	if !strings.Contains(strings.Join(para, " "), "No retry.") {
		t.Fatal("the link-defect paragraph has no 'No retry.' sentence")
	}
	return para, start + 1
}

// cli#2371 F4: the link-defect paragraph in agentic-usage says the last line is
// the task ref in full, that control characters in it show as spaces (so reuse
// the ref you passed in), and that the ref should be quoted when passed back to
// a POSIX shell.
func TestAgenticUsageLinkDefectRefWording2371(t *testing.T) {
	lines, _ := linkDefectParagraph2440(t)
	para := strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
	for _, want := range []string{
		"control characters in the ref show as spaces",
		// cli#2440 BLIND P2: the displayed ref can differ from the original.
		"reuse the ref you passed in when you still hold it",
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
	lines, first := linkDefectParagraph2440(t)
	// The paragraph opens with older prose that was never wrapped; the recovery
	// text this pin guards starts at "A response with no link" and runs to the
	// paragraph's end, so that is the span held to the column limit.
	from := -1
	for i, l := range lines {
		if strings.Contains(l, "A response") {
			from = i
			break
		}
	}
	if from < 0 {
		t.Fatal("the recovery text ('A response with no link') is missing from the paragraph")
	}
	for i := from; i < len(lines); i++ {
		if n := utf8.RuneCountInString(lines[i]); n > 80 {
			t.Errorf("agentic-usage line %d is %d columns, want at most 80: %q", first+i, n, lines[i])
		}
	}
}
