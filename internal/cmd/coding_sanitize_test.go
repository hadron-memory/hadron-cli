package cmd

import (
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1104: the coding command tables and plan views render server-supplied
// cells (check locs, route labels, edge ids); text sanitizes them, --json
// keeps the originals (the coding --json payloads are pinned byte-for-byte by
// the existing coding_cmd_test.go JSON tests, which the text-only SafeCell
// wrap cannot reach).

// sanInEdge/sanOutEdge are inEdge/outEdge with every server-supplied string
// JSON-escaped, so a hostile or 1000-rune loc cannot break the fixture.
func sanInEdge(id, label, sourceLoc string) string {
	return `{"id":"` + id + `","name":` + jsonStr(label) + `,"loc":` + jsonStr(sourceLoc+":x:review") +
		`,"isRunnable":false,"priority":0,"source":{"id":"s_` + id + `","loc":` + jsonStr(sourceLoc) +
		`,"memoryId":"` + codingMem + `"}}`
}

func sanOutEdge(id, label, targetLoc string) string {
	return `{"id":"` + id + `","name":` + jsonStr(label) + `,"loc":` + jsonStr("preflight:x:"+targetLoc) +
		`,"isRunnable":false,"priority":0,"target":{"id":"t_` + id + `","loc":` + jsonStr(targetLoc) +
		`,"memoryId":"` + codingMem + `"}}`
}

func sanListNode(loc string) string {
	return `{"id":"n_x","memoryId":"mem1","loc":` + jsonStr(loc) + `,"name":` + jsonStr(loc) +
		`,"nodeType":"info","tags":[],"seq":null,"isRunnable":false,"updatedAt":"2026-07-30T00:00:00Z"}`
}

func sanBatchNode(loc string) string {
	return `{"id":"n_x","memoryId":"mem1","loc":` + jsonStr(loc) + `,"name":` + jsonStr(loc) +
		`,"alias":null,"nodeType":"info","objectType":null,"isRunnable":false,"description":"d",
		"abstract":null,"abstractOriginHash":null,"tags":["review"],"seq":null,"data":null,"properties":null,
		"content":null,"createdAt":"2026-07-30T00:00:00Z","updatedAt":"2026-07-30T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}`
}

// cutCellPrefixed is SafeCell's documented rendering of prefix+longCellText:
// 256 rendered runes in all, so a p-rune prefix leaves 255-p of the value
// before the ellipsis.
func cutCellPrefixed(prefix string) string {
	b := []rune(prefix + longCellText)
	return string(b[:255]) + "…"
}

// assertNoRawHostile fails when the raw hostile label or the uncut 1000-rune
// value survives into rendered text (the sanitized forms replace the newline
// and the ESC with spaces, and cut the long value).
func assertCut(t *testing.T, text string, prefix string, want int) {
	t.Helper()
	if n := strings.Count(text, cutCellPrefixed(prefix)); n != want {
		t.Errorf("want %d cell(s) cut from %q…, got %d; long lines: %q", want, prefix, n,
			strings.Join(strings.Split(text, "\n"), " | "))
	}
}

func assertNoRawHostile(t *testing.T, text string) {
	t.Helper()
	if strings.Contains(text, hostileCellLabel) {
		t.Fatalf("the raw hostile value survived sanitization:\n%q", text)
	}
	if strings.Contains(text, longCellText) {
		t.Fatalf("an uncut 1000-rune value survived sanitization")
	}
}

func TestCodingReviewLintSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("review",
			sanInEdge("e1", "", "review:"+hostileCellLabel)+","+sanInEdge("e2", "child-of", "review:"+longCellText), ""),
		"FindNodes": `{"data":{"nodes":[` + sanListNode("review:"+hostileCellLabel) + `,` +
			sanListNode("review:"+longCellText) + `]}}`,
		"NodeBatch": codingBatch([]string{sanBatchNode("review:" + hostileCellLabel),
			sanBatchNode("review:" + longCellText)}, ""),
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "lint", "-m", codingMem, "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Conflict {
		t.Fatalf("two broken checks should exit 5, got %d", got)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	assertCut(t, text, "review:", 1)
	assertNoRawHostile(t, text)
}

func TestCodingPreflightLintSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("preflight", "",
			sanOutEdge("e1", "to do a thing", "findings:"+hostileCellLabel)+","+
				sanOutEdge("e2", "to read a live one", "findings:x")),
		"NodeBatch": codingBatch([]string{sanBatchNode("findings:x")}, jsonStr("t_e1")),
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "lint", "-m", codingMem, "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Conflict {
		t.Fatalf("a dead route should exit 5, got %d", got)
	}
	assertSafeTableText(t, out.String(), -1, 0)
	assertNoRawHostile(t, out.String())
}

func TestCodingReviewListSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("review",
			sanInEdge("e1", hostileCellLabel, "review:one")+","+
				sanInEdge("e2", "Applies when a long loc appears", "review:"+longCellText), ""),
		"FindNodes": `{"data":{"nodes":[` + sanListNode("review:one") + `,` +
			sanListNode("review:"+longCellText) + `]}}`,
		"NodeBatch": codingBatch([]string{sanBatchNode("review:one"),
			sanBatchNode("review:" + longCellText)}, ""),
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "list", "-m", codingMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, 2, 0)
	assertCut(t, text, "review:", 1)
	assertNoRawHostile(t, text)
}

func TestCodingPreflightListSanitizesCellsInText(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("preflight", "",
			sanOutEdge("e1", hostileCellLabel, "ops:ci")+","+
				sanOutEdge("e2", "to read the long one", "ops:"+longCellText)),
		"NodeBatch": codingBatch([]string{sanBatchNode("ops:ci"), sanBatchNode("ops:" + longCellText)}, ""),
	})
	defer gql.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "list", "-m", codingMem, "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, 2, 0)
	assertCut(t, text, "ops:", 1)
	assertNoRawHostile(t, text)
}

func TestCodingReviewCreateSanitizesTheCreatedRow(t *testing.T) {
	mk := func(loc string) string {
		return `{"data":{"createReviewNode":{"id":"n_new","memoryId":"mem1","loc":` + jsonStr(loc) +
			`,"name":"thin","nodeType":"info","tags":["review","review-criteria"],"seq":null,` +
			`"isRunnable":false,"updatedAt":"2026-08-04T00:00:00Z"}}}`
	}
	confirmEdge := `{"id":"e_new","name":"Applies when a resolver changes","loc":"l","isRunnable":false,
		"priority":0,"target":{"id":"root","loc":"review","memoryId":"` + codingMem + `"}}`
	for _, tc := range []struct {
		name string
		loc  string
		cut  int
	}{
		{"hostile loc", "review:" + hostileCellLabel, 0},
		{"long loc", "review:" + longCellText, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := queueGraphQL(t, map[string][]string{
				"GetNode": {
					codingRootJSON("review", "", ""),
					`{"data":{"node":{"id":"n_new","memoryId":"mem1","loc":` + jsonStr(tc.loc) +
						`,"name":"thin","nodeType":"info","tags":[],"seq":null,"isRunnable":false,` +
						`"updatedAt":"2026-08-04T00:00:00Z","incomingEdges":[],"outgoingEdges":[` + confirmEdge + `]}}}`,
				},
				"CreateReviewNode": {mk(tc.loc)},
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"coding", "review", "create", "thin-resolver", "-m", codingMem,
				"--trigger", "a resolver changes", "--description", "d", "--server", gql.URL})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			text := out.String()
			assertSafeTableText(t, text, 0, 0)
			if tc.cut > 0 {
				assertCut(t, text, "review:", 1)
			}
			assertNoRawHostile(t, text)
		})
	}
}

func TestCodingPreflightRouteSanitizesPlanCells(t *testing.T) {
	target := `{"data":{"node":{"id":"n_target","memoryId":"mem1","loc":` + jsonStr("findings:"+longCellText) +
		`,"name":"t","description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info",` +
		`"objectType":null,"tags":[],"content":null,"data":null,"properties":null,"seq":null,` +
		`"isRunnable":false,"createdAt":"2026-07-30T00:00:00Z","updatedAt":"2026-07-30T00:00:00Z",` +
		`"outgoingEdges":[],"incomingEdges":[]}}}`
	hostileEdge := `{"data":{"createEdge":{"id":` + jsonStr(hostileCellLabel) +
		`,"name":"to fix a flaky OTP test","loc":"l","isRunnable":false,"priority":0,` +
		`"target":{"id":"n_target","loc":"t","memoryId":"` + codingMem + `"}}}}`
	gql, _ := queueGraphQL(t, map[string][]string{
		"GetNodeRaw": {
			codingRouterWithBody(flatRouterBody),
			codingRouterWithBody(flatRouterBody),
		},
		"GetNode":    {target},
		"ResolveUrn": {`{"data":{"resolveUrn":{"id":"n_target","kind":"node","memoryId":"mem1"}}}`},
		"CreateEdge": {hostileEdge, hostileEdge},
		"UpdateNode": {`{"data":{"updateNode":` + newRouteNodeJSON + `}}`},
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "preflight", "route", "findings:flaky-otp-timer",
		"-m", codingMem, "--route", "fix a flaky OTP test",
		"--description", "The countdown starts before the await", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	if !strings.Contains(text, cutCellPrefixed("findings:")) {
		t.Errorf("the long target loc must render cut to the documented form")
	}
	assertNoRawHostile(t, text)
}

func TestCodingReviewRunSanitizesCheckTableAndUnavailable(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"GetNode": codingRootJSON("review",
			sanInEdge("e1", "Applies when a file under `internal/api/queries/` changed", "review:"+hostileCellLabel)+","+
				sanInEdge("e2", "Applies when a refusal is reworded", "review:"+longCellText), ""),
		"FindNodes": `{"data":{"nodes":[` + sanListNode("review:"+hostileCellLabel) + `,` +
			sanListNode("review:"+longCellText) + `]}}`,
		"NodeBatch": codingBatch([]string{
			`{"id":"n_x","memoryId":"mem1","loc":` + jsonStr("review:"+hostileCellLabel) + `,"name":"x",
				"alias":null,"nodeType":"info","objectType":null,"isRunnable":false,"description":"d",
				"abstract":null,"abstractOriginHash":null,"tags":["review"],"seq":null,"data":null,"properties":null,
				"content":` + jsonStr("> **Scope.** ONLY when a file under `internal/api/queries/` changed.") + `,
				"createdAt":"2026-07-30T00:00:00Z","updatedAt":"2026-07-30T00:00:00Z",
				"outgoingEdges":[],"incomingEdges":[]}`,
		}, jsonStr("s_e2")),
	})
	defer gql.Close()
	diff := "diff --git a/internal/api/queries/team.graphql b/internal/api/queries/team.graphql\n" +
		"--- a/internal/api/queries/team.graphql\n+++ b/internal/api/queries/team.graphql\n@@ -1 +1 @@\n-a\n+b\n"
	f, out := testFactory(t)
	f.IOStreams.In = strings.NewReader(diff)
	root := NewRootCmd(f)
	root.SetArgs([]string{"coding", "review", "run", "-m", codingMem, "--diff", "-", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	assertCut(t, text, "review:", 1)
	assertNoRawHostile(t, text)
}
