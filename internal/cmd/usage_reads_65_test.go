package cmd

import (
	"encoding/json"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
	"strings"
	"testing"
)

func usage65Run(t *testing.T, response string, args ...string) (string, string, int, map[string]json.RawMessage) {
	t.Helper()
	srv, calls := captureGraphQLFunc(t, func(op string) string {
		if op != "UsageEventsPage" && op != "MemoryUsageSummary" {
			t.Errorf("unexpected %s", op)
		}
		return response
	})
	f, out := testFactory(t)
	stderr := &strings.Builder{}
	f.IOStreams.ErrOut = stderr
	root := NewRootCmd(f)
	root.SetArgs(append(args, "--server", srv.URL))
	err := root.Execute()
	code := 0
	if err != nil {
		code = renderError(f, err)
	}
	return out.String(), stderr.String(), code, calls
}

const usage65Event = `{"id":"e1","type":"read","createdAt":"2026-10-23T00:00:00Z","memoryId":"m1","nodeLoc":"a:b:c:d","provider":"p","model":"m","keyOwnerType":null,"tokensIn":null,"tokensOut":3,"costMicroCents":null}`

func TestUsage65LocAndNullableCost(t *testing.T) {
	response := `{"data":{"usageEventsPage":{"total":3,"hasMore":true,"items":[` + usage65Event + `]}}}`
	out, stderr, code, calls := usage65Run(t, response, "usage", "events", "--loc", "a:b:c:d", "--memory", "acme.com:kb", "--type", "read", "--limit", "1", "--json")
	if code != 0 {
		t.Fatalf("%s %s", out, stderr)
	}
	var vars map[string]any
	_ = json.Unmarshal(calls["UsageEventsPage"], &vars)
	if vars["loc"] != "a:b:c:d" || vars["memoryId"] != "hrn:mem:acme.com:kb" || vars["eventType"] != "read" || vars["limit"] != float64(1) {
		t.Fatal(vars)
	}
	for _, k := range []string{"nodeId", "offset"} {
		if _, ok := vars[k]; ok {
			t.Fatal(vars)
		}
	}
	if !strings.Contains(out, `"costMicroCents": null`) || !strings.Contains(out, `"tokensIn": null`) {
		t.Fatal(out)
	}
	if !strings.Contains(stderr, "showing 1-1 of 3") || !strings.Contains(stderr, "more: --offset 1") {
		t.Fatal(stderr)
	}
	text, _, code, _ := usage65Run(t, response, "usage", "events")
	if code != 0 || !strings.Contains(text, "unpriced") {
		t.Fatal(text)
	}
}
func TestUsage65BigintAndPage(t *testing.T) {
	big := "123456789012345678901234567890"
	response := `{"data":{"usageEventsPage":{"total":41,"hasMore":false,"items":[` + strings.Replace(usage65Event, `"costMicroCents":null`, `"costMicroCents":"`+big+`"`, 1) + `]}}}`
	out, stderr, code, calls := usage65Run(t, response, "usage", "events", "--offset", "40", "--json")
	if code != 0 || !strings.Contains(out, `"`+big+`"`) || strings.Contains(stderr, "more: --offset") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
	var vars map[string]any
	_ = json.Unmarshal(calls["UsageEventsPage"], &vars)
	if vars["offset"] != float64(40) || vars["limit"] != float64(20) {
		t.Fatal(vars)
	}
	for _, k := range []string{"loc", "memoryId", "eventType", "nodeId"} {
		if _, ok := vars[k]; ok {
			t.Fatal(vars)
		}
	}
}
func TestUsage65EmptyAndMemorySummary(t *testing.T) {
	out, _, code, _ := usage65Run(t, `{"data":{"usageEventsPage":{"total":0,"hasMore":false,"items":[]}}}`, "usage", "events", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" {
		t.Fatal(out)
	}
	out, _, code, calls := usage65Run(t, `{"data":{"memoryUsageSummary":[{"nodeLoc":"a:b","type":"read","count":4}]}}`, "memory", "usage", "acme.com:kb", "--json")
	if code != 0 || !strings.Contains(out, `"count": 4`) {
		t.Fatal(out)
	}
	var v map[string]any
	_ = json.Unmarshal(calls["MemoryUsageSummary"], &v)
	if v["memoryRef"] != "hrn:mem:acme.com:kb" {
		t.Fatal(v)
	}
}
func TestUsage65InvalidAndRefusal(t *testing.T) {
	for _, args := range [][]string{{"usage", "events", "--loc="}, {"usage", "events", "--memory="}, {"usage", "events", "--type="}, {"usage", "events", "--limit", "-1"}, {"usage", "events", "--limit", "201"}, {"usage", "events", "--offset", "-1"}, {"usage", "events", "--since", "yesterday"}, {"memory", "usage", ""}} {
		_, stderr, code, calls := usage65Run(t, `{}`, args...)
		if code != 2 || len(calls) != 0 {
			t.Fatalf("%v %d %s %v", args, code, stderr, calls)
		}
	}
	out, stderr, code, _ := usage65Run(t, `{"errors":[{"message":"denied or missing","extensions":{"code":"MEMORY_NOT_FOUND"}}]}`, "memory", "usage", "acme.com:kb")
	if code != 4 || out != "" || !strings.Contains(stderr, "denied or missing") {
		t.Fatalf("%d %s %s", code, out, stderr)
	}
}

func TestUsage65ActualDocumentUsesLoc(t *testing.T) {
	doc, err := parser.ParseQuery(&ast.Source{Input: gen.UsageEventsPage_Operation})
	if err != nil {
		t.Fatal(err)
	}
	op := doc.Operations.ForName("UsageEventsPage")
	if op == nil {
		t.Fatal("missing op")
	}
	field := op.SelectionSet[0].(*ast.Field)
	if field.Name != "usageEventsPage" {
		t.Fatal(field.Name)
	}
	loc := field.Arguments.ForName("loc")
	if loc == nil || loc.Value.Raw != "loc" || field.Arguments.ForName("nodeId") == nil || field.Arguments.ForName("nodeId").Value.Raw != "nodeId" {
		t.Fatal("actual document must keep loc and nodeId as distinct variables")
	}
}

// cli#1339 (QA, #1460): --limit 0 is the twin's count-only page: the request IS sent
// with limit 0, stdout/--json stay an empty array, and the real total is on stderr.
func TestUsage1339LimitZeroCountsOnly(t *testing.T) {
	out, stderr, code, calls := usage65Run(t, `{"data":{"usageEventsPage":{"total":42,"hasMore":true,"items":[]}}}`, "usage", "events", "--limit", "0", "--json")
	if code != 0 || strings.TrimSpace(out) != "[]" || !strings.Contains(stderr, "total 42") {
		t.Fatalf("%d out %q stderr %q", code, out, stderr)
	}
	var vars map[string]any
	_ = json.Unmarshal(calls["UsageEventsPage"], &vars)
	if v, ok := vars["limit"]; !ok || v != float64(0) {
		t.Fatalf("limit 0 must reach the wire, got %v", vars)
	}
}
