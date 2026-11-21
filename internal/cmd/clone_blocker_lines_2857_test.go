package cmd

import (
	"strings"
	"testing"
	"unicode"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// cli#2857: the render site is the last guard before the terminal for each blocker line. The
// producer (api.CloneAgentBlockerLines) already neutralises each part, so this pins the render
// site itself: whatever a line carries, nothing that can act on a terminal reaches the writer,
// and the line still names its code and subject (cor:dmo:050:18).
func TestWriteBlockerLinesNeutralisesWhatItIsGiven2857(t *testing.T) {
	var out strings.Builder
	writeBlockerLines(&out, []string{
		"blocker CODE  count 2  \"Shared \x1b[31mmem\u202eory\u200b\" (position 1), position 2",
		"blocker PLAIN  \"ordinary name\" (position 3)",
		"blocker OSC \x1b]0;title\x07 \"after\"",
		"blocker C1 \u009b31m \"x\"",
	})
	got := out.String()
	for _, r := range got {
		if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf)) {
			t.Fatalf("a control or format character reached the writer: %U in %q", r, got)
		}
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("one output line per blocker, got %d: %q", len(lines), got)
	}
	for i, want := range []string{"  blocker CODE  count 2", "  blocker PLAIN  \"ordinary name\" (position 3)", "  blocker OSC", "  blocker C1"} {
		if !strings.HasPrefix(lines[i], want) {
			t.Errorf("line %d lost its code or subject: %q", i, lines[i])
		}
	}
	if !strings.Contains(lines[0], "Shared") || !strings.Contains(lines[0], "(position 1)") || !strings.Contains(lines[0], "position 2") {
		t.Errorf("the named subject must stay readable: %q", lines[0])
	}
}

// End to end: a hostile blocker name from the server reaches stderr as readable text.
func TestCloneRefusalBlockerNameNeverReachesStderrRaw2857(t *testing.T) {
	cause := gqlerror.List{{Message: "cannot clone", Extensions: map[string]any{
		"code": "CLONE_TARGET_UNSUPPORTED", "reason": "REASON", "retryable": false,
		"blockers": []any{map[string]any{"code": "LINKED", "count": float64(1), "subjects": []any{
			map[string]any{"position": float64(1), "name": "Mem\x1b[31mory\u202e\u200b\u009b1m"}}}}}}}
	f, _ := testFactory(t)
	var errOut strings.Builder
	f.IOStreams.ErrOut = &errOut
	renderError(f, exitcode.New(exitcode.Usage, api.MapCloneAgentError(cause)))
	got := errOut.String()
	for _, r := range got {
		if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf)) {
			t.Fatalf("%U reached stderr: %q", r, got)
		}
	}
	if !strings.Contains(got, "blocker LINKED") || !strings.Contains(got, "Mem") || !strings.Contains(got, "(position 1)") {
		t.Errorf("the refusal must still name its blocker and subject: %q", got)
	}
}
