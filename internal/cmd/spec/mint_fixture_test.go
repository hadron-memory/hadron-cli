package spec

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The four mint-blocking lint rules exist twice: in the server's mint check
// (hadron-server src/lib/specMint.ts, #1448) and here in `spec lint`. The
// server keeps a shared fixture so the two copies can't drift; this runs the
// CLI's lint over a vendored copy of it.
//
// testdata/specMint.fixtures.json is copied VERBATIM from hadron-server
// merged hadron-server main @ 8a864c52 (#1462). A case added there and not
// here is exactly the drift this test exists to catch.
//
// duplicate-loc is a cross-node rule, so no single-node case exercises it.
var mintRules = map[string]bool{"nodetype-info": true, "tag-spec": true, "serialization-leak": true, "duplicate-loc": true}

func TestSpecLintSatisfiesTheServerMintFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/specMint.fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Cases []struct {
			Name string `json:"name"`
			Node struct {
				NodeType *string  `json:"nodeType"`
				Tags     []string `json:"tags"`
				Role     *string  `json:"role"`
				Abstract *string  `json:"abstract"`
				Content  *string  `json:"content"`
			} `json:"node"`
			Expect []string `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatal(err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("the fixture has no cases — a vendored copy that parses to nothing measures nothing")
	}
	for _, c := range fx.Cases {
		n := specNode{
			Loc: "msg:010:02", Name: "msg:010:02 — T", Tags: c.Node.Tags, Role: c.Node.Role,
			Abstract: c.Node.Abstract, Content: c.Node.Content, ContentIsRaw: true,
		}
		if c.Node.NodeType != nil {
			n.NodeType = *c.Node.NodeType
		}
		got := []string{}
		for _, f := range lintNode(n, "micromentor.org::platform-specs") {
			if mintRules[f.Rule] && f.Severity == sevError {
				got = append(got, f.Rule)
			}
		}
		want := c.Expect
		if want == nil {
			want = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: CLI lint gives %v, the server's mint check gives %v", c.Name, got, want)
		}
	}
}
