package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func c3Responses(warnings string) map[string]string {
	return map[string]string{
		"CreateNode":          `{"data":{"createNode":` + nodeJSON + `}}`,
		"GetNodeLintWarnings": `{"data":{"node":{"lintWarnings":` + warnings + `}}}`,
	}
}

func TestC3cOptInNativeReadAndNullableDTO(t *testing.T) {
	for _, page := range []string{`null`, `{"items":[],"total":0,"nextCursor":null}`, `{"items":[{"kind":"BROKEN_INCLUDE","position":3,"text":"server wording","targetUrn":null,"pinnedRevision":null,"newerRevision":null,"overriddenNodeLoc":null,"workerField":null}],"total":1,"nextCursor":null}`} {
		t.Run(page, func(t *testing.T) {
			calls, out, err := runTeamTask(t, c3Responses(page), "node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--lint-warnings", "--json")
			if err != nil {
				t.Fatal(err)
			}
			c2Mutation(t, calls, "CreateNode")
			c2Mutation(t, calls, "GetNodeLintWarnings")
			var dto map[string]json.RawMessage
			if err := json.Unmarshal([]byte(out), &dto); err != nil {
				t.Fatal(err)
			}
			if len(dto["lintWarnings"]) == 0 {
				t.Fatalf("opt-in must include nullable page: %s", out)
			}
			if page == "null" && string(dto["lintWarnings"]) != "null" {
				t.Fatalf("unavailable became empty: %s", out)
			}
			if strings.Contains(page, "server wording") && !strings.Contains(out, "server wording") {
				t.Fatalf("server text lost: %s", out)
			}
		})
	}
}

func TestC3cDefaultSendsNoNewOperationOrJSONField(t *testing.T) {
	calls, out, err := runTeamTask(t, c3Responses("null"), "node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Op != "CreateNode" || strings.Contains(out, `"lintWarnings"`) {
		t.Fatalf("default changed calls=%v output=%s", *calls, out)
	}
}

func TestC3cBodyOnlyAndDryRunRefuseBeforeRequests(t *testing.T) {
	for _, args := range [][]string{{"node", "get", "hrn:node:acme.com:kb:x", "--body-only", "--lint-warnings"}, {"node", "update", "hrn:node:acme.com:kb:x", "--name", "X", "--dry-run", "--lint-warnings"}} {
		calls, _, err := runTeamTask(t, nil, args...)
		if exitCodeFor(err) != 2 || len(*calls) != 0 {
			t.Fatalf("incompatible mode made requests: err=%v calls=%v", err, *calls)
		}
	}
}

func TestC3cNullItemsAreUnavailableNotClean(t *testing.T) {
	calls, out, err := runTeamTask(t, c3Responses(`{"items":null,"total":0,"nextCursor":null}`), "node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--lint-warnings", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || !strings.Contains(out, `"lintWarnings": null`) {
		t.Fatalf("malformed null items forged clean: %v %s", *calls, out)
	}
}

const c3Warning1 = `{"kind":"BROKEN_INCLUDE","position":3,"text":"first server wording","targetUrn":null,"pinnedRevision":null,"newerRevision":null,"overriddenNodeLoc":null,"workerField":null}`
const c3Warning2 = `{"kind":"PINNED_NEWER_APPROVED","position":8,"text":"second server wording","targetUrn":"hrn:node:acme.com:kb:part","pinnedRevision":2,"newerRevision":4,"overriddenNodeLoc":null,"workerField":null}`

func c3WarningEnvelope(page string) string { return `{"data":{"node":{"lintWarnings":` + page + `}}}` }

func TestC3cPagesAreCompleteOrUnavailableWithoutRetryingWrite(t *testing.T) {
	first := `{"items":[` + c3Warning1 + `],"total":2,"nextCursor":"next"}`
	for _, tc := range []struct {
		name, second string
		complete     bool
	}{
		{"complete", c3WarningEnvelope(`{"items":[` + c3Warning2 + `],"total":2,"nextCursor":null}`), true},
		{"failed", gqlErrorJSON("FORBIDDEN"), false},
		{"null", c3WarningEnvelope("null"), false},
		{"changed total", c3WarningEnvelope(`{"items":[],"total":3,"nextCursor":null}`), false},
		{"repeated cursor", c3WarningEnvelope(`{"items":[` + c3Warning2 + `],"total":2,"nextCursor":"next"}`), false},
		{"empty advancing page", c3WarningEnvelope(`{"items":[],"total":2,"nextCursor":"again"}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writeTeamBinding(t)
			responses := c3Responses(first)
			reads := 0
			writes := 0
			srv, calls := attnServerHook(t, responses, func(op string) {
				if op == "CreateNode" {
					writes++
				}
				if op == "GetNodeLintWarnings" {
					reads++
					if reads > 1 {
						responses[op] = tc.second
					}
				}
			})
			setTeamBindingServer(t, srv.URL)
			f, out := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"--server", srv.URL, "node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--lint-warnings", "--json"})
			if err := root.Execute(); err != nil {
				t.Fatalf("advisory read changed successful write: %v", err)
			}
			if writes != 1 || reads != 2 || len(*calls) != 3 {
				t.Fatalf("retry or truncated page: %v", *calls)
			}
			if (*calls)[1].Vars["ref"] != "n1" || (*calls)[2].Vars["ref"] != "n1" || (*calls)[2].Vars["after"] != "next" {
				t.Fatalf("wrong returned-node/cursor selection: %v", *calls)
			}
			if tc.complete {
				for _, want := range []string{"first server wording", "second server wording", `"total": 2`, `"nextCursor": null`, `"pinnedRevision": 2`, `"newerRevision": 4`} {
					if !strings.Contains(out.String(), want) {
						t.Fatalf("incomplete warning page: %s", out.String())
					}
				}
			} else if !strings.Contains(out.String(), `"lintWarnings": null`) || strings.Contains(out.String(), "first server wording") {
				t.Fatalf("partial/advisory failure forged clean: %s", out.String())
			}
		})
	}
}

func TestC3cUnsuccessfulWriteDoesNotReadLintAndFalseFlagIsOff(t *testing.T) {
	calls, _, err := runTeamTask(t, map[string]string{"CreateNode": gqlErrorJSON("FORBIDDEN")}, "node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--lint-warnings", "--json")
	if exitCodeFor(err) != 8 || len(*calls) != 1 {
		t.Fatalf("failed write read lint or changed refusal: %v %v", err, *calls)
	}
	calls, out, err := runTeamTask(t, c3Responses("null"), "node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--lint-warnings=false", "--json")
	if err != nil || len(*calls) != 1 || strings.Contains(out, `"lintWarnings"`) {
		t.Fatalf("explicit false is not opt-in: %v %v %s", err, *calls, out)
	}
}

func TestC3cNativeInstallCreateKeepsSelectorAndAddsOnlyRead(t *testing.T) {
	calls, _, err := runTeamTask(t, c3Responses(`{"items":[],"total":0,"nextCursor":null}`), "node", "add", "--install", c7Install, "--loc", "x", "--name", "X", "--lint-warnings", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := c2Mutation(t, calls, "CreateNode")
	input := c.Vars["input"].(map[string]any)
	if input["install"] == nil || input["memoryId"] != nil || len(*calls) != 2 || (*calls)[1].Op != "GetNodeLintWarnings" {
		t.Fatalf("native selector dropped or extra write: %v", *calls)
	}
}

func TestC3cApprovalReceiptCurrentReadIsAdvisory(t *testing.T) {
	responses := c3Responses("null")
	responses["ResolveUrn"] = `{"data":{"resolveUrn":{"id":"n1","kind":"node","memoryId":"mem1"}}}`
	responses["ApproveNode"] = `{"data":{"approveNode":{"nodeId":"n1","urn":"hrn:node:acme.com:kb:x","loc":"x","approval":{"revision":4,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":{"handle":"ada","urn":"hrn:user:ada"},"approvedByInfo":null,"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}}`
	calls, out, err := runTeamTask(t, responses, "node", "approve", "hrn:node:acme.com:kb:x", "--revision", "4", "--lint-warnings", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 || (*calls)[1].Op != "ApproveNode" || (*calls)[2].Op != "GetNodeLintWarnings" || (*calls)[2].Vars["ref"] != "n1" || !strings.Contains(out, `"revision": 4`) || !strings.Contains(out, `"lintWarnings": null`) {
		t.Fatalf("approval changed or not advisory: %v %s", *calls, out)
	}
}

func TestC3cGetIsOptInAndHumanTextIsSafe(t *testing.T) {
	responses := bodyOnlyStubs("stored source")
	responses["GetNodeLintWarnings"] = c3WarningEnvelope(`{"items":[{"kind":"BROKEN_INCLUDE","position":3,"text":"line1\nline2\u001b[31m","targetUrn":null,"pinnedRevision":null,"newerRevision":null,"overriddenNodeLoc":null,"workerField":null}],"total":1,"nextCursor":null}`)
	out, _, captured, err := runNode(t, responses, "", "node", "get", testNodeURN, "--raw", "--lint-warnings", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := captured["GetNodeLintWarnings"]; !ok || !strings.Contains(out, `line1\nline2\u001b[31m`) {
		t.Fatalf("get warning missing or JSON changed: %s %v", out, captured)
	}
	out, _, captured, err = runNode(t, responses, "", "node", "get", testNodeURN, "--raw", "--lint-warnings=false", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := captured["GetNodeLintWarnings"]; ok || strings.Contains(out, `"lintWarnings"`) {
		t.Fatalf("get default changed: %s %v", out, captured)
	}
	_, human, err := runTeamTask(t, c3Responses(`{"items":[{"kind":"BROKEN_INCLUDE","position":3,"text":"line1\nline2\u001b[31m","targetUrn":null,"pinnedRevision":null,"newerRevision":null,"overriddenNodeLoc":null,"workerField":null}],"total":1,"nextCursor":null}`), "node", "add", "-m", "mem1", "--loc", "x", "--name", "X", "--lint-warnings")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, "\x1b") || strings.Contains(human, "line1\nline2") || !strings.Contains(human, "line1 line2") {
		t.Fatalf("unsafe human warning: %q", human)
	}
}
