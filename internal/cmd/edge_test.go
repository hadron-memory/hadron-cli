package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const edgeJSON = `{"id":"e1","name":"routes-to","loc":"findings:flaky-ci:routes-to:start-here","isRunnable":false,"priority":0,
	"source":{"id":"n1","loc":"findings:flaky-ci"},
	"target":{"id":"n2","loc":"start-here"}}`

// resolveByURN serves ResolveUrn with a distinct node id per URN so
// edge add can resolve both endpoints in one fake.
func resolveByURN(t *testing.T, ids map[string]string, responses map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string         `json:"operationName"`
			Variables     map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "ResolveUrn" {
			urn, _ := body.Variables["urn"].(string)
			id, ok := ids[urn]
			if !ok {
				_, _ = w.Write([]byte(`{"data":{"resolveUrn":null}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"resolveUrn":{"id":"` + id + `","kind":"node","memoryId":"mem1"}}}`))
			return
		}
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestEdgeAdd(t *testing.T) {
	gql := resolveByURN(t,
		map[string]string{
			"hrn:node:acme.com::kb::findings:flaky-ci": "n1",
			"hrn:node:acme.com::kb::start-here":        "n2",
		},
		map[string]string{
			"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
		})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add",
		"--from", "acme.com::kb::findings:flaky-ci",
		"--to", "acme.com::kb::start-here",
		"--name", "routes-to", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("output not JSON: %v\n%s", err, out.String())
	}
	if dto["sourceId"] != "n1" || dto["targetId"] != "n2" || dto["name"] != "routes-to" {
		t.Errorf("unexpected edge DTO: %v", dto)
	}
}

// spec 037: --loc/--description/--runnable thread onto createEdge.
func TestEdgeAddThreadsNewFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add", "--from", "a.com::m::x", "--to", "a.com::m::y",
		"--name", "routes-to", "--loc", "x:routes-to:y", "--description", "d", "--runnable",
		"--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateEdge"], &vars)
	if vars["name"] != "routes-to" || vars["loc"] != "x:routes-to:y" || vars["description"] != "d" || vars["isRunnable"] != true {
		t.Errorf("new edge fields not threaded: %v", vars)
	}
}

// Unset optional edge args must be OMITTED, not serialized as explicit null
// (hadron-server#263 redux — the server reads omitted as "preserve").
func TestEdgeAddOmitsUnsetNewFields(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"ResolveUrn": `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`,
		"CreateEdge": `{"data":{"createEdge":` + edgeJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add", "--from", "a.com::m::x", "--to", "a.com::m::y", "--name", "routes-to", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateEdge"], &vars)
	for _, k := range []string{"loc", "description", "isRunnable", "priority", "condition", "data"} {
		if v, present := vars[k]; present {
			t.Errorf("unset %q must be omitted from createEdge variables, got %v", k, v)
		}
	}
}

func TestEdgeUpdateRejectsEmptyConditionFlag(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "update", "e1", "--condition", "", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected JSON validation error for explicit empty --condition, got %v", err)
	}
}

func TestEdgeAddRejectsBadJSONCondition(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "add",
		"--from", "a.com::m::x", "--to", "a.com::m::y", "--name", "l",
		"--condition", "{not json", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("expected JSON validation error, got %v", err)
	}
}

func TestEdgeLs(t *testing.T) {
	gql := resolveByURN(t,
		map[string]string{"hrn:node:acme.com::kb::findings:flaky-ci": "n1"},
		map[string]string{
			"GetNode": `{"data":{"node":` + nodeDetailJSON + `}}`,
		})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "ls", "acme.com::kb::findings:flaky-ci", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "routes-to") || !strings.Contains(out.String(), "start-here") {
		t.Errorf("unexpected edge ls output: %s", out.String())
	}
}

func TestEdgeUpdateRequiresAField(t *testing.T) {
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "update", "e1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("expected nothing-to-update error, got %v", err)
	}
}

func TestEdgeRmWithYes(t *testing.T) {
	gql := resolveByURN(t, nil, map[string]string{
		"DeleteEdge": `{"data":{"deleteEdge":true}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"edge", "rm", "e1", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

// #337 — filters are client-side over what GetNode already returns, so the row
// shape is unchanged and a filtered run is a subset of an unfiltered one.
func TestEdgeListFilters(t *testing.T) {
	const detail = `{"id":"n1","memoryId":"mem1","loc":"preflight","name":"preflight",
		"description":null,"abstract":null,"abstractOriginHash":null,"nodeType":"info","objectType":null,
		"tags":[],"content":null,"data":null,"properties":null,"seq":null,"isRunnable":false,
		"createdAt":"2026-08-01T00:00:00Z","updatedAt":"2026-08-01T00:00:00Z",
		"outgoingEdges":[
			{"id":"e1","name":"routes-to","loc":"l1","isRunnable":false,"priority":0,"target":{"id":"t1","loc":"findings:race","memoryId":"mem1"}},
			{"id":"e2","name":"to diagnose a slow query","loc":"l2","isRunnable":false,"priority":0,"target":{"id":"t2","loc":"findings:slow","memoryId":"mem1"}}],
		"incomingEdges":[
			{"id":"e3","name":"complements","loc":"l3","isRunnable":false,"priority":0,"source":{"id":"s1","loc":"instructions","memoryId":"mem1"}}]}`

	run := func(t *testing.T, extra ...string) []map[string]any {
		t.Helper()
		gql := fakeGraphQL(t, map[string]string{
			"ResolveUrn": resolveNodeJSON,
			"GetNode":    `{"data":{"node":` + detail + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"edge", "list", nodeURN, "--json", "--server", gql.URL}, extra...))
		if err := root.Execute(); err != nil {
			t.Fatalf("execute %v: %v", extra, err)
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
			t.Fatalf("--json must emit an array: %v (%q)", err, out.String())
		}
		return rows
	}

	edgeIDs := func(rows []map[string]any) []string {
		out := []string{}
		for _, r := range rows {
			out = append(out, r["id"].(string))
		}
		return out
	}

	cases := []struct {
		args []string
		want string
	}{
		{nil, "e1,e2,e3"},
		{[]string{"--direction", "outgoing"}, "e1,e2"},
		{[]string{"--direction", "incoming"}, "e3"},
		{[]string{"--name", "routes-to"}, "e1"},
		{[]string{"--to", "findings:slow"}, "e2"},
		{[]string{"--to", "t2"}, "e2"}, // by id
		{[]string{"--from", "instructions"}, "e3"},
		{[]string{"--name", "no-such-label"}, ""},
	}
	for _, tc := range cases {
		got := strings.Join(edgeIDs(run(t, tc.args...)), ",")
		if got != tc.want {
			t.Errorf("%v: got %q, want %q", tc.args, got, tc.want)
		}
	}

	// The row shape is untouched by filtering.
	full := run(t)
	filtered := run(t, "--direction", "outgoing")
	for k := range full[0] {
		if _, ok := filtered[0][k]; !ok {
			t.Errorf("filtering dropped the %q field from the row shape", k)
		}
	}
}

// A contradictory combination fails up front rather than returning an empty
// list that reads like "no such edges".
func TestEdgeListRejectsContradictoryFilters(t *testing.T) {
	for _, extra := range [][]string{
		{"--direction", "sideways"},
		{"--to", "x", "--from", "y"},
		{"--to", "x", "--direction", "incoming"},
		{"--from", "x", "--direction", "outgoing"},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"edge", "list", nodeURN, "--server", "http://127.0.0.1:1"}, extra...))
		if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
			t.Errorf("%v should be a usage error, got %d", extra, got)
		}
	}
}

// End-to-end: an unset shell variable must fail loudly rather than returning
// every edge (#340 review).
func TestEdgeListRejectsEmptyFilterValue(t *testing.T) {
	for _, extra := range [][]string{
		{"--to", ""},
		{"--from", ""},
		{"--name", ""},
		{"--direction", ""},
	} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"edge", "list", nodeURN, "--server", "http://127.0.0.1:1"}, extra...))
		err := root.Execute()
		if got := exitCodeFor(err); got != exitcode.Usage {
			t.Errorf("%v should be a usage error, got %d", extra, got)
		}
		if err != nil && !strings.Contains(err.Error(), "empty value") {
			t.Errorf("%v: expected an empty-value message, got %q", extra, err.Error())
		}
	}
}
