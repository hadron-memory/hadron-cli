package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

func TestAuthoredKindDoors1685(t *testing.T) {
	for _, door := range []string{"Node", "TaskNode", "SpecNode", "ReviewNode"} {
		for _, action := range []string{"Create", "Update", "Clear"} {
			for _, claimSet := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/%t", door, action, claimSet), func(t *testing.T) {
					var claim *gen.AuthoredWithInput
					if claimSet {
						claim = &gen.AuthoredWithInput{Task: "task1", Revision: 4}
					}
					expectedAction := action
					if action == "Clear" {
						expectedAction = "Update"
					}
					expectedOp := expectedAction + door
					if action == "Clear" {
						expectedOp += "ClearingRole"
						if claimSet {
							expectedOp += "Authored"
						}
					}
					calls := 0
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						var req struct {
							OperationName string
							Query         string
							Variables     map[string]json.RawMessage
						}
						if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
							t.Error(err)
						}
						if req.OperationName != expectedOp {
							t.Errorf("operation %s want%s", req.OperationName, expectedOp)
						}
						vars := req.Variables
						if action != "Clear" {
							if err := json.Unmarshal(vars["input"], &vars); err != nil {
								t.Error(err)
							}
						}
						raw, present := vars["authoredWith"]
						if claimSet {
							var got gen.AuthoredWithInput
							if json.Unmarshal(raw, &got) != nil || got != *claim {
								t.Errorf("claim dropped: %s", raw)
							}
						} else if present {
							t.Errorf("unset claim emitted: %s", raw)
						}
						if action == "Clear" && !strings.Contains(strings.ReplaceAll(req.Query, " ", ""), "role:null") {
							t.Error("role clear lost")
						}
						field := strings.ToLower(expectedAction[:1]) + expectedAction[1:] + door
						fmt.Fprintf(w, `{"data":{%q:{"id":"n1","memoryId":"m1","loc":"x"}}}`, field)
					}))
					defer srv.Close()
					client := graphql.NewClient(srv.URL, srv.Client())
					ctx := context.Background()
					role := ""
					runnable := false
					switch door {
					case "TaskNode":
						runnable = true
					case "SpecNode":
						role = "spec.rule"
					case "ReviewNode":
						role = "review.check"
					}
					id := "n1"
					var err error
					if action == "Create" {
						_, err = CreateNodeByKind(ctx, client, &gen.CreateNodeInput{MemoryId: id, Loc: "x", Name: "X", Role: &role, IsRunnable: &runnable, AuthoredWith: claim})
					} else {
						input := &gen.UpdateNodeInput{Id: &id, AuthoredWith: claim}
						cur := NodeKindState{Role: &role, IsRunnable: runnable}
						if action == "Clear" {
							_, err = UpdateNodeClearingRoleByKind(ctx, client, input, cur)
						} else {
							_, err = UpdateNodeByKind(ctx, client, input, cur)
						}
					}
					if err != nil {
						t.Fatal(err)
					}
					if calls != 1 {
						t.Fatalf("calls=%d", calls)
					}
				})
			}
		}
	}
}
