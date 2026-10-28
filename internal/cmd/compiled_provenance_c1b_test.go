package cmd

import (
	"encoding/json"
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
	if strings.Contains(c.Query, "outgoingEdges") || strings.Contains(c.Query, "content(") || strings.Contains(c.Query, "content\n") {
		t.Fatalf("requested body or live edges: %s", c.Query)
	}
}

func TestC1bCountsOnlyPreservesExplicitZero(t *testing.T) {
	response := strings.Replace(c1bPage, `"items":[`+strings.Split(strings.Split(c1bPage, `"items":[`)[1], `],"total"`)[0]+`]`, `"items":[]`, 1)
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
