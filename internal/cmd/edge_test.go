package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const edgeJSON = `{"id":"e1","label":"routes-to","priority":0,
	"source":{"id":"n1","loc":"findings:flaky-ci"},
	"target":{"id":"n2","loc":"start-here"}}`

// resolveByURN serves ResolveUrn with a distinct node id per URN so
// edge add can resolve both endpoints in one fake.
func resolveByURN(t *testing.T, ids map[string]string, responses map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "ResolveUrn" {
			urn, _ := body.Variables["urn"].(string)
			id, ok := ids[urn]
			if !ok {
				_, _ = w.Write([]byte(`{"data":{"resolveUrn":null}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"` + id + `","kind":"node","memoryId":"mem1"}}}`))
			return
		}
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestEdgeAdd(t *testing.T) {
	gql := resolveByURN(t,
		map[string]string{
			"urn:node:acme.com:kb:findings:flaky-ci": "n1",
			"urn:node:acme.com:kb:start-here":        "n2",
		},
		map[string]string{
			"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
		})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add",
		"--from", "acme.com:kb:findings:flaky-ci",
		"--to", "acme.com:kb:start-here",
		"--label", "routes-to", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto["sourceId"] != "n1" || dto["targetId"] != "n2" || dto["label"] != "routes-to" {
		t.Errorf("unexpected edge DTO: %v", dto)
	}
}

func TestEdgeAddRejectsBadJSONCondition(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add",
		"--from", "a.com:m:x", "--to", "a.com:m:y", "--label", "l",
		"--condition", "{not json", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected JSON validation error, got %v", err)
	}
}

func TestEdgeLs(t *testing.T) {
	gql := resolveByURN(t,
		map[string]string{"urn:node:acme.com:kb:findings:flaky-ci": "n1"},
		map[string]string{
			"GetNodeById": `{"data":{"nodeById":` + nodeDetailJSON + `}}`,
		})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "ls", "acme.com:kb:findings:flaky-ci", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "routes-to") || !strings.Contains(out.String(), "start-here") {
		t.Errorf("unexpected edge ls output: %s", out.String())
	}
}

func TestEdgeUpdateRequiresAField(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "update", "e1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update error, got %v", err)
	}
}

func TestEdgeRmWithYes(t *testing.T) {
	gql := resolveByURN(t, nil, map[string]string{
		"DeleteEdge": `{"data":{"deleteEdge":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "rm", "e1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
