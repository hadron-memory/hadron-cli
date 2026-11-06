package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Equal references are intentional: multiplicity is not an identity key.
func TestSpecReferencePages1554AllThreeConsumers(t *testing.T) {
	for _, verb := range []string{"backlinks", "unresolved", "lint"} {
		t.Run(verb, func(t *testing.T) {
			offsets := []int{}
			refs := 205
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var q struct {
					OperationName string         `json:"operationName"`
					Variables     map[string]any `json:"variables"`
				}
				if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch q.OperationName {
				case "SpecBacklinks", "SpecUnresolvedReferences":
					fmt.Fprint(w, `{"errors":[{"message":"legacy path must not be normal","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`)
				case "SpecBacklinksPage", "SpecUnresolvedReferencesPage":
					off := int(q.Variables["offset"].(float64))
					limit := int(q.Variables["limit"].(float64))
					offsets = append(offsets, off)
					if limit != 20 {
						t.Errorf("limit=%d", limit)
					}
					// A provider clamp is not exhaustion. Offset advances by actual served rows.
					n := 11
					if off+n > refs {
						n = refs - off
					}
					rows := make([]map[string]any, n)
					for i := range rows {
						rows[i] = map[string]any{"kind": "URN", "field": "content", "sourceLoc": "msg:010:02", "sourceNodeId": "n1", "targetLoc": "msg:020:02", "text": nil, "reason": "MISSING"}
					}
					field := "specUnresolvedReferencesPage"
					if q.OperationName == "SpecBacklinksPage" {
						field = "specBacklinksPage"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{field: map[string]any{"items": rows, "total": refs, "hasMore": off+n < refs}}})
				default:
					if off, ok := q.Variables["offset"].(float64); ok && off > 0 && (q.OperationName == "SpecPlaceholderScan" || q.OperationName == "FindNodes") {
						fmt.Fprint(w, `{"data":{"findNodes":{"hits":[]},"nodes":[]}}`)
						return
					}
					data := map[string]string{"Memories": memListMicromentorJSON, "GetMemory": memGetVectorEnabledJSON, "SpecCorpusState": draftStateJSON, "SpecPlaceholderScan": placeholderScanJSON(map[string]bool{"msg:010:02": false}), "FindNodes": `{"data":{"findNodes":{"hits":[{"node":` + specNodeList("msg:010:02", `["spec"]`) + `}],"total":1}}}`, "NodeBatch": `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[` + specBatchNode("msg:010:02") + `]}}}`}
					if v, ok := data[q.OperationName]; ok {
						fmt.Fprint(w, v)
					} else {
						t.Errorf("unexpected op %s", q.OperationName)
						fmt.Fprint(w, `{"data":{}}`)
					}
				}
			}))
			defer s.Close()
			f, out := testFactory(t)
			root := NewRootCmd(f)
			args := []string{"spec", verb}
			if verb == "backlinks" {
				args = append(args, "msg:010:02")
			}
			if verb == "lint" {
				args = append(args, "--all")
			}
			args = append(args, "-m", specMem, "--json", "--server", s.URL)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("whole read: %v; %s", err, out.String())
			}
			want := []int{}
			for i := 0; i < 205; i += 11 {
				want = append(want, i)
			}
			if !reflect.DeepEqual(offsets, want) {
				t.Fatalf("offsets=%v want %v", offsets, want)
			}
			if verb == "lint" {
				var got []struct {
					Rule string `json:"rule"`
				}
				if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
					t.Fatal(err)
				}
				n := 0
				for _, v := range got {
					if v.Rule == "unresolved-reference" {
						n++
					}
				}
				if n != 205 {
					t.Fatalf("lint unresolved=%d", n)
				}
			} else {
				var got struct {
					References []map[string]any `json:"references"`
				}
				if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
					t.Fatal(err)
				}
				if len(got.References) != 205 {
					t.Fatalf("refs=%d", len(got.References))
				}
				for _, v := range got.References {
					if v["sourceNodeId"] != "n1" {
						t.Fatal(v)
					}
				}
			}
		})
	}
}
