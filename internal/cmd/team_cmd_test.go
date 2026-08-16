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

const irisWorkerJSON = `{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","slug":"iris",
	"appId":"app1","agentId":"agt1","name":"Iris","role":"backend-engineer",
	"prompt":"You are Iris.","promptOverride":null,"memoryId":"mw1","retiredAt":null,"retiredBy":null,
	"createdAt":"2026-08-14T00:00:00Z","createdBy":"u-holger"}`

// A retired casting on the same staff page: hidden by default, listed with
// --include-retired (its name stays reserved forever).
const retiredWorkerJSON = `{"id":"wkr2","urn":"hrn:worker:acme.com:eng-team:uma","slug":"uma",
	"appId":"app1","agentId":"agt1","name":"Uma","role":"qa",
	"prompt":null,"promptOverride":null,"memoryId":null,"retiredAt":"2026-08-13T00:00:00Z","retiredBy":"u-holger",
	"createdAt":"2026-08-12T00:00:00Z","createdBy":null}`

const staffJSON = `{"data":{"workers":{"total":2,"items":[` + irisWorkerJSON + `,` + retiredWorkerJSON + `]}}}`

func TestTeamWorkerCast(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CastWorker": `{"data":{"castWorker":` + irisWorkerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--app", "acme.com:eng-team",
		"--role", "backend-engineer", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CastWorker"], &vars)
	if vars["appRef"] != "acme.com:eng-team" || vars["role"] != "backend-engineer" {
		t.Errorf("cast vars: %v", vars)
	}
	// Unset optionals are OMITTED, never null: no explicit name means the
	// SERVER allocates from the cast-list register; no agent means the role
	// picks it; no team-agent means the roles-branch marker resolves it.
	for _, k := range []string{"name", "agentRef", "teamAgentRef", "promptOverride"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
	var dto struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.Name != "Iris" || dto.ID != "wkr1" {
		t.Errorf("dto: %s", out.String())
	}
}

// --name, --agent, --team-agent, and --prompt-override pass through verbatim;
// the resolved boot briefing (Worker.prompt) prints on the human path.
func TestTeamWorkerCastExplicitNameAndBriefing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CastWorker": `{"data":{"castWorker":` + irisWorkerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--app", "acme.com:eng-team",
		"--agent", "hrn:agent:acme.com:backend", "--name", "Iris", "--team-agent", "agt-team",
		"--prompt-override", "You keep the release calm.", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CastWorker"], &vars)
	if vars["agentRef"] != "hrn:agent:acme.com:backend" || vars["name"] != "Iris" ||
		vars["teamAgentRef"] != "agt-team" || vars["promptOverride"] != "You keep the release calm." {
		t.Errorf("cast vars: %v", vars)
	}
	if !strings.Contains(out.String(), "You are Iris.") {
		t.Errorf("the boot briefing (Worker.prompt) must print: %s", out.String())
	}
}

// The CLI retries NOTHING (thin-CLI directive): server refusals map to exit
// codes and surface verbatim — WORKER_ROLE_NOT_FOUND carries the available
// roles, WORKER_AGENT_AMBIGUOUS says to pass --agent. The retry loop the
// persona model needed lives server-side now (#974).
func TestTeamWorkerCastServerRefusals(t *testing.T) {
	cases := []struct {
		name string
		resp string
		code int
		want string
	}{
		{"role not found lists roles",
			`{"errors":[{"message":"Role \"backend\" not found - available roles: backend-engineer, qa","extensions":{"code":"WORKER_ROLE_NOT_FOUND"}}]}`,
			exitcode.NotFound, "available roles: backend-engineer, qa"},
		{"register exhausted",
			`{"errors":[{"message":"every register name is taken","extensions":{"code":"WORKER_REGISTER_EXHAUSTED"}}]}`,
			exitcode.Conflict, "register"},
		{"explicit name taken",
			`{"errors":[{"message":"Worker name \"Iris\" is already taken in this App - pick another name.","extensions":{"code":"WORKER_NAME_TAKEN"}}]}`,
			exitcode.Conflict, "Iris"},
		{"agent ambiguous",
			`{"errors":[{"message":"several installed agents carry this persona role - pass agentRef","extensions":{"code":"WORKER_AGENT_AMBIGUOUS"}}]}`,
			exitcode.Usage, "agentRef"},
		{"agent not installed",
			`{"errors":[{"message":"agent is not installed in this App","extensions":{"code":"WORKER_AGENT_NOT_INSTALLED"}}]}`,
			exitcode.Usage, "not installed"},
		{"agent not found",
			`{"errors":[{"message":"no installed agent carries persona role \"backend\"","extensions":{"code":"WORKER_AGENT_NOT_FOUND"}}]}`,
			exitcode.NotFound, "backend"},
		{"team agent ambiguous",
			`{"errors":[{"message":"several installed agents carry roles - pass teamAgentRef","extensions":{"code":"TEAM_AGENT_AMBIGUOUS"}}]}`,
			exitcode.Usage, "teamAgentRef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{"CastWorker": tc.resp})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "worker", "cast", "--app", "a:t", "--role", "backend",
				"--name", "Iris", "--server", gql.URL})
			err := root.Execute()
			if code := exitcode.FromError(err); code != tc.code {
				t.Errorf("exit code = %d, want %d; err: %v", code, tc.code, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("server message must surface verbatim (want %q): %v", tc.want, err)
			}
		})
	}
}

// The server never guesses the agent to cast — and the CLI refuses the
// unanswerable call before making it.
func TestTeamWorkerCastRequiresRoleOrAgent(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--app", "a:t", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want Usage; err: %v", code, err)
	}
	if _, called := captured["CastWorker"]; called {
		t.Error("an unanswerable cast must not reach the mutation")
	}
}

// The STAFF read: retired workers hidden by default, listed on request.
func TestTeamWorkerList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Workers": staffJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["Workers"], &vars)
	// The scan always asks for retired rows (resolution needs them; names
	// stay bound to history) and filters client-side.
	if vars["appRef"] != "acme.com:eng-team" || vars["includeRetired"] != true {
		t.Errorf("workers vars: %v", vars)
	}
	var got []struct {
		Name    string `json:"name"`
		Retired bool   `json:"retired"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(got) != 1 || got[0].Name != "Iris" || got[0].Retired {
		t.Errorf("retired workers must be hidden by default: %s", out.String())
	}

	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team",
		"--include-retired", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var all []struct {
		Name    string `json:"name"`
		Retired bool   `json:"retired"`
	}
	if err := json.Unmarshal([]byte(out2.String()), &all); err != nil {
		t.Fatalf("json: %v (%s)", err, out2.String())
	}
	if len(all) != 2 || !all[1].Retired {
		t.Errorf("--include-retired must list the retired casting: %s", out2.String())
	}
}

// A worker NAME resolves within the App (case-insensitively, like the
// server's per-App uniqueness).
func TestTeamWorkerGetByName(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"Workers": staffJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "iris", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.ID != "wkr1" || dto.Name != "Iris" {
		t.Errorf("dto: %s", out.String())
	}
}

// The App-free spellings: a worker id, or its URN (#991/#445 — keyed on the
// permanent derived slug, so it addresses one casting without App context).
// Both ride `worker(ref:)` straight through.
func TestTeamWorkerGetByRefWithoutApp(t *testing.T) {
	teamGitDir(t)
	for _, ref := range []string{"wkr1", "hrn:worker:acme.com:eng-team:iris"} {
		gql, captured := captureGraphQL(t, map[string]string{
			"GetWorker": `{"data":{"worker":` + irisWorkerJSON + `}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "worker", "get", ref, "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("%s: execute: %v", ref, err)
		}
		var vars map[string]any
		_ = json.Unmarshal(captured["GetWorker"], &vars)
		if vars["ref"] != ref {
			t.Errorf("%s must pass through verbatim, got %v", ref, vars["ref"])
		}
		// The URN is part of the --json contract now — it is the portable
		// ref a script hands back.
		if !strings.Contains(out.String(), `"urn": "hrn:worker:acme.com:eng-team:iris"`) ||
			!strings.Contains(out.String(), `"slug": "iris"`) {
			t.Errorf("%s: the worker's address must be in --json: %s", ref, out.String())
		}
	}
}

// PR #446 review: a worker URN is App-independent by construction, so it
// must dispatch BEFORE any ambient App scope — otherwise a stale,
// uninstalled or unreadable --app/context breaks a self-contained ref, and
// buries the real error under "no worker … in this App". The fake omits
// Workers entirely, so a staff scan would fail the test loudly.
func TestTeamWorkerGetByURNIgnoresAmbientApp(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker": `{"data":{"worker":` + irisWorkerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris",
		"--app", "acme.com:some-other-app", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a URN must resolve regardless of the ambient App: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["GetWorker"], &vars)
	if vars["ref"] != "hrn:worker:acme.com:eng-team:iris" {
		t.Errorf("the URN must go straight to worker(ref:): %v", vars)
	}
	if _, scanned := captured["Workers"]; scanned {
		t.Error("a URN must not trigger an App staff scan")
	}

	// And its lookup failure surfaces as ITSELF, not as "no worker in this App".
	gql2, _ := captureGraphQL(t, map[string]string{
		"GetWorker": `{"errors":[{"message":"token expired","extensions":{"code":"UNAUTHENTICATED"}}]}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "get", "hrn:worker:acme.com:eng-team:iris",
		"--app", "acme.com:some-other-app", "--server", gql2.URL})
	err := root2.Execute()
	if code := exitcode.FromError(err); code != exitcode.AuthRequired {
		t.Errorf("exit code = %d, want %d (AuthRequired); err: %v", code, exitcode.AuthRequired, err)
	}
	if err == nil || !strings.Contains(err.Error(), "token expired") {
		t.Errorf("the real error must not be buried under an App-scoped not-found: %v", err)
	}
}

// The documented legacy case (PR #446 review): an App whose URN predates the
// flat grammar-v2 arity yields a NULL worker URN. Neither output mode may
// choke — the human branch simply omits the line, and --json preserves null.
func TestTeamWorkerGetTolueratesNullURN(t *testing.T) {
	teamGitDir(t)
	legacy := strings.Replace(irisWorkerJSON, `"urn":"hrn:worker:acme.com:eng-team:iris"`, `"urn":null`, 1)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker": `{"data":{"worker":` + legacy + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("a null URN must not break the read: %v", err)
	}
	if strings.Contains(out.String(), "urn:") {
		t.Errorf("no urn line belongs on a worker without one: %s", out.String())
	}
	if !strings.Contains(out.String(), "Iris") {
		t.Errorf("the rest of the receipt must still render: %s", out.String())
	}

	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "get", "wkr1", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out2.String(), `"urn": null`) {
		t.Errorf("--json must preserve the null address: %s", out2.String())
	}
}

func TestTeamWorkerGetByIdWithoutApp(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker": `{"data":{"worker":` + irisWorkerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["GetWorker"], &vars)
	if vars["ref"] != "wkr1" {
		t.Errorf("get vars: %v", vars)
	}
	if !strings.Contains(out.String(), `"name": "Iris"`) {
		t.Errorf("dto: %s", out.String())
	}
}

func TestTeamWorkerGetUnknownIsNotFound(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Workers":   staffJSON,
		"GetWorker": `{"data":{"worker":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "Nadia", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.NotFound {
		t.Errorf("exit code = %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
}

// A worker-id lookup failure must surface as ITSELF, not as a fabricated
// not-found: without an App scope the id lookup is the only lookup, so an
// auth/transport error reading as "no worker" would make an outage look like
// missing data (PR #431 review).
func TestTeamWorkerGetByIdPropagatesLookupErrors(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker": `{"errors":[{"message":"token expired","extensions":{"code":"UNAUTHENTICATED"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "wkr1", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.AuthRequired {
		t.Errorf("exit code = %d, want %d (AuthRequired), not a fabricated NotFound; err: %v", code, exitcode.AuthRequired, err)
	}
	if err == nil || !strings.Contains(err.Error(), "token expired") {
		t.Errorf("the real error must surface: %v", err)
	}
}

// Retire prompts: non-interactive without --yes is refused before any mutation.
func TestTeamWorkerRetireRequiresYes(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"Workers": staffJSON})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "retire", "Iris", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
	if _, called := captured["RetireWorker"]; called {
		t.Error("RetireWorker must not run without confirmation")
	}
}

func TestTeamWorkerRetire(t *testing.T) {
	retired := strings.Replace(irisWorkerJSON, `"retiredAt":null`, `"retiredAt":"2026-08-14T10:00:00Z"`, 1)
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":      staffJSON,
		"RetireWorker": `{"data":{"retireWorker":` + retired + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "retire", "Iris", "--yes", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["RetireWorker"], &vars)
	if vars["workerRef"] != "wkr1" {
		t.Errorf("retire must address the RESOLVED worker id: %v", vars)
	}
	if !strings.Contains(out.String(), "never re-cast") {
		t.Errorf("retire output should state the name stays bound: %s", out.String())
	}
}

// `worker rm` is the hard-delete escape for a NEVER-USED miscast; anything
// with history refuses WORKER_IN_USE (a state conflict) and retires instead.
func TestTeamWorkerRm(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":      staffJSON,
		"DeleteWorker": `{"data":{"deleteWorker":true}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "rm", "Iris", "--yes", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["DeleteWorker"], &vars)
	if vars["workerRef"] != "wkr1" {
		t.Errorf("rm vars: %v", vars)
	}
	if !strings.Contains(out.String(), `"status": "deleted"`) {
		t.Errorf("rm output: %s", out.String())
	}
}

func TestTeamWorkerRmInUseIsConflict(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Workers": staffJSON,
		"DeleteWorker": `{"errors":[{"message":"Worker \"Iris\" has history - retire it instead",
			"extensions":{"code":"WORKER_IN_USE"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "rm", "Iris", "--yes", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil || !strings.Contains(err.Error(), "retire it instead") {
		t.Errorf("server message must surface verbatim: %v", err)
	}
}

const teamRolesJSON = `{"data":{"teamRoles":{"total":2,"items":[
	{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be","description":"Backend role",
	 "register":[
	   {"name":"Fred","taken":false,"heldBy":null},
	   {"name":"Iris","taken":true,"heldBy":{"id":"wkr1","name":"Iris"}},
	   {"name":"Joe","taken":true,"heldBy":null}],
	 "freeCount":1,"exhausted":false,"nameRange":"F-J","nameConvention":"initial = role",
	 "roleAgent":{"id":"agt1","urn":"hrn:agent:acme.com:backend","name":"backend-engineer","personaRole":"backend-engineer"},
	 "hasNamePlaceholder":true},
	{"role":"qa","loc":"roles:qa","nodeId":"n-qa","description":null,
	 "register":[{"name":"Uma","taken":true,"heldBy":{"id":"wkr2","name":"Uma"}}],
	 "freeCount":0,"exhausted":true,"nameRange":null,"nameConvention":null,
	 "roleAgent":null,"hasNamePlaceholder":false}]}}}`

// #403: the pre-cast read — registers with server-computed taken/free, in
// allocation order. The free/taken verdicts are server truths (judged
// against the App's FULL roster), never recomputed client-side.
func TestTeamRoleList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["TeamRoles"], &vars)
	if vars["appRef"] != "acme.com:eng-team" {
		t.Errorf("teamRoles vars: %v", vars)
	}
	var got []struct {
		Role      string `json:"role"`
		FreeCount int    `json:"freeCount"`
		Exhausted bool   `json:"exhausted"`
		Register  []struct {
			Name     string  `json:"name"`
			Taken    bool    `json:"taken"`
			HeldByID *string `json:"heldById"`
		} `json:"register"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(got) != 2 || got[0].Role != "backend-engineer" || got[0].FreeCount != 1 || !got[1].Exhausted {
		t.Errorf("roles: %s", out.String())
	}
	// Allocation order preserved; the held name carries the worker id (the
	// actionable ref), and a taken-but-unreadable holder stays taken.
	r := got[0].Register
	if len(r) != 3 || r[0].Name != "Fred" || r[0].Taken ||
		r[1].HeldByID == nil || *r[1].HeldByID != "wkr1" ||
		!r[2].Taken || r[2].HeldByID != nil {
		t.Errorf("register: %s", out.String())
	}

	// Human table: taken marker + exhausted + the nameless-template warning.
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "role", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"Iris✓", "0 (exhausted)", "never binds {{name}}"} {
		if !strings.Contains(out2.String(), want) {
			t.Errorf("table must carry %q: %s", want, out2.String())
		}
	}
}

func TestTeamRoleGet(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Case-insensitive, like the server's own name matching.
	root.SetArgs([]string{"team", "role", "get", "Backend-Engineer", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"held by Iris (wkr1)", "Fred — free", "Joe — taken (holder not visible to you)",
		"range F-J", "backend-engineer (hrn:agent:acme.com:backend)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("get output must carry %q: %s", want, out.String())
		}
	}

	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "role", "get", "nope", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root2.Execute()
	if code := exitcode.FromError(err); code != exitcode.NotFound {
		t.Errorf("unknown role: exit %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
}

// #410: role create is one server-validated call; the receipt is the
// resulting register.
func TestTeamRoleCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateTeamRole": `{"data":{"createTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
			"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null},{"name":"Gwen","taken":false,"heldBy":null}],
			"freeCount":2,"exhausted":false,"nameRange":"F-J","nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "create", "backend-engineer", "--names", "Fred, Gwen",
		"--name-range", "F-J", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Role      string   `json:"role"`
		Names     []string `json:"names"`
		NameRange string   `json:"nameRange"`
	}
	_ = json.Unmarshal(captured["CreateTeamRole"], &vars)
	if vars.Role != "backend-engineer" || len(vars.Names) != 2 || vars.Names[0] != "Fred" || vars.Names[1] != "Gwen" ||
		vars.NameRange != "F-J" {
		t.Errorf("create vars: %+v", vars)
	}
	var raw map[string]any
	_ = json.Unmarshal(captured["CreateTeamRole"], &raw)
	for _, k := range []string{"nameConvention", "description", "allowOutOfRange", "teamAgentRef"} {
		if _, present := raw[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, raw[k])
		}
	}
	if !strings.Contains(out.String(), "✓ created role backend-engineer — register: Fred, Gwen (2 free)") {
		t.Errorf("the receipt is the resulting register: %s", out.String())
	}
}

// role update read-modify-writes the conventions: an untouched field resends
// its current value, a --clear-* flag sends the EXPLICIT null the server
// reads as "remove this key" — and both convention keys are always present
// on the wire (the operation deliberately has no omitempty on them).
func TestTeamRoleUpdateSetAndClear(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":"F-K","nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":          teamRolesJSON,
		"UpdateTeamRoleMeta": updated,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "update", "backend-engineer", "--name-range", "F-K",
		"--clear-name-convention", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateTeamRoleMeta"], &vars)
	if vars["nameRange"] != "F-K" {
		t.Errorf("set convention: %v", vars)
	}
	v, present := vars["nameConvention"]
	if !present || v != nil {
		t.Errorf("--clear-name-convention must send an EXPLICIT null (present, nil), got present=%v v=%v", present, v)
	}
	if _, present := vars["description"]; present {
		t.Errorf("unset description must be omitted, got %v", vars["description"])
	}

	// Refusals before any write: nothing to update, and value+clear conflict.
	for _, tc := range [][]string{
		{"team", "role", "update", "backend-engineer"},
		{"team", "role", "update", "backend-engineer", "--name-range", "F-K", "--clear-name-range"},
	} {
		f2, _ := testFactory(t)
		root2 := NewRootCmd(f2)
		root2.SetArgs(append(tc, "--app", "acme.com:eng-team", "--server", gql.URL))
		if code := exitcode.FromError(root2.Execute()); code != exitcode.Usage {
			t.Errorf("%v: exit %d, want Usage", tc, code)
		}
	}
}

// `names set` is the explicit whole-list replacement: no CAS precondition
// (it asserts final state). The operation carries only names, so
// conventions are structurally preserved.
func TestTeamRoleNamesSet(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":           teamRolesJSON,
		"UpdateTeamRoleNames": updated,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "names", "set", "backend-engineer", "Fred,Iris,Joe,Kim",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateTeamRoleNames"], &vars)
	names, _ := vars["names"].([]any)
	if len(names) != 4 || names[0] != "Fred" || names[3] != "Kim" {
		t.Errorf("set must submit the exact ordered list: %v", vars["names"])
	}
	// Conventions are structurally preserved: the names operation cannot
	// carry them at all.
	for _, k := range []string{"nameRange", "nameConvention", "description"} {
		if _, present := vars[k]; present {
			t.Errorf("%q must be absent from the names-only operation, got %v", k, vars[k])
		}
	}

	// set carries NO precondition: an explicit wholesale replacement asserts
	// the final state (the sugar verbs are the CAS path, below).
	if _, present := vars["expectedNames"]; present {
		t.Errorf("set must not send expectedNames, got %v", vars["expectedNames"])
	}
}

// #436: the sugar verbs are back, CAS-safe (hadron-server#987) — each write
// carries expectedNames = the register as read, so a concurrent edit refuses
// TEAM_ROLE_STALE instead of being clobbered.
// (teamRolesJSON's backend register: Fred, Iris, Joe.)
func TestTeamRoleNamesSugarVerbs(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"add appends in order", []string{"names", "add", "backend-engineer", "Gwen", "Hans"},
			[]string{"Fred", "Iris", "Joe", "Gwen", "Hans"}},
		{"rm removes case-insensitively", []string{"names", "rm", "backend-engineer", "fred"},
			[]string{"Iris", "Joe"}},
		{"mv repositions", []string{"names", "mv", "backend-engineer", "Joe", "1"},
			[]string{"Joe", "Fred", "Iris"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"TeamRoles":           teamRolesJSON,
				"UpdateTeamRoleNames": updated,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"team", "role"}, tc.args...), "--app", "acme.com:eng-team", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars struct {
				Names         []string `json:"names"`
				ExpectedNames []string `json:"expectedNames"`
			}
			_ = json.Unmarshal(captured["UpdateTeamRoleNames"], &vars)
			if strings.Join(vars.Names, "|") != strings.Join(tc.want, "|") {
				t.Errorf("composed register = %v, want %v", vars.Names, tc.want)
			}
			// The CAS precondition: the register exactly as read.
			if strings.Join(vars.ExpectedNames, "|") != "Fred|Iris|Joe" {
				t.Errorf("expectedNames must be the register as read: %v", vars.ExpectedNames)
			}
		})
	}

	// rm/mv of an absent name refuse before any write.
	for _, tc := range [][]string{
		{"team", "role", "names", "rm", "backend-engineer", "Nadia"},
		{"team", "role", "names", "mv", "backend-engineer", "Nadia", "1"},
		{"team", "role", "names", "mv", "backend-engineer", "Joe", "9"},
		{"team", "role", "names", "mv", "backend-engineer", "Joe", "zero"},
	} {
		gql, captured := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesJSON})
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(tc, "--app", "acme.com:eng-team", "--server", gql.URL))
		if code := exitcode.FromError(root.Execute()); code != exitcode.Usage {
			t.Errorf("%v: exit %d, want Usage", tc, code)
		}
		if _, called := captured["UpdateTeamRoleNames"]; called {
			t.Errorf("%v: a refused verb must not reach the mutation", tc)
		}
	}
}

// #442: `names add` splits every argument on commas, like its siblings
// (`create --names`, `names set`). Before this, the whole string was
// appended as ONE name — a corrupt entry the range check cannot catch (it
// inspects only the initial) that would become a PERMANENT worker name on
// the next register-mode cast.
func TestTeamRoleNamesAddSplitsCommas(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"comma form", []string{"Gwen,Hans"}},
		{"space form", []string{"Gwen", "Hans"}},
		{"mixed, with stray spaces", []string{"Gwen, Hans"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"TeamRoles":           teamRolesJSON,
				"UpdateTeamRoleNames": updated,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(append([]string{"team", "role", "names", "add", "backend-engineer"}, tc.args...),
				"--app", "acme.com:eng-team", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var vars struct {
				Names []string `json:"names"`
			}
			_ = json.Unmarshal(captured["UpdateTeamRoleNames"], &vars)
			// Every form yields the SAME two appended names — never one
			// composite "Gwen,Hans" entry.
			if strings.Join(vars.Names, "|") != "Fred|Iris|Joe|Gwen|Hans" {
				t.Errorf("composed register = %v", vars.Names)
			}
		})
	}
}

// PR #444 review: the rebase path had its own false-update hole — when a
// concurrent admin adds the very names this command was adding, the
// recomposed register equals the stored one, and the loop used to submit it
// anyway and print "✓ updated". It must detect the no-op, skip the write,
// and report unchanged.
func TestTeamRoleNamesAddStaleRebaseToNoOpReportsUnchanged(t *testing.T) {
	var updateCalls int
	roleCalls := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TeamRoles":
			roleCalls++
			if roleCalls == 1 {
				_, _ = w.Write([]byte(teamRolesJSON)) // register: Fred, Iris, Joe
				return
			}
			// The re-read behind the unchanged receipt sees the concurrent add.
			_, _ = w.Write([]byte(`{"data":{"teamRoles":{"total":1,"items":[
				{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be","description":null,
				 "register":[{"name":"Fred","taken":false,"heldBy":null},{"name":"Iris","taken":true,"heldBy":{"id":"wkr1","name":"Iris"}},
				   {"name":"Joe","taken":false,"heldBy":null},{"name":"Hans","taken":false,"heldBy":null}],
				 "freeCount":3,"exhausted":false,"nameRange":null,"nameConvention":null,
				 "roleAgent":null,"hasNamePlaceholder":null}]}}}`))
		case "UpdateTeamRoleNames":
			updateCalls++
			// A concurrent admin already added Hans — exactly what we wanted.
			_, _ = w.Write([]byte(`{"errors":[{"message":"the register changed",
				"extensions":{"code":"TEAM_ROLE_STALE","storedNames":["Fred","Iris","Joe","Hans"]}}]}`))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "names", "add", "backend-engineer", "Hans",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Exactly ONE write attempt: the retry recomposed to a no-op and stopped.
	if updateCalls != 1 {
		t.Errorf("the rebased no-op must not re-submit, got %d update calls", updateCalls)
	}
	if strings.Contains(out.String(), "✓ updated") {
		t.Errorf("a rebased no-op must not claim an update: %s", out.String())
	}
	if !strings.Contains(out.String(), "unchanged") || !strings.Contains(out.String(), "Hans") {
		t.Errorf("the receipt must report unchanged and show the current register: %s", out.String())
	}
}

// A name already in the register is skipped rather than re-appended, and an
// add where EVERY name is already present says so instead of printing a
// "✓ updated" receipt for a write that changed nothing (#442, lower-severity
// finding).
func TestTeamRoleNamesAddSkipsPresentNames(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":           teamRolesJSON,
		"UpdateTeamRoleNames": updated,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	// Iris is already in the register (case-insensitively); only Gwen is new.
	root.SetArgs([]string{"team", "role", "names", "add", "backend-engineer", "iris,Gwen",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Names []string `json:"names"`
	}
	_ = json.Unmarshal(captured["UpdateTeamRoleNames"], &vars)
	if strings.Join(vars.Names, "|") != "Fred|Iris|Joe|Gwen" {
		t.Errorf("a present name must not be re-appended: %v", vars.Names)
	}

	// Every name already present: no write at all, and an honest receipt.
	gql2, captured2 := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesJSON})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "role", "names", "add", "backend-engineer", "Fred,Iris",
		"--app", "acme.com:eng-team", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured2["UpdateTeamRoleNames"]; called {
		t.Error("nothing to add must not reach the mutation")
	}
	if strings.Contains(out2.String(), "✓ updated") {
		t.Errorf("a no-op must not claim an update: %s", out2.String())
	}
	if !strings.Contains(out2.String(), "unchanged") {
		t.Errorf("the receipt must say nothing changed: %s", out2.String())
	}
}

// `names rm` deliberately matches LITERALLY — quoting a comma-bearing entry
// is how a register corrupted by an older CLI is repaired (#442).
func TestTeamRoleNamesRmMatchesLiterally(t *testing.T) {
	corrupt := `{"data":{"teamRoles":{"total":1,"items":[
		{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be","description":null,
		 "register":[{"name":"Fred","taken":false,"heldBy":null},{"name":"Linn,Mia","taken":false,"heldBy":null}],
		 "freeCount":2,"exhausted":false,"nameRange":null,"nameConvention":null,
		 "roleAgent":null,"hasNamePlaceholder":null}]}}}`
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":           corrupt,
		"UpdateTeamRoleNames": updated,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "names", "rm", "backend-engineer", "Linn,Mia",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("the recovery path must work: %v", err)
	}
	var vars struct {
		Names []string `json:"names"`
	}
	_ = json.Unmarshal(captured["UpdateTeamRoleNames"], &vars)
	if strings.Join(vars.Names, "|") != "Fred" {
		t.Errorf("the corrupt entry must be removable as one literal name: %v", vars.Names)
	}
}

// A sugar edit from an EMPTY register must still be conditional: the
// precondition is present-but-empty on the wire ([]), never omitted — a
// dropped empty precondition would be an unconditional write, reopening the
// race (PR #440 review, P1). The *[]string binding is what keeps [] alive.
func TestTeamRoleNamesAddFromEmptyRegisterKeepsCAS(t *testing.T) {
	emptyRole := `{"data":{"teamRoles":{"total":1,"items":[
		{"role":"qa","loc":"roles:qa","nodeId":"n-qa","description":null,"register":[],
		 "freeCount":0,"exhausted":true,"nameRange":null,"nameConvention":null,
		 "roleAgent":null,"hasNamePlaceholder":null}]}}}`
	updated := `{"data":{"updateTeamRole":{"role":"qa","loc":"roles:qa","nodeId":"n-qa",
		"description":null,"register":[{"name":"Uma","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":           emptyRole,
		"UpdateTeamRoleNames": updated,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "names", "add", "qa", "Uma",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateTeamRoleNames"], &vars)
	raw, present := vars["expectedNames"]
	if !present {
		t.Fatalf("expectedNames must be PRESENT (empty, not omitted) — an omitted precondition is an unconditional write: %v", vars)
	}
	if arr, _ := raw.([]any); len(arr) != 0 {
		t.Errorf("the empty register's precondition is []: %v", raw)
	}
}

// A TEAM_ROLE_STALE refusal WITHOUT the storedNames payload must not rebase
// onto a fabricated empty register (PR #440 review, P2) — the loop re-reads
// instead, and the retry's precondition comes from that re-read.
func TestTeamRoleNamesStaleWithoutPayloadRereads(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	teamRolesCalls := 0
	var updateCalls []json.RawMessage
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TeamRoles":
			teamRolesCalls++
			_, _ = w.Write([]byte(teamRolesJSON))
		case "UpdateTeamRoleNames":
			updateCalls = append(updateCalls, body.Variables)
			if len(updateCalls) == 1 {
				_, _ = w.Write([]byte(`{"errors":[{"message":"the register changed",
					"extensions":{"code":"TEAM_ROLE_STALE"}}]}`))
				return
			}
			_, _ = w.Write([]byte(updated))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "names", "add", "backend-engineer", "Hans",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if teamRolesCalls != 2 {
		t.Errorf("a payload-less stale refusal must trigger a re-read, got %d TeamRoles calls", teamRolesCalls)
	}
	var second struct {
		ExpectedNames []string `json:"expectedNames"`
	}
	_ = json.Unmarshal(updateCalls[1], &second)
	if strings.Join(second.ExpectedNames, "|") != "Fred|Iris|Joe" {
		t.Errorf("the retry's precondition must come from the re-read, not a fabricated empty register: %v", second.ExpectedNames)
	}
}

// The CAS loop rebases on TEAM_ROLE_STALE: the refusal's storedNames become
// the new base AND the new precondition, and the edit is recomposed — the
// concurrent addition (Gwen) survives the retry.
func TestTeamRoleNamesAddRebasesOnStale(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":null,"register":[{"name":"Fred","taken":false,"heldBy":null}],
		"freeCount":1,"exhausted":false,"nameRange":null,"nameConvention":null,"roleAgent":null,"hasNamePlaceholder":null}}}`
	var updateCalls []json.RawMessage
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TeamRoles":
			_, _ = w.Write([]byte(teamRolesJSON))
		case "UpdateTeamRoleNames":
			updateCalls = append(updateCalls, body.Variables)
			if len(updateCalls) == 1 {
				// A concurrent edit added Gwen since the read.
				_, _ = w.Write([]byte(`{"errors":[{"message":"the register changed",
					"extensions":{"code":"TEAM_ROLE_STALE","storedNames":["Fred","Iris","Joe","Gwen"]}}]}`))
				return
			}
			_, _ = w.Write([]byte(updated))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "names", "add", "backend-engineer", "Hans",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(updateCalls) != 2 {
		t.Fatalf("expected refuse-then-retry, got %d update calls", len(updateCalls))
	}
	var second struct {
		Names         []string `json:"names"`
		ExpectedNames []string `json:"expectedNames"`
	}
	_ = json.Unmarshal(updateCalls[1], &second)
	// The retry is rebased: the concurrent Gwen survives, Hans lands after,
	// and the precondition is the storedNames the refusal carried.
	if strings.Join(second.Names, "|") != "Fred|Iris|Joe|Gwen|Hans" {
		t.Errorf("rebased submission = %v", second.Names)
	}
	if strings.Join(second.ExpectedNames, "|") != "Fred|Iris|Joe|Gwen" {
		t.Errorf("rebased precondition = %v", second.ExpectedNames)
	}
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	if !strings.Contains(errOut, "rebasing") {
		t.Errorf("the rebase should be narrated: %q", errOut)
	}
}

// The register invariants are the SERVER's; the CLI maps their typed
// refusals — minted/duplicate/exists are state conflicts (TEAM_ROLE_EXISTS
// needs its explicit mapping: the generic suffix rule matches
// _ALREADY_EXISTS, not _EXISTS), out-of-range is fixable input.
func TestTeamRoleWriteServerRefusals(t *testing.T) {
	cases := []struct {
		name string
		resp string
		code int
	}{
		{"minted name is conflict",
			`{"errors":[{"message":"\"Iris\" was minted in this App and can never leave the register","extensions":{"code":"TEAM_ROLE_NAME_MINTED"}}]}`,
			exitcode.Conflict},
		{"cross-register duplicate is conflict",
			`{"errors":[{"message":"\"Rufus\" is already in frontend-engineer's register","extensions":{"code":"TEAM_ROLE_NAME_DUPLICATE"}}]}`,
			exitcode.Conflict},
		{"out of range is usage",
			`{"errors":[{"message":"\"Zoe\" falls outside range F-J","extensions":{"code":"TEAM_ROLE_NAME_OUT_OF_RANGE"}}]}`,
			exitcode.Usage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gql, _ := captureGraphQL(t, map[string]string{
				"TeamRoles":           teamRolesJSON,
				"UpdateTeamRoleNames": tc.resp,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "role", "names", "set", "backend-engineer", "Fred,Iris,Joe,Zoe",
				"--app", "acme.com:eng-team", "--server", gql.URL})
			if code := exitcode.FromError(root.Execute()); code != tc.code {
				t.Errorf("exit %d, want %d", code, tc.code)
			}
		})
	}

	gql, _ := captureGraphQL(t, map[string]string{
		"CreateTeamRole": `{"errors":[{"message":"role backend-engineer already exists - updateTeamRole is the edit path","extensions":{"code":"TEAM_ROLE_EXISTS"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "create", "backend-engineer", "--names", "Fred",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("existing role: exit %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil || !strings.Contains(err.Error(), "updateTeamRole is the edit path") {
		t.Errorf("server guidance must surface verbatim: %v", err)
	}
}

// #404: --dry-run routes to the castWorkerPreview QUERY — the mutation is
// absent from the fake, so any cast call fails loudly. The receipt carries
// the not-a-reservation caveat; the preview creates nothing.
func TestTeamWorkerCastDryRun(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CastWorkerPreview": `{"data":{"castWorkerPreview":{"name":"Joe","role":"backend-engineer",
			"agentId":"agt1","agentName":"backend-engineer","teamAgentId":"agt-team","teamAgentName":"Eng Team Agent",
			"prompt":"You are Joe.","hasNamePlaceholder":true}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--dry-run", "--app", "acme.com:eng-team",
		"--role", "backend-engineer", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CastWorkerPreview"], &vars)
	if vars["appRef"] != "acme.com:eng-team" || vars["role"] != "backend-engineer" {
		t.Errorf("preview vars: %v", vars)
	}
	for _, k := range []string{"name", "agentRef", "teamAgentRef", "promptOverride"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
	for _, want := range []string{"would cast Joe", "You are Joe.", "register of Eng Team Agent", "NOT reserved"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry-run output must carry %q: %s", want, out.String())
		}
	}
	if _, called := captured["CastWorker"]; called {
		t.Error("a dry run must never reach the mutation")
	}

	// The --json contract (PR #434 review): the actionable ids beside the
	// names, the nullable fields present, and the explicit always-false
	// reserved signal — so machine consumers cannot misread a preview as a
	// reservation.
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "cast", "--dry-run", "--app", "acme.com:eng-team",
		"--role", "backend-engineer", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Name               string  `json:"name"`
		Role               *string `json:"role"`
		AgentID            string  `json:"agentId"`
		TeamAgentID        *string `json:"teamAgentId"`
		Prompt             *string `json:"prompt"`
		HasNamePlaceholder *bool   `json:"hasNamePlaceholder"`
		Reserved           bool    `json:"reserved"`
	}
	if err := json.Unmarshal([]byte(out2.String()), &dto); err != nil {
		t.Fatalf("--json: %v (%s)", err, out2.String())
	}
	if dto.Name != "Joe" || dto.AgentID != "agt1" ||
		dto.TeamAgentID == nil || *dto.TeamAgentID != "agt-team" ||
		dto.Role == nil || *dto.Role != "backend-engineer" ||
		dto.Prompt == nil || *dto.Prompt != "You are Joe." ||
		dto.HasNamePlaceholder == nil || !*dto.HasNamePlaceholder {
		t.Errorf("preview --json contract: %s", out2.String())
	}
	if dto.Reserved {
		t.Errorf("reserved must be explicitly false — the preview holds nothing: %s", out2.String())
	}
	if !strings.Contains(out2.String(), `"reserved": false`) {
		t.Errorf("the reserved key must be present, not omitted: %s", out2.String())
	}
}

// A dry-run refusal IS the answer: the preview runs the cast's exact
// resolution, so its typed refusals surface exactly like the cast's.
func TestTeamWorkerCastDryRunRefusalIsTheAnswer(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"CastWorkerPreview": `{"errors":[{"message":"Worker name \"Iris\" is already taken in this App","extensions":{"code":"WORKER_NAME_TAKEN"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--dry-run", "--app", "a:t",
		"--role", "backend-engineer", "--name", "Iris", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil || !strings.Contains(err.Error(), "Iris") {
		t.Errorf("the refusal must surface verbatim: %v", err)
	}
}

// #383 lineage: --app is the App-CONTEXT flag, not a filter. `agent list`
// must keep returning every readable row (that IS its contract) — but say so
// on stderr, pointing at the reads that DO answer per-App questions.
func TestAppFlagOnListingsPrintsScopeNote(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Agents": `{"data":{"agents":{"total":1,"items":[{"id":"agt1","urn":"hrn:agent:acme.com:iris","name":"Iris",
			"description":null,"type":"ASSISTANT","visibility":"ORGANIZATION","organizationId":"o1",
			"surfaces":[],"systemMemoryId":null,"systemPrompt":null,"aiProvider":null,"aiModel":null,
			"hasAiApiKey":false,"personaRole":null,"personaPrompt":null,"createdAt":"2026-08-11T00:00:00Z"}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"agent", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	if !strings.Contains(errOut, "does NOT scope this listing") || !strings.Contains(errOut, "app agent list") {
		t.Errorf("stderr must say --app did not scope this and point at the roster reads: %q", errOut)
	}
	// The note is on stderr precisely so --json stays a clean contract.
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "[") {
		t.Errorf("--json stdout must stay unpolluted: %s", out.String())
	}

	// No --app: no note. A configured default App is ambient context nobody
	// passed expecting a filter, so this must not become noise.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"agent", "list", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if s := f2.IOStreams.ErrOut.(*strings.Builder).String(); s != "" {
		t.Errorf("no --app must mean no note, got %q", s)
	}
}

const activeSessionJSON = `{"id":"s-old","agentId":"agt1","workerId":"wkr1",
	"worker":{"id":"wkr1","name":"Iris","role":"backend-engineer"},"userId":"u-holger","type":"DEVELOPER",
	"repo":"hadron-memory/hadron-cli","branch":null,"prNumber":null,
	"startedAt":"2026-08-11T09:00:00Z","endedAt":null,"host":"mac1","tool":"claude-code",
	"transcriptPath":null,"llmModel":null}`

const endedSessionJSON = `{"id":"s-done","agentId":"agt1","workerId":"wkr1",
	"worker":{"id":"wkr1","name":"Iris","role":"backend-engineer"},"userId":"u-holger","type":"DEVELOPER",
	"repo":"hadron-memory/hadron-cli","branch":null,"prNumber":42,
	"startedAt":"2026-08-10T09:00:00Z","endedAt":"2026-08-10T18:00:00Z","host":"mac1",
	"tool":"claude-code","transcriptPath":null,"llmModel":null}`

const startedSessionJSON = `{"id":"s-new","agentId":"agt1","workerId":"wkr1",
	"worker":{"id":"wkr1","name":"Iris","role":"backend-engineer"},"userId":"u-holger","type":"DEVELOPER",
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
		"Workers":          staffJSON,
		"TeamSessions":     `{"data":{"sessions":[` + endedSessionJSON + `]}}`,
		"TeamMemoryApp":    `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
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
	// The session binds the WORKER (cor:agt:020:03); the agent is stamped
	// server-side from the casting, so agentRef is never sent.
	if vars.Input["workerRef"] != "wkr1" || vars.Input["tool"] != "claude-code" ||
		vars.Input["transcriptPath"] != "/tmp/t.jsonl" {
		t.Errorf("start vars: %v", vars.Input)
	}
	if _, present := vars.Input["agentRef"]; present {
		t.Errorf("agentRef must not be sent — the server derives it from the worker: %v", vars.Input["agentRef"])
	}
	if id, _ := vars.Input["id"].(string); id == "" {
		t.Errorf("start must mint a session id, got %v", vars.Input["id"])
	}
	if host, _ := vars.Input["host"].(string); host == "" {
		t.Errorf("host must default to the hostname, got %v", vars.Input["host"])
	}
	// With -m the resolved App rides along so the server can verify it
	// matches the worker's App (a mismatched -m fails loudly).
	if vars.Input["appRef"] != "app1" {
		t.Errorf("-m must bind the session to ITS App, got appRef=%v", vars.Input["appRef"])
	}
	// Unset optional SessionInput fields are OMITTED, never null (`appRef`
	// without -m is covered by TestTeamSessionStartWithoutMemoryOmitsAppRef).
	// `force` and `plan` (#940/#934) in particular: refreshed input fields do
	// not inherit omitempty, so their absence here is the loud check.
	for _, k := range []string{"branch", "llmModel", "prNumber", "type", "force", "plan"} {
		if _, present := vars.Input[k]; present {
			t.Errorf("unset %q must be omitted from SessionInput, got %v", k, vars.Input[k])
		}
	}
	// The taken-check narrows server-side (#974): sessions(workerRef:).
	var sessVars map[string]any
	_ = json.Unmarshal(captured["TeamSessions"], &sessVars)
	if sessVars["workerRef"] != "wkr1" {
		t.Errorf("the activity check must filter by workerRef: %v", sessVars)
	}
	data, err := os.ReadFile(filepath.Join(dir, "hadron-team-session.json"))
	if err != nil {
		t.Fatalf("binding not written: %v", err)
	}
	var b map[string]any
	_ = json.Unmarshal(data, &b)
	if b["sessionId"] != "s-new" || b["workerId"] != "wkr1" || b["workerName"] != "Iris" || b["agentId"] != "agt1" {
		t.Errorf("binding: %s", data)
	}
	// #399: the worker's App rides into the binding — the worklog home.
	if b["appId"] != "app1" {
		t.Errorf("binding must record the worker's App: %s", data)
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

// The worker's resolved boot briefing prints on session start (the epic's
// print-on-cast/start contract) — it is the identity the driver adopts.
func TestTeamSessionStartPrintsBriefing(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "You are Iris.") {
		t.Errorf("the boot briefing must print on start: %s", out.String())
	}
}

// #399: the binding records the worker's App, so a start without -m is
// COMPLETE — no worklog warning of any kind (the condition the old note
// warned about no longer exists).
func TestTeamSessionStartWithoutMemoryIsQuiet(t *testing.T) {
	dir := teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	for _, stale := range []string{"worklog home", "team init", "session end"} {
		if strings.Contains(errOut, stale) {
			t.Errorf("no worklog warning belongs on start anymore: %q", errOut)
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "hadron-team-session.json"))
	if !strings.Contains(string(data), `"appId": "app1"`) {
		t.Errorf("the binding must carry the worker's App: %s", data)
	}
}

// With -m there is nothing to warn about.
func TestTeamSessionStartWithMemoryIsQuiet(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"Workers":          staffJSON,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"TeamMemoryApp":    `{"data":{"memory":{"id":"m1","appId":"app-1"}}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris",
		"-m", "acme.com::eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if s := f.IOStreams.ErrOut.(*strings.Builder).String(); strings.Contains(s, "worklog home") {
		t.Errorf("-m was given — no warning expected: %q", s)
	}
}

// A retired worker takes no new sessions — refused before the server call,
// with the row's own retirement instant.
func TestTeamSessionStartRefusesRetiredWorker(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker": `{"data":{"worker":` + retiredWorkerJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr2", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "retired") {
		t.Errorf("the refusal must say the worker is retired: %v", err)
	}
	if _, called := captured["StartTeamSession"]; called {
		t.Error("StartTeamSession must not run for a retired worker")
	}
}

func TestTeamSessionStartOccupiedNeedsForce(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	for _, want := range []string{"u-holger", "--force", "2026-08-11T09:00:00Z"} {
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
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[` + activeSessionJSON + `]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// #940/#432: the override rides to the server — the atomic gate is its
	// WORKER_TAKEN refusal, and force is what waives it, explicitly.
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["StartTeamSession"], &vars)
	if vars.Input["force"] != true {
		t.Errorf("--force must ride as SessionInput.force: %v", vars.Input)
	}
	if !strings.Contains(out.String(), `"tookOver": true`) {
		t.Errorf("takeover output: %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "hadron-team-session.json")); err != nil {
		t.Errorf("binding not written: %v", err)
	}
}

// The race the client pre-flight cannot close: the worker was free at the
// check and taken by the time startSession ran. The refusal renders from the
// WORKER_TAKEN EXTENSIONS payload (#940/#432 — the documented contract), so
// the fake carries a deliberately GENERIC message: driver/time/session must
// come from extensions alone (PR #433 review).
func TestTeamSessionStartRacedTakenSurfacesForce(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions": `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"errors":[{"message":"This worker is taken.",
			"extensions":{"code":"WORKER_TAKEN","workerId":"wkr1","sessionId":"s-race",
			"lastDriver":"u-rufus","lastSeenAt":"2026-08-15T09:00:00Z"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	for _, want := range []string{"u-rufus", "2026-08-15T09:00:00Z", "s-race", "--force"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must carry %q from the extensions payload: %v", want, err)
		}
	}

	// A null lastDriver (unattributed session) must not render as an empty
	// hole — the payload's absent fields degrade to honest placeholders.
	gql2, _ := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions": `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"errors":[{"message":"This worker is taken.",
			"extensions":{"code":"WORKER_TAKEN","workerId":"wkr1","sessionId":"s-race",
			"lastDriver":null,"lastSeenAt":"2026-08-15T09:00:00Z"}}]}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql2.URL})
	err2 := root2.Execute()
	if err2 == nil || !strings.Contains(err2.Error(), "an unknown driver") {
		t.Errorf("a null lastDriver must degrade honestly: %v", err2)
	}
}

const bindingFixture = `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","appId":"app1","startedAt":"2026-08-11T10:00:00Z",
	"repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// A pre-#399 worker binding: no appId, but a recorded team memory — the
// worklog path falls back to resolving it.
const bindingPre399Fixture = `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","startedAt":"2026-08-11T10:00:00Z","appBound":true,
	"teamMemory":"hrn:mem:acme.com:eng-team","tool":"claude-code",
	"repo":"hadron-memory/hadron-cli","prNumbers":[]}`

const teamChatMsgJSON = `{"nodeId":"n8","seq":8,"body":"@rufus schema is live","at":"2026-08-12T10:00:00Z",
	"authorUserId":null,"authorWorkerId":"wkr1","authorName":"Iris","sessionId":"s-new",
	"replyToSeq":null,"mentions":["rufus"]}`

const teamChatHumanMsgJSON = `{"nodeId":"n9","seq":1,"body":"hi","at":"2026-08-12T10:00:00Z",
	"authorUserId":"u1","authorWorkerId":null,"authorName":"holger","sessionId":null,
	"replyToSeq":null,"mentions":[]}`

// #406: the two chat dialects name the author field differently, and reading
// with the wrong one returned null rather than erroring — a wrong field name
// yielding a plausible wrong answer. The canonical output carries BOTH, and
// separates a human post from a worker post in the transcript too.
func TestTeamChatReadEmitsAuthorAliasAndKind(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":2,"items":[` +
			teamChatHumanMsgJSON + `,` + teamChatMsgJSON + `]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Messages []struct {
			Author         *string `json:"author"`
			AuthorName     *string `json:"authorName"`
			AuthorUserID   *string `json:"authorUserId"`
			AuthorWorkerID *string `json:"authorWorkerId"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("read --json: %v (%s)", err, out.String())
	}
	if len(dto.Messages) != 2 {
		t.Fatalf("want 2 messages: %s", out.String())
	}
	for i, m := range dto.Messages {
		if m.Author == nil || m.AuthorName == nil || *m.Author != *m.AuthorName {
			t.Errorf("message %d: `author` must alias `authorName`, got %v / %v", i, m.Author, m.AuthorName)
		}
	}
	// The Worker envelope (#974) distinguishes the two, which the old single
	// `author` string could not.
	if dto.Messages[0].AuthorUserID == nil || dto.Messages[1].AuthorWorkerID == nil {
		t.Errorf("human vs worker must stay distinguishable: %s", out.String())
	}

	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Assert each label BESIDE its author — checking only that both strings
	// appear somewhere would still pass if the two branches were swapped
	// (PR #417 review). holger is the authorUserId post, Iris the worker one.
	if !strings.Contains(out2.String(), "holger (human)") {
		t.Errorf("a user-authored post must be marked (human): %s", out2.String())
	}
	if !strings.Contains(out2.String(), "Iris (worker)") {
		t.Errorf("a worker-authored post must be marked (worker): %s", out2.String())
	}
}

// A binding whose session was started with -m (team memory) and --tool.
const bindingWithTeamFixture = `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","appId":"app1","startedAt":"2026-08-11T10:00:00Z","appBound":true,
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
		WorkerName string `json:"workerName"`
		WorkerID   string `json:"workerId"`
		SessionID  string `json:"sessionId"`
	}
	_ = json.Unmarshal([]byte(out.String()), &dto)
	if dto.WorkerName != "Iris" || dto.WorkerID != "wkr1" || dto.SessionID != "s-new" {
		t.Errorf("whoami: %s", out.String())
	}
}

// A binding written by a pre-Worker CLI (agentId/personaName keys) is a
// DEGRADED read, not an error: the session id still resolves (and `session
// end` is the recovery), and whoami says the binding predates the model.
func TestTeamSessionWhoamiLegacyBindingDegrades(t *testing.T) {
	dir := teamGitDir(t)
	legacy := `{"sessionId":"s-old-model","agentId":"agt1","agentUrn":"hrn:agent:acme.com:iris",
		"personaName":"Iris","personaRole":"backend-engineer","startedAt":"2026-08-11T10:00:00Z","prNumbers":[]}`
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "whoami"})
	if err := root.Execute(); err != nil {
		t.Fatalf("a legacy binding must degrade, not error: %v", err)
	}
	if !strings.Contains(out.String(), "predates the Worker model") {
		t.Errorf("whoami should say the binding predates the model: %s", out.String())
	}
	if !strings.Contains(out.String(), "s-old-model") {
		t.Errorf("the session id is still good and must show: %s", out.String())
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
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Bare number → qualified by the binding's repo. No -m and no
	// TeamMemoryApp in the fake: the binding's appId IS the worklog home
	// (#399), so any resolution round trip fails the test loudly.
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
	// #396: the record goes through the dedicated operation, so the CLI sends
	// only what it knows (session, tool, kind, ref, action) — the server owns
	// the record shape, the `at` stamp, and the worker derivation. No
	// client-composed field map, and no generic object write.
	var workVars struct {
		AppRef     string `json:"appRef"`
		SessionRef string `json:"sessionRef"`
		Tool       string `json:"tool"`
		Kind       string `json:"kind"`
		Ref        string `json:"ref"`
		Action     string `json:"action"`
	}
	_ = json.Unmarshal(captured["RecordTeamWork"], &workVars)
	if workVars.AppRef != "app1" || workVars.SessionRef != "s-new" || workVars.Tool != "claude-code" ||
		workVars.Kind != "pr" || workVars.Ref != "hadron-memory/hadron-cli#371" || workVars.Action != "worked-on" {
		t.Errorf("recordTeamWork vars: %+v", workVars)
	}
	if _, wrote := captured["CreateObject"]; wrote {
		t.Errorf("the worklog must not be hand-written through the generic object surface")
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

// The --issue/--commit refusal is the second log diagnostic that once named
// `team init`; it must name only -m (PR #420 review). Since #399 it is
// reachable only through a binding that predates the App-recording CLI.
func TestTeamSessionLogIssueWithoutMemoryNamesOnlyM(t *testing.T) {
	dir := teamGitDir(t)
	legacy := `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
		"agentId":"agt1","startedAt":"2026-08-11T10:00:00Z","repo":"hadron-memory/hadron-cli","prNumbers":[]}`
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--issue", "12", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want Usage; err: %v", code, err)
	}
	if err == nil || strings.Contains(err.Error(), "team init") {
		t.Errorf("`team init` is not a precondition for worklog writes: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "-m") {
		t.Errorf("the refusal must name -m: %v", err)
	}
}

func TestTeamSessionLogBranch(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":"team-chat","prNumber":null,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"branch","ref":"hadron-memory/hadron-cli:team-chat","action":"pushed",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--branch", "team-chat", "--action", "pushed", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateTeamSession"], &vars)
	if vars["branch"] != "team-chat" {
		t.Errorf("branch denormalization: %v", vars)
	}
	if _, present := vars["prNumber"]; present {
		t.Errorf("a branch milestone must not touch prNumber, got %v", vars["prNumber"])
	}
	var workVars struct {
		Kind   string `json:"kind"`
		Ref    string `json:"ref"`
		Action string `json:"action"`
	}
	_ = json.Unmarshal(captured["RecordTeamWork"], &workVars)
	if workVars.Kind != "branch" || workVars.Ref != "hadron-memory/hadron-cli:team-chat" ||
		workVars.Action != "pushed" {
		t.Errorf("recordTeamWork vars: %+v", workVars)
	}
	if !strings.Contains(out.String(), `"recorded": "worklog"`) {
		t.Errorf("log output: %s", out.String())
	}
}

// The provenance query generalizes past --pr: --commit matches kind=commit.
// The worker name joins from the worklog row itself (workerName, #974) — no
// roster read.
func TestTeamSessionListProvenanceByCommit(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"TeamWorkItems": `{"data":{"teamWorkItems":{"total":1,"items":[
			{"nodeId":"w1","sessionId":"s-done","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"commit",
			 "ref":"hadron-memory/hadron-cli@93200b2","action":"pushed","at":"2026-08-13T10:00:00Z",
			 "detail":null}]}}}`,
		"GetTeamSession": `{"data":{"session":` + endedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--commit", "hadron-memory/hadron-cli@93200b2",
		"-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Ref  string `json:"ref"`
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(captured["TeamWorkItems"], &vars)
	if vars.Ref != "hadron-memory/hadron-cli@93200b2" || vars.Kind != "commit" {
		t.Errorf("worklog lookup: %+v", vars)
	}
	if !strings.Contains(out.String(), `"id": "s-done"`) ||
		!strings.Contains(out.String(), `"workerName": "Iris"`) {
		t.Errorf("provenance rows: %s", out.String())
	}
}

// An issue milestone sends the EMPTY updateSession — the #932 liveness
// touch — alongside the worklog write, so a driver logging only
// issues/commits is never reaped for inactivity; no denormalized field is
// sent (an explicit null would CLEAR prNumber/branch).
func TestTeamSessionLogIssueTouchesLiveness(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":null,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"issue","ref":"hadron-memory/hadron-cli#362","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--issue", "362", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateTeamSession"], &vars)
	if vars["id"] != "s-new" {
		t.Errorf("liveness touch must target the bound session: %v", vars)
	}
	for _, k := range []string{"prNumber", "branch"} {
		if _, present := vars[k]; present {
			t.Errorf("an issue milestone must send the EMPTY touch — %q present: %v", k, vars[k])
		}
	}
	var workVars struct {
		Kind string `json:"kind"`
		Ref  string `json:"ref"`
	}
	_ = json.Unmarshal(captured["RecordTeamWork"], &workVars)
	if workVars.Kind != "issue" || workVars.Ref != "hadron-memory/hadron-cli#362" {
		t.Errorf("recordTeamWork vars: %+v", workVars)
	}
}

// A modern binding needs no -m: the worklog write rides the recorded App.
// This is the #399 payoff — the degraded "recorded: session" path now needs
// a binding that predates the App-recording CLI (next test).
func TestTeamSessionLogPre399BindingResolvesTeamMemory(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingPre399Fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The pre-#399 binding has no appId, so its recorded team memory is
	// resolved — back-compat, not the modern path.
	var memVars map[string]any
	_ = json.Unmarshal(captured["TeamMemoryApp"], &memVars)
	if memVars["ref"] != "hrn:mem:acme.com:eng-team" {
		t.Errorf("a pre-#399 binding must fall back to its team memory: %v", memVars)
	}
	if !strings.Contains(out.String(), `"recorded": "worklog"`) {
		t.Errorf("log output: %s", out.String())
	}
}

// Only a binding that predates the App-recording CLI degrades: --pr falls to
// the Session.prNumber denormalization (recorded: "session", with a note),
// and an issue/commit milestone has nowhere durable to go, so it refuses.
func TestTeamSessionLogLegacyBindingDegrades(t *testing.T) {
	dir := teamGitDir(t)
	legacy := `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
		"agentId":"agt1","startedAt":"2026-08-11T10:00:00Z","repo":"hadron-memory/hadron-cli","prNumbers":[]}`
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
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
	// #414: the degraded note must not name `team init` as the remedy — the
	// original bad advice lived in BOTH log diagnostics, and only the start
	// warning was covered (PR #420 review).
	errOut := f.IOStreams.ErrOut.(*strings.Builder).String()
	if strings.Contains(errOut, "team init") {
		t.Errorf("`team init` is not what enables the worklog: %q", errOut)
	}
	if !strings.Contains(errOut, "-m") {
		t.Errorf("the note must name -m, the actual requirement: %q", errOut)
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
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
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
	if dto.Recorded != "worklog" {
		t.Errorf("the binding's App records to the worklog (#399): %s", out.String())
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
		"EndTeamSession": `{"data":{"endSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u-holger",
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
	if !strings.Contains(out.String(), `"workerName": "Iris"`) {
		t.Errorf("end output: %s", out.String())
	}
}

// The occupancy check must read past the first sessions page: an old
// still-active session can hide behind 200 newer ended ones, and stopping
// early would report the worker free (the issue-#23 failure mode). The pages
// are the WORKER's own (sessions(workerRef:), #974), but paging still applies.
func TestTeamSessionStartFindsActiveSessionOnLaterPage(t *testing.T) {
	teamGitDir(t)
	filler := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		filler = append(filler, fmt.Sprintf(`{"id":"e%d","agentId":"agt1","workerId":"wkr1","userId":"u1","type":"DEVELOPER",
			"repo":null,"branch":null,"prNumber":null,"startedAt":"2026-08-11T0%d:00:00Z",
			"endedAt":"2026-08-11T09:00:00Z","host":null,"tool":null,"transcriptPath":null,"llmModel":null}`, i, i%10))
	}
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Offset    *int    `json:"offset"`
				WorkerRef *string `json:"workerRef"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "GetWorker":
			_, _ = w.Write([]byte(`{"data":{"worker":` + irisWorkerJSON + `}}`))
		case "TeamSessions":
			if body.Variables.WorkerRef == nil || *body.Variables.WorkerRef != "wkr1" {
				t.Errorf("the activity check must filter by workerRef, got %v", body.Variables.WorkerRef)
			}
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
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", gql.URL})
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
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}

	gql, captured := captureGraphQL(t, map[string]string{
		"EndTeamSession": `{"data":{"endSession":{"id":"s-prev","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":null,
			"startedAt":"2026-08-11T08:00:00Z","endedAt":"2026-08-11T10:00:00Z","host":null,
			"tool":null,"transcriptPath":null,"llmModel":null}}}`,
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--force", "--json", "--server", gql.URL})
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
		"EndTeamSession": `{"data":{"endSession":{"id":"s-orphan","agentId":null,"workerId":null,"userId":"u1",
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
// the mutation would miss the real session and leave its worker held.
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
// silently dropped. Worker names come from the worklog rows themselves
// (workerName, #974) — no roster read.
func TestTeamSessionListProvenanceQuery(t *testing.T) {
	teamGitDir(t)
	// The worklog match must page to exhaustion: a full first page of s-done
	// milestones, then a tail page carrying s-hidden.
	fullPage := make([]string, 0, 200)
	for i := 0; i < 200; i++ {
		fullPage = append(fullPage, fmt.Sprintf(`{"nodeId":"w%d","sessionId":"s-done","workerId":"wkr1","workerName":"Iris",
			"tool":"github","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"opened",
			"at":"2026-08-13T10:00:00Z","detail":null}`, i))
	}
	var workItemVars json.RawMessage
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "TeamMemoryApp":
			_, _ = w.Write([]byte(`{"data":{"memory":{"id":"m1","appId":"app1"}}}`))
		case "TeamWorkItems":
			workItemVars = body.Variables
			var vars struct {
				Offset *int `json:"offset"`
			}
			_ = json.Unmarshal(body.Variables, &vars)
			if vars.Offset == nil || *vars.Offset == 0 {
				_, _ = w.Write([]byte(`{"data":{"teamWorkItems":{"total":201,"items":[` + strings.Join(fullPage, ",") + `]}}}`))
			} else {
				if *vars.Offset != 200 {
					t.Errorf("unexpected offset %d", *vars.Offset)
				}
				_, _ = w.Write([]byte(`{"data":{"teamWorkItems":{"total":201,"items":[
					{"nodeId":"w200","sessionId":"s-hidden","workerId":"wkr2","workerName":"Uma","tool":"github","kind":"pr",
					 "ref":"hadron-memory/hadron-cli#371","action":"opened","at":"2026-08-13T10:00:00Z",
					 "detail":null}]}}}`))
			}
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
	var lookup struct {
		AppRef string `json:"appRef"`
		Ref    string `json:"ref"`
		Kind   string `json:"kind"`
	}
	_ = json.Unmarshal(workItemVars, &lookup)
	// #396: the dedicated read addresses the App and filters server-side.
	// kind stays part of the lookup — PRs and issues share GitHub's number
	// space, so ref alone would mix artifact kinds.
	if lookup.Ref != "hadron-memory/hadron-cli#371" || lookup.Kind != "pr" || lookup.AppRef != "app1" {
		t.Errorf("worklog lookup: %+v", lookup)
	}
	var got []struct {
		ID         string  `json:"id"`
		StartedAt  string  `json:"startedAt"`
		WorkerName *string `json:"workerName"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	// Both sessions list (deduped across pages), the unreadable one as an
	// id-only stub — which still carries the worklog's worker name.
	if len(got) != 2 || got[0].ID != "s-done" || got[1].ID != "s-hidden" || got[1].StartedAt != "" {
		t.Errorf("provenance rows: %s", out.String())
	}
	if got[1].WorkerName == nil || *got[1].WorkerName != "Uma" {
		t.Errorf("the hidden session still carries the worklog's worker name: %s", out.String())
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

// #400: `team init --app` — no -m; the App resolves its own shared memory
// (App.sharedMemory, hadron-server#965) for the status pre-read, and the
// declaration lands wherever the server says (read back by id).
func TestTeamInitAppPath(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetAppSharedMemory": `{"data":{"app":{"id":"app1","sharedMemory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team",
			"class":"app","schema":{"objectTypes":{"worklog":{"fields":{"ref":{"type":"text","required":true}}}}}}}}}`,
		"UpdateTeamCollections": `{"data":{"updateTeamCollections":{"memoryId":"m1",
			"collections":["worklog"],"changed":false}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		AppRef string `json:"appRef"`
	}
	_ = json.Unmarshal(captured["UpdateTeamCollections"], &vars)
	if vars.AppRef != "acme.com:eng-team" {
		t.Errorf("the App addresses the convergence directly: %v", vars)
	}
	// Worklog already declared + unchanged ⇒ the three-value contract holds
	// on the App path; no memory-resolution round trips.
	if !strings.Contains(out.String(), `"status": "unchanged"`) {
		t.Errorf("init output: %s", out.String())
	}
	for _, op := range []string{"GetMemory", "TeamMemoryApp"} {
		if _, called := captured[op]; called {
			t.Errorf("the App path must not resolve memories via %s", op)
		}
	}

	// An App with NO shared memory yet: nothing declared by definition ⇒
	// status created, and the target is read back from the response.
	gql2, _ := captureGraphQL(t, map[string]string{
		"GetAppSharedMemory": `{"data":{"app":{"id":"app1","sharedMemory":null}}}`,
		"UpdateTeamCollections": `{"data":{"updateTeamCollections":{"memoryId":"m9",
			"collections":["worklog"],"changed":true}}}`,
		"GetMemory": `{"data":{"memory":{"id":"m9","urn":"hrn:mem:acme.com:eng-team","name":"Eng Team",
			"shortDescription":null,"description":null,"class":"app","visibility":"ORGANIZATION",
			"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
			"vectorIndexEnabled":false,"maxRevCount":10,"data":null,"schema":null,
			"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`,
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "init", "--app", "acme.com:eng-team", "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out2.String(), `"status": "created"`) ||
		!strings.Contains(out2.String(), "hrn:mem:acme.com:eng-team") {
		t.Errorf("fresh-App init output: %s", out2.String())
	}

	// Neither -m nor any App scope: an honest usage refusal.
	teamGitDir(t)
	f3, _ := testFactory(t)
	root3 := NewRootCmd(f3)
	root3.SetArgs([]string{"team", "init", "--server", "http://127.0.0.1:1"})
	if code := exitcode.FromError(root3.Execute()); code != exitcode.Usage {
		t.Errorf("no App, no -m: exit %d, want Usage", code)
	}
}

// #399: the provenance query from an unbound checkout takes --app as a
// first-class alternative to -m; from a bound worktree the binding's App is
// the default and no flag is needed.
func TestTeamSessionListProvenanceAppSources(t *testing.T) {
	worklogResp := `{"data":{"teamWorkItems":{"total":1,"items":[
		{"nodeId":"w1","sessionId":"s-done","workerId":"wkr1","workerName":"Iris","tool":"github","kind":"pr",
		 "ref":"hadron-memory/hadron-cli#371","action":"opened","at":"2026-08-13T10:00:00Z","detail":null}]}}}`

	// Unbound checkout + explicit --app: no memory resolution at all.
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamWorkItems":  worklogResp,
		"GetTeamSession": `{"data":{"session":` + endedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--pr", "hadron-memory/hadron-cli#371",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		AppRef string `json:"appRef"`
	}
	_ = json.Unmarshal(captured["TeamWorkItems"], &vars)
	if vars.AppRef != "acme.com:eng-team" {
		t.Errorf("--app must scope the worklog read directly: %v", vars)
	}

	// Bound worktree: the binding's appId is the default — no flags.
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql2, captured2 := captureGraphQL(t, map[string]string{
		"TeamWorkItems":  worklogResp,
		"GetTeamSession": `{"data":{"session":` + endedSessionJSON + `}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "session", "list", "--pr", "371", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars2 struct {
		AppRef string `json:"appRef"`
	}
	_ = json.Unmarshal(captured2["TeamWorkItems"], &vars2)
	if vars2.AppRef != "app1" {
		t.Errorf("the binding's App is the default worklog scope: %v", vars2)
	}
}

// #401: `team init` no longer OWNS the collection definition — it asks the
// server to converge the collections it owns. The CLI must not write the memory
// schema itself; its own copy of the definition had already drifted (its `kind`
// enum predated the `repo` kind), silently refusing a kind the server accepts.
func TestTeamInit(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory": `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team","name":"Eng Team",
			"shortDescription":null,"description":null,"class":"app","visibility":"ORGANIZATION",
			"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
			"vectorIndexEnabled":false,"maxRevCount":10,"data":null,
			"schema":{"objectTypes":{"competitor":{"fields":{"name":{"type":"text","required":true}}}}},
			"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`,
		"GetAppSharedMemory": `{"data":{"app":{"id":"app1","sharedMemory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team",
			"class":"app","schema":null}}}}`,
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"UpdateTeamCollections": `{"data":{"updateTeamCollections":{"memoryId":"m1",
			"collections":["worklog"],"changed":true}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The declaration goes through the server operation, addressed by App.
	var vars struct {
		AppRef string `json:"appRef"`
	}
	_ = json.Unmarshal(captured["UpdateTeamCollections"], &vars)
	if vars.AppRef != "app1" {
		t.Errorf("updateTeamCollections must address the App, got %q", vars.AppRef)
	}
	// The CLI must no longer write Memory.schema — that is what carried the
	// stale definition. Preservation of sibling collections is the server's
	// invariant now (hadron-server#958), not a client merge.
	if _, called := captured["UpdateMemory"]; called {
		t.Error("the CLI must not write the memory schema itself")
	}
	// No worklog declared before ⇒ "created"; collections come from the payload.
	if !strings.Contains(out.String(), `"status": "created"`) ||
		!strings.Contains(out.String(), `"worklog"`) {
		t.Errorf("init output: %s", out.String())
	}
}

// A memory whose declaration already matches reports the idempotent no-op.
func TestTeamInitIdempotent(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetMemory": `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team","name":"Eng Team",
			"shortDescription":null,"description":null,"class":"app","visibility":"ORGANIZATION",
			"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
			"vectorIndexEnabled":false,"maxRevCount":10,"data":null,
			"schema":{"objectTypes":{"worklog":{"fields":{"ref":{"type":"text","required":true}}}}},
			"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`,
		"GetAppSharedMemory": `{"data":{"app":{"id":"app1","sharedMemory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team",
			"class":"app","schema":{"objectTypes":{"worklog":{"fields":{"ref":{"type":"text","required":true}}}}}}}}}`,
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"UpdateTeamCollections": `{"data":{"updateTeamCollections":{"memoryId":"m1",
			"collections":["worklog"],"changed":false}}}`,
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

// The repair case #401 exists for: a declaration written by an older CLI is
// present but stale, and the server converges it. Reported as "updated", which
// is what distinguishes it from a first declaration.
func TestTeamInitConvergesADriftedDeclaration(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"GetMemory": `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team","name":"Eng Team",
			"shortDescription":null,"description":null,"class":"app","visibility":"ORGANIZATION",
			"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
			"vectorIndexEnabled":false,"maxRevCount":10,"data":null,
			"schema":{"objectTypes":{"worklog":{"fields":{"kind":{"type":"enum","required":true,"values":["pr","issue","commit","branch"]}}}}},
			"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`,
		"GetAppSharedMemory": `{"data":{"app":{"id":"app1","sharedMemory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team",
			"class":"app","schema":{"objectTypes":{"worklog":{"fields":{"kind":{"type":"enum","required":true,"values":["pr","issue","commit","branch"]}}}}}}}}}`,
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"UpdateTeamCollections": `{"data":{"updateTeamCollections":{"memoryId":"m1",
			"collections":["worklog"],"changed":true}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "-m", "acme.com::eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), `"status": "updated"`) {
		t.Errorf("a converged drifted declaration must report updated: %s", out.String())
	}
}

// #384: the worklog lives in the team App's OWN memory (D13/D14). Any other class
// is refused BEFORE the write — a system memory in particular is read-only
// from every App that runs it (cor:dmo:050:03), so declaring the collection
// there reports success on a setup that can never be written.
func TestTeamInitRefusesNonAppClassMemory(t *testing.T) {
	for _, tc := range []struct {
		class string
		want  string
	}{
		{"system", "read-only from every App"},
		{"knowledge", `is class "knowledge", not "app"`},
		{"group", `is class "group", not "app"`},
	} {
		t.Run(tc.class, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"GetMemory": `{"data":{"memory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team-system","name":"Eng Team System",
					"shortDescription":null,"description":null,"class":"` + tc.class + `","visibility":"ORGANIZATION",
					"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
					"vectorIndexEnabled":false,"maxRevCount":10,"data":null,"schema":null,
					"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "init", "-m", "acme.com::eng-team-system", "--server", gql.URL})
			err := root.Execute()
			if code := exitcode.FromError(err); code != exitcode.Usage {
				t.Errorf("exit code = %d, want Usage; err: %v", code, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error must name the class it got (want %q): %v", tc.want, err)
			}
			if _, called := captured["UpdateMemory"]; called {
				t.Error("a refused memory must not be written to")
			}
		})
	}
}

// `team chat post` is a THIN wrapper over the platform operation
// (hadron-server#939, Worker envelope #974): the App resolves from the
// binding's team memory, the bound session rides as sessionRef (the server
// derives the worker author and records the driving session, D16), and the
// CLI composes no node.
func TestTeamChatPostAsWorker(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatMsgJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "--body", "@rufus schema is live", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// #399: the binding's recorded App scopes the post directly — no
	// TeamMemoryApp in the fake, so a resolution round trip fails loudly.
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateTeamChatMessage"], &vars)
	if vars["appRef"] != "app1" || vars["body"] != "@rufus schema is live" || vars["sessionRef"] != "s-new" {
		t.Errorf("post vars: %v", vars)
	}
	if _, present := vars["replyToSeq"]; present {
		t.Errorf("unset replyToSeq must be omitted, got %v", vars["replyToSeq"])
	}
	var dto struct {
		Seq       int      `json:"seq"`
		SessionID *string  `json:"sessionId"`
		Mentions  []string `json:"mentions"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if dto.Seq != 8 || dto.SessionID == nil || *dto.SessionID != "s-new" ||
		len(dto.Mentions) != 1 || dto.Mentions[0] != "rufus" {
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
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamMemoryApp":         `{"data":{"memory":{"id":"mem1","appId":"app-1"}}}`,
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "hello positional", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateTeamChatMessage"], &vars)
	if vars["body"] != "hello positional" {
		t.Errorf("positional body: %v", vars["body"])
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

// Without a binding the post is authored by the calling HUMAN (the server
// supports both author paths, hadron-server#939) — but the App must then be
// resolvable, so no binding AND no --app is a usage error.
func TestTeamChatPostUnbound(t *testing.T) {
	teamGitDir(t)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "--body", "hi", "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("no binding, no app: exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}

	// With --app the human post goes through: appRef is the flag value (no
	// TeamMemoryApp lookup — it is not in the allowed operations) and
	// sessionRef is OMITTED.
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatHumanMsgJSON + `}}`,
	})
	f2, out := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "chat", "post", "--body", "hi", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateTeamChatMessage"], &vars)
	if vars["appRef"] != "acme.com:eng-team" {
		t.Errorf("post vars: %v", vars)
	}
	if _, present := vars["sessionRef"]; present {
		t.Errorf("a human post must omit sessionRef, got %v", vars["sessionRef"])
	}
	if !strings.Contains(out.String(), `"authorUserId": "u1"`) {
		t.Errorf("post output: %s", out.String())
	}
}

// --reply-to passes the seq straight through — the SERVER validates it
// (typed TEAM_CHAT_REPLY_NOT_FOUND) and wires the reply edge; the CLI no
// longer resolves seqs to locs. A non-numeric value is a usage error.
func TestTeamChatPostReplyToSeq(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamMemoryApp":         `{"data":{"memory":{"id":"mem1","appId":"app-1"}}}`,
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatMsgJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "--body", "done", "--reply-to", "5", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateTeamChatMessage"], &vars)
	if vars["replyToSeq"] != float64(5) {
		t.Errorf("replyToSeq: %v", vars["replyToSeq"])
	}

	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "chat", "post", "--body", "done", "--reply-to", "chat:messages:a", "--server", gql.URL})
	err := root2.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("non-numeric reply-to: exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
}

// Outside a git worktree there is no binding to read — with an App context
// the commands proceed as the unbound-human path instead of failing in
// `git rev-parse` (PR #382 review); without one, the worktree error stands.
func TestTeamChatOutsideWorktreeWithApp(t *testing.T) {
	t.Setenv("HADRON_TEAM_GIT_DIR", "")
	t.Chdir(t.TempDir()) // no .git anywhere above a fresh temp dir
	gql, captured := captureGraphQL(t, map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatHumanMsgJSON + `}}`,
		"TeamChatMessages":      `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "--body", "hi", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("post outside a worktree with --app must work: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CreateTeamChatMessage"], &vars)
	if vars["appRef"] != "acme.com:eng-team" {
		t.Errorf("post vars: %v", vars)
	}
	if _, present := vars["sessionRef"]; present {
		t.Errorf("no worktree means no session binding: %v", vars["sessionRef"])
	}

	f2, out := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("read outside a worktree with --app must work: %v", err)
	}
	if !strings.Contains(out.String(), `"nextSince": 0`) {
		t.Errorf("read output: %s", out.String())
	}

	// No App context: the worktree error is the actionable one.
	f3, _ := testFactory(t)
	root3 := NewRootCmd(f3)
	root3.SetArgs([]string{"team", "chat", "post", "--body", "hi", "--server", gql.URL})
	err := root3.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
}

// `--mentions <ref>` passes through RAW: the server resolves a worker id or
// NAME of this App, or a user handle/id, against the App's own staff and
// members only. No client-side resolution, and no ambiguity UX — mention
// tokens carry no uniqueness (hadron-server#979).
func TestTeamChatReadMentionsPassthrough(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamMemoryApp":    `{"data":{"memory":{"id":"mem1","appId":"app-1"}}}`,
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--mentions", "Iris", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["TeamChatMessages"], &vars)
	if vars["mentionsRef"] != "Iris" {
		t.Errorf("--mentions must pass through raw (the server resolves names): %v", vars["mentionsRef"])
	}
}

// The CLI retries NOTHING (thin-CLI directive): the server's typed team-chat
// refusals map to exit codes and surface verbatim.
func TestTeamChatPostServerRefusals(t *testing.T) {
	cases := []struct {
		name string
		resp string
		code int
		want string
	}{
		{"body too large is usage",
			`{"errors":[{"message":"body exceeds the team-chat cap of 65536 characters","extensions":{"code":"TEAM_CHAT_BODY_TOO_LARGE"}}]}`,
			exitcode.Usage, "65536"},
		{"missing reply target is not-found",
			`{"errors":[{"message":"replyToSeq 99 does not name a message in this team chat.","extensions":{"code":"TEAM_CHAT_REPLY_NOT_FOUND"}}]}`,
			exitcode.NotFound, "replyToSeq 99"},
		{"session not worker-bound surfaces verbatim",
			`{"errors":[{"message":"Session is not bound to a worker","extensions":{"code":"SESSION_NOT_WORKER_BOUND"}}]}`,
			exitcode.Error, "not bound to a worker"},
		{"retired worker is a conflict",
			`{"errors":[{"message":"Worker \"Iris\" is retired and no longer authors messages","extensions":{"code":"WORKER_RETIRED"}}]}`,
			exitcode.Conflict, "retired"},
		{"cross-app worker surfaces verbatim",
			`{"errors":[{"message":"the session's worker does not belong to this App","extensions":{"code":"SESSION_WORKER_NOT_IN_APP"}}]}`,
			exitcode.Error, "does not belong"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := teamGitDir(t)
			if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
				t.Fatal(err)
			}
			gql, _ := captureGraphQL(t, map[string]string{
				"TeamMemoryApp":         `{"data":{"memory":{"id":"mem1","appId":"app-1"}}}`,
				"CreateTeamChatMessage": tc.resp,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "chat", "post", "--body", "hi", "--server", gql.URL})
			err := root.Execute()
			if code := exitcode.FromError(err); code != tc.code {
				t.Errorf("exit code = %d, want %d; err: %v", code, tc.code, err)
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message must surface verbatim (want %q): %v", tc.want, err)
			}
		})
	}
}

// --mentions-me maps to the SERVER-side mentions filter (mentionsRef = the
// bound WORKER's id), matching the tokens extracted at write time — the CLI
// never re-parses bodies. nextSince advances past the returned messages.
func TestTeamChatReadMentionsMe(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":2,"items":[
			{"nodeId":"n5","seq":5,"body":"@iris ping","at":"t1","authorUserId":"u2","authorWorkerId":null,"authorName":"rufus","sessionId":null,"replyToSeq":null,"mentions":["iris"]},
			{"nodeId":"n7","seq":7,"body":"@iris again","at":"t3","authorUserId":"u2","authorWorkerId":null,"authorName":"rufus","sessionId":null,"replyToSeq":5,"mentions":["iris"]}]}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--mentions-me", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["TeamChatMessages"], &vars)
	if vars["appRef"] != "app1" || vars["mentionsRef"] != "wkr1" {
		t.Errorf("read vars: %v", vars)
	}
	var dto struct {
		Messages []struct {
			Seq        int  `json:"seq"`
			ReplyToSeq *int `json:"replyToSeq"`
		} `json:"messages"`
		NextSince int `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(dto.Messages) != 2 || dto.Messages[0].Seq != 5 || dto.Messages[1].Seq != 7 {
		t.Errorf("messages: %s", out.String())
	}
	if dto.NextSince != 7 {
		t.Errorf("nextSince = %d, want 7", dto.NextSince)
	}

	// --mentions-me and --mentions together is ambiguous.
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "chat", "read", "--mentions-me", "--mentions", "wkr9", "--server", gql.URL})
	err := root2.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("both mention flags: exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
}

// --active filters client-side (no server-side filter exists) and the worker
// name is joined from the NESTED Session.worker (#980/#432) — no per-row
// GetWorker fan-out (its absence from the fake makes any lookup call fail).
func TestTeamSessionListActive(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamSessions": `{"data":{"sessions":[` + activeSessionJSON + `,` + endedSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--active", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got []struct {
		ID         string  `json:"id"`
		WorkerName *string `json:"workerName"`
		Active     bool    `json:"active"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(got) != 1 || got[0].ID != "s-old" || !got[0].Active {
		t.Errorf("active list: %s", out.String())
	}
	if got[0].WorkerName == nil || *got[0].WorkerName != "Iris" {
		t.Errorf("worker join: %s", out.String())
	}
}

// --as narrows SERVER-side via sessions(workerRef:) (#974) — no client-side
// scan of the whole list.
func TestTeamSessionListAsFiltersServerSide(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":    `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions": `{"data":{"sessions":[` + endedSessionJSON + `]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "list", "--as", "wkr1", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["TeamSessions"], &vars)
	if vars["workerRef"] != "wkr1" {
		t.Errorf("--as must ride as the workerRef filter: %v", vars)
	}
	if !strings.Contains(out.String(), `"workerName": "Iris"`) {
		t.Errorf("worker join: %s", out.String())
	}
}

// Without -m and without an App context there is no appRef to send: `appRef`
// must be OMITTED, never sent as null — the server then derives the session's
// App from the WORKER. workerRef itself arrived via a schema refresh, and a
// refreshed input field does not inherit the operation's omitempty (the
// PR-#139 contentType trap); this assertion makes the next refresh that drops
// the directive fail loudly instead of silently clearing fields.
func TestTeamSessionStartWithoutMemoryOmitsAppRef(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":        `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamSessions":     `{"data":{"sessions":[` + endedSessionJSON + `]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "wkr1", "--tool", "claude-code",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars struct {
		Input map[string]any `json:"input"`
	}
	_ = json.Unmarshal(captured["StartTeamSession"], &vars)
	if _, present := vars.Input["appRef"]; present {
		t.Errorf("unset appRef must be omitted from SessionInput, got %v", vars.Input["appRef"])
	}
	if vars.Input["workerRef"] != "wkr1" {
		t.Errorf("workerRef must be sent: %v", vars.Input)
	}
	if _, called := captured["TeamMemoryApp"]; called {
		t.Errorf("no -m means no App resolution call")
	}
}

// `session log -m <memory>` overrides the binding's worklog home, so the App
// the record lands in must come from THAT memory — not from the binding's, and
// not from an ambient --app context. Resolving via the App-context helper let
// an explicit override record against the wrong App (Codex P1 on PR #409).
func TestTeamSessionLogMemoryOverrideResolvesItsOwnApp(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"TeamMemoryApp": `{"data":{"memory":{"id":"m2","appId":"other-app"}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "-m", "acme.com::other-team",
		"--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var memVars struct {
		Ref string `json:"ref"`
	}
	_ = json.Unmarshal(captured["TeamMemoryApp"], &memVars)
	if memVars.Ref != "hrn:mem:acme.com:other-team" {
		t.Errorf("the App must be resolved from the -m memory, got %q", memVars.Ref)
	}
	var workVars struct {
		AppRef string `json:"appRef"`
	}
	_ = json.Unmarshal(captured["RecordTeamWork"], &workVars)
	if workVars.AppRef != "other-app" {
		t.Errorf("record must target the overridden memory's App, got %q", workVars.AppRef)
	}
}

// A worker session binds to the WORKER's App, so SESSION_NOT_IN_APP on the
// worklog write means -m named a DIFFERENT App's memory — a mismatch to fix,
// not a session to restart. The CLI must say so rather than surfacing the
// bare typed code (Codex P1 lineage, PR #409).
func TestTeamSessionLogMismatchedMemoryExplainsMismatch(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"RecordTeamWork": `{"errors":[{"message":"Session \"s-new\" is not a session of this App",
			"extensions":{"code":"SESSION_NOT_IN_APP"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("mismatched memory: exit %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "different App") {
		t.Errorf("the error must name the mismatch, got: %v", err)
	}
}

// A binding written by a pre-Worker CLI may name a session that was never
// App-bound, where `-m` can never work — the remedy is end-and-restart, not
// "pass the right memory" (PR #431 review).
func TestTeamSessionLogLegacyBindingNotInAppSaysRestart(t *testing.T) {
	dir := teamGitDir(t)
	legacy := `{"sessionId":"s-old-model","agentId":"agt1","personaName":"Iris","startedAt":"2026-08-11T10:00:00Z","prNumbers":[]}`
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-old-model","agentId":"agt1","workerId":null,"userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"TeamMemoryApp": `{"data":{"memory":{"id":"m1","appId":"app1"}}}`,
		"RecordTeamWork": `{"errors":[{"message":"Session is not a session of this App",
			"extensions":{"code":"SESSION_NOT_IN_APP"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "log", "--pr", "hadron-memory/hadron-cli#371",
		"-m", "acme.com::eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Usage {
		t.Errorf("exit code = %d, want %d (Usage); err: %v", code, exitcode.Usage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "session end") {
		t.Errorf("a legacy binding's remedy is end-and-restart: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "different App") {
		t.Errorf("the mismatch message is for worker sessions only: %v", err)
	}
}

// An App may hold several app-class memories, but the collections belong to its
// ONE team shared memory, which the server resolves from the App. If -m names a
// different app-class memory, the declaration lands elsewhere — report where it
// actually landed rather than echoing what was asked for (Codex P2 on PR #413).
func TestTeamInitReportsWhereTheDeclarationLanded(t *testing.T) {
	calls := 0
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		switch body.OperationName {
		case "GetMemory":
			calls++
			// Only the -m-named memory is fetched (the class check); the
			// status pre-read and the target URN come from App.sharedMemory,
			// so no readback round trip is needed.
			_, _ = w.Write([]byte(`{"data":{"memory":{"id":"m-other","urn":"hrn:mem:acme.com:other-app-mem","name":"M",
				"shortDescription":null,"description":null,"class":"app","visibility":"ORGANIZATION",
				"organizationId":"o1","isEncrypted":false,"tags":[],"source":null,"syncStatus":"NONE",
				"vectorIndexEnabled":false,"maxRevCount":10,"data":null,"schema":null,
				"createdAt":"2026-08-11T00:00:00Z","updatedAt":"2026-08-11T00:00:00Z"}}}`))
		case "GetAppSharedMemory":
			_, _ = w.Write([]byte(`{"data":{"app":{"id":"app1","sharedMemory":{"id":"m-team",
				"urn":"hrn:mem:acme.com:eng-team-shared","class":"app","schema":null}}}}`))
		case "TeamMemoryApp":
			_, _ = w.Write([]byte(`{"data":{"memory":{"id":"m-other","appId":"app1"}}}`))
		case "UpdateTeamCollections":
			_, _ = w.Write([]byte(`{"data":{"updateTeamCollections":{"memoryId":"m-team",
				"collections":["worklog"],"changed":true}}}`))
		default:
			t.Errorf("unexpected operation %q", body.OperationName)
			_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
		}
	}))
	t.Cleanup(gql.Close)

	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "-m", "acme.com::other-app-mem", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The reported memory is where the server declared, not what -m named.
	if !strings.Contains(out.String(), "hrn:mem:acme.com:eng-team-shared") {
		t.Errorf("must report the memory actually declared on: %s", out.String())
	}
	if strings.Contains(out.String(), "other-app-mem") {
		t.Errorf("must not echo the requested memory as the target: %s", out.String())
	}
	// The status basis and the target URN come from the App's shared memory
	// (PR #437 review) — the named memory is fetched once, for its class
	// check, and never read back.
	if calls != 1 {
		t.Errorf("expected exactly one GetMemory (the -m class check), got %d", calls)
	}
}
