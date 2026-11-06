package cmd

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const emptyAppPage1642 = `{"data":{"appNodesPage":{"items":[],"total":3,"hasMore":true}}}`

func TestAppReadHelpDecisions1642(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		wants []string
	}{
		{[]string{"app", "list", "--help"}, []string{"forwarded verbatim", "whitespace-only"}},
		{[]string{"app", "nodes", "--help"}, []string{"--prefix=", "--type=", "--object-type=", "--tag=", "all supplied tags", "literal empty tag"}},
	} {
		_, r := (run1592{}).exec(t, tc.args...)
		if r.err != nil {
			t.Fatal(r.err)
		}
		for _, want := range tc.wants {
			if !strings.Contains(r.out, want) {
				t.Errorf("%v lacks %q", tc.args, want)
			}
		}
	}
}

func TestAppListAgentWhitespace1642(t *testing.T) {
	for _, agent := range []string{" c123456789012345678901234 ", " hrn:agent:acme.com:iris "} {
		calls, r := (run1592{response: map[string]string{"Apps": appsOwnedJSON}}).exec(t, "app", "list", "--agent", agent, "--json")
		if r.err != nil || len(*calls) != 1 {
			t.Fatalf("err=%v calls=%v", r.err, *calls)
		}
		filter, ok := (*calls)[0].Vars["filter"].(map[string]any)
		if !ok {
			t.Fatalf("missing filter object: %#v", (*calls)[0].Vars)
		}
		if filter["agentId"] != agent {
			t.Fatalf("nonblank ref altered: %v", filter)
		}
	}
	calls, r := (run1592{}).exec(t, "app", "list", "--agent", " \t ")
	if exitCodeFor(r.err) != 2 || len(*calls) != 0 || !strings.Contains(r.err.Error(), "--agent") {
		t.Fatalf("err=%v calls=%v", r.err, *calls)
	}
}

func TestAppNodesEmptyFilters1642(t *testing.T) {
	for _, tc := range []struct {
		flag, key string
		value     any
	}{
		{"prefix", "locPrefix", ""}, {"type", "nodeType", ""}, {"object-type", "objectType", ""}, {"tag", "tags", []any{""}},
	} {
		calls, r := (run1592{response: map[string]string{"AppNodesPage": emptyAppPage1642}}).exec(t, "app", "nodes", appNodesRef1626, "--"+tc.flag+"=", "--json")
		if r.err != nil || len(*calls) != 1 {
			t.Fatalf("%s err=%v calls=%v", tc.flag, r.err, *calls)
		}
		filter, ok := (*calls)[0].Vars["filter"].(map[string]any)
		if !ok {
			t.Fatalf("missing filter object: %#v", (*calls)[0].Vars)
		}
		if !reflect.DeepEqual(filter[tc.key], tc.value) {
			t.Fatalf("%s filter=%#v", tc.flag, filter)
		}
	}
	for _, flag := range []string{"created-by", "role", "updated-after", "updated-before"} {
		calls, r := (run1592{}).exec(t, "app", "nodes", appNodesRef1626, "--"+flag+"=")
		if exitCodeFor(r.err) != 2 || len(*calls) != 0 {
			t.Fatalf("%s err=%v calls=%v", flag, r.err, *calls)
		}
	}
}

func TestAppNodesPagingAndRefusals1642(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		calls, r := (run1592{}).exec(t, "app", "nodes", appNodesRef1626, "--offset", "2147483648")
		if exitCodeFor(r.err) != 2 || len(*calls) != 0 || !strings.Contains(r.err.Error(), "--offset") {
			t.Fatalf("err=%v calls=%v", r.err, *calls)
		}
	})
	t.Run("count only continuation", func(t *testing.T) {
		calls, r := (run1592{response: map[string]string{"AppNodesPage": emptyAppPage1642}}).exec(t, "app", "nodes", appNodesRef1626, "--limit", "0", "--offset", "7")
		if r.err != nil || len(*calls) != 1 {
			t.Fatalf("err=%v calls=%v", r.err, *calls)
		}
		if !strings.Contains(r.errOut, "total: 3; more: yes\n") || !strings.Contains(r.errOut, "--limit 20 --offset 7") {
			t.Fatalf("wrong count/hint: %q", r.errOut)
		}
	})
	t.Run("forbidden", func(t *testing.T) {
		calls, r := (run1592{response: map[string]string{"AppNodesPage": `{"errors":[{"message":"denied","extensions":{"code":"FORBIDDEN"}}]}`}}).exec(t, "app", "nodes", appNodesRef1626)
		if exitCodeFor(r.err) != 8 || r.out != "" || len(*calls) != 1 {
			t.Fatalf("err=%v out=%q calls=%v", r.err, r.out, *calls)
		}
	})
	t.Run("exact total", func(t *testing.T) {
		_, r := (run1592{response: map[string]string{"AppNodesPage": emptyAppPage1642}}).exec(t, "app", "nodes", appNodesRef1626, "--json")
		if r.err != nil {
			t.Fatal(r.err)
		}
		var page struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal([]byte(r.out), &page); err != nil || page.Total != 3 {
			t.Fatalf("total=%d err=%v out=%q", page.Total, err, r.out)
		}
	})
}

func TestAppNodesIncompleteIdentity1642(t *testing.T) {
	valid := map[string]any{"id": "n1", "memoryId": "m1", "loc": "alpha", "name": "first", "nodeType": "info"}
	for _, field := range []string{"id", "memoryId"} {
		for _, afterValid := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/afterValid=%t", field, afterValid), func(t *testing.T) {
				bad := map[string]any{"id": "n2", "memoryId": "m2", "loc": "beta", "name": "bad", "nodeType": "info"}
				bad[field] = ""
				rows := []any{bad}
				if afterValid {
					rows = []any{valid, bad}
				}
				b, err := json.Marshal(map[string]any{"data": map[string]any{"appNodesPage": map[string]any{"items": rows, "total": len(rows), "hasMore": false}}})
				if err != nil {
					t.Fatal(err)
				}
				calls, r := (run1592{response: map[string]string{"AppNodesPage": string(b)}}).exec(t, "app", "nodes", appNodesRef1626)
				if exitCodeFor(r.err) != 1 || r.out != "" || len(*calls) != 1 || !strings.Contains(r.err.Error(), "incomplete App node row") {
					t.Fatalf("err=%v out=%q calls=%v", r.err, r.out, *calls)
				}
			})
		}
	}
}
