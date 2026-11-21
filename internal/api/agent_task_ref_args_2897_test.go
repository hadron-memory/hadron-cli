package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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

// retiredAgentTaskArg matches the pre-hadron-server#4782 spelling of the 15
// agent-task arguments (cli#2897): the mutations' `taskId:`, `agentTask(id:)`,
// the list reads' `parentTaskId:` filter, and the events reads' `taskId:`
// filter. The server renamed them to taskRef / ref / parentTaskRef with no
// alias, so a document that still sends the old name fails on a current server
// with "Unknown argument". CreateAgentTaskInput.parentTaskId and
// CloneAgentTaskInput.parentTaskId are INPUT-object fields the server kept; a
// fragment's `parentTaskId` selection is likewise untouched, so neither matches.
var retiredAgentTaskArg = regexp.MustCompile(
	`\bagentTask\(id:` +
		`|\b(?:assign|reassign|start|cancel|park|finish|clone|update)AgentTask\(taskId:` +
		`|\b(?:recordAgentTaskProgress|makeAgentTaskReady|releaseAgentTaskExternalId)\(taskId:` +
		`|(?m:^\s+parentTaskId:\s*\$)` +
		`|(?m:^\s+taskId:\s*\$)`)

// TestNoRetiredAgentTaskArgsInAnyQueryDocument2897 sweeps every document under
// queries/ and pins the new spelling in the schema snapshot it is generated from.
func TestNoRetiredAgentTaskArgsInAnyQueryDocument2897(t *testing.T) {
	files, err := filepath.Glob("queries/*.graphql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no query documents found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if m := retiredAgentTaskArg.FindString(string(raw)); m != "" {
			t.Errorf("%s still sends a retired agent-task argument: %q", f, strings.TrimSpace(m))
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
