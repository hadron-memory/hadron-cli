package cmd

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
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
	return c1bReadPagesLimit(t, pages, 1)
}

func c1bReadPagesLimit(t *testing.T, pages []string, limit int) (*[]attnCall, string, error) {
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
	root.SetArgs([]string{"--server", srv.URL, "node", "provenance", c1bRef, "--revision", "7", "--all", "--limit", strconv.Itoa(limit), "--json"})
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

// Source concealment is independent of compiled pruning: never fill a null
// source status from the readable compiled record's definite pruning state.
func TestC1bHumanMaskedSourceDoesNotInferFromCompiledPruning(t *testing.T) {
	for _, compiled := range []string{"false", "true"} {
		page := strings.Replace(c1bPage, `"pruned":false`, `"pruned":`+compiled, 1)
		calls, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + page + `}}`}, "node", "provenance", c1bRef, "--revision", "7")
		if err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || !regexp.MustCompile(`(?m)^.*source1.*not visible`).MatchString(out) {
			t.Fatalf("masked status inferred or enriched: %s %v", out, *calls)
		}
	}
}

func TestC1bRejectsInvalidPageShapes(t *testing.T) {
	for name, page := range map[string]string{
		"items exceed total": strings.Replace(c1bPage, `"total":1`, `"total":0`, 1),
		"missing selection":  strings.Replace(c1bPage, `"selection":{"rev":null,"approved":true,"signed":false}`, `"selection":null`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + page + `}}`}, "node", "provenance", c1bRef, "--revision", "7", "--json")
			if exitCodeFor(err) != 1 || out != "" {
				t.Fatalf("invalid page accepted: %v %s", err, out)
			}
		})
	}
}

func TestC1bAdditionalInputBoundaries(t *testing.T) {
	for _, flags := range [][]string{
		{"--revision", "7", "--all", "--offset", "0"},
		{"--revision", "2147483648"},
		{"--revision", "7", "--offset", "2147483648"},
	} {
		calls, _, err := runTeamTask(t, nil, append([]string{"node", "provenance", c1bRef}, flags...)...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("bad boundary reached provider: %v %v", err, *calls)
		}
	}
}

func TestC1bHumanSourceStatusesAndJSONOnlyDetailHelp(t *testing.T) {
	for value, word := range map[string]string{"false": "retained", "true": "pruned"} {
		page := strings.Replace(c1bPage, `"pruned":null`, `"pruned":`+value, 1)
		_, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + page + `}}`}, "node", "provenance", c1bRef, "--revision", "7")
		if err != nil || !regexp.MustCompile(`(?m)^.*source1.*`+word).MatchString(out) {
			t.Fatalf("wrong human source status: %v %s", err, out)
		}
	}
	_, out, err := runTeamTask(t, nil, "node", "provenance", "--help")
	if err != nil || !strings.Contains(out, "--json includes the full recorded occurrence") {
		t.Fatalf("JSON-only detail not disclosed: %v %s", err, out)
	}
}

func TestC1bBareLocRequiresMemoryAndNormalizesWithoutLookup(t *testing.T) {
	calls, _, err := runTeamTask(t, nil, "node", "provenance", "boot:compiled", "--revision", "7")
	if exitCodeFor(err) != 2 || len(*calls) != 0 {
		t.Fatalf("bare loc escaped scope validation: %v %v", err, *calls)
	}
	calls, _, err = runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + c1bPage + `}}`}, "node", "provenance", "boot:compiled", "-m", "acme.com:iris-system", "--revision", "7", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || c2Mutation(t, calls, "CompiledPromptProvenance").Vars["nodeRef"] != c1bRef {
		t.Fatalf("wrong normalization or live lookup: %v", *calls)
	}
}

func TestC1bCountAndPagingEdges(t *testing.T) {
	t.Run("items exceed limit", func(t *testing.T) {
		_, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + c1bPage + `}}`}, "node", "provenance", c1bRef, "--revision", "7", "--limit", "0", "--json")
		if exitCodeFor(err) != 1 || out != "" {
			t.Fatalf("over-limit items accepted: %v %s", err, out)
		}
	})
	t.Run("short final total", func(t *testing.T) {
		_, out, err := c1bReadPages(t, []string{c1bPageAt(t, 0, false)})
		if exitCodeFor(err) != 1 || out != "" {
			t.Fatalf("incomplete final page accepted: %v %s", err, out)
		}
	})
	t.Run("counts with more", func(t *testing.T) {
		page := strings.Replace(c1bPage, `"items":[`, `"items":[`, 1)
		var record map[string]any
		if err := json.Unmarshal([]byte(page), &record); err != nil {
			t.Fatal(err)
		}
		record["items"] = []any{}
		record["hasMore"] = true
		raw, _ := json.Marshal(record)
		calls, out, err := runTeamTask(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + string(raw) + `}}`}, "node", "provenance", c1bRef, "--revision", "7", "--limit", "0", "--json")
		var dto struct {
			Items   []any
			HasMore bool
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(out), &dto); err != nil {
			t.Fatal(err)
		}
		if len(*calls) != 1 || dto.Items == nil || len(dto.Items) != 0 || !dto.HasMore {
			t.Fatalf("counts altered: %s", out)
		}
	})
	t.Run("next offset hint", func(t *testing.T) {
		writeTeamBinding(t)
		srv, _ := attnServer(t, map[string]string{"CompiledPromptProvenance": `{"data":{"compiledPromptProvenance":` + c1bPageAt(t, 0, true) + `}}`})
		setTeamBindingServer(t, srv.URL)
		f, _ := testFactory(t)
		var stderr bytes.Buffer
		f.IOStreams.ErrOut = &stderr
		root := NewRootCmd(f)
		root.SetArgs([]string{"--server", srv.URL, "node", "provenance", c1bRef, "--revision", "7", "--limit", "1"})
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stderr.String(), "next: --offset 1") {
			t.Fatalf("missing recovery hint: %s", stderr.String())
		}
	})
}

// #1169: pages can contain several occurrences. The offset advances by the
// returned length, not by one or by the requested page limit.
func c1bOccurrencesPage(t *testing.T, first, count, total int, more bool) string {
	t.Helper()
	var page map[string]json.RawMessage
	if err := json.Unmarshal([]byte(c1bPage), &page); err != nil {
		t.Fatal(err)
	}
	items := make([]map[string]any, 0, count)
	for i := first; i < first+count; i++ {
		var itemPage map[string]any
		if err := json.Unmarshal([]byte(c1bPageAt(t, i, false)), &itemPage); err != nil {
			t.Fatal(err)
		}
		item := itemPage["items"].([]any)[0].(map[string]any)
		item["nodeId"] = "source" + strconv.Itoa(i+1)
		items = append(items, item)
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	page["items"] = raw
	page["total"] = json.RawMessage(strconv.Itoa(total))
	page["hasMore"] = json.RawMessage(strconv.FormatBool(more))
	raw, err = json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestC1bMultiItemOffsetAdvance1169(t *testing.T) {
	pages := []string{c1bOccurrencesPage(t, 0, 2, 5, true), c1bOccurrencesPage(t, 2, 2, 5, true), c1bOccurrencesPage(t, 4, 1, 5, false)}
	calls, out, err := c1bReadPagesLimit(t, pages, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 {
		t.Fatalf("calls=%v", *calls)
	}
	for i, want := range []float64{0, 2, 4} {
		c := (*calls)[i]
		if c.Op != "CompiledPromptProvenance" || c.Vars["offset"] != want || c.Vars["limit"] != float64(3) {
			t.Fatalf("call%d=%+v", i, c)
		}
	}
	var dto struct {
		Items []struct {
			Order  int
			NodeID string
		}
		Total, Limit, Offset int
		HasMore              bool
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 5 || dto.Total != 5 || dto.Limit != 3 || dto.Offset != 0 || dto.HasMore {
		t.Fatalf("partial/altered result: %s", out)
	}
	for i, item := range dto.Items {
		if item.Order != i || item.NodeID != "source"+strconv.Itoa(i+1) {
			t.Fatalf("occurrence%d=%+v", i, item)
		}
	}
}

func TestC1bShrinkingTotalRefusesBeforePartialOutput1169(t *testing.T) {
	// A changed total is rejected by the identity/count guard, before the later
	// over-total pagination guard. Keep that exit-5 distinction explicit.
	pages := []string{c1bOccurrencesPage(t, 0, 2, 5, true), c1bOccurrencesPage(t, 2, 1, 3, false)}
	calls, out, err := c1bReadPagesLimit(t, pages, 3)
	if exitCodeFor(err) != 5 || !strings.Contains(err.Error(), "count or compiled pruning state changed") || out != "" || len(*calls) != 2 {
		t.Fatalf("err=%v out=%s calls=%v", err, out, *calls)
	}
}

func TestC1bExhaustedTotalStillHasMoreRefuses1169(t *testing.T) {
	// Stable total bypasses the changed-count guard and actually reaches the
	// over-total guard: offset2 + two returned occurrences == total4, hasMore.
	pages := []string{c1bOccurrencesPage(t, 0, 2, 4, true), c1bOccurrencesPage(t, 2, 2, 4, true)}
	calls, out, err := c1bReadPagesLimit(t, pages, 3)
	if exitCodeFor(err) != 1 || !strings.Contains(err.Error(), "pagination did not advance consistently") || out != "" || len(*calls) != 2 {
		t.Fatalf("err=%v out=%s calls=%v", err, out, *calls)
	}
}
