package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Fixtures for `hadron skill lint`. Memory mem1 is org-owned with a prefix;
// mem2 is org-owned WITHOUT one; mem3 is user-owned (no org at all).
const (
	skillMemOrg      = `{"data":{"memory":{"id":"mem1","urn":"hrn:mem:hadronmemory.com:core","name":"Core","class":"knowledge","visibility":"PUBLIC","organizationId":"org1","organization":{"skillPrefix":"hadron-"},"isEncrypted":false,"tags":[],"maxRevCount":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z"}}}`
	skillMemNoPrefix = `{"data":{"memory":{"id":"mem2","urn":"hrn:mem:acme.com:ops","name":"Ops","class":"knowledge","visibility":"ORGANIZATION","organizationId":"org2","organization":{"skillPrefix":null},"isEncrypted":false,"tags":[],"maxRevCount":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z"}}}`
	skillMemUser     = `{"data":{"memory":{"id":"mem3","urn":"hrn:mem:holger:assistant","name":"Assistant","class":"personal","visibility":null,"organizationId":null,"organization":null,"isEncrypted":false,"tags":[],"maxRevCount":null,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z"}}}`
)

func skillNode(id, memID, urn, loc string, runnable bool, props, content string) string {
	r := "false"
	if runnable {
		r = "true"
	}
	return `{"id":"` + id + `","urn":"` + urn + `","memoryId":"` + memID + `","loc":"` + loc + `","name":"` + loc + `","nodeType":"task","isRunnable":` + r +
		`,"tags":[],"properties":` + props + `,"content":` + content + `,"createdAt":"2026-06-11T00:00:00Z","updatedAt":"2026-06-11T00:00:00Z","outgoingEdges":[],"incomingEdges":[]}`
}

func batchOf(nodes ...string) string {
	return `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` + strings.Join(nodes, ",") + `]}}}`
}

func listOf(ids ...string) string {
	rows := make([]string, len(ids))
	for i, id := range ids {
		rows[i] = `{"id":"` + id + `","memoryId":"mem1","loc":"tasks:x","name":"x","nodeType":"task","tags":[],"isRunnable":true,"updatedAt":"2026-06-11T00:00:00Z"}`
	}
	return `{"data":{"nodes":[` + strings.Join(rows, ",") + `]}}`
}

func runSkillLint(t *testing.T, responses map[string]string, args ...string) (string, error) {
	t.Helper()
	gql, _ := captureGraphQL(t, responses)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"skill", "lint", "--server", gql.URL}, args...))
	err := root.Execute()
	return out.String(), err
}

func findingRules(t *testing.T, out string) map[string]string {
	t.Helper()
	var rows []struct {
		Node, Memory, Rule, Severity, Message string
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	m := map[string]string{}
	for _, r := range rows {
		m[r.Rule] = r.Severity
	}
	return m
}

func TestSkillLintCleanCorpusExitsZero(t *testing.T) {
	good := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:create-release-tag", "tasks:create-release-tag", true,
		`{"skill":{"description":"Use when the user says 'cut a release'."}}`, `"# Cut\n\nSteps."`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(good),
	}, "-m", "hrn:mem:hadronmemory.com:core")
	if err != nil {
		t.Fatalf("clean corpus errored: %v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ 1 skill-declaring node(s) OK") {
		t.Errorf("unexpected text output:\n%s", out)
	}
}

func TestSkillLintReportsFindingsAndExitsConflict(t *testing.T) {
	long := strings.Repeat("x", 1100)
	bad := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:start-worker-session-desktop", "tasks:start-worker-session-desktop", false,
		`{"claudeSkill":{"name":"start-worker-session-desktop","description":"Use when `+long+`"}}`, `"# Body"`)
	// A fork in a second memory of the SAME org derives the same name.
	fork := skillNode("n2", "mem1", "hrn:node:hadronmemory.com:core:tasks:start-worker-session-desktop:copy", "tasks:start-worker-session-desktop", true,
		`{"skill":{"description":"Use when x"}}`, `"# Body"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1", "n2"), "NodeBatch": batchOf(bad, fork),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d, want %d (Conflict); err=%v\n%s", got, exitcode.Conflict, err, out)
	}
	rules := findingRules(t, out)
	for rule, sev := range map[string]string{
		"skill-description-too-long": "error",
		"skill-name-hand-set":        "error",
		"skill-not-runnable":         "error",
		"skill-legacy-key":           "warning",
		"skill-name-collision":       "error",
	} {
		if rules[rule] != sev {
			t.Errorf("want %s=%s, got %v", rule, sev, rules)
		}
	}
}

func TestSkillLintStrictPromotesWarnings(t *testing.T) {
	// Only a warning: legacy key, everything else clean. Without --strict that
	// is exit 0; with it, the warning becomes an error and exits 5.
	n := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"claudeSkill":{"description":"Use when a."}}`, `"# A"`)
	responses := map[string]string{"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n)}
	if _, err := runSkillLint(t, responses, "-m", "hrn:mem:hadronmemory.com:core"); exitCodeFor(err) != exitcode.OK {
		t.Fatalf("warning-only corpus should exit 0, got %v", err)
	}
	out, err := runSkillLint(t, responses, "-m", "hrn:mem:hadronmemory.com:core", "--strict", "--json")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("--strict should exit 5, got %v\n%s", err, out)
	}
	if findingRules(t, out)["skill-legacy-key"] != "error" {
		t.Errorf("warning not promoted: %s", out)
	}
}

func TestSkillLintOrgWithoutPrefixIsAFinding(t *testing.T) {
	n := skillNode("n1", "mem2", "hrn:node:acme.com:ops:tasks:rotate", "tasks:rotate", true,
		`{"skill":{"description":"Use when rotating."}}`, `"# Rotate"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemNoPrefix, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:acme.com:ops", "--json")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("missing prefix should exit 5, got %v\n%s", err, out)
	}
	if findingRules(t, out)["skill-prefix-missing"] != "error" {
		t.Errorf("no prefix-missing finding: %s", out)
	}
	// --prefix supplies one and the corpus is clean.
	if _, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemNoPrefix, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:acme.com:ops", "--prefix", "acme-"); exitCodeFor(err) != exitcode.OK {
		t.Errorf("--prefix override should clear the finding, got %v", err)
	}
}

func TestSkillLintPrefixlessMemoryWithNoDeclaringNodesIsClean(t *testing.T) {
	// The finding is about a name that cannot be derived; a memory with nothing
	// to derive a name for must not turn `--all` red (46 such memories on the
	// first live run).
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemNoPrefix, "FindNodes": listOf(), "NodeBatch": batchOf(),
	}, "-m", "hrn:mem:acme.com:ops")
	if exitCodeFor(err) != exitcode.OK {
		t.Fatalf("empty prefix-less memory should be clean, got %v\n%s", err, out)
	}
	if !strings.Contains(out, "✓ 0 skill-declaring node(s) OK") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestSkillLintUserOwnedMemoryTakesPlatformPrefix(t *testing.T) {
	// No org ⇒ hadron-; a hand-set name equal to the derived one is accepted.
	n := skillNode("n1", "mem3", "hrn:node:holger:assistant:tasks:mm-briefing", "tasks:mm-briefing", true,
		`{"skill":{"name":"hadron-mm-briefing","description":"Use when Holger asks for his briefing."}}`, `"# Briefing"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemUser, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:holger:assistant")
	if err != nil {
		t.Fatalf("user-owned memory errored: %v\n%s", err, out)
	}
}

func TestSkillLintAllIncludesEveryMemoryClass(t *testing.T) {
	// Codex on #589: a nil memories filter excludes agent-system memories by
	// default, so a task declared in one was never scanned while --all
	// reported a clean corpus. Assert the filter on the wire.
	memories := `{"data":{"memories":{"total":1,"items":[{"id":"mem1","urn":"hrn:mem:hadronmemory.com:core","name":"Core","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org1","organization":{"skillPrefix":"hadron-"},"isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-06-11T00:00:00Z"}]}}}`
	shared := `{"data":{"memories":{"total":0,"items":[]}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"Memories": memories, "MemoriesSharedWithMe": shared, "FindNodes": listOf(), "NodeBatch": batchOf(),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"skill", "lint", "--all", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("--all: %v", err)
	}
	for _, op := range []string{"Memories", "MemoriesSharedWithMe"} {
		vars := string(captured[op])
		for _, class := range []string{"system", "knowledge", "app"} {
			if !strings.Contains(vars, `"`+class+`"`) {
				t.Errorf("%s filter does not name memory class %q: %s", op, class, vars)
			}
		}
	}
}

func TestSkillLintRefusesAmbiguousSelector(t *testing.T) {
	for _, args := range [][]string{
		{}, // nothing
		{"-m", "hrn:mem:a:b", "--all"},
		{"--all", "--node", "hrn:node:a:b:c"},
	} {
		_, err := runSkillLint(t, map[string]string{}, args...)
		if exitCodeFor(err) != exitcode.Usage {
			t.Errorf("args %v: exit %d, want %d (Usage)", args, exitCodeFor(err), exitcode.Usage)
		}
	}
	_, err := runSkillLint(t, map[string]string{}, "-m", "hrn:mem:a:b", "--prefix", "Bad_")
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("invalid --prefix: exit %d, want Usage", exitCodeFor(err))
	}
	// An EMPTY --prefix (an unset shell variable) is a usage error, not "no
	// override" — and it is refused before any request is made.
	_, err = runSkillLint(t, map[string]string{}, "-m", "hrn:mem:a:b", "--prefix", "")
	if exitCodeFor(err) != exitcode.Usage {
		t.Errorf("empty --prefix: exit %d, want Usage", exitCodeFor(err))
	}
}

func TestSkillLintNodeRefShapes(t *testing.T) {
	good := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"skill":{"description":"Use when a."}}`, `"# A"`)
	responses := map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good)}
	// A fully-qualified URN and a raw id both reach the batch read.
	for _, ref := range []string{"hrn:node:hadronmemory.com:core:tasks:a", "urn:node:hadronmemory.com:core:tasks:a", "hadronmemory.com::core::tasks:a", "n1"} {
		if _, err := runSkillLint(t, responses, "--node", ref); exitCodeFor(err) != exitcode.OK {
			t.Errorf("--node %q: exit %d, want 0 (%v)", ref, exitCodeFor(err), err)
		}
	}
	// A bare loc is refused client-side as a usage error, before any request.
	for _, ref := range []string{"tasks:a", "core::tasks:a"} {
		if _, err := runSkillLint(t, map[string]string{}, "--node", ref); exitCodeFor(err) != exitcode.Usage {
			t.Errorf("--node %q: exit %d, want 2 (Usage)", ref, exitCodeFor(err))
		}
	}
}

func TestSkillLintCleanCorpusJSONIsAnEmptyArray(t *testing.T) {
	// Asserted on the raw text: a decode cannot tell `[]` from `null`
	// (review:stable-json-dto).
	good := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"skill":{"description":"Use when a."}}`, `"# A"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(good),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("clean --json output = %q, want []", out)
	}
}

func TestSkillLintMalformedDeclarationIsReported(t *testing.T) {
	// The discovery predicate lists the node (the key exists); Declared reads
	// it as undeclared. It must be a finding, not a silent skip.
	bad := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true, `{"skill":"yes"}`, `"# A"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(bad),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("malformed declaration should exit 5, got %v\n%s", err, out)
	}
	if findingRules(t, out)["skill-declaration-malformed"] != "error" {
		t.Errorf("no malformed finding: %s", out)
	}
}

func TestSkillLintUnavailableNodeIsReportedNotDropped(t *testing.T) {
	good := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"skill":{"description":"Use when a."}}`, `"# A"`)
	batch := `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":["n2"],"nodes":[` + good + `]}}}`
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1", "n2"), "NodeBatch": batch,
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	if exitCodeFor(err) != exitcode.OK {
		t.Fatalf("an unavailable node is a warning, not an error: %v\n%s", err, out)
	}
	if findingRules(t, out)["skill-node-unavailable"] != "warning" {
		t.Errorf("unavailable ref not surfaced: %s", out)
	}
}
