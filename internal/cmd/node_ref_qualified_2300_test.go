package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2300: `node get <qualified URN> -m <other memory>` reads the node
// the URN names; -m composes only a bare loc (cor:urn:010:02, cor:api:140:00).

func nodeGetResolveSent2300(t *testing.T, resolve string, args ...string) (string, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"ResolveUrn": resolve, "GetNode": nodeGetJSON("")})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "get"}, args...), "--json", "--server", gql.URL))
	err := root.Execute()
	var sent struct {
		Urn string `json:"urn"`
	}
	raw, ok := captured["ResolveUrn"]
	if !ok {
		// An empty capture would compare "" with "" and pass with nothing sent.
		t.Fatalf("ResolveUrn was never sent (err=%v)", err)
	}
	if uerr := json.Unmarshal(raw, &sent); uerr != nil {
		t.Fatal(uerr)
	}
	return sent.Urn, err
}

const resolved2300 = `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`

func TestNodeGet2300QualifiedURNIgnoresMemory(t *testing.T) {
	for _, ref := range []string{"hrn:node:example.test:source:demo", "example.test::source::demo"} {
		t.Run(ref, func(t *testing.T) {
			want, err := nodeGetResolveSent2300(t, resolved2300, ref)
			if err != nil {
				t.Fatal(err)
			}
			got, err := nodeGetResolveSent2300(t, resolved2300, ref, "-m", "example.test:other")
			if err != nil {
				t.Fatalf("-m must not change a qualified read: %v", err)
			}
			if got != want {
				t.Fatalf("ResolveUrn sent %q with -m, want %q (as without -m)", got, want)
			}
		})
	}
}

func TestNodeGet2300BareLocStillComposes(t *testing.T) {
	got, err := nodeGetResolveSent2300(t, resolved2300, "demo", "-m", "example.test:other")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hrn:node:example.test:other:demo" {
		t.Fatalf("bare loc + -m sent %q", got)
	}
}

// cli#2902 (the #2898 no-echo policy): the not-found no longer names the URN at all, as given or composed.
// What cli#2300 protected stays: a qualified ref is never rewritten with -m, and the message does not
// pretend it was.
func TestNodeGet2300UnknownQualifiedIsNotComposedAndNotEchoed(t *testing.T) {
	const ref = "hrn:node:example.test:source:missing"
	_, err := nodeGetResolveSent2300(t, `{"data":{"resolveUrn":null}}`, ref, "-m", "example.test:other")
	if got := exitCodeFor(err); got != exitcode.NotFound {
		t.Fatalf("exit = %d, want NotFound (%v)", got, err)
	}
	if strings.Contains(err.Error(), ref) || strings.Contains(err.Error(), "example.test:other:") || !strings.Contains(err.Error(), "not shown") {
		t.Fatalf("not-found names no URN (neither as given nor composed) and says so: %v", err)
	}
}

// nodeGetBatchRefs2300 runs a batch `node get` with -m and returns the refs
// sent in the one NodeBatch call.
func nodeGetBatchRefs2300(t *testing.T, refs ...string) []string {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"node", "get"}, refs...),
		"-m", "example.test:other", "--json", "--server", gql.URL))
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	raw, ok := captured["NodeBatch"]
	if !ok {
		t.Fatal("NodeBatch was never sent")
	}
	var vars struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatal(err)
	}
	return vars.Refs
}

func TestNodeGet2300BatchQualifiedIgnoresMemory(t *testing.T) {
	const id = "019fce8418e8798285e7fc179f5bc8f4"
	for _, tc := range []struct {
		name string
		refs []string
		want []string
	}{
		{"mixed-with-bare-loc",
			[]string{"hrn:node:example.test:source:demo", "example.test::source::legacy", "alpha"},
			[]string{"hrn:node:example.test:source:demo", "hrn:node:example.test::source::legacy", "hrn:node:example.test:other:alpha"}},
		{"all-qualified",
			[]string{"hrn:node:example.test:source:demo", "example.test::source::legacy"},
			[]string{"hrn:node:example.test:source:demo", "hrn:node:example.test::source::legacy"}},
		{"with-node-id",
			[]string{"hrn:node:example.test:source:demo", id, "alpha"},
			[]string{"hrn:node:example.test:source:demo", id, "hrn:node:example.test:other:alpha"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeGetBatchRefs2300(t, tc.refs...)
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Fatalf("refs sent %q, want %q", got, tc.want)
			}
		})
	}
}

// The write side shares the helper: `node update <qualified URN> -m <other>`
// resolves the URN as given and updates that node, never a composed one.
func TestNodeUpdate2300QualifiedURNIgnoresMemory(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": resolveNodeJSON,
		"GetNode":    `{"data":{"node":` + nodeDetailJSON + `}}`,
		"UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "update", nodeURN, "-m", "example.test:other", "--name", "renamed", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var sent struct {
		Urn string `json:"urn"`
	}
	if err := json.Unmarshal(captured["ResolveUrn"], &sent); err != nil {
		t.Fatal(err)
	}
	// nodeURN is the legacy spelling, which ResolveNodeURN only prefixes.
	if want := "hrn:node:" + nodeURN; sent.Urn != want {
		t.Fatalf("ResolveUrn sent %q, want %q (as without -m)", sent.Urn, want)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["UpdateNode"], &vars)
	if vars.Input["id"] != "n1" {
		t.Fatalf("update must target the resolved node: %v", vars.Input)
	}
}
