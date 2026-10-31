package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Capture the actual mutation variables, not only the parsed Document: a null
// fixture body is omitted on both create and update, never an implicit clear.
// Each case also pins its route: only the expected mutation is sent.
func TestNodeImportFraming945(t *testing.T) {
	var fixture struct {
		Cases []struct {
			Name, Body string
			Loaded     *string
		}
	}
	raw, err := os.ReadFile("../nodedoc/testdata/markdownBodyFraming.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, c := range fixture.Cases {
		for _, create := range []bool{false, true} {
			name := c.Name + "/update"
			if create {
				name = c.Name + "/create"
			}
			t.Run(name, func(t *testing.T) {
				replies := map[string]string{"ResolveUrn": resolveNullJSON, "UpdateNode": `{"data":{"updateNode":` + nodeJSON + `}}`, "CreateNode": `{"data":{"createNode":` + nodeJSON + `}}`}
				args := []string{"node", "import"}
				op, other := "UpdateNode", "CreateNode"
				if create {
					args = append(args, "--create-only")
					op, other = "CreateNode", "UpdateNode"
				}
				server, captured := captureGraphQL(t, replies)
				f, _ := testFactory(t)
				file := filepath.Join(t.TempDir(), "body.md")
				text := "---\nname: fixture\nloc: topic\nmemory: acme.com:kb\ncontentHash: untrusted\n---\n" + c.Body
				if err := os.WriteFile(file, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				root := NewRootCmd(f)
				root.SetArgs(append(args, file, "--json", "--server", server.URL))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				// The route, not only the payload (#1124): a matching mutation
				// must not ride alongside the opposite one — an update that
				// also falls back to create (or a create-only that updates)
				// would otherwise pass on the payload check alone.
				if _, sent := captured[other]; sent {
					t.Fatalf("%s sent alongside %s", other, op)
				}
				var vars struct {
					Input map[string]json.RawMessage `json:"input"`
				}
				if err := json.Unmarshal(captured[op], &vars); err != nil {
					t.Fatal(err)
				}
				content, present := vars.Input["content"]
				if c.Loaded == nil {
					if present {
						t.Fatalf("absent body must omit content, got %s", content)
					}
				} else {
					var got string
					if !present {
						t.Fatal("content omitted")
					}
					if err := json.Unmarshal(content, &got); err != nil {
						t.Fatal(err)
					}
					if got != *c.Loaded {
						t.Fatalf("content %q, want %q", got, *c.Loaded)
					}
				}
				if _, present := vars.Input["contentHash"]; present {
					t.Fatal("input hash sent")
				}
			})
		}
	}
}
