package cmd

import (
	"encoding/json"
	"strings"
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
		if dto["urn"] != "hrn:agent:target.org:returned-clone" || len(dto) != 19 {
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
		_, _, code, calls := runClone994(t, `{}`, args...)
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
		}
	}
	for _, tc := range []struct {
		code string
		exit int
	}{{"AGENT_NOT_FOUND", 4}, {"AGENT_NOT_PUBLIC", 8}, {"CLONE_ENCRYPTED_CONTENT", 2}, {"CLONE_LINKED_MEMORY_ENCRYPTED", 2}, {"CLONE_LINKED_MEMORY_UNREADABLE", 8}, {"CLONE_BASE_UNUSABLE", 8}, {"CLONE_TARGET_UNSUPPORTED", 2}, {"AGENT_URN_TAKEN", 5}, {"CONFLICT", 5}, {"NEW_UNKNOWN_CODE", 1}} {
		out, _, code, calls := runClone994(t, `{"errors":[{"message":"Server refusal","extensions":{"code":"`+tc.code+`","reason":"AGENT_CLONE_SOURCE_CHANGED","retryable":true}}]}`, "source", "--to", "org", "--json")
		if code != tc.exit || len(calls) != 1 || !strings.Contains(out, tc.code) {
			t.Fatalf("%s: %d %s %v", tc.code, code, out, calls)
		}
	}
	_, stderr, code, calls := runClone994(t, `{"errors":[{"message":"Cannot query field cloneAgent","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`, "source", "--to", "org")
	if code != 2 || len(calls) != 1 || !strings.Contains(stderr, "out of sync") {
		t.Fatalf("%d %s %v", code, stderr, calls)
	}
}
