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
