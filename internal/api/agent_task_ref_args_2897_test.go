package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// Compatibility (cli#2897, ruling 0274, Bruce #135369): the rename ships in the
// ordered hard-break order, notices, then the server, then the companions. This
// CLI takes NO old-name fallback: against a server still on the old names every
// agent-task call fails loud at GraphQL validation ("Unknown argument"), nothing
// executes, and there is no silent-success case. A fallback would have to step
// down only on that validation error, never on an execution error, never by
// comparing versions, and never by resending a write that may have executed
// (cor:api:220:01); it is deliberately not built.

// retiredAgentTaskArg matches the pre-hadron-server#4782 spellings of the 16
// agent-task arguments (cli#2897; 11 mutations, agentTask, 2 list reads, 2
// events reads): a mutation's or read's `taskId:` / `id:` / `parentTaskId:`
// argument. It is tolerant of argument order, whitespace and newlines inside the
// argument list. The server renamed them to taskRef / ref / parentTaskRef with
// no alias, so a document still sending the old name fails on a current server
// with "Unknown argument". CreateAgentTaskInput.parentTaskId and
// CloneAgentTaskInput.parentTaskId are INPUT-object fields the server kept, and a
// fragment's `parentTaskId` selection is untouched, so neither matches. The
// authoritative check on the query documents is the schema validation below; this
// regex is the sweep over the generated client's operation strings.
var retiredAgentTaskArg = regexp.MustCompile(
	`\bagentTask\s*\([^)]*\bid\s*:` +
		`|\b(?:assignAgentTask|reassignAgentTask|startAgentTask|cancelAgentTask|parkAgentTask|finishAgentTask|cloneAgentTask|updateAgentTask|recordAgentTaskProgress|makeAgentTaskReady|releaseAgentTaskExternalId)\s*\([^)]*\btaskId\s*:` +
		`|\b(?:agentTasks|agentTasksPage)\s*\([^)]*\bparentTaskId\s*:` +
		`|\b(?:agentTeamEvents|agentTeamEventsPage)\s*\([^)]*\btaskId\s*:`)

// TestRetiredAgentTaskArgRegexShapes2897 pins what the sweep matches and, as
// importantly, what it must leave alone.
func TestRetiredAgentTaskArgRegexShapes2897(t *testing.T) {
	for _, in := range []string{
		`startAgentTask(taskId: $id)`,
		"startAgentTask(\n  taskId: $id)",
		`startAgentTask( taskId : $id )`,
		`cloneAgentTask(input: $in, taskId: $id)`,
		`updateAgentTask(input: $i, instruction: $x, taskId: $id)`,
		`agentTask(id: $id)`,
		"agentTasks(\n appRef: $a\n parentTaskId: $p\n)",
		`agentTasksPage(appRef: $a, parentTaskId: $p)`,
		`agentTeamEventsPage(appRef: $a, since: $s, taskId: $t)`,
	} {
		if !retiredAgentTaskArg.MatchString(in) {
			t.Errorf("must match, did not: %q", in)
		}
	}
	for _, in := range []string{
		`startAgentTask(taskRef: $id)`,
		`agentTask(ref: $id)`,
		`agentTasks(appRef: $a, parentTaskRef: $p)`,
		`createAgentTask(input: {parentTaskId: $p})`,
		`cloneAgentTask(taskRef: $t, input: {parentTaskId: $p})`,
		"fragment F on AgentTask {\n  parentTaskId\n  id\n}",
		`agentTaskTree(root: $r)`,
	} {
		if m := retiredAgentTaskArg.FindString(in); m != "" {
			t.Errorf("must not match, matched %q in %q", m, in)
		}
	}
}

// TestQueryDocumentsValidateAgainstTheSnapshot2897 validates every document under
// queries/ against the committed SDL with the same validator family the server
// uses, so a retired argument in ANY position or spacing fails here as "Unknown
// argument" does on a server (independent of the regex above).
func TestQueryDocumentsValidateAgainstTheSnapshot2897(t *testing.T) {
	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	schema, perr := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphql", Input: string(sdl)})
	if perr != nil {
		t.Fatalf("schema snapshot does not load: %v", perr)
	}
	files, err := filepath.Glob("queries/*.graphql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: err=%v files=%d", err, len(files))
	}
	var sources []*ast.Source
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, &ast.Source{Name: f, Input: string(raw)})
	}
	merged := &ast.QueryDocument{}
	for _, src := range sources {
		d, qerr := parser.ParseQuery(src)
		if qerr != nil {
			t.Fatalf("%s does not parse: %v", src.Name, qerr)
		}
		merged.Operations = append(merged.Operations, d.Operations...)
		merged.Fragments = append(merged.Fragments, d.Fragments...)
	}
	for _, e := range validator.Validate(schema, merged) {
		t.Errorf("document does not validate against the snapshot: %v", e)
	}
}

// TestNoRetiredAgentTaskArgsInAnyQueryDocument2897 sweeps every document under
// queries/ and pins the new spelling in the schema snapshot it is generated from.
func TestNoRetiredAgentTaskArgsInAnyQueryDocument2897(t *testing.T) {
	files, err := filepath.Glob("queries/*.graphql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no query documents found")
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range retiredAgentTaskArg.FindAllString(string(raw), -1) {
			t.Errorf("%s still sends a retired agent-task argument: %q", f, strings.TrimSpace(m))
		}
	}

	// The operation strings actually sent live in the generated client; sweep
	// them too, so a stale generate (documents fixed, generated.go not) is red.
	genFiles, err := filepath.Glob("gen/*.go")
	if err != nil {
		t.Fatal(err)
	}
	var generated []string
	for _, f := range genFiles {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw[:min(len(raw), 512)]), "Code generated") {
			continue // only genqlient output, not a hand-written file in gen/
		}
		generated = append(generated, string(raw))
		for _, m := range retiredAgentTaskArg.FindAllString(string(raw), -1) {
			t.Errorf("%s still sends a retired agent-task argument: %q (run make generate)", f, strings.TrimSpace(m))
		}
	}
	if len(generated) == 0 {
		t.Fatal("no generated client found under gen/")
	}
	// Positive control for EVERY renamed operation, order- and whitespace-
	// tolerant like the retired-argument sweep: the operation's own argument
	// list carries the new spelling, so a silent non-match cannot pass and a
	// dropped operation goes red. Scope, stated exactly: the CLI sends no unpaged
	// agentTeamEvents (and agentTasks list reads moved to agentTasksPage, #2808), so the table is the 11 mutations,
	// agentTask(ref:), agentTasksPage(parentTaskRef:) and agentTeamEventsPage(taskRef:).
	// [^)]* spans only the call's own argument list (no argument value holds a
	// ")"); the argument must start after "(", whitespace or ",", so a "$taskRef:"
	// variable declaration cannot satisfy it.
	all := strings.Join(generated, "\n")
	controls := map[string]string{
		"assignAgentTask": "taskRef", "reassignAgentTask": "taskRef", "startAgentTask": "taskRef",
		"cancelAgentTask": "taskRef", "parkAgentTask": "taskRef", "finishAgentTask": "taskRef",
		"cloneAgentTask": "taskRef", "updateAgentTask": "taskRef", "recordAgentTaskProgress": "taskRef",
		"makeAgentTaskReady": "taskRef", "releaseAgentTaskExternalId": "taskRef",
		"agentTask": "ref", "agentTasksPage": "parentTaskRef",
		"agentTeamEventsPage": "taskRef",
	}
	ops := make([]string, 0, len(controls))
	for op := range controls {
		ops = append(ops, op)
	}
	sort.Strings(ops)
	for _, op := range ops {
		arg := controls[op]
		re := regexp.MustCompile(`\b` + op + `\s*\((?:[^)]*[\s,])?` + arg + `\s*:`)
		if !re.MatchString(all) {
			t.Errorf("the generated client never sends %s(%s:): stale client or the sweep is blind", op, arg)
		}
	}

	sdl, err := os.ReadFile("../../schema/schema.graphql")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"agentTask(ref: ID!): AgentTask",
		"startAgentTask(taskRef: ID!)",
		"cloneAgentTask(input: CloneAgentTaskInput, taskRef: ID!)",
		"updateAgentTask(input: UpdateAgentTaskInput!, instruction: ActInstructionInput, taskRef: ID!)",
	} {
		if !strings.Contains(string(sdl), want) {
			t.Errorf("schema snapshot lacks %q (hadron-server#4782 spelling)", want)
		}
	}
	if m := regexp.MustCompile(`(?m)^  (?:agentTask\(id: |startAgentTask\(taskId: )`).FindString(string(sdl)); m != "" {
		t.Errorf("schema snapshot still carries a retired agent-task argument: %q", m)
	}
}

// TestAgentTaskOperationsSendTheRefArguments2897 runs representative operations
// from each renamed family through a fake server and asserts the document the
// server receives names the new argument and not the old one.
func TestAgentTaskOperationsSendTheRefArguments2897(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		queries = append(queries, body.Query)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "hdr_user_test", srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ref, parent := "AT1", "AT0"
	_, _ = gen.AgentTaskStart(ctx, client, ref)
	_, _ = gen.AgentTaskGet(ctx, client, ref, 1, 0)
	_, _ = gen.AgentTaskList(ctx, client, "app", nil, nil, nil, nil, &parent, nil, nil, nil, nil)
	_, _ = gen.AgentTeamEventsPage(ctx, client, "app", nil, nil, nil, nil, nil, nil, nil, nil, &ref, nil)

	want := []string{"startAgentTask(taskRef:", "agentTask(ref:", "parentTaskRef:", "taskRef:"}
	if len(queries) != len(want) {
		t.Fatalf("expected %d requests, got %d", len(want), len(queries))
	}
	for i, q := range queries {
		if !strings.Contains(q, want[i]) {
			t.Errorf("request %d lacks %q:\n%s", i, want[i], q)
		}
		if m := retiredAgentTaskArg.FindString(q); m != "" {
			t.Errorf("request %d sends retired argument %q", i, strings.TrimSpace(m))
		}
	}
}
