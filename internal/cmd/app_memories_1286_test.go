package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppMemories1286HelpNamesAppMemory(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"app", "memories", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "APP_MEMORY      the App's own tenant memory") {
		t.Fatalf("help must explain APP_MEMORY in plain words:\n%s", out.String())
	}
}

// APP_MEMORY is a current schema kind; FUTURE_SOURCE simulates a later server
// enum value to pin the existing forward-compatible string rendering only.
func TestAppMemories1286SourceKinds(t *testing.T) {
	for _, kind := range []string{"APP_MEMORY", "FUTURE_SOURCE"} {
		t.Run(kind, func(t *testing.T) {
			response := `{"data":{"appMemoryScope":{"total":1,"items":[{"memoryId":"0123456789abcdef0123456789abcdef","memoryUrn":"hrn:mem:acme.com:tenant-notes","name":"Tenant notes","class":"app","sources":[{"kind":"` + kind + `","agentId":null}],"viewerCanWrite":false}]}}}`
			for _, asJSON := range []bool{false, true} {
				url, _, order := appMemoriesFake(t, response)
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"app", "memories", "hrn:app:acme.com:eng-team", "--server", url}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				if len(*order) != 1 || (*order)[0] != "AppMemoryScope" {
					t.Fatalf("source with no agent must need only scope read: %v", *order)
				}
				if !asJSON {
					lines := strings.Split(strings.TrimSpace(out.String()), "\n")
					if len(lines) != 2 || !strings.Contains(lines[1], kind) || !strings.Contains(lines[1], "hrn:mem:acme.com:tenant-notes") {
						t.Fatalf("source row missing: %s", out.String())
					}
					continue
				}
				var page struct {
					Total int `json:"total"`
					Items []struct {
						Sources []map[string]json.RawMessage `json:"sources"`
					} `json:"items"`
				}
				if err := json.Unmarshal([]byte(out.String()), &page); err != nil {
					t.Fatal(err)
				}
				if page.Total != 1 || len(page.Items) != 1 || len(page.Items[0].Sources) != 1 {
					t.Fatalf("lost source row: %s", out.String())
				}
				src := page.Items[0].Sources[0]
				if string(src["kind"]) != `"`+kind+`"` || string(src["agentId"]) != "null" || string(src["agentName"]) != "null" {
					t.Fatalf("changed kind or nullable fields: %s", out.String())
				}
			}
		})
	}
}
