package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #1138: fixture-only contract pins prepared before the #2610 provider and
// urn-lib-go release. They are deliberately red until those dependencies land.
const actionNodeID1138 = "0123456789abcdef0123456789abcdef"

const actionURN1138 = "hrn:action:hadronmemory.com:team-agent:task.finish.open-children"

func TestActionURN1138SingleRead(t *testing.T) {
	for _, jsonMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "text", true: "json"}[jsonMode], func(t *testing.T) {
			payload := strings.Replace(nodeGetJSON(""), `"memoryId":"mem1"`, `"actionUrn":"`+actionURN1138+`","memoryId":"mem1"`, 1)
			gql, captured := captureGraphQL(t, map[string]string{
				"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"action","memoryId":"mem1"}}}`,
				"GetNode":    payload,
			})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "get", actionURN1138, "--server", gql.URL}
			if jsonMode {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("action must resolve to the register node: %v", err)
			}
			var sent struct {
				Urn string `json:"urn"`
			}
			if err := json.Unmarshal(captured["ResolveUrn"], &sent); err != nil {
				t.Fatal(err)
			}
			if sent.Urn != actionURN1138 {
				t.Errorf("resolve input=%q, want original action URN", sent.Urn)
			}
			if jsonMode {
				var dto map[string]any
				if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
					t.Fatal(err)
				}
				if dto["actionUrn"] != actionURN1138 {
					t.Errorf("actionUrn=%v", dto["actionUrn"])
				}
				if dto["urn"] != testNodeURN {
					t.Errorf("node address must survive: %v", dto["urn"])
				}
			} else if !strings.Contains(out.String(), actionURN1138) {
				t.Errorf("missing server action address: %s", out.String())
			}
		})
	}
}

func TestActionURN1138Refusal(t *testing.T) {
	for _, ref := range []string{
		"hrn:action:foreign.example:team-agent:task.finish.open-children",
		"hrn:action:hadronmemory.com:team-agent:missing",
		"hrn:action:hadronmemory.com:team-agent:",
		"hrn:action:hadronmemory.com:team-agent:bad:leaf",
	} {
		t.Run(ref, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"ResolveUrn": `{"data":{"resolveUrn":null}}`})
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"node", "get", ref, "--server", gql.URL})
			err := root.Execute()
			if err == nil || exitCodeFor(err) != exitcode.NotFound {
				t.Fatalf("uniform not-found required, got %v", err)
			}
			if !strings.Contains(err.Error(), "hrn:action:<root>:<agent-slug>:<code>") {
				t.Errorf("action-aware not-found hint missing: %v", err)
			}
			if strings.Contains(err.Error(), "not a node") || strings.Contains(err.Error(), "parse") {
				t.Errorf("distinguishing refusal: %v", err)
			}
			if out.Len() != 0 {
				t.Errorf("partial stdout: %q", out.String())
			}
		})
	}
}

// nodeBatch(refs:) screens every ref as a node URN and fails the WHOLE call on
// any other kind (hadron-server src/api/graphql/schema/resolvers.query.node.ts,
// the validateUrnType screen), so the CLI resolves an action URN to the register
// node's id first and batches ids. The batch never carries the action spelling.
func TestActionURN1138BatchRead(t *testing.T) {
	payload := strings.Replace(batchNodeJSON(actionNodeID1138, "register:task.finish.open-children"), `"memoryId":"mem1"`, `"actionUrn":"`+actionURN1138+`","memoryId":"mem1"`, 1)
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"` + actionNodeID1138 + `","kind":"action","memoryId":"mem1"}}}`,
		"NodeBatch":  nodeBatchResult([]string{payload}, ""),
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", actionURN1138, testNodeURN, "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("mixed action/node batch must work: %v", err)
	}
	var sent struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["NodeBatch"], &sent); err != nil {
		t.Fatal(err)
	}
	if len(sent.Refs) != 2 || sent.Refs[0] != actionNodeID1138 || sent.Refs[1] != testNodeURN {
		t.Errorf("refs=%v, want the resolved id then the node URN, never the action spelling", sent.Refs)
	}
	var dto struct {
		Nodes []struct {
			ActionURN string `json:"actionUrn"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Nodes) != 1 || dto.Nodes[0].ActionURN != actionURN1138 {
		t.Errorf("missing action address: %s", out.String())
	}
}

// An action URN that names nothing is "unavailable" in a batch, under the
// caller's own spelling, and the exit is the partial-read one.
func TestActionURN1138BatchUnresolvedIsUnavailable(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"ResolveUrn": `{"data":{"resolveUrn":null}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", actionURN1138, "hrn:action:hadronmemory.com:team-agent:bad:leaf", "--json", "--server", gql.URL})
	err := root.Execute()
	if err == nil || exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("want exit 4, got %v", err)
	}
	if _, called := captured["NodeBatch"]; called {
		t.Error("nothing resolved, so there is nothing to batch")
	}
	var dto struct {
		Nodes       []any    `json:"nodes"`
		Unavailable []string `json:"unavailable"`
	}
	if jerr := json.Unmarshal([]byte(out.String()), &dto); jerr != nil {
		t.Fatalf("%v: %s", jerr, out.String())
	}
	if len(dto.Nodes) != 0 || len(dto.Unavailable) != 2 || dto.Unavailable[0] != actionURN1138 {
		t.Errorf("unavailable=%v nodes=%v", dto.Unavailable, dto.Nodes)
	}
}

func TestActionURN1138SelectsProviderField(t *testing.T) {
	for name, query := range map[string]string{"GetNode": gen.GetNode_Operation, "NodeBatch": gen.NodeBatch_Operation} {
		t.Run(name, func(t *testing.T) {
			found := false
			for _, line := range strings.Split(query, "\n") {
				if strings.TrimSpace(line) == "actionUrn" {
					found = true
				}
			}
			if !found {
				t.Error("actual generated operation must select actionUrn")
			}
		})
	}
}

func TestActionURN1138HelpExample(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "hadron node get "+actionURN1138) {
		t.Error("missing actionable action URN example")
	}
}

// -m composes a bare loc into a memory; an action URN names its own agent and
// must reach resolveUrn untouched rather than being joined onto the memory.
func TestActionURN1138IgnoresMemoryFlag(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"` + actionNodeID1138 + `","kind":"action","memoryId":"mem1"}}}`,
		"GetNode":    nodeGetJSON(""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", actionURN1138, "-m", "hrn:mem:acme.com:kb", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var sent struct {
		Urn string `json:"urn"`
	}
	if err := json.Unmarshal(captured["ResolveUrn"], &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Urn != actionURN1138 {
		t.Errorf("resolve input=%q, want the action URN verbatim", sent.Urn)
	}
}
