package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// cli#1375 (hadron-server#2771): `team roster` and `team worker get` show the
// worker's latest terminal report beside the check status. Part 1 only — no
// operation selects actorHost.

// terminalJSON spells a Worker.terminal object. checkedAgo is how long before
// the test ran the host classified it; reportedAt is deliberately a different
// moment so an age taken from the wrong field is visible.
func terminalJSON(status, prompt, reason string, checkedAgo time.Duration) string {
	now := time.Now().UTC()
	r := "null"
	if reason != "" {
		r = `"` + reason + `"`
	}
	return `{"host":{"id":"h1","label":"alpha"},"status":"` + status + `","prompt":"` + prompt + `","reason":` + r +
		`,"lastTurnAt":"` + now.Add(-checkedAgo-time.Hour).Format(time.RFC3339) + `","checkedAt":"` + now.Add(-checkedAgo).Format(time.RFC3339) +
		`","reportedAt":"` + now.Add(-2*time.Hour).Format(time.RFC3339) + `"}`
}

// withTerminal splices a terminal into a rosterRow993 fixture.
func withTerminal(row, terminal string) string {
	return strings.Replace(row, `"checkStatus":null`, `"checkStatus":null,"terminal":`+terminal, 1)
}

type terminalCase struct {
	name, terminal, wantCell string
}

func terminalCases() []terminalCase {
	return []terminalCase{
		{"null", "null", "—"},
		{"busy", terminalJSON("busy", "not_empty", "", 30*time.Second), "busy (<1m)"},
		{"idle", terminalJSON("idle", "empty", "", 3*time.Minute+10*time.Second), "idle (3m)"},
		{"locked", terminalJSON("locked", "menu", "model_at_capacity", 2*time.Hour+time.Minute), "locked (model_at_capacity, 2h)"},
		{"dark", terminalJSON("dark", "unknown", "no_turn_for_due_ticker", 12*time.Minute+5*time.Second), "dark (no_turn_for_due_ticker, 12m)"},
	}
}

func TestTeamRosterShowsTerminalAndAge(t *testing.T) {
	for _, tc := range terminalCases() {
		t.Run(tc.name, func(t *testing.T) {
			resp := rosterResponses993(withTerminal(rosterRow993("Ann", "AVAILABLE", true, "null", "null"), tc.terminal))
			_, out, err := runTeamTask(t, resp, "team", "roster")
			if err != nil {
				t.Fatal(err)
			}
			header := ""
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(l, "WORKER") && strings.Contains(l, "CHECK") {
					header = l
				}
			}
			if i, j := strings.Index(header, "CHECK"), strings.Index(header, "TERMINAL"); i < 0 || j <= i || j >= strings.Index(header, "ROLE") {
				t.Errorf("want CHECK < TERMINAL < ROLE, header %q", header)
			}
			if !strings.Contains(out, tc.wantCell) {
				t.Errorf("want cell %q in:\n%s", tc.wantCell, out)
			}

			_, jout, err := runTeamTask(t, resp, "team", "roster", "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Workers []map[string]json.RawMessage `json:"workers"`
			}
			if err := json.Unmarshal([]byte(jout), &got); err != nil {
				t.Fatal(err)
			}
			raw, ok := got.Workers[0]["terminal"]
			if !ok {
				t.Fatalf("terminal key missing: %s", jout)
			}
			if tc.terminal == "null" {
				if string(raw) != "null" {
					t.Errorf("terminal = %s, want null", raw)
				}
				return
			}
			var obj map[string]any
			if err := json.Unmarshal(raw, &obj); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"host", "status", "prompt", "reason", "lastTurnAt", "checkedAt", "reportedAt"} {
				if _, ok := obj[k]; !ok {
					t.Errorf("terminal.%s missing: %s", k, raw)
				}
			}
			if h := obj["host"].(map[string]any); h["id"] != "h1" || h["label"] != "alpha" {
				t.Errorf("host = %v", h)
			}
		})
	}
}

// A reader without the Working-state gate gets null from the server (the row
// is masked): the CLI shows a dash and exits 0, never an error.
func TestTeamRosterMaskedTerminalIsADash(t *testing.T) {
	row := strings.Replace(rosterRow993("Ann", "AVAILABLE", false, "null", "null"), `"liveSession":true`, `"liveSession":false`, 1)
	_, out, err := runTeamTask(t, rosterResponses993(withTerminal(row, "null")), "team", "roster")
	if err != nil {
		t.Fatalf("masked terminal must not error: %v", err)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("want a dash:\n%s", out)
	}
}

// An older server rejects Worker.terminal before execution: the roster refuses
// as schema skew, like model/tier (no fallback), rather than showing a dash that
// would read as "never reported".
func TestTeamRosterOlderServerRefusesAsSchemaSkew(t *testing.T) {
	resp := rosterResponses993(rosterRow993("Ann", "AVAILABLE", true, "null", "null"))
	resp["TeamRoster"] = `{"errors":[{"message":"Cannot query field \"terminal\" on type \"TeamRosterEntry\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	_, _, err := runTeamTask(t, resp, "team", "roster")
	if err == nil {
		t.Fatal("want a refusal")
	}
	if !api.IsSchemaSkew(err) && !strings.Contains(err.Error(), "terminal") {
		t.Errorf("want schema skew naming terminal, got %v", err)
	}
}

func terminalWorkerGet(t *testing.T, terminalResp string, args ...string) (string, error) {
	t.Helper()
	teamGitDir(t)
	gql, _ := captureGraphQLFunc(t, func(op string) string {
		switch op {
		case "GetWorker":
			return `{"data":{"worker":` + strings.Replace(profileWorkerJSON, `"hasLiveSession":null`, `"hasLiveSession":true`, 1) + `}}`
		case "GetWorkerProfile":
			return profileNames
		case "GetWorkerTerminal":
			return terminalResp
		case "TeamAppIdentity":
			return teamAppIdentityJSON
		}
		return ""
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"team", "worker", "get", profileHexID}, args...), "--server", gql.URL))
	err := root.Execute()
	return out.String(), err
}

func TestWorkerGetShowsTerminal(t *testing.T) {
	for _, tc := range terminalCases() {
		t.Run(tc.name, func(t *testing.T) {
			resp := `{"data":{"worker":{"terminal":` + tc.terminal + `}}}`
			out, err := terminalWorkerGet(t, resp)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "terminal: "+tc.wantCell) {
				t.Errorf("want terminal line %q in:\n%s", tc.wantCell, out)
			}
			jout, err := terminalWorkerGet(t, resp, "--json")
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(jout), &got); err != nil {
				t.Fatal(err)
			}
			if _, ok := got["terminal"]; !ok {
				t.Errorf("terminal key missing: %s", jout)
			}
			if tc.terminal == "null" && string(got["terminal"]) != "null" {
				t.Errorf("terminal = %s, want null", got["terminal"])
			}
		})
	}
}

// An older server without Worker.terminal: worker get still serves the lookup
// and renders null, because the field is read in its own operation.
func TestWorkerGetOlderServerTerminalFallsBackToNull(t *testing.T) {
	resp := `{"errors":[{"message":"Cannot query field \"terminal\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	out, err := terminalWorkerGet(t, resp, "--json")
	if err != nil {
		t.Fatalf("older server must not fail the lookup: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if string(got["terminal"]) != "null" {
		t.Errorf("terminal = %s, want null", got["terminal"])
	}
	// A different validation failure is not that capability absence.
	other := `{"errors":[{"message":"Cannot query field \"nope\" on type \"Worker\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	if _, err := terminalWorkerGet(t, other); err == nil {
		t.Error("an unrelated failure must surface, not read as an old server")
	}
}

// host.label is registration text: neutralised in the text line, raw in --json.
func TestWorkerGetTerminalHostLabelIsNeutralisedInText(t *testing.T) {
	label := "bad\\u001b[31m\\nhost" + strings.Repeat("x", 600)
	term := strings.Replace(terminalJSON("idle", "empty", "", time.Minute), `"label":"alpha"`, `"label":"`+label+`"`, 1)
	resp := `{"data":{"worker":{"terminal":` + term + `}}}`
	out, err := terminalWorkerGet(t, resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("escape byte leaked into text: %q", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "terminal:") && len([]rune(l)) > 400 {
			t.Errorf("host label not bounded: %d runes", len([]rune(l)))
		}
	}
	jout, err := terminalWorkerGet(t, resp, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jout, `\u001b[31m`) {
		t.Errorf("--json must carry the label raw: %s", jout)
	}
}

// Part 1 must not select actorHost (it breaks team events / alarm ack against a
// server without it), and terminal stays out of WorkerFields, which seven other
// operations share.
func TestTerminalOperationsStayWithinPart1(t *testing.T) {
	for name, op := range map[string]string{
		"TeamRoster": gen.TeamRoster_Operation, "GetWorkerTerminal": gen.GetWorkerTerminal_Operation,
		"AgentTeamEvents": gen.AgentTeamEvents_Operation, "AlarmAcknowledge": gen.AlarmAcknowledge_Operation,
		"GetWorker": gen.GetWorker_Operation,
	} {
		if strings.Contains(op, "actorHost") {
			t.Errorf("%s selects actorHost", name)
		}
	}
	if strings.Contains(gen.GetWorker_Operation, "terminal") {
		t.Error("GetWorker (WorkerFields) must not select terminal")
	}
	if !strings.Contains(gen.TeamRoster_Operation, "terminal") || !strings.Contains(gen.GetWorkerTerminal_Operation, "terminal") {
		t.Error("the part-1 operations must select terminal")
	}
}
