package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// ---- cli#1094, cli#3267: `agent tasks list --phrase` (hadron-server#2421, #5284) ----------
//
// phrase/limit ride the existing agentTaskList operation. The server owns the
// limit rules (default 3, cap 10, outside 1 to 10 is BAD_USER_INPUT), so the CLI
// sends --limit as given and never clamps or validates it (cor:api:240:01).

const phraseRowsJSON = `{"data":{"agentTaskList":[
	{"node":` + listedNodeJSON + `,"trigger":"tasks:agent","matchedBy":"BOTH","score":0.81},
	{"node":` + listedOwnNodeJSON + `,"trigger":null,"matchedBy":"KEYWORD","score":null}]}}`

// runTasksList runs `agent tasks list` and returns stdout, the captured
// request variables and the error.
func runTasksList(t *testing.T, resp string, extra ...string) (string, map[string]json.RawMessage, error) {
	t.Helper()
	gql, captured := captureGraphQL(t, map[string]string{"AgentTaskIndexList": resp, "AgentTaskPhraseSearch": resp})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"agent", "tasks", "list", "hrn:agent:acme.com:support", "--server", gql.URL}, extra...))
	err := root.Execute()
	return out.String(), captured, err
}

func taskListVars(t *testing.T, captured map[string]json.RawMessage) map[string]any {
	t.Helper()
	raw, ok := captured["AgentTaskPhraseSearch"]
	if !ok {
		raw = captured["AgentTaskIndexList"]
	}
	var vars map[string]any
	if err := json.Unmarshal(raw, &vars); err != nil {
		t.Fatalf("variables: %v (%s)", err, raw)
	}
	return vars
}

func TestAgentTasksListPhraseSendsVariables(t *testing.T) {
	_, captured, err := runTasksList(t, phraseRowsJSON, "--phrase", "the build is red", "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	vars := taskListVars(t, captured)
	if vars["phrase"] != "the build is red" || vars["limit"] != float64(2) {
		t.Fatalf("phrase/limit must ride the request: %v", vars)
	}
}

func TestAgentTasksListNoPhraseLeavesRequestUnchanged(t *testing.T) {
	_, plain, err := runTasksList(t, phraseRowsJSON)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// --limit alone is IGNORED (the server returns before reading it): the
	// request stays byte-identical to the plain list.
	_, limited, err := runTasksList(t, phraseRowsJSON, "--limit", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := `{"ref":"hrn:agent:acme.com:support"}`
	for name, got := range map[string]json.RawMessage{"no flags": plain["AgentTaskIndexList"], "--limit only": limited["AgentTaskIndexList"]} {
		if _, searched := plain["AgentTaskPhraseSearch"]; searched {
			t.Errorf("%s: the plain list must not use the phrase document", name)
		}
		if string(got) != want {
			t.Errorf("%s: variables = %s, want %s", name, got, want)
		}
	}
}

// The limit is the server's to judge: 0 and 99 go out untouched.
func TestAgentTasksListLimitSentAsGiven(t *testing.T) {
	for _, limit := range []string{"0", "99"} {
		_, captured, err := runTasksList(t, phraseRowsJSON, "--phrase", "x", "--limit", limit)
		if err != nil {
			t.Fatalf("--limit %s: %v", limit, err)
		}
		want := map[string]float64{"0": 0, "99": 99}[limit]
		if got, ok := taskListVars(t, captured)["limit"]; !ok || got != want {
			t.Errorf("--limit %s: limit variable = %v (present=%v)", limit, got, ok)
		}
	}
	// No --limit: the variable is omitted so the server's default applies.
	_, captured, _ := runTasksList(t, phraseRowsJSON, "--phrase", "x")
	if _, ok := taskListVars(t, captured)["limit"]; ok {
		t.Errorf("an unset --limit must be omitted: %s", captured["AgentTaskPhraseSearch"])
	}
}

func TestAgentTasksListPhraseTextShowsMatchedBy(t *testing.T) {
	out, _, err := runTasksList(t, phraseRowsJSON, "--phrase", "x")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"MATCHED BY", "BOTH", "KEYWORD", "hrn:node:acme.com:base-mem:tasks:onboarding", "hrn:agent:acme.com:base", "hrn:node:acme.com:support-tasks:escalate"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should carry %q:\n%s", want, out)
		}
	}
}

func TestAgentTasksListWithoutPhraseHasNoMatchedByColumn(t *testing.T) {
	// A server answering matchedBy anyway must not change the plain output.
	out, _, err := runTasksList(t, phraseRowsJSON)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out, "MATCHED BY") || strings.Contains(out, "BOTH") {
		t.Errorf("the plain list is unchanged:\n%s", out)
	}
	jsonOut, _, _ := runTasksList(t, phraseRowsJSON, "--json")
	if strings.Contains(jsonOut, "matchedBy") {
		t.Errorf("plain --json must not gain matchedBy:\n%s", jsonOut)
	}
}

func TestAgentTasksListPhraseJSONKeepsMatchedBy(t *testing.T) {
	out, _, err := runTasksList(t, phraseRowsJSON, "--phrase", "x", "--json")
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

// The server's refusal (empty phrase, limit < 1) is surfaced as is.
func TestAgentTasksListEmptyPhraseIsSentNotDropped(t *testing.T) {
	// An empty --phrase is not an absent one: it must reach the server (which
	// refuses it) rather than silently fall back to the plain list.
	_, captured, _ := runTasksList(t, memberGQLError("phrase must not be empty", "BAD_USER_INPUT"), "--phrase", "")
	if v, ok := taskListVars(t, captured)["phrase"]; !ok || v != "" {
		t.Errorf("empty phrase must be sent: %s", captured["AgentTaskPhraseSearch"])
	}
}

func TestAgentTasksListPhraseRefusalSurfaced(t *testing.T) {
	refusal := memberGQLError("phrase must not be empty", "BAD_USER_INPUT")
	for _, args := range [][]string{{"--phrase", ""}, {"--phrase", "   "}, {"--phrase", "x", "--limit", "0"}} {
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
	for _, want := range []string{"up to 3 tasks by default", "--limit applies only with --phrase", "--symptom flag, which was removed, not aliased", "score", "no score"} {
		if !strings.Contains(help, want) {
			t.Errorf("help should say %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(help, "runnable") {
		t.Errorf("help must not say \"runnable\" (Victor's wording fix):\n%s", out.String())
	}
}

// The plain list's DOCUMENT (not only its variables) must stay free of the new
// phrase/limit/matchedBy names: an older server validates the document and
// would reject it even with no flag set. --limit alone is the same plain list.
func TestAgentTasksListPlainDocumentHasNoPhraseNames(t *testing.T) {
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
		for _, name := range []string{"phrase", "limit", "matchedBy", "score"} {
			if strings.Contains(query, name) {
				t.Errorf("%v: the plain document must not mention %q:\n%s", extra, name, query)
			}
		}
	}
}

// cli#3267: the score column. A null score prints "no score" (never 0) and
// --json carries null; a real score prints as given.
func TestAgentTasksListPhraseShowsScoreAndNoScore(t *testing.T) {
	out, _, err := runTasksList(t, phraseRowsJSON, "--phrase", "x")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "SCORE") || !strings.Contains(out, "0.81") || !strings.Contains(out, "no score") {
		t.Errorf("want a SCORE column with 0.81 and \"no score\":\n%s", out)
	}
	if strings.Contains(out, "0.00") {
		t.Errorf("a missing score must never print as 0:\n%s", out)
	}
	jsonOut, _, _ := runTasksList(t, phraseRowsJSON, "--phrase", "x", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, jsonOut)
	}
	if rows[0]["score"] != 0.81 {
		t.Errorf("row 0 score = %v", rows[0]["score"])
	}
	if v, present := rows[1]["score"]; !present || v != nil {
		t.Errorf("row 1 score must be present and null, got %v (present=%v)", v, present)
	}
}

// --symptom is removed, not aliased.
func TestAgentTasksListSymptomFlagIsUnknown(t *testing.T) {
	out, captured, err := runTasksList(t, phraseRowsJSON, "--symptom", "x")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("want unknown flag error, got %v", err)
	}
	if len(captured) != 0 || out != "" {
		t.Errorf("nothing may be sent or printed: %v %q", captured, out)
	}
}

// <agentRef> stays required: no request without it.
func TestAgentTasksListRequiresAgentRef(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "tasks", "list", "--phrase", "x", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("want a usage error")
	}
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Errorf("exit = %d, want %d (%v)", got, exitcode.Usage, err)
	}
	if len(captured) != 0 {
		t.Errorf("no request before the usage error: %v", captured)
	}
}
