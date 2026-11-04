package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The transport records every operation: a dry-run must never write, including
// after a partial read. Pages are deliberately shorter than the requested cap.
func TestNodeMoveDryRun741(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     string
		json     bool
		toMemory bool
		single   bool
		long     bool
	}{
		{name: "single JSON", json: true, single: true}, {name: "paged JSON", json: true}, {name: "occupied text"}, {name: "memory target", toMemory: true}, {name: "long requested target", toMemory: true, long: true},
		{name: "second page failure", fail: "page"}, {name: "truncated", fail: "truncated", json: true},
		{name: "unavailable", fail: "unavailable"}, {name: "omitted", fail: "omitted", json: true},
		{name: "source read failure", fail: "source"}, {name: "missing batch node", fail: "missing"},
		{name: "repeated page", fail: "repeat"}, {name: "null listing", fail: "null-list"}, {name: "null batch", fail: "null-batch"}, {name: "destination read failure", fail: "destination"}, {name: "changed memory", fail: "changed", json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			sourcePages := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					fmt.Fprint(w, `{"capabilities":[]}`)
					return
				}
				var q struct {
					OperationName string         `json:"operationName"`
					Variables     map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": v}) }
				switch q.OperationName {
				case "ResolveUrn":
					reply(map[string]any{"resolveUrn": map[string]any{"id": "n1", "kind": "node", "memoryId": "m1"}})
				case "MovePreviewSource":
					if tc.fail == "source" {
						fmt.Fprint(w, `{"errors":[{"message":"read failed"}]}`)
						return
					}
					reply(map[string]any{"node": map[string]any{"id": "n1root", "urn": "hrn:node:acme.com:kb:a:b", "memoryId": "m1", "loc": "a:b", "name": "Root"}})
				case "MovePreviewMemory":
					reply(map[string]any{"memory": map[string]any{"id": "m2", "urn": "hrn:mem:acme.com:archive"}})
				case "MovePreviewList":
					mem := q.Variables["memory"].(string)
					off := int(q.Variables["offset"].(float64))
					hits := []any{}
					if mem == "m1" {
						sourcePages++
						if tc.fail == "repeat" {
							off = 0
						}
						if off > 0 && tc.fail == "page" {
							fmt.Fprint(w, `{"errors":[{"message":"second page failed"}]}`)
							return
						}
						// 201 source nodes, and a text-prefix neighbour that is NOT a descendant.
						for i := off; i < off+100 && i < 202 && (!tc.single || i == 0); i++ {
							id, loc := fmt.Sprintf("n%d", i), fmt.Sprintf("a:b:c%d", i)
							if i == 0 {
								id = "n1root"
								loc = "a:b"
							}
							if i == 201 {
								loc = "a:bc"
							}
							hits = append(hits, map[string]any{"node": map[string]any{"id": id, "memoryId": "m1", "loc": loc, "name": id}})
						}
					} else if off == 0 {
						if tc.fail == "destination" {
							fmt.Fprint(w, `{"errors":[{"message":"destination read failed"}]}`)
							return
						}
						hits = append(hits, map[string]any{"node": map[string]any{"id": "d1", "memoryId": "m2", "loc": q.Variables["prefix"], "name": "Occupied"}})
					}
					if tc.fail == "null-list" {
						reply(map[string]any{"findNodes": nil})
						return
					}
					reply(map[string]any{"findNodes": map[string]any{"hits": hits}})
				case "MovePreviewBatch":
					nodes := []any{}
					refs := q.Variables["refs"].([]any)
					for _, ref := range refs {
						id := ref.(string)
						loc := "a:b:c" + strings.TrimPrefix(id, "n")
						mem := "m1"
						if id == "n1root" {
							loc = "a:b"
						}
						if id == "d1" {
							loc = "target"
							if tc.toMemory {
								loc = "a:b"
							}
							mem = "m2"
						}
						nodes = append(nodes, map[string]any{"id": id, "urn": "hrn:node:acme.com:kb:" + loc, "memoryId": mem, "loc": loc, "name": id})
					}
					if tc.fail == "null-batch" {
						reply(map[string]any{"nodeBatch": nil})
						return
					}
					if tc.fail == "changed" {
						nodes[0].(map[string]any)["memoryId"] = "other"
					}
					b := map[string]any{"nodes": nodes, "truncated": false, "omitted": []any{}, "unavailable": []any{}}
					switch tc.fail {
					case "truncated":
						b["truncated"] = true
					case "omitted":
						b["omitted"] = []string{"lost"}
					case "unavailable":
						b["unavailable"] = []string{"hidden"}
					case "missing":
						if refs[0] == "d1" {
							b["nodes"] = []any{}
						}
					}
					reply(map[string]any{"nodeBatch": b})
				default:
					writes++
					t.Errorf("unexpected operation (possible write): %s", q.OperationName)
					fmt.Fprint(w, `{"errors":[{"message":"unexpected write"}]}`)
				}
			}))
			defer srv.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"node", "move", "a:b", "-m", "acme.com:kb", "--dry-run", "--server", srv.URL}
			requested := "hrn:node:acme.com:archive:target"
			if tc.toMemory {
				requested = "acme.com::archive"
				if tc.long {
					requested = strings.Repeat("long", 90) + "tail"
				}
				args = append(args, "--to-memory", requested)
			} else {
				args = append(args, "--to-urn", requested)
			}
			if tc.json {
				args = append(args, "--json")
			}
			root.SetArgs(args)
			err := root.Execute()
			if writes != 0 {
				t.Fatalf("sent %d unexpected operations", writes)
			}
			if tc.fail != "" {
				if exitCodeFor(err) != 1 || !strings.Contains(out.String(), "INCOMPLETE") {
					t.Fatalf("incomplete must exit1 and be visible: err=%v output=%s", err, out.String())
				}
				if tc.json {
					var partial struct {
						Complete     bool
						Source       struct{ Nodes []any }
						Destination  struct{ Present []any }
						NotPredicted []string
					}
					if e := json.Unmarshal([]byte(out.String()), &partial); e != nil {
						t.Fatal(e)
					}
					if partial.Complete || partial.Source.Nodes == nil || partial.Destination.Present == nil || len(partial.Source.Nodes) > 0 || len(partial.Destination.Present) > 0 || partial.NotPredicted == nil {
						t.Fatalf("partial JSON is not safely empty: %s", out.String())
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tc.single && sourcePages < 3 {
				t.Fatalf("did not page source: %d", sourcePages)
			}
			if tc.json {
				var dto struct {
					Advisory bool `json:"advisory"`
					Complete bool `json:"complete"`
					Source   struct {
						Nodes []struct {
							URN string `json:"urn"`
						}
					} `json:"source"`
					Destination struct {
						Requested string `json:"requested"`
						Present   []any  `json:"present"`
					} `json:"destination"`
				}
				if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
					t.Fatal(err)
				}
				expected := 201
				if tc.single {
					expected = 1
				}
				if !dto.Advisory || !dto.Complete || len(dto.Source.Nodes) != expected || dto.Destination.Requested != requested || len(dto.Destination.Present) != 1 {
					t.Fatalf("bad preview: %s", out.String())
				}
				for _, n := range dto.Source.Nodes {
					if strings.Contains(n.URN, "a:bc") {
						t.Fatal("crossed colon boundary")
					}
				}
			} else {
				for _, s := range []string{"Advisory:", "already present at the destination", requested} {
					if !strings.Contains(out.String(), s) {
						t.Fatalf("missing %q: %s", s, out.String())
					}
				}
			}
			for _, s := range []string{"will fail", "collision", "refused", "newUrn"} {
				if strings.Contains(out.String(), s) {
					t.Fatalf("prediction %q in preview", s)
				}
			}
		})
	}
}
