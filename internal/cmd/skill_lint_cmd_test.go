package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
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

// nodeID is an ID-shaped node id: `--node` accepts a node ID or a URN, and a
// short fixture token like "n1" is (correctly) refused as a bare loc.
const nodeID = "01a0a5ba59a377d2a01a8ea32ae98194"

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
		`{"exports":{"claudeSkill":{"name":"hadron-create-release-tag","description":"Use when the user says 'cut a release'."}}}`, `"# Cut\n\nSteps."`)
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
	// Retired top-level key, not runnable, description over the cap, and a
	// stored name that COLLIDES with the fork below.
	bad := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:start-worker-session-desktop", "tasks:start-worker-session-desktop", false,
		`{"claudeSkill":{"name":"hadron-start-worker","description":"Use when `+long+`","enable":true}}`, `"# Body"`)
	// D12: a collision is two nodes STORING one name — the locs need not match.
	fork := skillNode("n2", "mem1", "hrn:node:hadronmemory.com:core:tasks:swd-copy", "tasks:swd-copy", true,
		`{"exports":{"claudeSkill":{"name":"hadron-start-worker","description":"Use when x","enable":true}}}`, `"# Body"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1", "n2"), "NodeBatch": batchOf(bad, fork),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d, want %d (Conflict); err=%v\n%s", got, exitcode.Conflict, err, out)
	}
	rules := findingRules(t, out)
	for rule, sev := range map[string]string{
		"skill-description-too-long": "error",
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
		`{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}`, `"# A"`)
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

func TestSkillLintNotEnabledDeclarationIsStillLinted(t *testing.T) {
	// D12: `enable` defaults OFF and gates PUBLISHING, not declaration. Lint must
	// still judge a not-enabled declaration — a broken name is worth reporting
	// before somebody turns it on, and `status` has to be able to name a file
	// whose declaration is switched off.
	n := skillNode("n1", "mem2", "hrn:node:acme.com:ops:tasks:rotate", "tasks:rotate", true,
		`{"exports":{"claudeSkill":{"name":"Bad_Name","description":"Use when rotating.","enable":false}}}`, `"# Rotate"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemNoPrefix, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:acme.com:ops", "--json")
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("a not-enabled but broken declaration should still exit 5, got %v\n%s", err, out)
	}
	if findingRules(t, out)["skill-name-invalid"] != "error" {
		t.Errorf("not-enabled declaration was not linted: %s", out)
	}
}

func TestSkillLintNoPrefixNeededFromAnyOrg(t *testing.T) {
	// D12 retired Organization.skillPrefix: an org that never chose one is no
	// longer a finding, because no name is derived from it. This is the rule
	// whose retirement the change is most visible in — 6 live findings went.
	n := skillNode("n1", "mem2", "hrn:node:acme.com:ops:tasks:rotate", "tasks:rotate", true,
		`{"exports":{"claudeSkill":{"name":"acme-rotate","description":"Use when rotating."}}}`, `"# Rotate"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemNoPrefix, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:acme.com:ops")
	if err != nil {
		t.Fatalf("prefix-less org should now be clean: %v\n%s", err, out)
	}
	if strings.Contains(out, "prefix") {
		t.Errorf("output still mentions a prefix:\n%s", out)
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

func TestSkillLintUserOwnedMemoryNeedsNoPrefix(t *testing.T) {
	// No org and no prefix machinery: a stored name is simply accepted.
	n := skillNode("n1", "mem3", "hrn:node:holger:assistant:tasks:mm-briefing", "tasks:mm-briefing", true,
		`{"exports":{"claudeSkill":{"name":"hadron-mm-briefing","description":"Use when Holger asks for his briefing."}}}`, `"# Briefing"`)
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
	// The last Memories call is the PUBLIC pass — other orgs' public memories
	// are readable and are their own listing slice (Codex on #589, round 4).
	if vars := string(captured["Memories"]); !strings.Contains(vars, `"PUBLIC"`) {
		t.Errorf("no PUBLIC-visibility listing pass in --all: %s", vars)
	}
}

func TestSkillLintAllLintsAPublicOnlyMemory(t *testing.T) {
	// Copilot on #589: the PUBLIC pass must be exercised as a DISTINCT
	// result — a memory returned only by the visibility:PUBLIC call, with a
	// declaring node, whose finding names that memory and its org's prefix.
	pubMem := `{"id":"mempub","urn":"hrn:mem:acme.com:playbooks","name":"Playbooks","shortDescription":null,"class":"knowledge","visibility":"PUBLIC","organizationId":"org9","organization":{"skillPrefix":"acme-"},"isEncrypted":false,"maxRevCount":null,"updatedAt":"2026-06-11T00:00:00Z"}`
	node := skillNode("01a0a5ba59a377d2a01a8ea32ae98195", "mempub", "hrn:node:acme.com:playbooks:tasks:rotate", "tasks:rotate", true,
		`{"exports":{"claudeSkill":{"name":"Bad_Name","description":"Use when rotating."}}}`, `"# Rotate"`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		var resp string
		switch body.OperationName {
		case "Memories":
			if strings.Contains(string(body.Variables), `"PUBLIC"`) {
				resp = `{"data":{"memories":{"total":1,"items":[` + pubMem + `]}}}`
			} else {
				resp = `{"data":{"memories":{"total":0,"items":[]}}}`
			}
		case "MemoriesSharedWithMe":
			resp = `{"data":{"memories":{"total":0,"items":[]}}}`
		case "FindNodes":
			resp = translateFindNodes("FindNodes", `{"data":{"nodes":[{"id":"01a0a5ba59a377d2a01a8ea32ae98195","memoryId":"mempub","loc":"tasks:rotate","name":"rotate","nodeType":"task","tags":[],"isRunnable":true,"updatedAt":"2026-06-11T00:00:00Z"}]}}`)
		case "NodeBatch":
			resp = batchOf(node)
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected"}]}`
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"skill", "lint", "--all", "--json", "--server", srv.URL})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("public-only memory not linted: exit %d\n%s", exitCodeFor(err), out.String())
	}
	var rows []struct{ Node, Memory, Rule, Message string }
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatal(err)
	}
	// The point is REACHABILITY: a PUBLIC memory in another org is covered by
	// --all and its nodes are actually judged. D12 retired the rule this used to
	// assert on (hand-set names), so it asserts on a rule that survives — and
	// the finding must name the node and its memory, not merely exist.
	var sawJudged bool
	for _, r := range rows {
		if r.Rule == "skill-name-invalid" && r.Memory == "hrn:mem:acme.com:playbooks" &&
			r.Node == "hrn:node:acme.com:playbooks:tasks:rotate" && strings.Contains(r.Message, `"Bad_Name"`) {
			sawJudged = true
		}
	}
	if !sawJudged {
		t.Errorf("the public memory's node was not linted: %s", out.String())
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
	// A blank selector value is a usage error before any request.
	for _, args := range [][]string{{"-m", ""}, {"-m", "  "}, {"--node", ""}} {
		if _, err := runSkillLint(t, map[string]string{}, args...); exitCodeFor(err) != exitcode.Usage {
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
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`, `"# A"`)
	responses := map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good)}
	// A fully-qualified URN and a raw id both reach the batch read.
	for _, ref := range []string{"hrn:node:hadronmemory.com:core:tasks:a", "urn:node:hadronmemory.com:core:tasks:a", "hadronmemory.com::core::tasks:a", nodeID} {
		if _, err := runSkillLint(t, responses, "--node", ref); exitCodeFor(err) != exitcode.OK {
			t.Errorf("--node %q: exit %d, want 0 (%v)", ref, exitCodeFor(err), err)
		}
	}
	// A bare loc (with or without colons), an empty token, or a
	// scheme-prefixed ref of another KIND, is refused client-side as a usage
	// error, before any request.
	for _, ref := range []string{"tasks:a", "core::tasks:a", "start-here", "", "hrn:mem:hadronmemory.com:core", "hrn:app:hadronmemory.com:hadron-dev-team"} {
		if _, err := runSkillLint(t, map[string]string{}, "--node", ref); exitCodeFor(err) != exitcode.Usage {
			t.Errorf("--node %q: exit %d, want 2 (Usage)", ref, exitCodeFor(err))
		}
	}
}

func TestSkillLintOneNodeInTwoSpellingsLintsOnce(t *testing.T) {
	// Copilot on #589, round 4: id + URN name one node; the batch returns it
	// twice and the result must be de-duplicated by node id, or it collides
	// with itself.
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a.","enable":true}}}`, `"# A"`)
	gql, _ := captureGraphQL(t, map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good, good)})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"skill", "lint", "--node", nodeID, "--node", "hrn:node:hadronmemory.com:core:tasks:a", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("two spellings: %v\n%s", err, out.String())
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("self-collision reported for one node in two spellings: %s", out.String())
	}
}

func TestSkillLintRepeatedNodeRefLintsOnce(t *testing.T) {
	// Copilot on #589: a --node named twice must not be read twice, or
	// LintCollisions reports a node colliding with itself.
	good := skillNode(nodeID, "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a.","enable":true}}}`, `"# A"`)
	gql, captured := captureGraphQL(t, map[string]string{"GetMemory": skillMemOrg, "NodeBatch": batchOf(good)})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"skill", "lint", "--node", nodeID, "--node", nodeID, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("repeated ref: %v\n%s", err, out.String())
	}
	if strings.Count(string(captured["NodeBatch"]), `"`+nodeID+`"`) != 1 {
		t.Errorf("repeated ref sent more than once: %s", captured["NodeBatch"])
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("self-collision reported: %s", out.String())
	}
}

func TestSkillLintCleanCorpusJSONIsAnEmptyArray(t *testing.T) {
	// Asserted on the raw text: a decode cannot tell `[]` from `null`
	// (review:stable-json-dto).
	good := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`, `"# A"`)
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
		`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`, `"# A"`)
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

// ── #665: lint walks every host ─────────────────────────────────────────

type lintRow struct {
	Node, Memory, Rule, Severity, Message string
	Hosts                                 []string
}

func lintRows(t *testing.T, out string) []lintRow {
	t.Helper()
	var rows []lintRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out)
	}
	return rows
}

// rowsFor returns the rows carrying rule, with their hosts joined, so an
// assertion can say exactly which host(s) reported it.
func rowsFor(rows []lintRow, rule string) []string {
	var got []string
	for _, r := range rows {
		if r.Rule == rule {
			got = append(got, r.Node+" "+strings.Join(r.Hosts, ","))
		}
	}
	return got
}

func propsJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// P11 of the cross-host matrix (hadron-cli#622), made real: `skill lint` over
// L01 reports Codex's over-limit description. L01's properties are read from
// the matrix file itself, so the case and the command cannot drift apart.
func TestSkillLintCodexOverLimitIsReported_MatrixP11(t *testing.T) {
	raw, err := os.ReadFile("../skilldoc/testdata/crosshost-acceptance.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Lint []struct {
			ID         string         `json:"id"`
			Properties map[string]any `json:"properties"`
		} `json:"lint"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var props map[string]any
	for _, c := range m.Lint {
		if c.ID == "L01" {
			props = c.Properties
		}
	}
	if props == nil {
		t.Fatal("matrix has no L01; P11 has nothing to run against")
	}
	n := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:demo", "tasks:demo", true, propsJSON(t, props), `"# Demo\n\nSteps."`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Fatalf("exit = %d, want %d (Conflict): the over-limit Codex description must be an error\n%s", got, exitcode.Conflict, out)
	}
	rows := lintRows(t, out)
	want := []string{"hrn:node:hadronmemory.com:core:tasks:demo codexSkill"}
	if got := rowsFor(rows, "skill-description-too-long"); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill-description-too-long rows = %v, want %v\n%s", got, want, out)
	}
	if len(rows) != 1 {
		t.Errorf("want exactly the one Codex finding (Claude's declaration is clean), got %d:\n%s", len(rows), out)
	}
}

func TestSkillLintCodexOnlyDeclarationIsLintedAndCounted(t *testing.T) {
	n := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:demo", "tasks:demo", true,
		`{"exports":{"codexSkill":{"name":"hadron-demo","description":"Use when demoing.","enable":true}}}`, `"# Demo"`)
	out, err := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:hadronmemory.com:core")
	if err != nil {
		t.Fatalf("clean Codex-only declaration errored: %v\n%s", err, out)
	}
	// Counted as a skill-declaring node, which it was not when only Claude
	// was judged.
	if !strings.Contains(out, "✓ 1 skill-declaring node(s) OK") {
		t.Errorf("Codex-only node not counted:\n%s", out)
	}
}

func TestSkillLintCapsAreIndependentPerHost(t *testing.T) {
	// A 65-character Codex name fails Codex's cap; Claude's declaration beside
	// it is clean, so only Codex reports it.
	n := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:demo", "tasks:demo", true,
		`{"exports":{"claudeSkill":{"name":"hadron-demo","description":"Use when demoing."},"codexSkill":{"name":"`+strings.Repeat("a", 65)+`","description":"Use when demoing."}}}`, `"# Demo"`)
	out, _ := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	want := []string{"hrn:node:hadronmemory.com:core:tasks:demo codexSkill"}
	if got := rowsFor(lintRows(t, out), "skill-name-invalid"); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill-name-invalid rows = %v, want %v\n%s", got, want, out)
	}
}

func TestSkillLintLegacyAliasWarnsForClaudeOnly(t *testing.T) {
	n := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:demo", "tasks:demo", true,
		`{"skill":{"name":"hadron-demo","description":"Use when demoing."},"exports":{"codexSkill":{"name":"hadron-demo","description":"Use when demoing."}}}`, `"# Demo"`)
	out, _ := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	want := []string{"hrn:node:hadronmemory.com:core:tasks:demo claudeSkill"}
	if got := rowsFor(lintRows(t, out), "skill-legacy-key"); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill-legacy-key rows = %v, want %v\n%s", got, want, out)
	}
}

func TestSkillLintNodeLevelFindingIsOneRowNamingEveryHost(t *testing.T) {
	// Not runnable, declared for both hosts: one finding about the NODE,
	// reported by both hosts' judgment, is one row, not two.
	n := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:demo", "tasks:demo", false,
		`{"exports":{"claudeSkill":{"name":"hadron-demo","description":"Use when demoing."},"codexSkill":{"name":"hadron-demo","description":"Use when demoing."}}}`, `"# Demo"`)
	out, _ := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1"), "NodeBatch": batchOf(n),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	want := []string{"hrn:node:hadronmemory.com:core:tasks:demo claudeSkill,codexSkill"}
	if got := rowsFor(lintRows(t, out), "skill-not-runnable"); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill-not-runnable rows = %v, want %v\n%s", got, want, out)
	}
}

func TestSkillLintCollisionsArePerHost(t *testing.T) {
	// Two nodes share a Codex name and keep distinct Claude names: a Codex
	// collision on both members, and none for Claude.
	a := skillNode("n1", "mem1", "hrn:node:hadronmemory.com:core:tasks:a", "tasks:a", true,
		`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."},"codexSkill":{"name":"hadron-x","description":"Use when x.","enable":true}}}`, `"# A"`)
	b := skillNode("n2", "mem1", "hrn:node:hadronmemory.com:core:tasks:b", "tasks:b", true,
		`{"exports":{"claudeSkill":{"name":"hadron-b","description":"Use when b."},"codexSkill":{"name":"hadron-x","description":"Use when x.","enable":true}}}`, `"# B"`)
	out, _ := runSkillLint(t, map[string]string{
		"GetMemory": skillMemOrg, "FindNodes": listOf("n1", "n2"), "NodeBatch": batchOf(a, b),
	}, "-m", "hrn:mem:hadronmemory.com:core", "--json")
	want := []string{"hrn:node:hadronmemory.com:core:tasks:a codexSkill", "hrn:node:hadronmemory.com:core:tasks:b codexSkill"}
	if got := rowsFor(lintRows(t, out), "skill-name-collision"); !reflect.DeepEqual(got, want) {
		t.Fatalf("skill-name-collision rows = %v, want %v\n%s", got, want, out)
	}
}
