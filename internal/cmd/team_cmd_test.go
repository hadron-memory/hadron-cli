package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const irisJSON = `{"id":"agt1","urn":"hrn:agent:acme.com:iris","name":"Iris","description":null,
	"organizationId":"o1","personaName":"Iris","personaRole":"backend-engineer",
	"personaPrompt":"You are Iris.","createdAt":"2026-08-11T00:00:00Z"}`

// A plain agent on the same roster page: persona list/get must skip it
// (personaName null == not a persona).
const plainAgentJSON = `{"id":"agt2","urn":"hrn:agent:acme.com:support-bot","name":"Support Bot",
	"description":null,"organizationId":"o1","personaName":null,"personaRole":null,
	"personaPrompt":null,"createdAt":"2026-08-11T00:00:00Z"}`

const rosterJSON = `{"data":{"agents":{"total":2,"items":[` + irisJSON + `,` + plainAgentJSON + `]}}}`

func TestTeamPersonaCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreatePersonaAgent": `{"data":{"createAgent":` + irisJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "create", "--org", "acme.com", "--name", "Iris",
		"--role", "backend-engineer", "--prompt", "You are Iris.", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreatePersonaAgent"], &vars)
	if vars["name"] != "Iris" || vars["personaName"] != "Iris" ||
		vars["personaRole"] != "backend-engineer" || vars["personaPrompt"] != "You are Iris." ||
		vars["orgId"] != "acme.com" {
		t.Errorf("create vars: %v", vars)
	}
	// Unset optionals are OMITTED, never null (the repo-wide wire contract).
	if _, present := vars["description"]; present {
		t.Errorf("unset description must be omitted, got %v", vars["description"])
	}
	var dto struct {
		PersonaName string `json:"personaName"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.PersonaName != "Iris" {
		t.Errorf("dto: %s", out.String())
	}
}

const personaTakenJSON = `{"errors":[{"message":"Persona name \"Iris\" is already taken for this owner — pick another name.",
	"extensions":{"code":"PERSONA_NAME_TAKEN"}}]}`

// The retry-with-next-name contract: a PERSONA_NAME_TAKEN rejection falls
// through to the next --name candidate.
func TestTeamPersonaCreateRetriesNextName(t *testing.T) {
	var names []string
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				PersonaName string `json:"personaName"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		names = append(names, body.Variables.PersonaName)
		w.Header().Set("Content-Type", "application/json")
		if len(names) == 1 {
			_, _ = w.Write([]byte(personaTakenJSON))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"createAgent":` + irisJSON + `}}`))
	}))
	t.Cleanup(gql.Close)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "create", "--name", "Iris", "--name", "Ivy",
		"--role", "backend-engineer", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(names) != 2 || names[0] != "Iris" || names[1] != "Ivy" {
		t.Errorf("candidates tried: %v", names)
	}
	if !strings.Contains(out.String(), `"personaName"`) {
		t.Errorf("expected the created persona in output, got %s", out.String())
	}
}

// Every candidate taken → exit-code Conflict, and the message says why the
// name can't be freed (names bind forever, retired included).
func TestTeamPersonaCreateAllTakenIsConflict(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CreatePersonaAgent": personaTakenJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "create", "--name", "Iris", "--server", gql.URL})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if !strings.Contains(err.Error(), "forever") {
		t.Errorf("error should explain the forever-binding: %v", err)
	}
}

func TestTeamPersonaListKeepsOnlyPersonas(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"PersonaAgents": rosterJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(got) != 1 || got[0]["personaName"] != "Iris" {
		t.Errorf("roster: %s", out.String())
	}
}

func TestTeamPersonaGetByName(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"PersonaAgents": rosterJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Case-insensitive: the server's uniqueness is case-insensitive too.
	root.SetArgs([]string{"team", "persona", "get", "iris", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		ID          string `json:"id"`
		PersonaName string `json:"personaName"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.ID != "agt1" || dto.PersonaName != "Iris" {
		t.Errorf("dto: %s", out.String())
	}
}

func TestTeamPersonaGetUnknownNameIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"PersonaAgents":   rosterJSON,
		"GetPersonaAgent": `{"data":{"agent":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "get", "Nadia", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.NotFound {
		t.Errorf("exit code = %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
}

// Retire prompts like a deletion: non-interactive without --yes is refused
// before any mutation.
func TestTeamPersonaRetireRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"PersonaAgents": rosterJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "retire", "Iris", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
	if _, called := captured["DeleteAgent"]; called {
		t.Error("DeleteAgent must not run without confirmation")
	}
}

func TestTeamPersonaRetire(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"PersonaAgents": rosterJSON,
		"DeleteAgent":   `{"data":{"deleteAgent":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "retire", "Iris", "--yes", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteAgent"], &vars)
	if vars["ref"] != "agt1" {
		t.Errorf("retire ref: %v", vars)
	}
	if !strings.Contains(out.String(), "never re-minted") {
		t.Errorf("retire output should state the name stays bound: %s", out.String())
	}
}

const activeSessionJSON = `{"id":"s-old","agentId":"agt1","userId":"u-holger","type":"DEVELOPER",
	"repo":"hadron-memory/hadron-cli","branch":null,"prNumber":null,
	"startedAt":"2026-08-11T09:00:00Z","endedAt":null,"host":"mac1","tool":"claude-code",
	"transcriptPath":null,"llmModel":null}`

const endedSessionJSON = `{"id":"s-done","agentId":"agt1","userId":"u-holger","type":"DEVELOPER",
	"repo":"hadron-memory/hadron-cli","branch":null,"prNumber":42,
	"startedAt":"2026-08-10T09:00:00Z","endedAt":"2026-08-10T18:00:00Z","host":"mac1",
	"tool":"claude-code","transcriptPath":null,"llmModel":null}`

const startedSessionJSON = `{"id":"s-new","agentId":"agt1","userId":"u-holger","type":"DEVELOPER",
	"repo":null,"branch":null,"prNumber":null,"startedAt":"2026-08-11T10:00:00Z","endedAt":null,
	"host":"mac1","tool":"claude-code","transcriptPath":"/tmp/t.jsonl","llmModel":null}`

func teamGitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HADRON_TEAM_GIT_DIR", dir)
	return dir
}

func TestTeamSessionStartWritesBinding(t *testing.T) {
	dir := teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"PersonaAgents":    rosterJSON,
		"TeamSessions":     `{"data":{"sessions":[` + endedSessionJSON + `]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--tool", "claude-code",
		"--transcript", "/tmp/t.jsonl", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["StartTeamSession"], &vars)
	if vars.Input["agentRef"] != "agt1" || vars.Input["tool"] != "claude-code" ||
		vars.Input["transcriptPath"] != "/tmp/t.jsonl" {
		t.Errorf("start vars: %v", vars.Input)
	}
	if id, _ := vars.Input["id"].(string); id == "" {
		t.Errorf("start must mint a session id, got %v", vars.Input["id"])
	}
	if host, _ := vars.Input["host"].(string); host == "" {
		t.Errorf("host must default to the hostname, got %v", vars.Input["host"])
	}
	// Unset optional SessionInput fields are OMITTED, never null.
	for _, k := range []string{"repo", "branch", "llmModel", "prNumber", "type"} {
		if _, present := vars.Input[k]; present {
			t.Errorf("unset %q must be omitted from SessionInput, got %v", k, vars.Input[k])
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "hadron-team-session.json"))
	if err != nil {
		t.Fatalf("binding not written: %v", err)
	}
	var b map[string]any
	_ = json.Unmarshal(data, &b)
	if b["sessionId"] != "s-new" || b["personaName"] != "Iris" || b["agentId"] != "agt1" {
		t.Errorf("binding: %s", data)
	}
	if !strings.Contains(out.String(), `"tookOver": false`) {
		t.Errorf("start output: %s", out.String())
	}
}

// An active session holds the persona: without --force the start refuses
// (Conflict), names the driver, and points at the missing reaper (#930).
func TestTeamSessionStartOccupiedNeedsForce(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"PersonaAgents": rosterJSON,
		"TeamSessions":  `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	for _, want := range []string{"u-holger", "--force", "hadron-server#930", "2026-08-11T09:00:00Z"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q: %v", want, err)
		}
	}
	if _, called := captured["StartTeamSession"]; called {
		t.Error("StartTeamSession must not run without --force")
	}
}

func TestTeamSessionStartForceTakesOver(t *testing.T) {
	dir := teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"PersonaAgents":    rosterJSON,
		"TeamSessions":     `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--force", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"tookOver": true`) {
		t.Errorf("takeover output: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "hadron-team-session.json")); err != nil {
		t.Errorf("binding not written: %v", err)
	}
}

const bindingFixture = `{"sessionId":"s-new","agentId":"agt1","agentUrn":"hrn:agent:acme.com:iris",
	"personaName":"Iris","personaRole":"backend-engineer","startedAt":"2026-08-11T10:00:00Z",
	"prNumbers":[]}`

// whoami is the compaction-recovery read: local only, no server round-trip
// (no fake server is running here).
func TestTeamSessionWhoami(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "whoami", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		PersonaName string `json:"personaName"`
		SessionID   string `json:"sessionId"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.PersonaName != "Iris" || dto.SessionID != "s-new" {
		t.Errorf("whoami: %s", out.String())
	}
}

func TestTeamSessionWhoamiUnboundIsNotFound(t *testing.T) {
	teamGitDir(t)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "whoami"})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.NotFound {
		t.Errorf("exit code = %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
}

// Slice 1: `session log --pr` records locally (the server has no
// session-update surface yet — worklog + Session.prNumber are slice 3).
func TestTeamSessionLogRecordsPRLocally(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Recorded string `json:"recorded"`
		PRNumber int    `json:"prNumber"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Recorded != "local" || dto.PRNumber != 371 {
		t.Errorf("log output: %s", out.String())
	}
	data, _ := os.ReadFile(path)
	var b struct {
		PRNumbers []int `json:"prNumbers"`
	}
	_ = json.Unmarshal(data, &b)
	if len(b.PRNumbers) != 1 || b.PRNumbers[0] != 371 {
		t.Errorf("binding prNumbers: %s", data)
	}
}

func TestTeamSessionEndClearsBinding(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"data":{"endSession":{"id":"s-new","agentId":"agt1","userId":"u-holger",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":null,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":"2026-08-11T12:00:00Z","host":null,
			"tool":null,"transcriptPath":null,"llmModel":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--summary", "shipped #371", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["EndTeamSession"], &vars)
	if vars["id"] != "s-new" || vars["summary"] != "shipped #371" {
		t.Errorf("end vars: %v", vars)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("binding should be cleared, stat err: %v", err)
	}
	if !strings.Contains(out.String(), `"personaName": "Iris"`) {
		t.Errorf("end output: %s", out.String())
	}
}

// The roster scan must read BOTH slices: the unfiltered member-org list and
// the caller's own user-owned agents (filter.ownedByMe, #782) — an org-less
// persona lives only in the second. It must also page each slice to
// exhaustion (issue #23: the server truncates an unbounded list).
func TestTeamPersonaListMergesOwnedByMeAndPaginates(t *testing.T) {
	// Org slice: one full 200-row page (199 fillers + Iris) + a tail page.
	// OwnedByMe slice: one user-owned persona, absent from the org slice.
	fullPage := make([]string, 0, 200)
	for i := 0; i < 199; i++ {
		fullPage = append(fullPage, fmt.Sprintf(`{"id":"f%d","urn":"hrn:agent:acme.com:f%d","name":"F%d",
			"description":null,"organizationId":"o1","personaName":null,"personaRole":null,
			"personaPrompt":null,"createdAt":"2026-08-11T00:00:00Z"}`, i, i, i))
	}
	fullPage = append(fullPage, irisJSON)
	tailRow := `{"id":"agt3","urn":"hrn:agent:acme.com:uma","name":"Uma","description":null,
		"organizationId":"o1","personaName":"Uma","personaRole":null,"personaPrompt":null,
		"createdAt":"2026-08-11T00:00:00Z"}`
	ownedRow := `{"id":"agt4","urn":"hrn:agent:@holger:nadia","name":"Nadia","description":null,
		"organizationId":null,"personaName":"Nadia","personaRole":null,"personaPrompt":null,
		"createdAt":"2026-08-11T00:00:00Z"}`

	type call struct {
		Offset    *int
		OwnedByMe bool
	}
	var calls []call
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables struct {
				Offset *int `json:"offset"`
				Filter *struct {
					OwnedByMe *bool `json:"ownedByMe"`
				} `json:"filter"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		owned := body.Variables.Filter != nil && body.Variables.Filter.OwnedByMe != nil && *body.Variables.Filter.OwnedByMe
		calls = append(calls, call{body.Variables.Offset, owned})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case owned:
			_, _ = w.Write([]byte(`{"data":{"agents":{"total":1,"items":[` + ownedRow + `]}}}`))
		case body.Variables.Offset == nil || *body.Variables.Offset == 0:
			_, _ = w.Write([]byte(`{"data":{"agents":{"total":201,"items":[` + strings.Join(fullPage, ",") + `]}}}`))
		default:
			if *body.Variables.Offset != 200 {
				t.Errorf("unexpected offset %d", *body.Variables.Offset)
			}
			_, _ = w.Write([]byte(`{"data":{"agents":{"total":201,"items":[` + tailRow + `]}}}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "list", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []struct {
		PersonaName string `json:"personaName"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	names := make([]string, len(got))
	for i, p := range got {
		names[i] = p.PersonaName
	}
	if len(got) != 3 || names[0] != "Iris" || names[1] != "Uma" || names[2] != "Nadia" {
		t.Errorf("merged roster: %v", names)
	}
	// 2 org pages + 1 ownedByMe page, in that order.
	if len(calls) != 3 || calls[0].OwnedByMe || calls[1].OwnedByMe || !calls[2].OwnedByMe {
		t.Errorf("calls: %+v", calls)
	}
}

// The occupancy check must read past the first sessions page: an old
// still-active session can hide behind 200 newer ended ones, and stopping
// early would report the persona free (the issue-#23 failure mode).
func TestTeamSessionStartFindsActiveSessionOnLaterPage(t *testing.T) {
	teamGitDir(t)
	filler := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		filler = append(filler, fmt.Sprintf(`{"id":"e%d","agentId":"other","userId":"u1","type":"DEVELOPER",
			"repo":null,"branch":null,"prNumber":null,"startedAt":"2026-08-11T0%d:00:00Z",
			"endedAt":"2026-08-11T09:00:00Z","host":null,"tool":null,"transcriptPath":null,"llmModel":null}`, i, i%10))
	}
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset *int `json:"offset"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "PersonaAgents":
			_, _ = w.Write([]byte(rosterJSON))
		case "TeamSessions":
			if body.Variables.Offset == nil || *body.Variables.Offset == 0 {
				_, _ = w.Write([]byte(`{"data":{"sessions":[` + strings.Join(filler, ",") + `]}}`))
			} else {
				if *body.Variables.Offset != 200 {
					t.Errorf("unexpected offset %d", *body.Variables.Offset)
				}
				_, _ = w.Write([]byte(`{"data":{"sessions":[` + activeSessionJSON + `]}}`))
			}
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
}

// Without --force an existing worktree binding refuses; with --force the
// bound session is best-effort ENDED before the new one starts, so the
// overwritten binding never orphans an active session.
func TestTeamSessionStartForceEndsPreviouslyBoundSession(t *testing.T) {
	dir := teamGitDir(t)
	prev := strings.Replace(bindingFixture, "s-new", "s-prev", 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(prev), 0o600); err != nil {
		t.Fatal(err)
	}

	// Refusal first: no --force.
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}

	gql, captured := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"data":{"endSession":{"id":"s-prev","agentId":"agt1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":null,
			"startedAt":"2026-08-11T08:00:00Z","endedAt":"2026-08-11T10:00:00Z","host":null,
			"tool":null,"transcriptPath":null,"llmModel":null}}}`,
		"PersonaAgents":    rosterJSON,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--force", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var endVars map[string]any
	_ = json.Unmarshal(captured["EndTeamSession"], &endVars)
	if endVars["id"] != "s-prev" {
		t.Errorf("previously bound session must be ended, got vars: %v", endVars)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "hadron-team-session.json"))
	if !strings.Contains(string(data), "s-new") {
		t.Errorf("binding should now name the new session: %s", data)
	}
}

// `end --session <id>` is the recovery path when no binding exists.
func TestTeamSessionEndExplicitSession(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"data":{"endSession":{"id":"s-orphan","agentId":null,"userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":null,
			"startedAt":"2026-08-11T08:00:00Z","endedAt":"2026-08-11T10:00:00Z","host":null,
			"tool":null,"transcriptPath":null,"llmModel":null}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--session", "s-orphan", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["EndTeamSession"], &vars)
	if vars["id"] != "s-orphan" {
		t.Errorf("end vars: %v", vars)
	}
}

// A binding started against another server refuses to end against this one —
// the mutation would miss the real session and leave its persona held.
func TestTeamSessionEndServerMismatch(t *testing.T) {
	dir := teamGitDir(t)
	b := strings.Replace(bindingFixture, `"prNumbers":[]`, `"server":"https://other.example","prNumbers":[]`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "end", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
	if !strings.Contains(err.Error(), "https://other.example") {
		t.Errorf("error should name the recorded server: %v", err)
	}
	if _, called := captured["EndTeamSession"]; called {
		t.Error("EndTeamSession must not run against the wrong server")
	}
}

// --active filters client-side (no server-side filter exists) and the
// persona name is joined onto each row.
func TestTeamSessionListActive(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"PersonaAgents": rosterJSON,
		"TeamSessions":  `{"data":{"sessions":[` + activeSessionJSON + `,` + endedSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--active", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []struct {
		ID          string  `json:"id"`
		PersonaName *string `json:"personaName"`
		Active      bool    `json:"active"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(got) != 1 || got[0].ID != "s-old" || !got[0].Active {
		t.Errorf("active list: %s", out.String())
	}
	if got[0].PersonaName == nil || *got[0].PersonaName != "Iris" {
		t.Errorf("persona join: %s", out.String())
	}
}
