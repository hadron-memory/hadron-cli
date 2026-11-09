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
		if body.OperationName != "SkillLintPlan" || body.Variables.Input.Intent != "LINT" || !reflect.DeepEqual(body.Variables.Input.Memories, []string{"m1", "m2"}) {
			t.Errorf("wrong plan scope: %#v", body)
		}
		if strings.Contains(body.Query, "renderedBody") || strings.Contains(body.Query, "content(") {
			t.Error("lint requested a body")
		}
		hosts = append(hosts, body.Variables.Input.Host)
		w.Header().Set("Content-Type", "application/json")
		// A repeated identical finding within one host is deduplicated without
		// duplicating that host; distinct node ids never merge, even on URN reuse.
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":3,"entries":[
   {"nodeId":"n1","findings":[{"urn":"same-urn","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"neutral warning"},{"urn":"same-urn","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"neutral warning"}]},
   {"nodeId":"n2","findings":[{"urn":"same-urn","memory":"m2","rule":"skill-include-broken","severity":"warning","message":"neutral warning"}]},
   {"nodeId":"outside","findings":[{"urn":"outside","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"must not appear"}]}],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n2", MemoryId: "m2"}, {Id: "n1", MemoryId: "m1"}, {Id: "n1", MemoryId: "m1"}})
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
		"null entry":               `[null]`,
		"null findings":            `[{"nodeId":"n1","findings":null}]`,
		"null finding":             `[{"nodeId":"n1","findings":[null]}]`,
		"unselected null findings": `[{"nodeId":"other","findings":null}]`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":{"skillPlan":{"judged":1,"entries":%s,"unrecognized":[]}}}`, entries)
			}))
			defer srv.Close()
			rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}})
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
	rows, err := providerLintFindings(context.Background(), client, nil)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty result changed: %#v %v", rows, err)
	}
	if _, err := providerLintFindings(context.Background(), client, []*batchNode{{Id: "n1"}}); err == nil {
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
		fmt.Fprintf(w, `{"data":{"skillPlan":{"scanned":4,"judged":1,"entries":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-include-broken","severity":"error","message":"%s"},{"urn":"urn1","memory":"m1","rule":"skill-name-invalid","severity":"error","message":"excluded"}]}],"unrecognized":[]}}}`, body.Variables.Input.Host)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n3", MemoryId: "m3"}, {Id: "n1", MemoryId: "m1"}, {Id: "n2", MemoryId: "m2"}, {Id: "n4", MemoryId: "m1"}})
	if err != nil || calls != 2 || len(rows) != 3 {
		t.Fatalf("host gap mistaken for truncation: %v %#v", err, rows)
	}
	includeRows := 0
	for _, row := range rows {
		if row.Rule == "skill-include-broken" {
			includeRows++
			if row.Severity != "error" || len(row.Hosts) != 1 || row.Message != row.Hosts[0] {
				t.Fatalf("severity/message grouping changed: %#v", row)
			}
		}
		if row.Rule == "skill-name-invalid" && !reflect.DeepEqual(row.Hosts, []string{"claudeSkill", "codexSkill"}) {
			t.Fatalf("non-include server finding was not preserved and merged: %#v", row)
		}
	}
	if includeRows != 2 {
		t.Fatalf("include findings changed: %#v", rows)
	}
}

func TestProviderLintUnavailableIsNotClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":1,"entries":[{"urn":"urn1","nodeId":"n1","class":"unavailable","findings":[]}],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("locked entry became a false clean: %#v %v", rows, err)
	}
	row := rows[0]
	if row.Node != "urn1" || row.Rule != "skill-node-unavailable" || row.Severity != "warning" || len(row.Hosts) != 0 {
		t.Fatalf("wrong unavailable row: %#v", row)
	}
}

func TestProviderLintRejectsJudgedMismatch(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":1,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}})
	if err == nil || len(rows) != 0 || calls != 1 {
		t.Fatalf("inconsistent plan became a false clean: %#v %v calls=%d", rows, err, calls)
	}
}

func TestProviderLintValidatesLaterHostUnrecognized(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[null]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}})
	if err == nil || len(rows) != 0 || calls != 2 {
		t.Fatalf("later malformed host-free plan accepted: %#v %v calls=%d", rows, err, calls)
	}
}

func TestProviderLintRejectsInconsistentLaterHostUnrecognized(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"later host only"}]}]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}})
	if err == nil || len(rows) != 0 || calls != 2 {
		t.Fatalf("inconsistent host-free plan accepted: %#v %v calls=%d", rows, err, calls)
	}
}

func TestProviderLintRejectsSelectedDeclarationOmission(t *testing.T) {
	props := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":0,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1", Properties: &props}})
	if err == nil || len(rows) != 0 {
		t.Fatalf("selected declaration omission became clean: %#v %v", rows, err)
	}
}

func TestProviderLintReportsUnavailableFromEitherHost(t *testing.T) {
	props := json.RawMessage(`{"exports":{"codexSkill":{"name":"hadron-a","description":"Use when a."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct{ Host string }
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Variables.Input.Host == "claudeSkill" {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":1,"entries":[{"urn":"urn1","nodeId":"n1","class":"unavailable","findings":[]}],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1", Properties: &props}})
	if err != nil || len(rows) != 1 || rows[0].Rule != "skill-node-unavailable" {
		t.Fatalf("Codex-only locked entry became a false clean: %#v %v", rows, err)
	}
}

func TestProviderLintRejectsMissingOrEmptyUnknownHostJudgment(t *testing.T) {
	props := json.RawMessage(`{"exports":{"futureHost":{"name":"hadron-a","description":"Use when a."}}}`)
	for name, unrecognized := range map[string]string{
		"missing entry":  `[]`,
		"empty findings": `[{"nodeId":"n1","findings":[]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":%s}}}`, unrecognized)
			}))
			defer srv.Close()
			rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1", Properties: &props}})
			if err == nil || len(rows) != 0 {
				t.Fatalf("incomplete unknown-host judgment became clean: %#v %v", rows, err)
			}
		})
	}
}
