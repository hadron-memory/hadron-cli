package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestAgentOrigin1001Selection(t *testing.T) {
	if !strings.Contains(gen.GetAgent_Operation, "clonedFromAgentRef") {
		t.Fatal("GetAgent must select origin")
	}
	for op, doc := range map[string]string{"Agents": gen.Agents_Operation, "CloneAgent": gen.CloneAgent_Operation, "CreateAgent": gen.CreateAgent_Operation} {
		if strings.Contains(doc, "clonedFromAgentRef") {
			t.Fatalf("%s selection inflated", op)
		}
	}
}

func TestAgentOrigin1001(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		want        any
	}{
		{"current origin", `,"clonedFromAgentRef":"hrn:agent:source.test:current"`, "hrn:agent:source.test:current"},
		{"concealed", `,"clonedFromAgentRef":null`, nil},
		{"omitted", "", nil},
	} {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: " JSON", false: " text"}[asJSON], func(t *testing.T) {
				fixture := strings.TrimSuffix(agentJSON, "}") + tc.field + "}"
				var mu sync.Mutex
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					calls++
					mu.Unlock()
					var req struct {
						OperationName, Query string
						Variables            map[string]any
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.OperationName != "GetAgent" || req.Query != gen.GetAgent_Operation || req.Variables["ref"] != "unchanged-caller-ref" {
						t.Errorf("unexpected lookup/mutation/projection/ref: %+v", req)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"agent":` + fixture + `}}`))
				}))
				defer srv.Close()
				f, out := testFactory(t)
				root := NewRootCmd(f)
				args := []string{"agent", "get", "unchanged-caller-ref", "--server", srv.URL}
				if asJSON {
					args = append(args, "--json")
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				count := calls
				mu.Unlock()
				if count != 1 {
					t.Fatalf("actual GetAgent HTTP calls = %d, want exactly 1", count)
				}
				if asJSON {
					var got map[string]any
					_ = json.Unmarshal([]byte(out.String()), &got)
					v, ok := got["clonedFromAgentRef"]
					if !ok || v != tc.want {
						t.Fatalf("origin key/value %v %v", ok, v)
					}
				} else if tc.want != nil {
					if !strings.Contains(out.String(), "Cloned from: "+tc.want.(string)) {
						t.Fatal(out.String())
					}
				} else if strings.Contains(out.String(), "Cloned from:") || strings.Contains(out.String(), "unknown") || strings.Contains(out.String(), "deleted") {
					t.Fatal(out.String())
				}
			})
		}
	}
}
