package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#1485 S3 — `team roster` and `team worker get` show the server's current
// model-group names (cor:agt:060:12: advisory, never authorization). The roster
// tolerates ONE field error (exactly teamRoster.workers[i].modelGroups on a
// null membership); worker get tolerates none.

// groupsRow1485 is a roster row with the given modelGroups JSON literal.
func groupsRow1485(name, groups string) string {
	row := rosterRow993(name, "AVAILABLE", true, `"claude-opus-5-5"`, `"high"`)
	if !strings.Contains(row, `"modelGroups":[],`) {
		panic("rosterRow993 no longer carries a default modelGroups to replace")
	}
	return strings.Replace(row, `"modelGroups":[],`, `"modelGroups":`+groups+`,`, 1)
}

// rosterGroups1485 builds the TeamRoster response: rows (already JSON), plus an
// optional raw `errors` array literal.
func rosterGroups1485(errs string, rows ...string) map[string]string {
	r := taskResponses()
	resp := `{"data":{"teamRoster":{"appId":"capp100000000000000000000","asOf":"2026-10-24T01:00:00Z","workers":[` + strings.Join(rows, ",") + `]}}`
	if errs != "" {
		resp += `,"errors":` + errs
	}
	r["TeamRoster"] = resp + `}`
	return r
}

func groupsErr1485(path, extensions string) string {
	return `{"message":"RAW SERVER TEXT secret-detail","path":` + path + `,"extensions":` + extensions + `}`
}

func rosterJSON1485(t *testing.T, responses map[string]string) (map[string]any, string, error) {
	t.Helper()
	_, out, err := runTeamTask(t, responses, "team", "roster", "--json")
	var dto map[string]any
	if err == nil {
		if jerr := json.Unmarshal([]byte(out), &dto); jerr != nil {
			t.Fatalf("not JSON: %v\n%s", jerr, out)
		}
	}
	return dto, out, err
}

func TestTeamRosterShowsModelGroups1485(t *testing.T) {
	responses := rosterGroups1485("",
		groupsRow1485("Ann", `["frontier","fast"]`),
		groupsRow1485("Bo", `[]`),
	)
	dto, _, err := rosterJSON1485(t, responses)
	if err != nil {
		t.Fatal(err)
	}
	rows := dto["workers"].([]any)
	if g := rows[0].(map[string]any)["modelGroups"]; len(g.([]any)) != 2 || g.([]any)[0] != "frontier" {
		t.Errorf("row0 modelGroups = %v", g)
	}
	if g, ok := rows[1].(map[string]any)["modelGroups"].([]any); !ok || len(g) != 0 {
		t.Errorf("row1 modelGroups = %#v, want a real []", rows[1].(map[string]any)["modelGroups"])
	}
	for i, r := range rows {
		if e := r.(map[string]any)["modelGroupsError"]; e != nil {
			t.Errorf("row%d modelGroupsError = %v, want null", i, e)
		}
	}
	_, out, err := runTeamTask(t, responses, "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "GROUPS") || !strings.Contains(out, "frontier,fast") {
		t.Errorf("text roster missing the group names:\n%s", out)
	}
}

func TestTeamRosterToleratesAdvisoryGroupErrors1485(t *testing.T) {
	adv := func(i string, ext string) string {
		return groupsErr1485(`["teamRoster","workers",`+i+`,"modelGroups"]`, ext)
	}
	// Two affected rows (and a healthy one between them): both keep their
	// other fields, show unavailable with the safe code, and the command succeeds.
	responses := rosterGroups1485(`[`+adv("0", `{"code":"MODEL_GROUP_LOOKUP_FAILED"}`)+`,`+adv("2", `{"code":"INTERNAL_SERVER_ERROR"}`)+`]`,
		groupsRow1485("Ann", "null"),
		groupsRow1485("Bo", `["fast"]`),
		groupsRow1485("Cy", "null"),
	)
	dto, raw, err := rosterJSON1485(t, responses)
	if err != nil {
		t.Fatalf("advisory errors must not fail the roster: %v", err)
	}
	rows := dto["workers"].([]any)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want all 3 preserved", len(rows))
	}
	r0, r1, r2 := rows[0].(map[string]any), rows[1].(map[string]any), rows[2].(map[string]any)
	if r0["modelGroups"] != nil || r0["modelGroupsError"] != "MODEL_GROUP_LOOKUP_FAILED" {
		t.Errorf("row0 = %v / %v, want null + code", r0["modelGroups"], r0["modelGroupsError"])
	}
	if r2["modelGroups"] != nil || r2["modelGroupsError"] != "INTERNAL_SERVER_ERROR" {
		t.Errorf("row2 = %v / %v", r2["modelGroups"], r2["modelGroupsError"])
	}
	if g := r1["modelGroups"].([]any); len(g) != 1 || g[0] != "fast" || r1["modelGroupsError"] != nil {
		t.Errorf("unaffected row1 changed: %v / %v", r1["modelGroups"], r1["modelGroupsError"])
	}
	if r1["family"] != "claude" || r1["model"] != "claude-opus-5-5" {
		t.Errorf("other row fields not preserved: %v", r1)
	}
	if strings.Contains(raw, "RAW SERVER TEXT") || strings.Contains(raw, "secret-detail") {
		t.Errorf("raw server error text leaked into JSON:\n%s", raw)
	}
	_, out, err := runTeamTask(t, responses, "team", "roster")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "unavailable (MODEL_GROUP_LOOKUP_FAILED)") || strings.Contains(out, "RAW SERVER TEXT") {
		t.Errorf("text must show unavailable(code) and no raw text:\n%s", out)
	}
}

func TestTeamRosterAdvisoryErrorCodeIsSafe1485(t *testing.T) {
	path := `["teamRoster","workers",0,"modelGroups"]`
	for name, ext := range map[string]string{
		"missing":     `{}`,
		"null ext":    `null`,
		"non-string":  `{"code":42}`,
		"unsafe text": `{"code":"bad code\n\u001b[31mred"}`,
		"too long":    `{"code":"` + strings.Repeat("A", 80) + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dto, raw, err := rosterJSON1485(t, rosterGroups1485(`[`+groupsErr1485(path, ext)+`]`, groupsRow1485("Ann", "null")))
			if err != nil {
				t.Fatal(err)
			}
			if e := dto["workers"].([]any)[0].(map[string]any)["modelGroupsError"]; e != "unknown" {
				t.Errorf("modelGroupsError = %v, want the fixed placeholder", e)
			}
			if strings.Contains(raw, "RAW SERVER TEXT") || strings.Contains(raw, "\\u001b") {
				t.Errorf("unsafe content leaked:\n%s", raw)
			}
		})
	}
}

// Every shape outside the one tolerated path keeps the whole-command failure,
// with nothing printed as a partial success.
func TestTeamRosterGroupErrorOutsideTheExceptionFails1485(t *testing.T) {
	good := groupsErr1485(`["teamRoster","workers",0,"modelGroups"]`, `{"code":"X"}`)
	rows := func() []string { return []string{groupsRow1485("Ann", "null"), groupsRow1485("Bo", `["fast"]`)} }
	cases := map[string]string{
		"mixed with a fatal error elsewhere": `[` + good + `,` + groupsErr1485(`["teamRoster","asOf"]`, `{"code":"BOOM"}`) + `]`,
		"wrong field":                        `[` + groupsErr1485(`["teamRoster","workers",0,"model"]`, `{"code":"X"}`) + `]`,
		"wrong root (aliased)":               `[` + groupsErr1485(`["roster","workers",0,"modelGroups"]`, `{"code":"X"}`) + `]`,
		"wrong list":                         `[` + groupsErr1485(`["teamRoster","members",0,"modelGroups"]`, `{"code":"X"}`) + `]`,
		"index out of range":                 `[` + groupsErr1485(`["teamRoster","workers",7,"modelGroups"]`, `{"code":"X"}`) + `]`,
		"negative index":                     `[` + groupsErr1485(`["teamRoster","workers",-1,"modelGroups"]`, `{"code":"X"}`) + `]`,
		"index is a string":                  `[` + groupsErr1485(`["teamRoster","workers","0","modelGroups"]`, `{"code":"X"}`) + `]`,
		"path too short":                     `[` + groupsErr1485(`["teamRoster","workers",0]`, `{"code":"X"}`) + `]`,
		"path too long":                      `[` + groupsErr1485(`["teamRoster","workers",0,"modelGroups",0]`, `{"code":"X"}`) + `]`,
		"no path":                            `[{"message":"boom","extensions":{"code":"X"}}]`,
	}
	for name, errs := range cases {
		t.Run(name, func(t *testing.T) {
			_, out, err := runTeamTask(t, rosterGroups1485(errs, rows()...), "team", "roster")
			if err == nil {
				t.Fatalf("must fail, got success:\n%s", out)
			}
			if strings.Contains(out, "Ann") {
				t.Errorf("partial output printed before failing:\n%s", out)
			}
		})
	}
	// The error targets a row whose membership is a real value: the response
	// contradicts itself, so nothing is tolerated (and nothing is guessed).
	t.Run("error beside a non-null value", func(t *testing.T) {
		r := rosterGroups1485(`[`+good+`]`, groupsRow1485("Ann", `["fast"]`), groupsRow1485("Bo", `["fast"]`))
		if _, out, err := runTeamTask(t, r, "team", "roster"); err == nil {
			t.Fatalf("must fail:\n%s", out)
		}
	})
	// A damaged roster (a null core row) is not rescued by an advisory error on
	// ANOTHER row: tolerating it would skip the null row and print the rest
	// as a complete roster (Joost, cli#1505 P2).
	t.Run("null core row beside a valid advisory error", func(t *testing.T) {
		good1 := groupsErr1485(`["teamRoster","workers",1,"modelGroups"]`, `{"code":"X"}`)
		for name, rows := range map[string][]string{
			"null first":  {"null", groupsRow1485("Ann", "null")},
			"null last":   {groupsRow1485("Ann", "null"), "null"},
			"null middle": {groupsRow1485("Ann", "null"), "null", groupsRow1485("Bo", `["fast"]`)},
		} {
			errPath := good1
			if name != "null first" {
				errPath = good
			}
			r := rosterGroups1485(`[`+errPath+`]`, rows...)
			if _, out, err := runTeamTask(t, r, "team", "roster"); err == nil {
				t.Errorf("%s: must fail, got success:\n%s", name, out)
			}
		}
	})
	t.Run("null core roster with errors", func(t *testing.T) {
		r := taskResponses()
		r["TeamRoster"] = `{"data":{"teamRoster":null},"errors":[` + good + `]}`
		if _, out, err := runTeamTask(t, r, "team", "roster"); err == nil {
			t.Fatalf("must fail:\n%s", out)
		}
	})
	t.Run("data error only (no roster payload)", func(t *testing.T) {
		r := taskResponses()
		r["TeamRoster"] = `{"errors":[` + good + `]}`
		if _, out, err := runTeamTask(t, r, "team", "roster"); err == nil {
			t.Fatalf("must fail:\n%s", out)
		}
	})
}

// A null membership with NO matching error is outside the server contract: it
// fails closed and is never shown as "no groups".
func TestTeamRosterNullGroupsWithoutErrorFailsClosed1485(t *testing.T) {
	_, out, err := runTeamTask(t, rosterGroups1485("", groupsRow1485("Ann", "null")), "team", "roster")
	if err == nil {
		t.Fatalf("null without an error must not succeed:\n%s", out)
	}
	if strings.Contains(out, "Ann") {
		t.Errorf("printed a row it could not vouch for:\n%s", out)
	}
}

func TestRosterGroupSelectionIsScoped1485(t *testing.T) {
	if !strings.Contains(gen.TeamRoster_Operation, "modelGroups") {
		t.Error("TeamRoster does not select modelGroups")
	}
	// The error path the exception matches is the UNALIASED teamRoster.workers
	// path; an alias would silently turn tolerance into failure.
	for _, bad := range []string{": modelGroups", ":modelGroups"} {
		if strings.Contains(gen.TeamRoster_Operation, bad) {
			t.Errorf("modelGroups is aliased in TeamRoster (%q): the exception's path would no longer match", bad)
		}
	}
	for name, op := range map[string]string{
		"TeamReportingLines": gen.TeamReportingLines_Operation,
		"GetWorker":          gen.GetWorker_Operation,
		"GetWorkerInApp":     gen.GetWorkerInApp_Operation,
		"Workers":            gen.WorkersRoster_Operation,
	} {
		if strings.Contains(op, "modelGroups") {
			t.Errorf("%s must not select modelGroups (shared-fragment consumers keep their wire shape)", name)
		}
	}
	if !strings.Contains(gen.GetWorkerModelGroups_Operation, "modelGroups") {
		t.Error("GetWorkerModelGroups does not select modelGroups")
	}
}

// ---- worker get -----------------------------------------------------------

func runWorkerGetGroups1485(t *testing.T, groupsResp string, args ...string) (string, error) {
	t.Helper()
	teamGitDir(t)
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + profileWorkerJSON + `}}`
		case "GetWorkerProfile":
			return profileNames
		case "GetWorkerModelGroups":
			return groupsResp
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris"}, append(args, "--server", gql.URL)...))
	err := root.Execute()
	return out.String(), err
}

func TestWorkerGetModelGroups1485(t *testing.T) {
	t.Run("names", func(t *testing.T) {
		out, err := runWorkerGetGroups1485(t, `{"data":{"worker":{"modelGroups":["frontier","fast"]}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "model groups: frontier, fast") {
			t.Errorf("missing the group line:\n%s", out)
		}
		out, err = runWorkerGetGroups1485(t, `{"data":{"worker":{"modelGroups":["frontier","fast"]}}}`, "--json")
		if err != nil {
			t.Fatal(err)
		}
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if g, _ := dto["modelGroups"].([]any); len(g) != 2 {
			t.Errorf("json modelGroups = %v", dto["modelGroups"])
		}
	})
	t.Run("genuine empty omits the line, JSON is []", func(t *testing.T) {
		out, err := runWorkerGetGroups1485(t, `{"data":{"worker":{"modelGroups":[]}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "model groups") {
			t.Errorf("[] must print no group line:\n%s", out)
		}
		out, _ = runWorkerGetGroups1485(t, `{"data":{"worker":{"modelGroups":[]}}}`, "--json")
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if g, ok := dto["modelGroups"].([]any); !ok || len(g) != 0 {
			t.Errorf("json modelGroups = %#v, want []", dto["modelGroups"])
		}
	})
	t.Run("denied null with no error is unavailable, never none", func(t *testing.T) {
		out, err := runWorkerGetGroups1485(t, `{"data":{"worker":{"modelGroups":null}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "model groups: unavailable") {
			t.Errorf("null must read unavailable:\n%s", out)
		}
		out, _ = runWorkerGetGroups1485(t, `{"data":{"worker":{"modelGroups":null}}}`, "--json")
		var dto map[string]any
		_ = json.Unmarshal([]byte(out), &dto)
		if v, present := dto["modelGroups"]; !present || v != nil {
			t.Errorf("json modelGroups = %#v (present=%v), want null", v, present)
		}
	})
	t.Run("unsafe name is made safe in text", func(t *testing.T) {
		out, err := runWorkerGetGroups1485(t, `{"data":{"worker":{"modelGroups":["a\u001b[31mb"]}}}`)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "\x1b") {
			t.Errorf("raw escape reached the terminal:\n%q", out)
		}
	})
}

// worker get has NO tolerated path: a field error — whether at the worker's
// own path, shaped like the roster's, or a schema refusal — fails the command.
func TestWorkerGetModelGroupsErrorsFailTheCommand1485(t *testing.T) {
	cases := map[string]string{
		"field error with null data":     `{"data":{"worker":{"modelGroups":null}},"errors":[{"message":"lookup failed","path":["worker","modelGroups"],"extensions":{"code":"MODEL_GROUP_LOOKUP_FAILED"}}]}`,
		"field error in the roster path": `{"data":{"worker":{"modelGroups":null}},"errors":[{"message":"lookup failed","path":["teamRoster","workers",0,"modelGroups"],"extensions":{"code":"X"}}]}`,
		"root error":                     `{"errors":[{"message":"lookup failed","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		"older server (unknown field)":   `{"errors":[{"message":"Cannot query field \"modelGroups\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`,
	}
	for name, resp := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := runWorkerGetGroups1485(t, resp)
			if err == nil {
				t.Fatalf("must fail, got success:\n%s", out)
			}
			if strings.Contains(out, "model groups: unavailable") {
				t.Errorf("a failed lookup rendered as unavailable:\n%s", out)
			}
		})
	}
}

// Andi's BLIND P2 (cli#1485 S3): the model-group requirement fields arrived
// with the schema refresh, but no command sets them yet. They must be ABSENT
// from every requirements body (never null: null reads as "clear", and an older
// server rejects an input field it does not know).
func TestRequirementsOmitUnsetModelGroups1485(t *testing.T) {
	// Every operation shares this input type, so the marshalled type covers
	// create and update alike.
	b, err := json.Marshal(gen.AgentTaskRequirementsInput{Role: ptrStr1485("qa")})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"modelGroupIn", "modelGroupNotIn"} {
		if strings.Contains(string(b), key) {
			t.Errorf("unset %s serialised into the requirements body: %s", key, b)
		}
	}
	// And through the real command: a create with ordinary requirements sends
	// neither key.
	calls, _, err := runTeamTask(t, taskResponses(),
		"team", "task", "create", "T", "--require-role", "qa", "--family-in", "claude")
	if err != nil {
		t.Fatal(err)
	}
	for i := range *calls {
		c := (*calls)[i]
		if c.Op != "AgentTaskCreate" {
			continue
		}
		input, _ := c.Vars["input"].(map[string]any)
		req, _ := input["requirements"].(map[string]any)
		if req == nil {
			t.Fatalf("no requirements in the create body: %v", c.Vars)
		}
		for _, key := range []string{"modelGroupIn", "modelGroupNotIn"} {
			if _, present := req[key]; present {
				t.Errorf("requirements carry %s on an ordinary create: %v", key, req)
			}
		}
		return
	}
	t.Fatal("no AgentTaskCreate call")
}

func ptrStr1485(s string) *string { return &s }
