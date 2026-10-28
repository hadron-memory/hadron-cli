package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Khan/genqlient/graphql"
)

func TestProviderIncludesScopeIdentityAndHostMerging(t *testing.T) {
	var hosts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName, Query string
			Variables            struct {
				Input struct {
					Intent, Host string
					Memories     []string
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.OperationName != "SkillPlan" || body.Variables.Input.Intent != "LINT" || !reflect.DeepEqual(body.Variables.Input.Memories, []string{"m1", "m2"}) {
			t.Errorf("wrong plan scope: %#v", body)
		}
		if strings.Contains(body.Query, "renderedBody") || strings.Contains(body.Query, "content(") {
			t.Error("lint requested a body")
		}
		hosts = append(hosts, body.Variables.Input.Host)
		w.Header().Set("Content-Type", "application/json")
		// Distinct ids can emit the same legacy URN. They must remain distinct
		// findings, while each node's identical finding merges across the hosts.
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"entries":[
   {"nodeId":"n1","findings":[{"urn":"same-urn","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"neutral warning"}]},
   {"nodeId":"n2","findings":[{"urn":"same-urn","memory":"m2","rule":"skill-include-broken","severity":"warning","message":"neutral warning"}]},
   {"nodeId":"outside","findings":[{"urn":"outside","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"must not appear"}]}],"orphans":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerIncludeFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n2", MemoryId: "m2"}, {Id: "n1", MemoryId: "m1"}, {Id: "n1", MemoryId: "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hosts, []string{"claudeSkill", "codexSkill"}) || len(rows) != 2 {
		t.Fatalf("host/identity inflation: %v %#v", hosts, rows)
	}
	if rows[0].Memory != "m1" || rows[1].Memory != "m2" {
		t.Fatalf("lost separate memory identities: %#v", rows)
	}
	for _, row := range rows {
		if !reflect.DeepEqual(row.Hosts, hosts) || row.Message != "neutral warning" {
			t.Fatalf("wrong returned finding: %#v", row)
		}
	}
}
