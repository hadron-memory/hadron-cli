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
	if raw, ok := captured["ResolveUrn"]; ok {
		if uerr := json.Unmarshal(raw, &sent); uerr != nil {
			t.Fatal(uerr)
		}
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

func TestNodeGet2300UnknownQualifiedNamesURNAsGiven(t *testing.T) {
	const ref = "hrn:node:example.test:source:missing"
	_, err := nodeGetResolveSent2300(t, `{"data":{"resolveUrn":null}}`, ref, "-m", "example.test:other")
	if got := exitCodeFor(err); got != exitcode.NotFound {
		t.Fatalf("exit = %d, want NotFound (%v)", got, err)
	}
	if !strings.Contains(err.Error(), ref) || strings.Contains(err.Error(), "example.test:other:") {
		t.Fatalf("not-found must name the URN as given, not a composed one: %v", err)
	}
}

func TestNodeGet2300BatchQualifiedIgnoresMemory(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"NodeBatch": nodeBatchResult([]string{batchNodeJSON("n1", "alpha")}, ""),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"node", "get", "hrn:node:example.test:source:demo", "example.test::source::legacy", "alpha",
		"-m", "example.test:other", "--json", "--server", gql.URL})
	_ = root.Execute()
	var vars struct {
		Refs []string `json:"refs"`
	}
	if err := json.Unmarshal(captured["NodeBatch"], &vars); err != nil {
		t.Fatalf("NodeBatch not sent: %v", err)
	}
	want := []string{"hrn:node:example.test:source:demo", "hrn:node:example.test::source::legacy", "hrn:node:example.test:other:alpha"}
	if strings.Join(vars.Refs, " ") != strings.Join(want, " ") {
		t.Fatalf("refs sent %q, want %q", vars.Refs, want)
	}
}
