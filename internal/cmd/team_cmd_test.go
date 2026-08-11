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

// An empty roster — the create tests' pre-scan (handle-collision guard) must
// not see the very name being created.
const emptyRosterJSON = `{"data":{"agents":{"total":0,"items":[]}}}`

func TestTeamPersonaCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreatePersonaAgent": `{"data":{"createAgent":` + irisJSON + `}}`,
		"PersonaAgents":      emptyRosterJSON,
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
			OperationName string `json:"operationName"`
			Variables     struct {
				PersonaName string `json:"personaName"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName == "PersonaAgents" {
			_, _ = w.Write([]byte(emptyRosterJSON))
			return
		}
		names = append(names, body.Variables.PersonaName)
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
		"PersonaAgents":      emptyRosterJSON,
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

// A candidate whose FOLDED chat handle collides with an existing persona's is
// skipped client-side (never sent to the server): "Dev Rufus" and "Dev-Rufus"
// would both answer to @dev-rufus, making chat attribution ambiguous.
func TestTeamPersonaCreateSkipsHandleCollision(t *testing.T) {
	rufusRoster := `{"data":{"agents":{"total":1,"items":[
		{"id":"agt9","urn":"hrn:agent:acme.com:dev-rufus","name":"Dev-Rufus","description":null,
		 "organizationId":"o1","personaName":"Dev-Rufus","personaRole":null,"personaPrompt":null,
		 "createdAt":"2026-08-11T00:00:00Z"}]}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"PersonaAgents":      rufusRoster,
		"CreatePersonaAgent": `{"data":{"createAgent":` + irisJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "persona", "create", "--name", "Dev Rufus", "--name", "Ivy",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreatePersonaAgent"], &vars)
	if vars["personaName"] != "Ivy" {
		t.Errorf("colliding candidate must be skipped client-side; created: %v", vars)
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
		"--transcript", "/tmp/t.jsonl", "--repo", "hadron-memory/hadron-cli",
		"-m", "acme.com::eng-team", "--json", "--server", gql.URL})
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
	for _, k := range []string{"branch", "llmModel", "prNumber", "type"} {
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
	// The worklog inputs travel through the binding: team memory
	// (canonicalized), tool, and the repo that qualifies bare refs.
	if b["teamMemory"] != "hrn:mem:acme.com:eng-team" || b["tool"] != "claude-code" ||
		b["repo"] != "hadron-memory/hadron-cli" {
		t.Errorf("binding worklog inputs: %s", data)
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
	"repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// A binding whose session was started with -m (team memory) and --tool.
const bindingWithTeamFixture = `{"sessionId":"s-new","agentId":"agt1","agentUrn":"hrn:agent:acme.com:iris",
	"personaName":"Iris","personaRole":"backend-engineer","startedAt":"2026-08-11T10:00:00Z",
	"teamMemory":"hrn:mem:acme.com:eng-team","tool":"claude-code",
	"repo":"hadron-memory/hadron-cli","prNumbers":[]}`

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

// `session log --pr` writes the worklog milestone (canonical normalized ref,
// flat fields) AND denormalizes onto Session.prNumber, keeping the local
// binding's history for whoami.
func TestTeamSessionLogWritesWorklogAndSession(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"CreateObject": `{"data":{"createObject":{"id":"o1"}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Bare number → qualified by the binding's repo.
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateTeamSession"], &vars)
	if vars["id"] != "s-new" || vars["prNumber"] != float64(371) {
		t.Errorf("update vars: %v", vars)
	}
	// branch unset → omitted, not null (an explicit null would CLEAR it).
	if _, present := vars["branch"]; present {
		t.Errorf("unset branch must be omitted, got %v", vars["branch"])
	}
	var objVars struct {
		MemoryRef  string         `json:"memoryRef"`
		ObjectType string         `json:"objectType"`
		Fields     map[string]any `json:"fields"`
	}
	_ = json.Unmarshal(captured["CreateObject"], &objVars)
	if objVars.MemoryRef != "hrn:mem:acme.com:eng-team" || objVars.ObjectType != "worklog" {
		t.Errorf("worklog target: %+v", objVars)
	}
	fld := objVars.Fields
	if fld["sessionId"] != "s-new" || fld["personaName"] != "Iris" || fld["tool"] != "claude-code" ||
		fld["kind"] != "pr" || fld["ref"] != "hadron-memory/hadron-cli#371" || fld["action"] != "worked-on" {
		t.Errorf("worklog fields: %v", fld)
	}
	if at, _ := fld["at"].(string); at == "" {
		t.Errorf("worklog at must be set, got %v", fld["at"])
	}
	var dto struct {
		Recorded string `json:"recorded"`
		Ref      string `json:"ref"`
		PRNumber int    `json:"prNumber"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Recorded != "worklog" || dto.Ref != "hadron-memory/hadron-cli#371" || dto.PRNumber != 371 {
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

// Without a team memory, --pr degrades to the Session.prNumber
// denormalization (recorded: "session", with a note) — but an issue/commit
// milestone has nowhere durable to go, so it refuses.
func TestTeamSessionLogWithoutTeamMemory(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["CreateObject"]; called {
		t.Error("no worklog write without a team memory")
	}
	if !strings.Contains(out.String(), `"recorded": "session"`) {
		t.Errorf("log output: %s", out.String())
	}

	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "log", "--issue", "362", "--server", gql.URL})
	err := root2.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("issue without team memory: exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
}

// The server write cannot be rolled back, so a failed local-binding append
// afterwards must degrade (stderr note, whoami history only) — the command
// still succeeds and still reports the server-recorded milestone.
func TestTeamSessionLogSucceedsWhenLocalWriteFails(t *testing.T) {
	dir := teamGitDir(t)
	// readBinding follows the symlink; WriteFileAtomic refuses to write
	// through one — a deterministic read-ok/write-fails binding.
	target := filepath.Join(dir, "real-binding.json")
	if err := os.WriteFile(target, []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "hadron-team-session.json")); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
	})
	f, out := testFactory(t)
	errOut, ok := f.IOStreams.ErrOut.(*strings.Builder)
	if !ok {
		t.Fatal("testFactory ErrOut is not a strings.Builder")
	}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute must succeed despite the local write failure: %v", err)
	}
	if _, called := captured["UpdateTeamSession"]; !called {
		t.Error("the server write must have happened")
	}
	var dto struct {
		Recorded string `json:"recorded"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Recorded != "session" {
		t.Errorf("log output: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "updating the local binding failed") {
		t.Errorf("stderr should note the degraded local record: %q", errOut.String())
	}
}

// log talks to the server now, so the binding's server guard applies to it
// exactly as it does to end.
func TestTeamSessionLogServerMismatch(t *testing.T) {
	dir := teamGitDir(t)
	b := strings.Replace(bindingFixture, `"prNumbers":[]`, `"server":"https://other.example","prNumbers":[]`, 1)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
	if _, called := captured["UpdateTeamSession"]; called {
		t.Error("UpdateTeamSession must not run against the wrong server")
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

// `session list --pr` is THE provenance query: normalized ref → worklog
// match → session rows (deduped; several per PR expected). A recorded
// session the caller cannot read still lists (id only) instead of being
// silently dropped.
func TestTeamSessionListProvenanceQuery(t *testing.T) {
	teamGitDir(t)
	// The worklog match must page to exhaustion: a full first page of s-done
	// milestones, then a tail page carrying s-hidden.
	fullPage := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		fullPage = append(fullPage, fmt.Sprintf(`{"id":"o%d","type":"worklog","sessionId":"s-done","ref":"hadron-memory/hadron-cli#371"}`, i))
	}
	var findObjectsVars json.RawMessage
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "FindObjects":
			findObjectsVars = body.Variables
			var vars struct {
				Offset *int `json:"offset"`
			}
			_ = json.Unmarshal(body.Variables, &vars)
			if vars.Offset == nil || *vars.Offset == 0 {
				_, _ = w.Write([]byte(`{"data":{"findObjects":{"total":201,"objects":[` + strings.Join(fullPage, ",") + `]}}}`))
			} else {
				if *vars.Offset != 200 {
					t.Errorf("unexpected offset %d", *vars.Offset)
				}
				_, _ = w.Write([]byte(`{"data":{"findObjects":{"total":201,"objects":[
					{"id":"o200","type":"worklog","sessionId":"s-hidden","ref":"hadron-memory/hadron-cli#371"}]}}}`))
			}
		case "PersonaAgents":
			_, _ = w.Write([]byte(rosterJSON))
		case "GetTeamSession":
			var vars struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(body.Variables, &vars)
			if vars.ID == "s-done" {
				_, _ = w.Write([]byte(`{"data":{"session":` + endedSessionJSON + `}}`))
			} else {
				// s-hidden: recorded in the worklog but not visible.
				_, _ = w.Write([]byte(`{"data":{"session":null}}`))
			}
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371",
		"-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var match struct {
		MemoryRef string            `json:"memoryRef"`
		Match     map[string]string `json:"match"`
	}
	_ = json.Unmarshal(findObjectsVars, &match)
	// kind is part of the match — PRs and issues share GitHub's number
	// space, so ref alone would mix artifact kinds.
	if match.Match["ref"] != "hadron-memory/hadron-cli#371" || match.Match["kind"] != "pr" ||
		match.MemoryRef != "hrn:mem:acme.com:eng-team" {
		t.Errorf("worklog lookup: %+v", match)
	}
	var got []struct {
		ID        string `json:"id"`
		StartedAt string `json:"startedAt"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	// Both sessions list (deduped across pages), the unreadable one as an
	// id-only stub.
	if len(got) != 2 || got[0].ID != "s-done" || got[1].ID != "s-hidden" || got[1].StartedAt != "" {
		t.Errorf("provenance rows: %s", out.String())
	}

	// --pr ignores no flags silently: presence filters and paging are
	// rejected loudly.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "list", "--pr", "371", "-m", "acme.com::eng-team",
		"--limit", "5", "--server", gql.URL})
	err := root2.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("--pr with --limit: exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
}

// team init merges the worklog collection into the memory schema without
// clobbering other collections, and converges idempotently.
func TestTeamInit(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory": `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team","name":"Eng Team",
			"shortDescription":null,"description":null,"class":"app","visibility":"ORGANIZATION",
			"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
			"vectorIndexEnabled":false,"maxRevCount":10,"data":null,
			"schema":{"objectTypes":{"competitor":{"fields":{"name":{"type":"text","required":true}}}}},
			"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`,
		"UpdateMemory": `{"data":{"updateMemory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team","name":"Eng Team",
			"shortDescription":null,"class":"app","visibility":"ORGANIZATION","organizationId":"o1",
			"isEncrypted":false,"maxRevCount":10,"updatedAt":"2026-08-11T00:00:00Z"}}}`,
		// The best-effort chat-parent materialization (chat:messages).
		"CreateNode": `{"data":{"createNode":{"id":"n-chat","loc":"chat:messages","seq":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Schema struct {
			ObjectTypes map[string]json.RawMessage `json:"objectTypes"`
		} `json:"schema"`
	}
	_ = json.Unmarshal(captured["UpdateMemory"], &vars)
	if _, ok := vars.Schema.ObjectTypes["worklog"]; !ok {
		t.Errorf("worklog collection not declared: %s", captured["UpdateMemory"])
	}
	if _, ok := vars.Schema.ObjectTypes["competitor"]; !ok {
		t.Errorf("existing collection must be preserved: %s", captured["UpdateMemory"])
	}
	var worklog struct {
		Fields map[string]struct {
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"fields"`
	}
	_ = json.Unmarshal(vars.Schema.ObjectTypes["worklog"], &worklog)
	for _, fname := range []string{"sessionId", "personaName", "tool", "kind", "ref", "action", "at"} {
		if !worklog.Fields[fname].Required {
			t.Errorf("worklog field %q must be required: %v", fname, worklog.Fields[fname])
		}
	}
	if !strings.Contains(out.String(), `"status": "created"`) {
		t.Errorf("init output: %s", out.String())
	}
}

// A schema that already carries the canonical worklog definition is left
// untouched — no UpdateMemory round-trip.
func TestTeamInitIdempotent(t *testing.T) {
	// Serve back exactly what init would write, keys deliberately reordered.
	worklogDef := `{"fields":{"at":{"type":"datetime","required":true},"action":{"type":"text","required":true},
		"ref":{"type":"text","required":true},"kind":{"type":"enum","required":true,"values":["pr","issue","commit","branch"]},
		"tool":{"type":"text","required":true},"personaName":{"type":"text","required":true},
		"sessionId":{"type":"text","required":true}},
		"description":"Append-only external-artifact milestones per session (hadron-cli#369 D13/D14) - the PR/session provenance join. ref is the canonical normalized artifact string (owner/repo#N, owner/repo@sha)."}`
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory": `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team","name":"Eng Team",
			"shortDescription":null,"description":null,"class":"app","visibility":"ORGANIZATION",
			"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
			"vectorIndexEnabled":false,"maxRevCount":10,"data":null,
			"schema":{"objectTypes":{"worklog":` + worklogDef + `}},
			"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`,
		"CreateNode": `{"data":{"createNode":{"id":"n-chat","loc":"chat:messages","seq":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["UpdateMemory"]; called {
		t.Error("an unchanged schema must not be rewritten")
	}
	if !strings.Contains(out.String(), `"status": "unchanged"`) {
		t.Errorf("init output: %s", out.String())
	}
}

// `team chat post` posts as the bound persona through the SHARED chat
// dialect: handle from the persona name, role/identity from the binding, and
// — D16 — the sessionId in the data payload so the message traces to the
// driving human.
func TestTeamChatPostAsPersona(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var creates []json.RawMessage
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body.OperationName != "CreateNode" {
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
			return
		}
		creates = append(creates, body.Variables)
		_, _ = w.Write([]byte(`{"data":{"createNode":{"id":"n1","loc":"chat:messages:x-iris","seq":7}}}`))
	}))
	t.Cleanup(gql.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "--body", "@rufus schema is live", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Two creates: the best-effort chat parent, then the message.
	if len(creates) != 2 {
		t.Fatalf("expected parent + message creates, got %d", len(creates))
	}
	var msg struct {
		Input struct {
			MemoryID string          `json:"memoryId"`
			Loc      string          `json:"loc"`
			NodeType string          `json:"nodeType"`
			Data     json.RawMessage `json:"data"`
		} `json:"input"`
	}
	_ = json.Unmarshal(creates[1], &msg)
	if msg.Input.MemoryID != "hrn:mem:acme.com:eng-team" || msg.Input.NodeType != "message" ||
		!strings.HasPrefix(msg.Input.Loc, "chat:messages:") || !strings.HasSuffix(msg.Input.Loc, "-iris") {
		t.Errorf("message input: %+v", msg.Input)
	}
	var data struct {
		Author    string   `json:"author"`
		Role      string   `json:"role"`
		Identity  string   `json:"identity"`
		SessionID string   `json:"sessionId"`
		Mentions  []string `json:"mentions"`
	}
	_ = json.Unmarshal(msg.Input.Data, &data)
	if data.Author != "iris" || data.Role != "backend-engineer" || data.SessionID != "s-new" ||
		data.Identity != "claude-code" || len(data.Mentions) != 1 || data.Mentions[0] != "rufus" {
		t.Errorf("message data: %+v", data)
	}
	if !strings.Contains(out.String(), `"sessionId": "s-new"`) {
		t.Errorf("post output: %s", out.String())
	}
}

// The #369 surface takes the body positionally (`team chat post <body|->`);
// the --body/--body-file flags remain as the hadron-chat-compatible form.
func TestTeamChatPostPositionalBody(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var lastCreate json.RawMessage
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Variables json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		lastCreate = body.Variables
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"createNode":{"id":"n1","loc":"chat:messages:x-iris","seq":9}}}`))
	}))
	t.Cleanup(gql.Close)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "hello positional", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var msg struct {
		Input struct {
			Data json.RawMessage `json:"data"`
		} `json:"input"`
	}
	_ = json.Unmarshal(lastCreate, &msg)
	var data struct {
		Body string `json:"body"`
	}
	_ = json.Unmarshal(msg.Input.Data, &data)
	if data.Body != "hello positional" {
		t.Errorf("positional body: %s", msg.Input.Data)
	}

	// Both a positional and --body is ambiguous — refused.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "chat", "post", "x", "--body", "y", "--server", gql.URL})
	err := root2.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("double body: exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
}

func TestTeamChatPostUnboundIsNotFound(t *testing.T) {
	teamGitDir(t)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "--body", "hi", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.NotFound {
		t.Errorf("exit code = %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
}

const teamChatMessagesJSON = `{"data":{"findNodes":{"hits":[
	{"node":{"loc":"chat:messages:a-rufus","seq":5,
		"data":{"author":"rufus","body":"@iris ping","timestamp":"t1","mentions":["iris"]}}},
	{"node":{"loc":"chat:messages:b-holger","seq":6,
		"data":{"author":"holger","body":"no mention here","timestamp":"t2"}}},
	{"node":{"loc":"chat:messages:c-rufus","seq":7,
		"data":{"author":"rufus","body":"also for @iris, no stored mentions","timestamp":"t3"}}}]}}}`

// --reply-to takes the seq readers see; it resolves to the message's loc for
// the reply edge.
func TestTeamChatPostReplyToSeq(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var lastCreate json.RawMessage
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "ChatMessages":
			_, _ = w.Write([]byte(teamChatMessagesJSON))
		case "CreateNode":
			lastCreate = body.Variables
			_, _ = w.Write([]byte(`{"data":{"createNode":{"id":"n1","loc":"chat:messages:x-iris","seq":8}}}`))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "--body", "done", "--reply-to", "5", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var msg struct {
		Input struct {
			Edges []struct {
				TargetID string `json:"targetId"`
				Name     string `json:"name"`
			} `json:"edges"`
		} `json:"input"`
	}
	_ = json.Unmarshal(lastCreate, &msg)
	if len(msg.Input.Edges) != 1 || msg.Input.Edges[0].TargetID != "chat:messages:a-rufus" || msg.Input.Edges[0].Name != "reply" {
		t.Errorf("reply edge: %+v", msg.Input.Edges)
	}
}

// --mentions-me filters to the persona's @handle (stored mentions OR
// recomputed from the body) while nextSince still advances past everything
// read — a mentions-only reader must never re-read skipped messages.
func TestTeamChatReadMentionsMe(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, _ := captureGraphQL(t, map[string]string{"ChatMessages": teamChatMessagesJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--mentions-me", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Messages []struct {
			Seq  *int   `json:"seq"`
			Body string `json:"body"`
		} `json:"messages"`
		NextSince int `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(dto.Messages) != 2 || *dto.Messages[0].Seq != 5 || *dto.Messages[1].Seq != 7 {
		t.Errorf("mention filter: %s", out.String())
	}
	if dto.NextSince != 7 {
		t.Errorf("nextSince must advance past filtered messages too, got %d", dto.NextSince)
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
