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

func TestProviderIncludesMalformedResultsNeverBecomeClean(t *testing.T) {
	for name, entries := range map[string]string{
		"null entry":    `[null]`,
		"null findings": `[{"nodeId":"n1","findings":null}]`,
		"null finding":  `[{"nodeId":"n1","findings":[null]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":{"skillPlan":{"entries":%s}}}`, entries)
			}))
			defer srv.Close()
			rows, err := providerIncludeFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}})
			if err == nil || len(rows) != 0 || calls != 1 {
				t.Fatalf("malformed plan accepted: rows=%#v err=%v calls=%d", rows, err, calls)
			}
		})
	}
}

func TestProviderIncludesEmptyAndMissingScopeMakeNoRequests(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; t.Error("unexpected plan request") }))
	defer srv.Close()
	client := graphql.NewClient(srv.URL, srv.Client())
	rows, err := providerIncludeFindings(context.Background(), client, nil)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty result changed: %#v %v", rows, err)
	}
	if _, err := providerIncludeFindings(context.Background(), client, []*batchNode{{Id: "n1"}}); err == nil {
		t.Fatal("missing memory accepted")
	}
	if calls != 0 {
		t.Fatalf("scope error made %d requests", calls)
	}
}

// scanned-minus-judged is host declaration coverage, not pagination. A node
// may legitimately have no entry on this host (including no declaration).
func TestProviderIncludesHostGapFilterSeverityAndDistinctMessages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct {
					Host     string
					Memories []string
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(body.Variables.Input.Memories, []string{"m1", "m2", "m3"}) {
			t.Errorf("scope not sorted/deduped: %#v", body)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":{"skillPlan":{"scanned":4,"judged":1,"entries":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-include-broken","severity":"error","message":"%s"},{"urn":"urn1","memory":"m1","rule":"skill-name-invalid","severity":"error","message":"excluded"}]}]}}}`, body.Variables.Input.Host)
	}))
	defer srv.Close()
	rows, err := providerIncludeFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n3", MemoryId: "m3"}, {Id: "n1", MemoryId: "m1"}, {Id: "n2", MemoryId: "m2"}, {Id: "n4", MemoryId: "m1"}})
	if err != nil || calls != 2 || len(rows) != 2 {
		t.Fatalf("host gap mistaken for truncation: %v %#v", err, rows)
	}
	for _, row := range rows {
		if row.Rule != "skill-include-broken" || row.Severity != "error" || len(row.Hosts) != 1 || row.Message != row.Hosts[0] {
			t.Fatalf("filter/severity/message grouping changed: %#v", row)
		}
	}
}
