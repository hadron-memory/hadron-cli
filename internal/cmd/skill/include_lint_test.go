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
		// Repeated identical findings within one host remain separate rows;
		// only the cross-host copies merge. Distinct node ids never merge, even
		// on URN reuse.
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":3,"entries":[
   {"nodeId":"n1","findings":[{"urn":"same-urn","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"neutral warning"},{"urn":"same-urn","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"neutral warning"}]},
   {"nodeId":"n2","findings":[{"urn":"same-urn","memory":"m2","rule":"skill-include-broken","severity":"warning","message":"neutral warning"}]},
   {"nodeId":"outside","findings":[{"urn":"outside","memory":"m1","rule":"skill-include-broken","severity":"warning","message":"must not appear"}]}],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n2", MemoryId: "m2"}, {Id: "n1", MemoryId: "m1"}, {Id: "n1", MemoryId: "m1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hosts, []string{"claudeSkill", "codexSkill"}) || len(rows) != 3 {
		t.Fatalf("host/identity inflation: %v %#v", hosts, rows)
	}
	if rows[0].Memory != "m1" || rows[1].Memory != "m1" || rows[2].Memory != "m2" {
		t.Fatalf("lost separate memory identities: %#v", rows)
	}
	for _, row := range rows {
		if !reflect.DeepEqual(row.Hosts, hosts) || row.Message != "neutral warning" {
			t.Fatalf("wrong returned finding: %#v", row)
		}
	}
}

func TestProviderIncludesDoesNotMergeDifferentFindingIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct{ Host string }
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Variables.Input.Host == "claudeSkill" {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":1,"entries":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-content-empty","severity":"error","message":"same"}]}],"unrecognized":[]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":1,"entries":[{"nodeId":"n1","findings":[{"urn":"urn2","memory":"m2","rule":"skill-content-empty","severity":"error","message":"same"}]}],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
	if err != nil || len(rows) != 2 {
		t.Fatalf("contradictory finding identities were coalesced: %#v %v", rows, err)
	}
	if rows[0].Node != "urn1" || rows[0].Memory != "m1" || rows[1].Node != "urn2" || rows[1].Memory != "m2" {
		t.Fatalf("contradictory finding identity was discarded: %#v", rows)
	}
	for i, host := range []string{"claudeSkill", "codexSkill"} {
		if !reflect.DeepEqual(rows[i].Hosts, []string{host}) {
			t.Fatalf("row %d has wrong hosts: %#v", i, rows[i])
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
			rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
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
	rows, err := providerLintFindings(context.Background(), client, nil, nil)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty result changed: %#v %v", rows, err)
	}
	if _, err := providerLintFindings(context.Background(), client, []*batchNode{{Id: "n1"}}, nil); err == nil {
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
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n3", MemoryId: "m3"}, {Id: "n1", MemoryId: "m1"}, {Id: "n2", MemoryId: "m2"}, {Id: "n4", MemoryId: "m1"}}, nil)
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
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
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
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
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
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
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
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
	if err == nil || len(rows) != 0 || calls != 2 {
		t.Fatalf("inconsistent host-free plan accepted: %#v %v calls=%d", rows, err, calls)
	}
}

func TestProviderLintRejectsDifferentUnknownHostDuplicateCounts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"same"},{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"same"}]}]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"same"}]}]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
	if err == nil || len(rows) != 0 || calls != 2 {
		t.Fatalf("host-free duplicate counts were treated as a set: %#v %v calls=%d", rows, err, calls)
	}
}

func TestProviderLintIgnoresUnselectedHostFreeChanges(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[{"nodeId":"other","keyCount":1,"keys":["a"],"findings":[{"urn":"other","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"a"}]}]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[{"nodeId":"other","keyCount":1,"keys":["b"],"findings":[{"urn":"other","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"b"}]}]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
	if err != nil || len(rows) != 0 || calls != 2 {
		t.Fatalf("an unselected concurrent unknown-key edit aborted node-scoped lint: %#v %v calls=%d", rows, err, calls)
	}
}

func TestProviderLintAllowsHostFreeFindingOrderToVary(t *testing.T) {
	props1 := json.RawMessage(`{"exports":{"futureHost":{"name":"hadron-a","description":"Use when a."}}}`)
	props2 := json.RawMessage(`{"exports":{"anotherHost":{"name":"hadron-b","description":"Use when b."}}}`)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[
				{"nodeId":"n2","findings":[{"urn":"urn2","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"b"}]},
				{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"a1"},{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"a2"}]}
			]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[
			{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"a2"},{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"a1"}]},
			{"nodeId":"n2","findings":[{"urn":"urn2","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"b"}]}
		]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{
		{Id: "n1", MemoryId: "m1", Properties: &props1}, {Id: "n2", MemoryId: "m1", Properties: &props2},
	}, nil)

	if err != nil || len(rows) != 3 {
		t.Fatalf("equivalent host-free order became an error: %#v %v", rows, err)
	}
}

func TestProviderLintReportsSelectedDeclarationOmission(t *testing.T) {
	props := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":0,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 1 || rows[0].Rule != "skill-node-not-judged" || rows[0].Severity != "warning" || rows[0].Node != "urn1" {
		t.Fatalf("selected declaration omission did not become an explicit error finding: %#v %v", rows, err)
	}
}

func TestProviderLintIncompleteRowsKeepHostAndMemoryIdentity(t *testing.T) {
	props := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":2,"judged":0,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{
		{Id: "n1", Urn: "same-urn", MemoryId: "m1", Properties: &props},
		{Id: "n2", Urn: "same-urn", MemoryId: "m2", Properties: &props},
	}, map[string]*memoryInfo{
		"m1": {URN: "hrn:mem:hadronmemory.com:one"},
		"m2": {URN: "hrn:mem:hadronmemory.com:two"},
	})
	if err != nil || len(rows) != 2 {
		t.Fatalf("missing per-host incomplete rows: %#v %v", rows, err)
	}
	for i, memory := range []string{"hrn:mem:hadronmemory.com:one", "hrn:mem:hadronmemory.com:two"} {
		if rows[i].Node != "same-urn" || rows[i].Memory != memory || !reflect.DeepEqual(rows[i].Hosts, []string{"claudeSkill"}) {
			t.Fatalf("incomplete row %d lost structured identity: %#v", i, rows[i])
		}
	}
}

func TestProviderLintReportsServerOnlyHostAsNotJudgedWarning(t *testing.T) {
	knownProps := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."}}}`)
	futureProps := json.RawMessage(`{"exports":{"futureHost":{"name":"hadron-b","description":"Use when b."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A newer server judges futureHost as a known host, so it correctly
		// returns no unknown-host row. This older CLI did not query that host
		// and reports a non-blocking not-judged warning rather than a false clean.
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":1,"entries":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-content-empty","severity":"error","message":"known finding"}]}],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{
		{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &knownProps},
		{Id: "n2", Urn: "urn2", MemoryId: "m1", Properties: &futureProps},
	}, nil)

	if err != nil || len(rows) != 2 || rows[0].Rule != "skill-content-empty" || rows[1].Rule != "skill-node-not-judged" || rows[1].Severity != "warning" {
		t.Fatalf("known finding or non-blocking newer-host warning was lost: %#v %v", rows, err)
	}
}

func TestProviderLintReportsMissingKnownHostDespiteOtherCoverage(t *testing.T) {
	props := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."},"codexSkill":{"name":"hadron-a","description":"Use when a."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct{ Host string }
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Variables.Input.Host == "claudeSkill" {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":1,"entries":[{"nodeId":"n1","findings":[]}],"unrecognized":[]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":0,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 1 || rows[0].Rule != "skill-node-not-judged" || !strings.Contains(rows[0].Message, "codexSkill") {
		t.Fatalf("Claude coverage hid the omitted Codex judgment: %#v %v", rows, err)
	}
}

func TestProviderLintUnknownHostWarningDoesNotCoverKnownDeclaration(t *testing.T) {
	props := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."},"futureHost":{"name":"hadron-b","description":"Use when b."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":0,"entries":[],"unrecognized":[{"nodeId":"n1","findings":[{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"future host"}]}]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 2 || rows[0].Rule != "skill-unknown-host-key" || rows[1].Rule != "skill-node-not-judged" || !strings.Contains(rows[1].Message, "claudeSkill") {
		t.Fatalf("unknown-host warning concealed the missing Claude judgment: %#v %v", rows, err)
	}
}

func TestProviderLintKnownCoverageKeepsServerOnlyHostAsWarning(t *testing.T) {
	props := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."},"futureHost":{"name":"hadron-b","description":"Use when b."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct{ Host string }
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Variables.Input.Host == "claudeSkill" {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":1,"entries":[{"nodeId":"n1","findings":[]}],"unrecognized":[]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":0,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 1 || rows[0].Rule != "skill-node-not-judged" || rows[0].Severity != "warning" {
		t.Fatalf("a server-only host was hidden or became blocking: %#v %v", rows, err)
	}
}

func TestProviderLintUnrecognizedWarningCanCoexistWithServerOnlyHost(t *testing.T) {
	props := json.RawMessage(`{"exports":{"futureHost":{"name":"hadron-a","description":"Use when a."},"typoHost":{"name":"hadron-b","description":"Use when b."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":0,"entries":[],"unrecognized":[{"nodeId":"n1","keyCount":1,"keys":["typoHost"],"findings":[{"urn":"urn1","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"typoHost"}]}]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 2 || rows[0].Rule != "skill-unknown-host-key" || rows[1].Rule != "skill-node-not-judged" || rows[1].Severity != "warning" {
		t.Fatalf("structured keys did not distinguish server-only and unrecognized keys: %#v %v", rows, err)
	}
}

func TestProviderLintDoesNotTreatScannedEmptyExportsAsSelectedCoverage(t *testing.T) {
	props := json.RawMessage(`{"exports":{}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":1,"judged":0,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("the server's intentional empty-exports gap became an error: %#v %v", rows, err)
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
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 1 || rows[0].Rule != "skill-node-unavailable" {
		t.Fatalf("Codex-only locked entry became a false clean: %#v %v", rows, err)
	}
}

func TestProviderLintOrdersServerUnavailableAfterIncomplete(t *testing.T) {
	bothProps := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-a","description":"Use when a."},"codexSkill":{"name":"hadron-a","description":"Use when a."}}}`)
	futureProps := json.RawMessage(`{"exports":{"futureHost":{"name":"hadron-b","description":"Use when b."}}}`)
	claudeProps := json.RawMessage(`{"exports":{"claudeSkill":{"name":"hadron-c","description":"Use when c."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Input struct{ Host string }
			}
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.Variables.Input.Host == "claudeSkill" {
			_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":4,"judged":1,"entries":[{"urn":"urn1","nodeId":"n1","class":"unavailable","findings":[]}],"unrecognized":[{"nodeId":"n3","findings":[{"urn":"urn3","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"future"}]}]}}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"scanned":4,"judged":2,"entries":[{"urn":"urn1","nodeId":"n1","class":"unavailable","findings":[]},{"nodeId":"n2","findings":[{"urn":"urn2","memory":"m1","rule":"skill-content-empty","severity":"error","message":"hosted"}]}],"unrecognized":[{"nodeId":"n3","findings":[{"urn":"urn3","memory":"m1","rule":"skill-unknown-host-key","severity":"warning","message":"future"}]}]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{
		{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &bothProps},
		{Id: "n2", Urn: "urn2", MemoryId: "m1"},
		{Id: "n3", Urn: "urn3", MemoryId: "m1", Properties: &futureProps},
		{Id: "n4", Urn: "urn4", MemoryId: "m1", Properties: &claudeProps},
	}, nil)

	if err != nil || len(rows) != 4 {
		t.Fatalf("unexpected ordered rows: %#v %v", rows, err)
	}
	want := []string{"skill-content-empty", "skill-unknown-host-key", "skill-node-not-judged", "skill-node-unavailable"}
	for i, rule := range want {
		if rows[i].Rule != rule {
			t.Fatalf("row %d = %s, want %s: %#v", i, rows[i].Rule, rule, rows)
		}
	}
}

func TestProviderLintMissingUnknownWarningBecomesNotJudgedWarning(t *testing.T) {
	props := json.RawMessage(`{"exports":{"futureHost":{"name":"hadron-a","description":"Use when a."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 1 || rows[0].Rule != "skill-node-not-judged" || rows[0].Severity != "warning" || rows[0].Hosts == nil {
		t.Fatalf("an omitted unknown-host judgment became clean or blocking: %#v %v", rows, err)
	}
}

func TestProviderLintEmptyUnknownHostJudgmentBecomesNotJudgedWarning(t *testing.T) {
	props := json.RawMessage(`{"exports":{"futureHost":{"name":"hadron-a","description":"Use when a."}}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":0,"entries":[],"unrecognized":[{"nodeId":"n1","findings":[]}]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", Urn: "urn1", MemoryId: "m1", Properties: &props}}, nil)
	if err != nil || len(rows) != 1 || rows[0].Rule != "skill-node-not-judged" || rows[0].Severity != "warning" {
		t.Fatalf("empty unknown-host judgment became clean, blocking, or a false verdict: %#v %v", rows, err)
	}
}

func TestProviderLintRejectsFindingsOnUnavailableEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"data":{"skillPlan":{"judged":1,"entries":[{"nodeId":"n1","class":"unavailable","findings":[{"urn":"urn1","memory":"m1","rule":"skill-content-empty","severity":"error","message":"ciphertext judgment"}]}],"unrecognized":[]}}}`)
	}))
	defer srv.Close()
	rows, err := providerLintFindings(context.Background(), graphql.NewClient(srv.URL, srv.Client()), []*batchNode{{Id: "n1", MemoryId: "m1"}}, nil)
	if err == nil || len(rows) != 0 {
		t.Fatalf("unavailable entry carried a purported content judgment: %#v %v", rows, err)
	}
}
