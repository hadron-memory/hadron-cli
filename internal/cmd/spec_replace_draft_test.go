package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func draftReplaceResponse(dry bool, plan string, matches int) string {
	result := fmt.Sprintf(`"nodesSelected":1,"nodesScanned":1,"nodesSkipped":0,"nodesChanged":%d,"totalReplacements":%d,"dryRun":%t,"plan":%q,"skips":[],"results":[]`, matches, matches, dry, plan)
	if matches > 0 {
		result = fmt.Sprintf(`"nodesSelected":1,"nodesScanned":1,"nodesSkipped":0,"nodesChanged":1,"totalReplacements":%d,"dryRun":%t,"plan":%q,"skips":[],"results":[{"nodeId":"id-msg:010:02","loc":"msg:010:02","memoryId":"mem1","replacements":%d,"fields":[{"field":"CONTENT","matches":%d}]}]`, matches, dry, plan, matches, matches)
	}
	return `{"data":{"searchReplaceInSpecNodes":{` + result + `}}}`
}

// The draft door owns literal boundaries and exact-plan binding. The generic
// door must never receive this governed spec, and apply must carry the token
// returned by the immediately preceding preview.
func TestSpecReplaceDraftBindsApplyToPreview(t *testing.T) {
	var calls []struct {
		DryRun bool
		Input  map[string]any
	}
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		var resp string
		switch req.OperationName {
		case "SpecCorpusState":
			resp = draftStateJSON
		case "FindNodes":
			resp = `{"data":{"nodes":[` + specNodeListWithRole("msg:010:02", `["spec"]`) + `]}}`
		case "SearchReplaceInSpecNodes":
			var vars struct {
				Input map[string]any `json:"input"`
			}
			if err := json.Unmarshal(req.Variables, &vars); err != nil {
				t.Errorf("variables: %v", err)
				return
			}
			dry, _ := vars.Input["dryRun"].(bool)
			calls = append(calls, struct {
				DryRun bool
				Input  map[string]any
			}{dry, vars.Input})
			resp = draftReplaceResponse(dry, "plan-one", 2)
		case "NodeBatch":
			resp = specBatchResp("msg:010:02")
		default:
			t.Errorf("unexpected operation %q", req.OperationName)
			resp = `{"errors":[{"message":"unexpected operation"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(translateFindNodes(req.OperationName, resp)))
	}))
	t.Cleanup(gql.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "h-read-node", "hadron_get_node", "-m", specMem, "--yes", "--max-specs", "2", "--reason", "rename tool", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if len(calls) != 2 || !calls[0].DryRun || calls[1].DryRun {
		t.Fatalf("want preview then apply, got %+v", calls)
	}
	preview, apply := calls[0].Input, calls[1].Input
	if preview["oldText"] != "h-read-node" || preview["regex"] != false || preview["wordBoundary"] != true {
		t.Errorf("draft literal/boundary input: %v", preview)
	}
	_, previewHasPlan := preview["expectedPlan"]
	if previewHasPlan || preview["maxNodesChanged"] != float64(2) || apply["expectedPlan"] != "plan-one" {
		t.Errorf("exact plan was not forwarded: preview=%v apply=%v", preview, apply)
	}
	if preview["memoryRef"] != specMem || apply["memoryRef"] != specMem || apply["maxNodesChanged"] != float64(2) || apply["reason"] != "rename tool" {
		t.Errorf("draft scope/apply input: preview=%v apply=%v", preview, apply)
	}
	fields, _ := preview["fields"].([]any)
	if fmt.Sprint(fields) != "[content abstract]" {
		t.Errorf("draft fields = %v", fields)
	}
	var dto struct {
		SpecsChanged int    `json:"specsChanged"`
		SpecsScanned int    `json:"specsScanned"`
		Plan         string `json:"plan"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil || dto.SpecsChanged != 1 || dto.SpecsScanned != 1 {
		t.Errorf("report = %+v, err %v: %s", dto, err, out.String())
	}
	for _, want := range []string{`"specsSkipped": 0`, `"skips": []`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("draft JSON must preserve known empty shape %q: %s", want, out.String())
		}
	}
}

func TestSpecReplaceDraftReportsProtectedNoMatch(t *testing.T) {
	gql := fakeGraphQL(t, map[string]string{
		"SpecCorpusState":          draftStateJSON,
		"FindNodes":                `{"data":{"nodes":[` + specNodeListWithRole("msg:010:02", `["spec"]`) + `]}}`,
		"SearchReplaceInSpecNodes": `{"data":{"searchReplaceInSpecNodes":{"nodesSelected":1,"nodesScanned":0,"nodesSkipped":1,"nodesChanged":0,"totalReplacements":0,"dryRun":true,"plan":"p","results":[],"skips":[{"nodeId":"id-msg:010:02","loc":"msg:010:02","reason":"OTHER_GOVERNED_KIND","governedKinds":["review"]}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "x", "y", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1 of 1 spec(s) in scope were protected", "msg:010:02: OTHER_GOVERNED_KIND (review)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q from %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "governed and were NOT searched") {
		t.Errorf("draft must not use minted-only skip explanation: %s", out.String())
	}
}

func TestSpecReplaceDraftRefusesApplyWithoutPlan(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SpecCorpusState":          draftStateJSON,
		"FindNodes":                `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`,
		"SearchReplaceInSpecNodes": draftReplaceResponse(true, "", 1),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "x", "y", "-m", specMem, "--yes", "--server", gql.URL})
	if got := exitCodeFor(root.Execute()); got != exitcode.Error {
		t.Errorf("missing plan should refuse before apply, exit=%d", got)
	}
	if _, ok := captured["SearchReplaceInNodes"]; ok {
		t.Error("draft must not fall back to generic mutation")
	}
}

func TestSpecReplaceDraftDoesNotDowngradeToGenericDoor(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SpecCorpusState":          draftStateJSON,
		"FindNodes":                `{"data":{"nodes":[` + specNodeListWithRole("msg:010:02", `["spec"]`) + `]}}`,
		"SearchReplaceInSpecNodes": `{"errors":[{"message":"Cannot query field \"searchReplaceInSpecNodes\" on type \"Mutation\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "x", "y", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Error("a draft server without the governed door must fail")
	}
	if _, ok := captured["SearchReplaceInNodes"]; ok {
		t.Error("a draft must never fall back to generic replacement")
	}
	var vars struct {
		Input map[string]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(captured["SearchReplaceInSpecNodes"], &vars); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"expectedPlan", "maxNodesChanged", "reason"} {
		if _, present := vars.Input[key]; present {
			t.Errorf("unset optional %s must be omitted, got %s", key, captured["SearchReplaceInSpecNodes"])
		}
	}
}

func TestSpecReplaceMintedRetainsGenericDoor(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SpecCorpusState":      mintedStateJSON,
		"FindNodes":            `{"data":{"nodes":[` + specNodeListWithRole("msg:010:02", `["spec"]`) + `]}}`,
		"SearchReplaceInNodes": noMatchReplaceResp(0),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "x", "y", "-m", specMem, "--dry-run", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, ok := captured["SearchReplaceInSpecNodes"]; ok {
		t.Error("minted replacement must retain the generic protected door")
	}
	if _, ok := captured["SearchReplaceInNodes"]; !ok {
		t.Error("minted replacement did not call the generic door")
	}
}

func TestSpecReplaceDraftLimitRefusesBeforeApply(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"SpecCorpusState":          draftStateJSON,
		"FindNodes":                `{"data":{"nodes":[` + specNodeList("msg:010:02", `["spec"]`) + `]}}`,
		"SearchReplaceInSpecNodes": strings.Replace(draftReplaceResponse(true, "plan-two", 2), `"nodesChanged":1`, `"nodesChanged":2`, 1),
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"spec", "replace", "x", "y", "-m", specMem, "--yes", "--max-specs", "1", "--server", gql.URL})
	err := root.Execute()
	if got := exitCodeFor(err); got != exitcode.Usage || !strings.Contains(err.Error(), "SEARCH_REPLACE_MAX_NODES_CHANGED") {
		t.Errorf("over-limit preview must refuse before apply with typed usage, exit=%d err=%v", got, err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(captured["SearchReplaceInSpecNodes"], &vars); err != nil {
		t.Fatal(err)
	}
	if vars.Input["dryRun"] != true || vars.Input["maxNodesChanged"] != float64(1) {
		t.Errorf("only capped preview should be sent: %v", vars.Input)
	}
}
