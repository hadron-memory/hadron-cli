package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func runClone994(t *testing.T, response string, args ...string) (string, string, int, []map[string]any) {
	t.Helper()
	calls := []map[string]any{}
	srv, _ := captureGraphQLFunc(t, func(op string) string {
		calls = append(calls, map[string]any{"op": op})
		if op != "CloneAgent" {
			t.Errorf("unexpected operation %s", op)
		}
		return response
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "agent", "clone"}, args...))
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	return out.String(), f.IOStreams.ErrOut.(*strings.Builder).String(), code, calls
}

func TestAgentClone994ReturnedDTOAndOptions(t *testing.T) {
	for _, options := range [][]string{nil, {"--name", "New Name", "--slug", "new-slug"}, {"--name=", "--slug="}} {
		srv, vars := captureGraphQL(t, map[string]string{"CloneAgent": `{"data":{"cloneAgent":` + strings.ReplaceAll(agentJSON, "acme.com::support-bot", "hrn:agent:target.org:returned-clone") + `}}`})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"--server", srv.URL, "agent", "clone", "source-alias", "--to", "target-org", "--json"}, options...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if len(vars) != 1 {
			t.Fatalf("want exactly one operation: %v", vars)
		}
		var sent map[string]any
		_ = json.Unmarshal(vars["CloneAgent"], &sent)
		if sent["source"] != "source-alias" || sent["target"] != "target-org" {
			t.Fatal(sent)
		}
		if len(options) == 4 && (sent["name"] != "New Name" || sent["slug"] != "new-slug") {
			t.Fatal(sent)
		}
		if len(options) == 2 && (sent["name"] != "" || sent["slug"] != "") {
			t.Fatal(sent)
		}
		for _, key := range []string{"name", "slug"} {
			_, present := sent[key]
			if present != (options != nil) {
				t.Fatalf("%s presence: %v", key, sent)
			}
		}
		var dto map[string]any
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatal(err)
		}
		if dto["urn"] != "hrn:agent:target.org:returned-clone" || len(dto) != 21 {
			t.Fatal(dto)
		}
		for _, key := range []string{"id", "urn", "name", "description", "type", "visibility", "organizationId", "surfaces", "systemMemoryId", "tasksIndexRef", "processesIndexRef", "extendsAgentId", "bootPromptRef", "bootPromptTargetDeleted", "aiProvider", "aiModel", "hasAiApiKey", "personaRole", "usesTasks", "memberRoles", "createdAt"} {
			if _, ok := dto[key]; !ok {
				t.Fatalf("missing%s:%v", key, dto)
			}
		}
		if surfaces, ok := dto["surfaces"].([]any); !ok || len(surfaces) != 0 {
			t.Fatal(dto)
		}
		if _, ok := dto["clonedFromAgentRef"]; ok {
			t.Fatal("origin must be absent")
		}
	}
	out, _, code, calls := runClone994(t, `{"data":{"cloneAgent":`+agentJSON+`}}`, "source", "--to", "org")
	if code != 0 || out != "acme.com::support-bot\n" || len(calls) != 1 {
		t.Fatalf("%d %q %v", code, out, calls)
	}
}

func TestAgentClone994InvalidZeroCalls(t *testing.T) {
	for _, args := range [][]string{nil, {"source"}, {"source", "--to="}, {"", "--to", "org"}, {"source", "extra", "--to", "org"}} {
		_, _, code, calls := runClone994(t, `{"data":{"cloneAgent":`+agentJSON+`}}`, args...)
		if code != 2 || len(calls) != 0 {
			t.Fatalf("%v: code%d calls%v", args, code, calls)
		}
	}
}

func TestAgentClone994RefusalDetailsAndNoRetry(t *testing.T) {
	ext := `{"code":"CLONE_IMPORT_UNUSABLE","secret":"HIDDEN-EXTENSION","blockers":[{"code":"CLONE_IMPORT_UNUSABLE","count":2,"private":"HIDDEN-BLOCKER","subjects":[{"position":0,"name":"Visible import","id":"HIDDEN-ID"},{"position":2} ]},{"code":"CLONE_BASE_UNUSABLE","count":1,"subjects":[{"position":0}]}]}`
	response := `{"errors":[{"message":"This Agent cannot be cloned.","extensions":` + ext + `}]}`
	for _, mode := range [][]string{nil, {"--json"}} {
		out, stderr, code, calls := runClone994(t, response, append([]string{"source", "--to", "org"}, mode...)...)
		if code != 8 || len(calls) != 1 {
			t.Fatalf("%d %v", code, calls)
		}
		rendered := out + stderr
		for _, want := range []string{"CLONE_IMPORT_UNUSABLE", "CLONE_BASE_UNUSABLE", "Visible import", "2"} {
			if !strings.Contains(rendered, want) {
				t.Fatalf("missing%s: %s", want, rendered)
			}
		}
		if strings.Contains(rendered, "HIDDEN-") {
			t.Fatal(rendered)
		}
		if mode != nil {
			var envelope map[string]any
			if err := json.Unmarshal([]byte(out), &envelope); err != nil {
				t.Fatal(err)
			}
			if stderr != "" {
				t.Fatal(stderr)
			}
			details := envelope["error"].(map[string]any)["extensions"].(map[string]any)
			blockers := details["blockers"].([]any)
			if len(blockers) != 2 || blockers[0].(map[string]any)["code"] != "CLONE_IMPORT_UNUSABLE" || blockers[1].(map[string]any)["code"] != "CLONE_BASE_UNUSABLE" {
				t.Fatal(blockers)
			}
			subjects := blockers[0].(map[string]any)["subjects"].([]any)
			if len(subjects) != 2 || subjects[1].(map[string]any)["position"] != float64(2) {
				t.Fatal(subjects)
			}
			if _, present := subjects[1].(map[string]any)["name"]; present {
				t.Fatal("redacted name must remain absent")
			}

		}
	}
	for _, tc := range []struct {
		code string
		exit int
	}{{"AGENT_NOT_FOUND", 4}, {"AGENT_NOT_PUBLIC", 8}, {"CLONE_ENCRYPTED_CONTENT", 2}, {"CLONE_LINKED_MEMORY_ENCRYPTED", 2}, {"CLONE_LINKED_MEMORY_UNREADABLE", 8}, {"CLONE_BASE_UNUSABLE", 8}, {"CLONE_TARGET_UNSUPPORTED", 2}, {"AGENT_URN_TAKEN", 5}, {"CONFLICT", 5}, {"NEW_UNKNOWN_CODE", 1}} {
		out, _, code, calls := runClone994(t, `{"errors":[{"message":"Server refusal","extensions":{"code":"`+tc.code+`","reason":"AGENT_CLONE_SOURCE_CHANGED","retryable":true}}]}`, "source", "--to", "org", "--json")
		if !strings.Contains(out, `"reason": "AGENT_CLONE_SOURCE_CHANGED"`) || !strings.Contains(out, `"retryable": true`) {
			t.Fatal(out)
		}
		if code != tc.exit || len(calls) != 1 || !strings.Contains(out, tc.code) {
			t.Fatalf("%s: %d %s %v", tc.code, code, out, calls)
		}
	}
	_, stderr, code, calls := runClone994(t, `{"errors":[{"message":"Cannot query field cloneAgent","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "source", "--to", "org")
	if code != 2 || len(calls) != 1 || !strings.Contains(stderr, "out of sync") {
		t.Fatalf("%d %s %v", code, stderr, calls)
	}
}

func TestAgentClone994HelpAndOperation(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "clone", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--to", "--name", "--slug", "PERSONAL-draft", "Linked memories stay links", "no install", "automatic retry"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing%s:%s", want, out.String())
		}
	}
}

func TestAgentClone994LostResponseNeverRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "agent", "clone", "source", "--to", "org", "--json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("lost response succeeded")
	}
	if code := renderError(f, err); code != 7 {
		t.Fatalf("code%d err%v", code, err)
	}
	if calls.Load() != 1 || !strings.Contains(strings.ToLower(out.String()), "unknown") {
		t.Fatalf("calls%d output%s", calls.Load(), out.String())
	}
}

func TestAgentClone994GeneratedSelectionHasNoOriginOrReadiness(t *testing.T) {
	for _, forbidden := range []string{"clonedFromAgentRef", "cloneOffer", "readiness"} {
		if strings.Contains(gen.CloneAgent_Operation, forbidden) {
			t.Fatal(gen.CloneAgent_Operation)
		}
	}
	if !strings.Contains(gen.CloneAgent_Operation, "cloneAgent(source: $source, target: $target, name: $name, slug: $slug)") {
		t.Fatal(gen.CloneAgent_Operation)
	}
}
