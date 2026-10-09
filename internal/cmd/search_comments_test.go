package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchCommentsOnlyWireAndOutput(t *testing.T) {
	for _, mode := range []string{"hybrid", "keyword", "vector", "regex"} {
		for _, asJSON := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/text", true: "/json"}[asJSON], func(t *testing.T) {
				response := strings.NewReplacer(`"nodeType":"finding"`, `"nodeType":"comment"`, `"nodeType":"info"`, `"nodeType":"comment"`).Replace(searchEnvelope)
				gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": response})
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"search", "feedback", "--comments-only", "--mode", mode, "-m", "acme.com::kb", "--prefix", "comments:", "--tag", "review", "--limit", "7", "--offset", "3", "--server", gql.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				var vars struct {
					Mode   string                     `json:"mode"`
					Limit  int                        `json:"limit"`
					Offset int                        `json:"offset"`
					Filter map[string]json.RawMessage `json:"filter"`
				}
				if err := json.Unmarshal(captured["SearchNodes"], &vars); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]string{"contentScope": `"COMMENTS"`, "memoryIds": `["acme.com::kb"]`, "locPrefix": `"comments:"`, "tags": `["review"]`} {
					if string(vars.Filter[key]) != want {
						t.Errorf("filter.%s = %s, want %s", key, vars.Filter[key], want)
					}
				}
				if vars.Mode != mode || vars.Limit != 7 || vars.Offset != 3 {
					t.Errorf("ranking/page args lost: %+v", vars)
				}
				if asJSON {
					var result struct {
						Hits []struct {
							NodeType string `json:"nodeType"`
						} `json:"hits"`
					}
					if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
						t.Fatal(err)
					}
					if len(result.Hits) != 2 || result.Hits[0].NodeType != "comment" {
						t.Fatalf("comment identity lost: %s", out.String())
					}
				} else if !strings.Contains(out.String(), "Feedback comments (not verified target content)") {
					t.Fatalf("feedback label missing: %s", out.String())
				}
			})
		}
	}
}

func TestSearchUnflaggedOmitsContentScope(t *testing.T) {
	for _, flags := range [][]string{nil, {"-m", "acme.com::kb"}, {"--type", "comment"}, {"--comments-only=false", "--tag", "review"}} {
		gql, captured := captureGraphQL(t, map[string]string{"SearchNodes": searchEnvelope})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		args := append([]string{"search", "q", "--server", gql.URL}, flags...)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var vars map[string]json.RawMessage
		if err := json.Unmarshal(captured["SearchNodes"], &vars); err != nil {
			t.Fatal(err)
		}
		if raw, ok := vars["filter"]; ok {
			var filter map[string]json.RawMessage
			if err := json.Unmarshal(raw, &filter); err != nil {
				t.Fatal(err)
			}
			if value, ok := filter["contentScope"]; ok {
				t.Fatalf("unflagged search sent contentScope %s", value)
			}
		}
	}
}
