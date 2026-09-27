package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #736 — `node get --raw` returns the STORED body, {{…}} placeholders intact;
// the default single-ref read is the RENDERED one, and --json says which.

// templatedNodeJSON is a GetNode(Raw) response whose content is `body` — the
// fake answers the rendered read and the raw read differently, the way the
// server does, so each test can see which read the command asked for.
func templatedNodeJSON(body string) string {
	b, _ := json.Marshal(body)
	return `{"data":{"node":{"id":"n1","urn":"` + testNodeURN + `","portalUrl":null,
		"memoryId":"mem1","loc":"findings:flaky","name":"flaky","description":null,"abstract":null,
		"abstractOriginHash":null,"nodeType":"info","objectType":null,"tags":[],"content":` + string(b) + `,
		"data":null,"properties":null,"seq":null,"isRunnable":false,
		"createdAt":"2026-08-24T00:00:00Z","updatedAt":"2026-08-24T00:00:00Z",
		"outgoingEdges":[],"incomingEdges":[]}}}`
}

const (
	storedBody   = "You are {{name}}, the {{role}}. {{absent.var}} stays."
	renderedBody = "You are , the . stays."
)

func rawGetStubs() map[string]string {
	return map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"GetNode":    templatedNodeJSON(renderedBody),
		"GetNodeRaw": templatedNodeJSON(storedBody),
	}
}

type rawGetDTO struct {
	Content  string `json:"content"`
	Rendered *bool  `json:"rendered"`
}

func TestNodeGetRawReturnsTheStoredBody(t *testing.T) {
	gql, captured := captureGraphQL(t, rawGetStubs())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--raw", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, asked := captured["GetNodeRaw"]; !asked {
		t.Fatal("--raw must read through GetNodeRaw")
	}
	if _, asked := captured["GetNode"]; asked {
		t.Error("--raw must not also read the rendered body")
	}
	var dto rawGetDTO
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	if dto.Content != storedBody {
		t.Errorf("content = %q, want the stored body %q", dto.Content, storedBody)
	}
	if dto.Rendered == nil || *dto.Rendered {
		t.Errorf("rendered must be present and false for --raw, got %v", dto.Rendered)
	}
}

// Without --raw nothing changes for a caller who wants the rendered text —
// except that --json now SAYS it is rendered.
func TestNodeGetDefaultIsRenderedAndSaysSo(t *testing.T) {
	gql, captured := captureGraphQL(t, rawGetStubs())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, asked := captured["GetNodeRaw"]; asked {
		t.Error("the default read must stay the rendered one")
	}
	var dto rawGetDTO
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	if dto.Content != renderedBody {
		t.Errorf("content = %q, want the rendered body", dto.Content)
	}
	if dto.Rendered == nil || !*dto.Rendered {
		t.Errorf("rendered must be present and true by default, got %v", dto.Rendered)
	}
}

// The human render names a stored body, so a reader copying it knows the
// placeholders are real; a rendered body carries no such line.
func TestNodeGetRawHumanOutputSaysStoredBody(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{{[]string{"--raw"}, true}, {nil, false}} {
		gql, _ := captureGraphQL(t, rawGetStubs())
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"node", "get", testNodeURN, "--server", gql.URL}, tc.args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got := strings.Contains(out.String(), "stored body"); got != tc.want {
			t.Errorf("args %v: stored-body line = %v, want %v:\n%s", tc.args, got, tc.want, out.String())
		}
	}
}

// A batch read is always raw, and says so on every node.
func TestNodeGetBatchIsRawAndSaysSo(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "alpha"), batchNodeJSON("n2", "beta")}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "hrn:node:acme.com:kb:alpha", "hrn:node:acme.com:kb:beta", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Nodes []rawGetDTO `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json must parse: %v (%s)", err, out.String())
	}
	if len(dto.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d", len(dto.Nodes))
	}
	for i, n := range dto.Nodes {
		if n.Rendered == nil || *n.Rendered {
			t.Errorf("node %d: a batch read is raw, rendered must be false, got %v", i, n.Rendered)
		}
	}
}

// The documented edit workflow, end to end (#736): read with --raw, edit, and
// write back with `node update --content-file`. The mutation must carry the
// STORED placeholders — asserted on what is sent, not on a rendered re-read,
// which would agree with a stripped write and prove nothing.
func TestNodeRawReadEditWriteRoundTripKeepsPlaceholders(t *testing.T) {
	gql, _ := captureGraphQL(t, rawGetStubs())
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", testNodeURN, "--raw", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("get: %v", err)
	}
	var got rawGetDTO
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatal(err)
	}
	edited := got.Content + "\nAdded by an editor."
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	gql2, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f2, _ := testFactory(t)
	root = NewRootCmd(f2)
	root.SetArgs([]string{"node", "update", nodeURN, "--content-file", path, "--server", gql2.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("update: %v", err)
	}
	var vars struct {
		Input struct {
			Content string `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(captured["UpdateNode"], &vars); err != nil {
		t.Fatal(err)
	}
	for _, ph := range []string{"{{name}}", "{{role}}", "{{absent.var}}"} {
		if !strings.Contains(vars.Input.Content, ph) {
			t.Errorf("the written body must keep %s; sent %q", ph, vars.Input.Content)
		}
	}
}
