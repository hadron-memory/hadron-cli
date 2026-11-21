package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// ---- spec link warning must not be cell-clipped (cli#2385 item 2) ----
//
// The not-governed warning carries the repair command, and the URN inside it
// grows one rune per loc segment: with a deep citation the whole line runs
// far past SafeCell's 256-rune cell bound, which used to cut the tail — the
// "hadron node update <urn> --role spec" the reader was told to run (Joshua's
// BLIND P3 on #2378; the gap was confirmed by the AT0.9221 sweep evidence).
// A warning line is not a table cell: it is sanitised whole (SanitizeLine,
// cli#2371) and never truncated, so the command stays copy-pasteable.

// longSourceLoc2385 and longTargetLoc2385 are the link's two ends, named by
// endpoint: link refuses to link a citation to itself, so the target differs
// in its last segment. Each end's node fixture describes its own citation, so
// a warning naming the wrong end cannot satisfy the per-endpoint assertions
// below (Joaquin's BLIND P2 on #2779).
var longSourceLoc2385 = strings.Join([]string{
	"cor", "dmo", "020", "04",
	"a1b2c3d4", "a1b2c3d4", "a1b2c3d4", "a1b2c3d4",
	"a1b2c3d4", "a1b2c3d4", "a1b2c3d4", "a1b2c3d4",
	"a1b2c3d4", "a1b2c3d4", "a1b2c3d4", "a1b2c3d4",
}, ":")

var longTargetLoc2385 = strings.TrimSuffix(longSourceLoc2385, "a1b2c3d4") + "z9y8x7w6"

// longLocDetail2385 is linkSpecDetail at loc: spec-tagged, no governed role,
// so the end at that citation warns. `spec link` fetches from before to, so
// queueing this for each end answers every fetch with the node at the
// citation that end asked about.
func longLocDetail2385(loc string) string {
	return strings.ReplaceAll(linkSpecDetail, "cor:dmo:060:02", loc)
}

// linkLongQueue2385 is the fake flow both tests drive: every end tag-only, so
// both warnings fire and the edge is still created.
func linkLongQueue2385() map[string][]string {
	return map[string][]string{
		"ResolveUrn": {resolveSpecJSON},
		"GetNode":    {longLocDetail2385(longSourceLoc2385), longLocDetail2385(longTargetLoc2385)},
		"CreateEdge": {linkEdgeResp},
	}
}

func TestSpecLinkWarningKeepsTheRepairCommandWhole2385(t *testing.T) {
	gql, captured := queueGraphQL(t, linkLongQueue2385())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", longSourceLoc2385, longTargetLoc2385, "-m", specMem, "--label", "documents the thing", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a tag-only end must still link (exit 0): %v", err)
	}
	if got := len(captured["CreateEdge"]); got != 1 {
		t.Fatalf("CreateEdge calls = %d, want 1", got)
	}
	urn := cmdutil.NodeURN(specMem, longSourceLoc2385)
	var warning string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "warning: ") {
			warning = strings.TrimPrefix(line, "warning: ")
			break
		}
	}
	if warning == "" {
		t.Fatalf("no warning line printed:\n%s", out.String())
	}
	// The case must exercise the bound: a cell-clipped render of this
	// warning cannot keep the command.
	if n := utf8.RuneCountInString(warning); n <= 256 {
		t.Fatalf("the fixture warning is %d runes; make the loc longer so a 256-rune cut would clip the remedy", n)
	}
	if strings.Contains(warning, "…") {
		t.Fatalf("the warning was truncated:\n%s", warning)
	}
	if !strings.Contains(warning, urn) {
		t.Fatalf("the warning must carry the whole node URN %s:\n%s", urn, warning)
	}
	if !strings.HasSuffix(warning, "--role spec") {
		t.Fatalf("the warning must end with the repair command's tail:\n%s", warning)
	}
	// The full line is the whole remedy, not a prefix of it: what the
	// reader copies is exactly the command they were told to run.
	if !strings.Contains(warning, fmt.Sprintf("use hadron node update %s --role spec", urn)) {
		t.Fatalf("the repair command must appear whole:\n%s", warning)
	}
}

// The --json arm already carried the full text (cli#2385: "--json is
// unaffected"); pin that the two renderings agree, so a future change cannot
// quietly make text the lossy one again by regenerating only one of them.
// Each warning names its own end, and the text's warning LINES are compared
// with the JSON array element-wise (through the same sanitiser the renderer
// uses) — a dropped, duplicated or mangled warning cannot pass a substring
// loop by matching one occurrence twice (#2779 BLIND P2).
func TestSpecLinkWarningJSONMatchesText2385(t *testing.T) {
	run := func(jsonOut bool) string {
		gql, _ := queueGraphQL(t, linkLongQueue2385())
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"spec", "link", longSourceLoc2385, longTargetLoc2385, "-m", specMem, "--label", "documents the thing", "--server", gql.URL}
		if jsonOut {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("link (json=%v): %v", jsonOut, err)
		}
		return out.String()
	}
	// Capture the JSON arm once: re-running it for the diagnostic would hit
	// a fresh fake server and could show output other than the one that
	// failed to parse (#2779 BLIND P3).
	jsonOut := run(true)
	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, jsonOut)
	}
	if len(got.Warnings) != 2 {
		t.Fatalf("warnings = %q, want one per tag-only end", got.Warnings)
	}
	// Each JSON warning names its own end and carries that end's whole
	// repair command — the source warning is not satisfiable by the target's
	// text and vice versa.
	for i, end := range []struct{ loc, urn string }{
		{longSourceLoc2385, cmdutil.NodeURN(specMem, longSourceLoc2385)},
		{longTargetLoc2385, cmdutil.NodeURN(specMem, longTargetLoc2385)},
	} {
		w := got.Warnings[i]
		if !strings.Contains(w, end.loc) || !strings.Contains(w, fmt.Sprintf("use hadron node update %s --role spec", end.urn)) {
			t.Fatalf("warning %d must name its own end %s and its whole repair command:\n%s", i, end.loc, w)
		}
	}
	text := run(false)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "warning: ") {
			lines = append(lines, strings.TrimPrefix(line, "warning: "))
		}
	}
	if len(lines) != len(got.Warnings) {
		t.Fatalf("text printed %d warning lines, want %d:\n%s", len(lines), len(got.Warnings), text)
	}
	for i, w := range got.Warnings {
		if want := output.SanitizeLine(w); lines[i] != want {
			t.Fatalf("text warning %d = %q, want the JSON warning sanitised = %q:\n%s", i, lines[i], want, text)
		}
	}
}

// linkFailingWriter2385 accepts `ok` writes then fails, like a pipe that
// closed mid-render: ok=0 fails the summary line; ok=1 lets the summary land
// and fails the first warning line — the repair command left unwritten.
type linkFailingWriter2385 struct{ ok int }

func (w *linkFailingWriter2385) Write(p []byte) (int, error) {
	if w.ok <= 0 {
		return 0, io.ErrClosedPipe
	}
	w.ok--
	return len(p), nil
}

// The edge write has already happened by the time the result renders, so a
// swallowed render error would report success for a link whose remedy the
// reader never saw whole (#2779 BLIND P3): both writes must be checked and
// the first error surfaced.
func TestSpecLinkRenderSurfacesWriteErrors2385(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   int
	}{
		{"summary line", 0},
		{"warning line", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := queueGraphQL(t, linkLongQueue2385())
			f, _ := testFactory(t)
			f.IOStreams.Out = &linkFailingWriter2385{ok: tc.ok}
			root := NewRootCmd(f)
			root.SetArgs([]string{"spec", "link", longSourceLoc2385, longTargetLoc2385, "-m", specMem, "--label", "documents the thing", "--server", gql.URL})
			if err := root.Execute(); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("a stdout that closed mid-render must surface its error, got %v", err)
			}
		})
	}
}
