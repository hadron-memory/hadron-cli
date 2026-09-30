package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#777 — `spec replace` in a DRAFT corpus goes through the governed spec
// door (hadron-server#1459): governed specs are searched, and the apply sends
// the preview's exact plan back, so nothing written can differ from what was
// previewed.

type doorCall struct {
	DryRun          bool    `json:"dryRun"`
	ExpectedPlan    *string `json:"expectedPlan"`
	OldText         string  `json:"oldText"`
	Regex           bool    `json:"regex"`
	WordBoundary    bool    `json:"wordBoundary"`
	MaxNodesChanged *int    `json:"maxNodesChanged"`
	NodeIds         []string
	MemoryRef       string `json:"memoryRef"`
}

func doorResult(dry bool, plan string) string {
	d := "false"
	if dry {
		d = "true"
	}
	return `{"data":{"searchReplaceInSpecNodes":{"nodesSelected":3,"nodesScanned":2,"nodesSkipped":1,"nodesChanged":1,` +
		`"totalReplacements":2,"dryRun":` + d + `,"plan":"` + plan + `",` +
		`"results":[{"nodeId":"id-cor:sec:040","loc":"cor:sec:040","replacements":2,"fields":[{"field":"content","matches":2}]}],` +
		`"skips":[{"nodeId":"id-cor:sec:040:01","loc":"cor:sec:040:01","reason":"CHANNEL_PROTECTED"}]}}}`
}

// draftReplaceServer answers the corpus-state read with `state` and records
// every door call; `apply` is the body for the non-dry call.
func draftReplaceServer(t *testing.T, state, apply string) (string, func() []doorCall, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []doorCall
	var ops []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Input json.RawMessage `json:"input"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		ops = append(ops, req.OperationName)
		mu.Unlock()
		var resp string
		switch req.OperationName {
		case "Memories":
			resp = memListJSON
		case "SpecCorpusState":
			resp = `{"data":{"memory":{"id":"mem1","urn":"hadronmemory.com:platform-specs","corpusState":"` + state + `"}}}`
		case "FindNodes":
			resp = governedCorpus
		case "SearchReplaceInSpecNodes":
			var c doorCall
			_ = json.Unmarshal(req.Variables.Input, &c)
			mu.Lock()
			calls = append(calls, c)
			mu.Unlock()
			if c.DryRun {
				resp = doorResult(true, "plan-1")
			} else {
				resp = apply
			}
		case "SearchReplaceInNodes":
			resp = noMatchReplaceResp(1)
		case "NodeBatch":
			resp = `{"data":{"nodeBatch":{"truncated":false,"omitted":[],"unavailable":[],"nodes":[]}}}`
		default:
			t.Errorf("unexpected operation %q", req.OperationName)
			resp = `{"errors":[{"message":"unexpected"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(translateFindNodes(req.OperationName, resp)))
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []doorCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]doorCall(nil), calls...)
	}, &ops
}

func TestSpecReplaceDraftAppliesTheExactPreviewedPlan(t *testing.T) {
	url, calls, ops := draftReplaceServer(t, "DRAFT", doorResult(false, ""))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "h-read-node", "hadron_get_node", "-m", specMem, "--yes", "--max-specs", "5", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("replace: %v\n%s", err, out.String())
	}
	c := calls()
	if len(c) != 2 || !c[0].DryRun || c[1].DryRun {
		t.Fatalf("want a dry run then an apply, got %+v", c)
	}
	if c[0].ExpectedPlan != nil {
		t.Errorf("the preview sends no plan: %+v", c[0])
	}
	if c[1].ExpectedPlan == nil || *c[1].ExpectedPlan != "plan-1" {
		t.Errorf("the apply must send the preview's exact plan: %+v", c[1])
	}
	// The door takes the RAW pattern and its own word-boundary flag, and
	// both calls must carry identical inputs or the plan can't match.
	for _, call := range c {
		if call.OldText != "h-read-node" || call.Regex || !call.WordBoundary || call.MaxNodesChanged == nil || *call.MaxNodesChanged != 5 {
			t.Errorf("unexpected door inputs: %+v", call)
		}
	}
	for _, op := range *ops {
		if op == "SearchReplaceInNodes" {
			t.Error("a draft corpus must not use the generic door")
		}
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("--json: %v (%q)", err, out.String())
	}
	if dto["door"] != "spec" || len(dto["skipped"].([]any)) != 1 || dto["totalReplacements"] != float64(2) {
		t.Errorf("unexpected report: %v", dto)
	}
}

// Anything that changed between preview and apply refuses the whole apply,
// with zero writes: exit 5, and the message says nothing was written.
func TestSpecReplaceDraftStalePlanExits5AndSaysNothingWasWritten(t *testing.T) {
	url, _, _ := draftReplaceServer(t, "DRAFT",
		`{"errors":[{"message":"The spec replacement preview is stale; run a new dry run.","extensions":{"code":"SEARCH_REPLACE_PLAN_STALE"}}]}`)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "h-read-node", "hadron_get_node", "-m", specMem, "--yes", "--server", url})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Conflict {
		t.Errorf("a stale plan should exit 5, got %d", got)
	}
	if err == nil || !strings.Contains(err.Error(), "NOTHING was written") {
		t.Errorf("the refusal must say nothing was written: %v", err)
	}
}

func TestSpecReplaceDraftDryRunIsOneCall(t *testing.T) {
	url, calls, _ := draftReplaceServer(t, "DRAFT", doorResult(false, ""))
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "h-read-node", "hadron_get_node", "-m", specMem, "--dry-run", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if c := calls(); len(c) != 1 || !c[0].DryRun {
		t.Errorf("a dry run is exactly one door preview, got %+v", c)
	}
	s := out.String()
	if !strings.Contains(s, "protected and were NOT searched") || !strings.Contains(s, "cor:sec:040:01") || strings.Contains(s, "are governed and were NOT searched") {
		t.Errorf("a draft names what the door skipped, and does not claim governed specs went unsearched:\n%s", s)
	}
}

// A minted corpus keeps the generic door, and says which one ran.
func TestSpecReplaceMintedKeepsTheGenericDoor(t *testing.T) {
	url, calls, ops := draftReplaceServer(t, "MINTED", "")
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "h-read-node", "hadron_get_node", "-m", specMem, "--dry-run", "--json", "--server", url})
	if err := root.Execute(); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(calls()) != 0 {
		t.Error("a minted corpus must not use the spec door")
	}
	var usedGeneric bool
	for _, op := range *ops {
		usedGeneric = usedGeneric || op == "SearchReplaceInNodes"
	}
	if !usedGeneric || !strings.Contains(out.String(), `"door": "generic"`) || !strings.Contains(out.String(), `"skipped": []`) {
		t.Errorf("want the generic door, reported as such: %s", out.String())
	}
}
