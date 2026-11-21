package cmd

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// linkDefectParagraph2440 returns the lines of the Markdown paragraph (the
// blank-line-bounded block) that holds the link-defect ref sentence, and its
// first line number. The sentence is found in the paragraph's whitespace-
// normalized text, so rewrapping it cannot hide it; the original lines and
// offsets are kept for the column check. Both tests run against this block, so
// a moved or missing "No retry." sentence cannot send them into unrelated prose
// (cli#2440, BLIND P3s).
func linkDefectParagraph2440(t *testing.T) (lines []string, firstLine int) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agentic-usage"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	// The message shows the ref in full (cli#2371): keep that claim in the anchor.
	const anchor = "The message's last line is that task ref alone, in full"
	all := strings.Split(out.String(), "\n")
	var found [][2]int
	for i := 0; i < len(all); {
		if strings.TrimSpace(all[i]) == "" {
			i++
			continue
		}
		j := i
		for j < len(all) && strings.TrimSpace(all[j]) != "" {
			j++
		}
		if strings.Contains(strings.Join(strings.Fields(strings.Join(all[i:j], " ")), " "), anchor) {
			found = append(found, [2]int{i, j})
		}
		i = j
	}
	switch len(found) {
	case 0:
		t.Fatal("the link-defect ref paragraph is missing")
	case 1:
	default:
		t.Fatal("the link-defect ref sentence must occur in exactly one paragraph (cli#2417 F2)")
	}
	para := all[found[0][0]:found[0][1]]
	if !strings.Contains(strings.Join(strings.Fields(strings.Join(para, " ")), " "), "No retry.") {
		t.Fatal("the link-defect paragraph has no 'No retry.' sentence")
	}
	return para, found[0][0] + 1
}

// Accepted limit (cli#2440, BLIND P2 raised by three reviewers and dispositioned
// by the coordinator on the PR): the ref in the failure message is the argument
// the caller just passed, so the doc tells them to reuse the ref they passed in.
// Copying the displayed ref is only for a ref without control characters, which
// the message shows as spaces; recovering a control-character ref from the
// displayed copy is not supported, and the CLI does not reject such a ref. The
// wording pin below holds that sentence.
//
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

// cli#2417 F3: the recovery text of the link-defect paragraph keeps the file's
// ~78-column wrap (no line past 80). The paragraph opens with older prose that
// was never wrapped, so the checked span starts where "A response with no link"
// begins, found across line breaks.
func TestAgenticUsageLinkDefectRecoveryTextIsWrapped2417(t *testing.T) {
	lines, first := linkDefectParagraph2440(t)
	const marker = "A response with no link"
	// Match the marker in the paragraph's whitespace-normalized text and map its
	// start back to the original line, so a rewrap anywhere inside it (even
	// between "A" and "response") cannot hide it.
	from := -1
	var norm strings.Builder
	var starts []int // starts[k]: the line that holds the k-th normalized byte
	for i, l := range lines {
		for _, w := range strings.Fields(l) {
			if norm.Len() > 0 {
				norm.WriteByte(' ')
				starts = append(starts, i)
			}
			norm.WriteString(w)
			for k := 0; k < len(w); k++ {
				starts = append(starts, i)
			}
		}
	}
	if at := strings.Index(norm.String(), marker); at >= 0 {
		from = starts[at]
	}
	if from < 0 {
		t.Fatalf("the recovery text (%q) is missing from the paragraph", marker)
	}
	for i := from; i < len(lines); i++ {
		if n := utf8.RuneCountInString(lines[i]); n > 80 {
			t.Errorf("agentic-usage line %d is %d columns, want at most 80: %q", first+i, n, lines[i])
		}
	}
}
