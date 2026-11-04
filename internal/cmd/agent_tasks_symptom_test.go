package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- cli#1094: `agent tasks list --symptom` (hadron-server#2421) ----------
//
// symptom/limit ride the existing agentTaskList operation. The server owns the
// limit rules (default 3, clamp to 3, below 1 is BAD_USER_INPUT), so the CLI
// sends --limit as given and never clamps or validates it (cor:api:240:01).

const symptomRowsJSON = `{"data":{"agentTaskList":[
	{"node":` + listedNodeJSON + `,"trigger":"tasks:agent","matchedBy":"BOTH"},
	{"node":` + listedOwnNodeJSON + `,"trigger":null,"matchedBy":"KEYWORD"}]}}`

// runTasksList runs `agent tasks list` and returns stdout, the captured
// request variables and the error.
func runTasksList(t *testing.T, resp string, extra ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"AgentTaskIndexList": resp, "AgentTaskSymptomSearch": resp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"agent", "tasks", "list", "hrn:agent:acme.com:support", "--server", gql.URL}, extra...))
	err := root.Execute()
	return out.String(), captured, err
}

func taskListVars(t *testing.T, captured map[string]json.RawMessage) map[string]any {
	t.Helper()
	raw, ok := captured["AgentTaskSymptomSearch"]
	if !ok {
		raw = captured["AgentTaskIndexList"]
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("variables: %v (%s)", err, raw)
	}
	return vars
}

func TestAgentTasksListSymptomSendsVariables(t *testing.T) {
	_, captured, err := runTasksList(t, symptomRowsJSON, "--symptom", "the build is red", "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := taskListVars(t, captured)
	if vars["symptom"] != "the build is red" || vars["limit"] != float64(2) {
		t.Fatalf("symptom/limit must ride the request: %v", vars)
	}
}

func TestAgentTasksListNoSymptomLeavesRequestUnchanged(t *testing.T) {
	_, plain, err := runTasksList(t, symptomRowsJSON)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// --limit alone is IGNORED (the server returns before reading it): the
	// request stays byte-identical to the plain list.
	_, limited, err := runTasksList(t, symptomRowsJSON, "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := `{"ref":"hrn:agent:acme.com:support"}`
	for name, got := range map[string]json.RawMessage{"no flags": plain["AgentTaskIndexList"], "--limit only": limited["AgentTaskIndexList"]} {
		if _, searched := plain["AgentTaskSymptomSearch"]; searched {
			t.Errorf("%s: the plain list must not use the symptom document", name)
		}
		if string(got) != want {
			t.Errorf("%s: variables = %s, want %s", name, got, want)
		}
	}
}

// The limit is the server's to judge: 0 and 99 go out untouched.
func TestAgentTasksListLimitSentAsGiven(t *testing.T) {
	for _, limit := range []string{"0", "99"} {
		_, captured, err := runTasksList(t, symptomRowsJSON, "--symptom", "x", "--limit", limit)
		if err != nil {
			t.Fatalf("--limit %s: %v", limit, err)
		}
		want := map[string]float64{"0": 0, "99": 99}[limit]
		if got, ok := taskListVars(t, captured)["limit"]; !ok || got != want {
			t.Errorf("--limit %s: limit variable = %v (present=%v)", limit, got, ok)
		}
	}
	// No --limit: the variable is omitted so the server's default applies.
	_, captured, _ := runTasksList(t, symptomRowsJSON, "--symptom", "x")
	if _, ok := taskListVars(t, captured)["limit"]; ok {
		t.Errorf("an unset --limit must be omitted: %s", captured["AgentTaskSymptomSearch"])
	}
}

func TestAgentTasksListSymptomTextShowsMatchedBy(t *testing.T) {
	out, _, err := runTasksList(t, symptomRowsJSON, "--symptom", "x")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"MATCHED BY", "BOTH", "KEYWORD", "hrn:node:acme.com:base-mem:tasks:onboarding", "hrn:agent:acme.com:base", "hrn:node:acme.com:support-tasks:escalate"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should carry %q:\n%s", want, out)
		}
	}
}

func TestAgentTasksListWithoutSymptomHasNoMatchedByColumn(t *testing.T) {
	// A server answering matchedBy anyway must not change the plain output.
	out, _, err := runTasksList(t, symptomRowsJSON)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "MATCHED BY") || strings.Contains(out, "BOTH") {
		t.Errorf("the plain list is unchanged:\n%s", out)
	}
	jsonOut, _, _ := runTasksList(t, symptomRowsJSON, "--json")
	if strings.Contains(jsonOut, "matchedBy") {
		t.Errorf("plain --json must not gain matchedBy:\n%s", jsonOut)
	}
}

func TestAgentTasksListSymptomJSONKeepsMatchedBy(t *testing.T) {
	out, _, err := runTasksList(t, symptomRowsJSON, "--symptom", "x", "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []struct {
		Node struct {
			URN string `json:"urn"`
		} `json:"node"`
		MatchedBy string `json:"matchedBy"`
	}
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(rows) != 2 || rows[0].MatchedBy != "BOTH" || rows[1].MatchedBy != "KEYWORD" || rows[1].Node.URN == "" {
		t.Errorf("rows = %+v", rows)
	}
}

// The server's refusal (empty symptom, limit < 1) is surfaced as is.
func TestAgentTasksListEmptySymptomIsSentNotDropped(t *testing.T) {
	// An empty --symptom is not an absent one: it must reach the server (which
	// refuses it) rather than silently fall back to the plain list.
	_, captured, _ := runTasksList(t, memberGQLError("symptom must not be empty", "BAD_USER_INPUT"), "--symptom", "")
	if v, ok := taskListVars(t, captured)["symptom"]; !ok || v != "" {
		t.Errorf("empty symptom must be sent: %s", captured["AgentTaskSymptomSearch"])
	}
}

func TestAgentTasksListSymptomRefusalSurfaced(t *testing.T) {
	refusal := memberGQLError("symptom must not be empty", "BAD_USER_INPUT")
	for _, args := range [][]string{{"--symptom", ""}, {"--symptom", "   "}, {"--symptom", "x", "--limit", "0"}} {
		out, _, err := runTasksList(t, refusal, args...)
		if exitCodeFor(err) != exitcode.Usage || out != "" {
			t.Errorf("%v: want exit %d with empty stdout, got %d (%v) out=%q", args, exitcode.Usage, exitCodeFor(err), err, out)
		}
	}
}

func TestAgentTasksListHelpWording(t *testing.T) {
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "list", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	help := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"experimental", "up to 3 tasks from the Agent's effective system memory, matched on the abstract", "--limit applies only with --symptom"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(help, "runnable") {
		t.Errorf("help must not say \"runnable\" (Victor's wording fix):\n%s", out.String())
	}
}

// The plain list's DOCUMENT (not only its variables) must stay free of the new
// symptom/limit/matchedBy names: an older server validates the document and
// would reject it even with no flag set. --limit alone is the same plain list.
func TestAgentTasksListPlainDocumentHasNoSymptomNames(t *testing.T) {
	for _, extra := range [][]string{nil, {"--limit", "2"}} {
		var query string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Query string `json:"query"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.Contains(body.Query, "agentTaskList") {
				query = body.Query
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"agentTaskList":[]}}`))
		}))
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"agent", "tasks", "list", "agt1", "--server", srv.URL}, extra...))
		err := root.Execute()
		srv.Close()
		if err != nil {
			t.Fatalf("%v: %v", extra, err)
		}
		if query == "" {
			t.Fatalf("%v: no agentTaskList document captured", extra)
		}
		for _, name := range []string{"symptom", "limit", "matchedBy"} {
			if strings.Contains(query, name) {
				t.Errorf("%v: the plain document must not mention %q:\n%s", extra, name, query)
			}
		}
	}
}
