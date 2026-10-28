package cmd

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

const c1bRef = "hrn:node:acme.com:iris-system:boot:compiled"
const c1bPage = `{"nodeId":"compiled1","revNo":7,"contentHash":"recordedhash","version":1,"pruned":false,"items":[{"order":0,"depth":0,"parent":null,"kind":"source","nodeId":"source1","revNo":3,"hash":"sourcehash","replacesNodeId":null,"position":null,"selection":{"rev":null,"approved":true,"signed":false},"pruned":null}],"total":1,"hasMore":false}`

func TestC1bExactRevisionReadKeepsMaskedSourceStatus(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + c1bPage + `}}`}, "node", "provenance", c1bRef, "--revision", "7", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "CompiledPromptProvenance")
	if c.Vars["nodeRef"] != c1bRef || c.Vars["revNo"] != float64(7) {
		t.Fatalf("wrong exact revision: %s", c.RawVars)
	}
	if len(*calls) != 1 {
		t.Fatalf("provenance must not resolve live edges or source bodies: %v", *calls)
	}
	var dto struct {
		Items       []map[string]json.RawMessage
		RevNo       int
		ContentHash string
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.RevNo != 7 || dto.ContentHash != "recordedhash" || len(dto.Items) != 1 || string(dto.Items[0]["pruned"]) != "null" {
		t.Fatalf("immutable/masked data changed: %s", out)
	}
	if strings.Contains(c.Query, "outgoingEdges") || regexp.MustCompile(`\bcontent\b`).MatchString(c.Query) {
		t.Fatalf("requested body or live edges: %s", c.Query)
	}
}

func TestC1bCountsOnlyPreservesExplicitZero(t *testing.T) {
	var page map[string]json.RawMessage
	if err := json.Unmarshal([]byte(c1bPage), &page); err != nil {
		t.Fatal(err)
	}
	page["items"] = json.RawMessage(`[]`)
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	response := string(raw)
	calls, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + response + `}}`}, "node", "provenance", c1bRef, "--revision", "7", "--limit", "0", "--offset", "2", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "CompiledPromptProvenance")
	if c.Vars["limit"] != float64(0) || c.Vars["offset"] != float64(2) || !strings.Contains(out, `"items": []`) {
		t.Fatalf("counts-only altered: %s %s", c.RawVars, out)
	}
}

func TestC1bUnavailableDoesNotBecomeEmptyProvenance(t *testing.T) {
	calls, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":null}}`}, "node", "provenance", c1bRef, "--revision", "7", "--json")
	if exitCodeFor(err) != 4 || strings.Contains(out, `"items"`) {
		t.Fatalf("unavailable forged success: %v %s", err, out)
	}
	c2Mutation(t, calls, "CompiledPromptProvenance")
}

func TestC1bInvalidInputsMakeNoRequests(t *testing.T) {
	for _, flags := range [][]string{{}, {"--revision", "0"}, {"--revision", "7", "--limit", "101"}, {"--revision", "7", "--offset", "-1"}, {"--revision", "7", "--limit", "0", "--all"}} {
		calls, _, err := runTeamTask(t, nil, append([]string{"node", "provenance", c1bRef}, flags...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("bad flags reached provider: %v %v", err, *calls)
		}
	}
}

func c1bPageAt(t *testing.T, order int, more bool) string {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal([]byte(c1bPage), &p); err != nil {
		t.Fatal(err)
	}
	p["total"] = 2
	p["hasMore"] = more
	item := p["items"].([]any)[0].(map[string]any)
	item["order"] = order
	if order > 0 {
		item["nodeId"] = "source2"
		item["depth"] = 1
		item["parent"] = 0
		item["kind"] = "include"
		item["position"] = map[string]int{"line": 1, "column": 8}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func c1bReadPages(t *testing.T, pages []string) (*[]attnCall, string, error) {
	t.Helper()
	writeTeamBinding(t)
	responses := map[string]string{}
	n := 0
	srv, calls := attnServerHook(t, responses, func(op string) {
		if op != "CompiledPromptProvenance" {
			return
		}
		if n >= len(pages) {
			t.Errorf("unexpected extra page request %d", n)
			responses[op] = `{"data":{"compiledPromptProvenance":null}}`
			return
		}
		if pages[n] == "integrity error" {
			responses[op] = `{"errors":[{"message":"provenance record is corrupt","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`
		} else {
			responses[op] = `{"data":{"compiledPromptProvenance":` + pages[n] + `}}`
		}
		n++
	})
	setTeamBindingServer(t, srv.URL)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "node", "provenance", c1bRef, "--revision", "7", "--all", "--limit", "1", "--json"})
	err := root.Execute()
	return calls, out.String(), err
}

func TestC1bAllReadsEveryOffsetBeforePrinting(t *testing.T) {
	calls, out, err := c1bReadPages(t, []string{c1bPageAt(t, 0, true), c1bPageAt(t, 1, false)})
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].Vars["offset"] != float64(0) || (*calls)[1].Vars["offset"] != float64(1) {
		t.Fatalf("wrong paging: %v", *calls)
	}
	var dto struct {
		Items   []any
		HasMore bool
		Total   int
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 2 || dto.Total != 2 || dto.HasMore {
		t.Fatalf("partial output: %s", out)
	}
}

func TestC1bAllFailureOrChangedPageNeverPrintsPartialRecord(t *testing.T) {
	for name, tc := range map[string]struct {
		page string
		code int
	}{
		"provider integrity error": {"integrity error", 1},
		"unavailable":              {"null", 4},
		"changed hash":             {strings.Replace(c1bPageAt(t, 1, false), "recordedhash", "newhash", 1), 5},
		"changed compiled prune":   {strings.Replace(c1bPageAt(t, 1, false), `"pruned":false`, `"pruned":true`, 1), 5},
		"duplicate occurrence":     {c1bPageAt(t, 0, false), 1},
		"empty stalled page":       {`{"nodeId":"compiled1","revNo":7,"contentHash":"recordedhash","version":1,"pruned":false,"items":[],"total":2,"hasMore":true}`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			calls, out, err := c1bReadPages(t, []string{c1bPageAt(t, 0, true), tc.page})
			if exitCodeFor(err) != tc.code || out != "" || len(*calls) != 2 {
				t.Fatalf("partial/misclassified: err=%v output=%s calls=%v", err, out, *calls)
			}
		})
	}
}

func TestC1bSourcePrunedKeepsAllThreeStates(t *testing.T) {
	for _, status := range []string{"null", "true", "false"} {
		t.Run(status, func(t *testing.T) {
			page := strings.Replace(c1bPage, `"pruned":null`, `"pruned":`+status, 1)
			_, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + page + `}}`}, "node", "provenance", c1bRef, "--revision", "7", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var dto struct{ Items []map[string]json.RawMessage }
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto.Items) != 1 || string(dto.Items[0]["pruned"]) != status {
				t.Fatalf("source status collapsed: %s", out)
			}
		})
	}
}
