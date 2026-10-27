package cmd

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1104: the skill command tables render plan- and scan-supplied cells
// (skill urns and names, plan entry names, scanned directories); text
// sanitizes them. The --json payloads are pinned by the existing skill tests,
// which the text-only SafeCell wrap cannot reach.

func sanSkillNode(id, urn, loc string, props string) string {
	return `{"id":"` + id + `","urn":` + jsonStr(urn) + `,"memoryId":"mem1","loc":` + jsonStr(loc) +
		`,"name":` + jsonStr(loc) + `,"nodeType":"task","isRunnable":false,"tags":[],"properties":` + props +
		`,"content":"# Body","createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z",` +
		`"outgoingEdges":[],"incomingEdges":[]}`
}

func TestSkillLintSanitizesCellsInText(t *testing.T) {
	const p = "hrn:node:hadronmemory.com:core:tasks:"
	props := `{"claudeSkill":{"name":"hadron-a","description":"Use when a.","enable":true}}`
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1", "n2"),
		"NodeBatch": batchOf(
			sanSkillNode("n1", p+hostileCellLabel, "tasks:a", props),
			sanSkillNode("n2", p+longCellText, "tasks:b", props)),
	}, "-m", "hrn:mem:hadronmemory.com:core")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("not-runnable nodes are errors: exit = %d, want %d; err=%v\n%s", got, exitcode.Conflict, err, out)
	}
	// The long-urn node draws several findings (not-runnable, legacy key), and
	// every finding row repeats the node's urn.
	assertSafeTableText(t, out, -1, 0)
	assertDetailLineCount(t, out, 9)
	assertCut(t, out, p, 3)
	assertNoRawHostile(t, out)
}

// skillPlanRespSan is skillPlanResp with every server-supplied string
// JSON-escaped, so a hostile name or a 1000-rune urn cannot break the fixture.
func skillPlanRespSan(name, node string) string {
	return `{"data":{"skillPlan":{"scanned":1,"judged":1,"entries":[{"urn":` + jsonStr(node) +
		`,"nodeId":"` + statusNodeID + `","name":` + jsonStr(name) +
		`,"class":"current","parseFailure":null,"movedFrom":null,"findings":[]}],"orphans":[]}}}`
}

func TestSkillStatusSanitizesPlanCellsInText(t *testing.T) {
	root := t.TempDir()
	writeUnparseableSkill(t, root, "example")
	out, _, err := runSkillStatus(t, map[string]string{
		"SkillPlan": skillPlanRespSan(hostileCellLabel, "hrn:node:hadronmemory.com:core:"+longCellText),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--to", root)
	if err != nil {
		t.Fatalf("status errored: %v\n%s", err, out)
	}
	assertSafeTableText(t, out, -1, 0)
	assertDetailLineCount(t, out, 6)
	assertCut(t, out, "hrn:node:hadronmemory.com:core:", 1)
	assertNoRawHostile(t, out)
}

// sanPlanEntryJSON is planEntryJSON with the name JSON-escaped everywhere.
func sanPlanEntryJSON(name, action, body string) string {
	b := "null"
	if body != "" {
		bb, _ := json.Marshal(body)
		b = string(bb)
	}
	return `{"urn":` + jsonStr("hrn:node:example.com:demo:tasks:"+name) + `,"nodeId":` + jsonStr("id-"+name) +
		`,"name":` + jsonStr(name) +
		`,"class":"never-exported","parseFailure":null,"movedFrom":null,"renderedBody":` + b +
		`,"exportPlan":{"action":"` + action + `","preservesExistingFile":false,"reasons":[{"code":"c","message":"m"}]},"findings":[]}`
}

func runPluginText(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"skill", "plugin", "--server", url}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestSkillPluginSanitizesPlanNamesInText(t *testing.T) {
	h := pluginHome(t)
	out := filepath.Join(h, "dist")
	srv, _ := pluginServer(t, map[string]string{
		"claudeSkill": planJSON(
			sanPlanEntryJSON(hostileCellLabel, "WRITE", "# a"),
			sanPlanEntryJSON(longCellText, "WRITE", "# b")),
		"codexSkill": planJSON(planEntryJSON("gamma", "WRITE", "# g")),
	}, "")
	// The 1000-rune entry name cannot become a directory under dist, so the
	// host finishes with a failure and a non-zero exit — the rows still render.
	text, _ := runPluginText(t, srv.URL, "--out", out)
	assertSafeTableText(t, text, -1, 0)
	assertDetailLineCount(t, text, 18)
	assertCut(t, text, "", 2)
	assertNoRawHostile(t, text)
}

func TestSkillExportSanitizesRowLabelsInText(t *testing.T) {
	accHome(t)
	url, _ := selectedServer(t, func(c selectedExportCall) map[string]any {
		return selectedPlanJSON([]map[string]any{
			planEntry(hostileCellLabel, "WRITE", rendered(t, c.Host), ""),
			planEntry(longCellText, "WRITE", rendered(t, c.Host), ""),
		}, nil)
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"skill", "export", "--node", accSource, "--server", url})
	_ = root.Execute() // the 1000-rune label cannot become a directory; expect a failed row, not a crash
	text := out.String()
	assertSafeTableText(t, text, -1, 0)
	assertDetailLineCount(t, text, 15)
	assertCut(t, text, "", 2) // one failed row per host
	assertNoRawHostile(t, text)
}
