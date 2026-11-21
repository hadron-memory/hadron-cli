package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
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

// linkLongLoc2385 is a valid citation (each colon-separated segment obeys the
// atom rule) long enough that the warning built on it exceeds 256 runes.
var linkLongLoc2385 = strings.Join([]string{
	"cor", "dmo", "020", "04",
	"a1b2c3d4", "a1b2c3d4", "a1b2c3d4", "a1b2c3d4",
	"a1b2c3d4", "a1b2c3d4", "a1b2c3d4", "a1b2c3d4",
	"a1b2c3d4", "a1b2c3d4", "a1b2c3d4", "a1b2c3d4",
}, ":")

// linkLongLocTo2385 differs from linkLongLoc2385 in its last segment: link
// refuses to link a citation to itself, so the two ends must be distinct
// citations. Both resolve to the same tag-only node shape in the fake.
var linkLongLocTo2385 = strings.TrimSuffix(linkLongLoc2385, "a1b2c3d4") + "z9y8x7w6"

// linkLongLocDetail2385 is linkSpecDetail at that loc: spec-tagged, no
// governed role, so both ends of the link warn.
func linkLongLocDetail2385() string {
	return strings.ReplaceAll(linkSpecDetail, "cor:dmo:060:02", linkLongLoc2385)
}

func TestSpecLinkWarningKeepsTheRepairCommandWhole2385(t *testing.T) {
	gql, captured := queueGraphQL(t, map[string][]string{
		"ResolveUrn": {resolveSpecJSON},
		"GetNode":    {linkLongLocDetail2385(), linkLongLocDetail2385()},
		"CreateEdge": {linkEdgeResp},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "link", linkLongLoc2385, linkLongLocTo2385, "-m", specMem, "--label", "documents the thing", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a tag-only end must still link (exit 0): %v", err)
	}
	if got := len(captured["CreateEdge"]); got != 1 {
		t.Fatalf("CreateEdge calls = %d, want 1", got)
	}
	urn := cmdutil.NodeURN(specMem, linkLongLoc2385)
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
func TestSpecLinkWarningJSONMatchesText2385(t *testing.T) {
	run := func(jsonOut bool) string {
		gql, _ := queueGraphQL(t, map[string][]string{
			"ResolveUrn": {resolveSpecJSON},
			"GetNode":    {linkLongLocDetail2385(), linkLongLocDetail2385()},
			"CreateEdge": {linkEdgeResp},
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"spec", "link", linkLongLoc2385, linkLongLocTo2385, "-m", specMem, "--label", "documents the thing", "--server", gql.URL}
		if jsonOut {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("link (json=%v): %v", jsonOut, err)
		}
		return out.String()
	}
	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(run(true)), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, run(true))
	}
	if len(got.Warnings) != 2 {
		t.Fatalf("warnings = %q, want one per tag-only end", got.Warnings)
	}
	text := run(false)
	for _, w := range got.Warnings {
		if !strings.Contains(text, w) {
			t.Fatalf("the text render must carry the full warning %q:\n%s", w, text)
		}
	}
}
