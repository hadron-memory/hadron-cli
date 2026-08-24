package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
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

// The readable identity of the team App (#458) — what turns the `app1` the
// ambient resolution hands back into something a reader can act on.
const teamAppIdentityJSON = `{"data":{"app":{"id":"app1","urn":"hrn:app:acme.com:eng-team","name":"Eng Team"}}}`

func TestTeamWorkerCast(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CastWorker": `{"data":{"castWorker":` + irisWorkerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--app", "acme.com:eng-team",
		"--role", "backend-engineer", "--name", "Iris", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CastWorker"], &vars)
	if vars["appRef"] != "acme.com:eng-team" || vars["role"] != "backend-engineer" || vars["name"] != "Iris" {
		t.Errorf("cast vars: %v", vars)
	}
	// Unset optionals are OMITTED, never null: no agent means the role picks
	// it. teamAgentRef is not in the operation at all now (hadron-cli#496), so
	// its presence would mean the removed argument came back.
	for _, k := range []string{"agentRef", "teamAgentRef", "promptOverride"} {
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

// --name, --agent and --prompt-override pass through verbatim; the resolved
// boot briefing (Worker.prompt) prints on the human path.
func TestTeamWorkerCastExplicitNameAndBriefing(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CastWorker": `{"data":{"castWorker":` + irisWorkerJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--app", "acme.com:eng-team",
		"--agent", "hrn:agent:acme.com:backend", "--name", "Iris",
		"--prompt-override", "You keep the release calm.", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CastWorker"], &vars)
	if vars["agentRef"] != "hrn:agent:acme.com:backend" || vars["name"] != "Iris" ||
		vars["promptOverride"] != "You keep the release calm." {
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
	gql, captured := captureGraphQL(t, map[string]string{"WorkersRoster": staffJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	// The ROSTER operation, not Workers (#459) — worker list stopped pulling
	// every worker's briefing, and this is the assertion that says so.
	_ = json.Unmarshal(captured["WorkersRoster"], &vars)
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

// #458: the staff table names the App it is listing AND where that scope came
// from. The source is the load-bearing half — the fallback chain is silent, so
// the same bare command in two worktrees bound to different teams prints
// different staff with visually identical output.
func TestTeamWorkerListNamesTheResolvedAppAndItsSource(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":         staffJSON,
		"WorkersRoster":   staffJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "app: hrn:app:acme.com:eng-team — Eng Team (from --app)") {
		t.Errorf("the scope line must name the App and its source: %s", out.String())
	}
	var idVars map[string]any
	_ = json.Unmarshal(captured["TeamAppIdentity"], &idVars)
	if idVars["appRef"] != "acme.com:eng-team" {
		t.Errorf("the identity read must use the resolved ref: %v", idVars)
	}
	// The worker URN replaces AGENT ID: addressable and readable (the App slug
	// is in it), where AGENT ID was an opaque id nobody acts on.
	if !strings.Contains(out.String(), "URN") ||
		!strings.Contains(out.String(), "hrn:worker:acme.com:eng-team:iris") {
		t.Errorf("the staff table must carry each worker's URN: %s", out.String())
	}
	if strings.Contains(out.String(), "AGENT ID") {
		t.Errorf("AGENT ID was the weakest column and is dropped: %s", out.String())
	}

	// An EMPTY staff is the sharpest case for the scope line: "no workers" and
	// "pointed at the wrong App" look identical without it.
	gql2, _ := captureGraphQL(t, map[string]string{
		"Workers":         `{"data":{"workers":{"total":0,"items":[]}}}`,
		"WorkersRoster":   `{"data":{"workers":{"total":0,"items":[]}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out2.String(), "app: hrn:app:acme.com:eng-team — Eng Team (from --app)") {
		t.Errorf("an empty staff must still say which App it found nobody in: %s", out2.String())
	}
}

// The hazard case: no --app, so the scope came from the worktree binding —
// which is exactly the resolution a reader cannot see. It reports itself, and
// the binding's raw AppID renders as the App's URN rather than a UUID.
func TestTeamWorkerListReportsBindingAsTheAppSource(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatalf("write binding: %v", err)
	}
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":         staffJSON,
		"WorkersRoster":   staffJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "app: hrn:app:acme.com:eng-team — Eng Team (from the worktree binding)") {
		t.Errorf("an ambient binding scope must say so: %s", out.String())
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["WorkersRoster"], &vars)
	if vars["appRef"] != "app1" {
		t.Errorf("the staff scan still uses the binding's AppID: %v", vars)
	}
}

// The scope line is a RENDER, so it stays out of --json: the shape is the bare
// array it has always been, every documented key intact, and the decorating
// read is not even issued (--json carries appId; a consumer resolves it).
func TestTeamWorkerListJSONShapeUnchangedByScopeLine(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":         staffJSON,
		"WorkersRoster":   staffJSON,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["TeamAppIdentity"]; called {
		t.Error("--json must not pay for a render-only identity read")
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("--json must stay a bare array: %v (%s)", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows: %s", out.String())
	}
	for _, k := range []string{"id", "urn", "slug", "appId", "agentId", "name", "role"} {
		if _, ok := rows[0][k]; !ok {
			t.Errorf("--json key %q must survive the render change: %s", k, out.String())
		}
	}
}

// describeApp decorates a render; it never gates one. An App record the caller
// cannot read must not turn a working staff listing into an error — the line
// degrades to the ref already in hand.
func TestTeamWorkerListSurvivesUnreadableApp(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"Workers":         staffJSON,
		"WorkersRoster":   staffJSON,
		"TeamAppIdentity": `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an unreadable App record must not fail the staff read: %v", err)
	}
	if !strings.Contains(out.String(), "app: acme.com:eng-team (from --app)") {
		t.Errorf("the scope line must fall back to the raw ref: %s", out.String())
	}
	if !strings.Contains(out.String(), "Iris") {
		t.Errorf("the staff must still render: %s", out.String())
	}
}

// #459: the roster stops carrying every worker's resolved briefing. This is a
// BREAKING --json change, taken deliberately while the surface is young, so it
// is pinned from three sides: the key is gone from list, still present on get,
// and the boot-briefing path is untouched.
func TestTeamWorkerListOmitsThePromptButGetKeepsIt(t *testing.T) {
	teamGitDir(t) // isolate from the developer's real worktree binding
	gql, _ := captureGraphQL(t, map[string]string{
		"WorkersRoster": staffJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "list", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.String()), &rows); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows: %s", out.String())
	}
	// OMITTED, not nulled. A null would preserve the shape while handing a
	// reader who wanted the briefing nothing — a wrong answer that looks like
	// an answer, which the issue called out as worse than an honest break.
	if _, present := rows[0]["prompt"]; present {
		t.Errorf("`prompt` must be ABSENT from a roster row, not null: %s", out.String())
	}
	// Everything else a roster reader needs survives, promptOverride included:
	// it is the short per-worker individuality, not the composed briefing.
	for _, k := range []string{"id", "urn", "slug", "appId", "agentId", "name", "role",
		"promptOverride", "memoryId", "retiredAt", "retiredBy", "createdAt", "createdBy", "retired"} {
		if _, ok := rows[0][k]; !ok {
			t.Errorf("roster key %q must survive the trim: %s", k, out.String())
		}
	}
	// And the roster never asks the server for the prompt in the first place —
	// the saving is on the wire, not just in the render.
	//
	// Asserted against the GENERATED OPERATION, not captured["WorkersRoster"]
	// (PR #491 review, found independently by both bots). captureGraphQL
	// records only the request VARIABLES — it discards the query document — so
	// searching it for "prompt" could never fail, whatever the projection
	// selected. That is a guard that cannot fail, dressed as the central
	// regression test for this change.
	//
	// Field-exact, not substring: `promptOverride` legitimately stays on the
	// roster and contains "prompt", so a Contains check would now false-POSITIVE
	// where it used to false-negative.
	for _, line := range strings.Split(gen.WorkersRoster_Operation, "\n") {
		if strings.TrimSpace(line) == "prompt" {
			t.Errorf("the roster query must not select prompt:\n%s", gen.WorkersRoster_Operation)
		}
	}

	// `worker get` is the prompt surface, unchanged.
	gql2, _ := captureGraphQL(t, map[string]string{
		"GetWorker":       `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "worker", "get", "wkr1", "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out2.String(), `"prompt": "You are Iris."`) {
		t.Errorf("worker get must still carry the resolved briefing: %s", out2.String())
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
		"GetWorker":       `{"data":{"worker":` + legacy + `}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
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

// #458: `worker get` named its App as a UUID in parentheses. It gets its own
// line, rendered readably — and no source phrase, because this App is the
// WORKER's own off the row, not an ambient scope that could have been wrong.
func TestTeamWorkerGetNamesTheApp(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"GetWorker":       `{"data":{"worker":` + irisWorkerJSON + `}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "wkr1", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "\n  app: hrn:app:acme.com:eng-team — Eng Team\n") {
		t.Errorf("the App must be named, not spelled as its UUID: %s", out.String())
	}
	if strings.Contains(out.String(), "(app app1)") {
		t.Errorf("the old parenthesised UUID is gone: %s", out.String())
	}
	var idVars map[string]any
	_ = json.Unmarshal(captured["TeamAppIdentity"], &idVars)
	if idVars["appRef"] != "app1" {
		t.Errorf("the identity read must use the worker's own appId: %v", idVars)
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
		"Workers":       staffJSON,
		"WorkersRoster": staffJSON,
		"GetWorker":     `{"data":{"worker":null}}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "Nadia", "--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.NotFound {
		t.Errorf("exit code = %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
}

// #464: a NAME with no App scope must get the designed remedy, not the raw
// wire error. The id lookup is the only lookup available, and a name-shaped
// argument is never a valid id, so the server always errors — which made the
// helpful message unreachable in exactly the case it was written for, and
// leaked `input:3: worker Worker not found.` instead. Onboarding path: a shell
// in the wrong directory is the permanent state of someone who has set nothing
// up yet.
func TestTeamWorkerGetByNameWithoutAppNamesTheRemedy(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"GetWorker": `{"errors":[{"message":"Worker not found.",
			"extensions":{"code":"WORKER_NOT_FOUND","workerRef":"Jonas"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "get", "Jonas", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.NotFound {
		t.Fatalf("exit code = %d, want %d (NotFound); err: %v", code, exitcode.NotFound, err)
	}
	// Names what was typed, and covers BOTH readings of the argument. A stale
	// id returns the same WORKER_NOT_FOUND as a name with no scope, and --app
	// cannot make a nonexistent id resolve — so advising only --app would
	// misdirect every id-based caller of this shared helper (PR #483 review).
	for _, want := range []string{
		`no worker "Jonas"`,
		"if that is a NAME", // the name reading: pass --app
		"pass --app <ref>",
		"an id or URN", // the id reading: scope will not help
		"nothing by that ref exists",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "input:3") || strings.Contains(err.Error(), "Worker not found.") {
		t.Errorf("the raw wire error must not leak:\n%s", err)
	}
}

// A worker-id lookup failure must surface as ITSELF, not as a fabricated
// not-found: without an App scope the id lookup is the only lookup, so an
// auth/transport error reading as "no worker" would make an outage look like
// missing data (PR #431 review). #464 narrowed the passthrough to non-
// WORKER_NOT_FOUND codes; this is the case that must still pass through.
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
		"Workers":       staffJSON,
		"WorkersRoster": staffJSON,
		"RetireWorker":  `{"data":{"retireWorker":` + retired + `}}`,
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
		"Workers":       staffJSON,
		"WorkersRoster": staffJSON,
		"DeleteWorker":  `{"data":{"deleteWorker":true}}`,
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
		"Workers":       staffJSON,
		"WorkersRoster": staffJSON,
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
	 "roleAgent":{"id":"agt1","urn":"hrn:agent:acme.com:backend","name":"backend-engineer","personaRole":"backend-engineer"},
	 "hasNamePlaceholder":true},
	{"role":"qa","loc":"roles:qa","nodeId":"n-qa","description":null,
	 "roleAgent":null,"hasNamePlaceholder":false}]}}}`

// #403: the pre-cast read — registers with server-computed taken/free, in
// allocation order. The free/taken verdicts are server truths (judged
// against the App's FULL roster), never recomputed client-side.
func TestTeamRoleList(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON})
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
		Role          string  `json:"role"`
		Description   *string `json:"description"`
		RoleAgentName *string `json:"roleAgentName"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if len(got) != 2 || got[0].Role != "backend-engineer" || got[1].Role != "qa" {
		t.Errorf("roles: %s", out.String())
	}
	if got[0].RoleAgentName == nil || *got[0].RoleAgentName != "backend-engineer" {
		t.Errorf("the role agent is what a role-mode cast resolves: %s", out.String())
	}
	// The register keys are GONE rather than emitted empty (hadron-cli#496):
	// a permanent `[]`/0 would keep promising an allocation surface the server
	// no longer has, which is a worse contract than a breaking change.
	var raw []map[string]any
	_ = json.Unmarshal([]byte(out.String()), &raw)
	for _, gone := range []string{"register", "freeCount", "exhausted", "nameRange", "nameConvention"} {
		if _, present := raw[0][gone]; present {
			t.Errorf("%q describes the removed name register: %s", gone, out.String())
		}
	}

	// Human table, and the nameless-template warning that survived.
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "role", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"ROLE", "backend-engineer", "never binds {{name}}"} {
		if !strings.Contains(out2.String(), want) {
			t.Errorf("table must carry %q: %s", want, out2.String())
		}
	}
	// The register columns are gone from the table too.
	for _, gone := range []string{"REGISTER", "FREE", "RANGE", "exhausted", "Iris✓"} {
		if strings.Contains(out2.String(), gone) {
			t.Errorf("the table still renders the removed register (%q): %s", gone, out2.String())
		}
	}
}

func TestTeamRoleGet(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Case-insensitive, like the server's own name matching.
	root.SetArgs([]string{"team", "role", "get", "Backend-Engineer", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{
		"backend-engineer (roles:backend-engineer)",
		"description: Backend role",
		"backend-engineer (hrn:agent:acme.com:backend)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("get output must carry %q: %s", want, out.String())
		}
	}
	// The register block is gone (hadron-cli#496), including the conventions
	// it was validated against.
	for _, gone := range []string{"register (", "held by", "— free", "conventions:", "range F-J"} {
		if strings.Contains(out.String(), gone) {
			t.Errorf("get still renders the removed register (%q): %s", gone, out.String())
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

// role create is one server-validated call. Once the register went
// (hadron-server#1050) the only field a client sets is the description, so the
// flags that carried the register — --names, --name-range, --name-convention,
// --allow-out-of-range — are gone with it.
func TestTeamRoleCreate(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamAppIdentity": teamAppIdentityJSON,
		"CreateTeamRole": `{"data":{"createTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
			"description":"Go services","roleAgent":null,"hasNamePlaceholder":null}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "create", "backend-engineer", "--description", "Go services",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(captured["CreateTeamRole"], &raw)
	if raw["role"] != "backend-engineer" || raw["description"] != "Go services" {
		t.Errorf("create vars: %v", raw)
	}
	// An unset optional is OMITTED, never null (CLAUDE.md wire semantics). The
	// register arguments are not in the operation at all now; their presence
	// would mean the removed surface came back.
	for _, k := range []string{"teamAgentRef", "names", "nameRange", "nameConvention", "allowOutOfRange"} {
		if _, present := raw[k]; present {
			t.Errorf("%q must not be on the wire, got %v", k, raw[k])
		}
	}
	if !strings.Contains(out.String(), "✓ created role backend-engineer — Go services") {
		t.Errorf("receipt: %s", out.String())
	}
	// A create with no description is legal — the role name is the whole
	// definition — so description must be omitted rather than sent empty.
	gql2, captured2 := captureGraphQL(t, map[string]string{
		"TeamAppIdentity": teamAppIdentityJSON,
		"CreateTeamRole": `{"data":{"createTeamRole":{"role":"qa","loc":"roles:qa","nodeId":"n-qa",
			"description":null,"roleAgent":null,"hasNamePlaceholder":null}}}`,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "role", "create", "qa", "--app", "acme.com:eng-team", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var raw2 map[string]any
	_ = json.Unmarshal(captured2["CreateTeamRole"], &raw2)
	if _, present := raw2["description"]; present {
		t.Errorf("unset description must be omitted, got %v", raw2["description"])
	}
}

// role update sets the description and nothing else. An update that names no
// field is refused rather than sent: omitted is "preserve" on this server, so
// the write would be a no-op reporting success.
func TestTeamRoleUpdateDescription(t *testing.T) {
	updated := `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer","nodeId":"n-be",
		"description":"Go services and the GraphQL API","roleAgent":null,"hasNamePlaceholder":null}}}`
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamRoles":          teamRolesJSON,
		"TeamAppIdentity":    teamAppIdentityJSON,
		"UpdateTeamRoleMeta": updated,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// Case-insensitive on the way in; the SERVER's spelling on the wire.
	root.SetArgs([]string{"team", "role", "update", "Backend-Engineer",
		"--description", "Go services and the GraphQL API", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["UpdateTeamRoleMeta"], &vars)
	if vars["role"] != "backend-engineer" || vars["description"] != "Go services and the GraphQL API" {
		t.Errorf("update vars: %v", vars)
	}
	for _, gone := range []string{"names", "nameRange", "nameConvention", "expectedNames", "allowOutOfRange"} {
		if _, present := vars[gone]; present {
			t.Errorf("%q must not be on the wire, got %v", gone, vars[gone])
		}
	}
	if !strings.Contains(out.String(), "✓ updated role backend-engineer") {
		t.Errorf("receipt: %s", out.String())
	}

	// Nothing to update is refused BEFORE the wire.
	gql2, captured2 := captureGraphQL(t, map[string]string{
		"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON,
	})
	f2, _ := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "role", "update", "backend-engineer", "--app", "acme.com:eng-team", "--server", gql2.URL})
	if code := exitcode.FromError(root2.Execute()); code != exitcode.Usage {
		t.Errorf("a no-field update must be Usage, got exit %d", code)
	}
	if _, called := captured2["UpdateTeamRoleMeta"]; called {
		t.Error("a no-field update must not reach the server")
	}
}

// `names rm` deliberately matches LITERALLY — quoting a comma-bearing entry
// is how a register corrupted by an older CLI is repaired (#442).
// #468: the role group resolves its App through the same silent chain as the
// worker surfaces did — but five of its seven call sites WRITE, so both the
// reads and the receipts have to say which team they landed in.
func TestTeamRoleScopeLineOnReadsAndReceipts(t *testing.T) {
	const wantApp = "hrn:app:acme.com:eng-team — Eng Team (from --app)"

	t.Run("list opens with the App and its source", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "role", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.HasPrefix(out.String(), "app: "+wantApp+"\n") {
			t.Errorf("the scope line must lead the table: %s", out.String())
		}
	})

	t.Run("an empty App still says which App is empty", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"TeamRoles":       `{"data":{"teamRoles":{"total":0,"items":[]}}}`,
			"TeamAppIdentity": teamAppIdentityJSON,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "role", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(out.String(), "app: "+wantApp) {
			t.Errorf("no roles and wrong team must not look identical: %q", out.String())
		}
	})

	t.Run("get names the App", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"TeamRoles": teamRolesJSON, "TeamAppIdentity": teamAppIdentityJSON,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "role", "get", "backend-engineer", "--app", "acme.com:eng-team", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(out.String(), "app: "+wantApp) {
			t.Errorf("role get must name the App: %s", out.String())
		}
	})

	t.Run("a write's receipt names where it landed", func(t *testing.T) {
		gql, _ := captureGraphQL(t, map[string]string{
			"TeamRoles":       teamRolesJSON,
			"TeamAppIdentity": teamAppIdentityJSON,
			"UpdateTeamRoleMeta": `{"data":{"updateTeamRole":{"role":"backend-engineer","loc":"roles:backend-engineer",
				"nodeId":"n-be","description":"Go services","roleAgent":null,"hasNamePlaceholder":null}}}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "role", "update", "backend-engineer", "--description", "Go services",
			"--app", "acme.com:eng-team", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		// An edit landing in another team is invisible without this, and the
		// receipt is the one thing the operator is guaranteed to read.
		if !strings.Contains(out.String(), "  app: "+wantApp) {
			t.Errorf("the write receipt must name the App: %s", out.String())
		}
		if !strings.Contains(out.String(), "✓ updated role backend-engineer") {
			t.Errorf("the existing receipt must survive: %s", out.String())
		}
	})
}

// The scope line is render-only, so an agent path pays nothing for it: no
// human branch runs and no identity read is issued. `role rm --yes --json` is
// the strict case — the prompt is skipped too, and composing it eagerly (it is
// an ARGUMENT to Confirm) would fire the read anyway.
func TestTeamRoleJSONIssuesNoIdentityRead(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"list", []string{"team", "role", "list"}},
		{"rm --yes", []string{"team", "role", "rm", "svelte-app-engineer", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gql, captured := captureGraphQL(t, map[string]string{
				"TeamRoles":       teamRolesRmJSON,
				"TeamAppIdentity": teamAppIdentityJSON,
				"DeleteTeamRole":  `{"data":{"deleteTeamRole":{"role":"svelte-app-engineer","nodesDeleted":1}}}`,
			})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs(append(tc.args, "--app", "acme.com:eng-team", "--json", "--server", gql.URL))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if _, called := captured["TeamAppIdentity"]; called {
				t.Error("--json must not pay for a render-only identity read")
			}
		})
	}
}

// describeApp decorates a render and never gates one: an App record the caller
// cannot read must not turn a working role listing into an error.
func TestTeamRoleListSurvivesUnreadableApp(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamRoles":       teamRolesJSON,
		"TeamAppIdentity": `{"errors":[{"message":"forbidden","extensions":{"code":"FORBIDDEN"}}]}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "list", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("an unreadable App record must not fail the role read: %v", err)
	}
	if !strings.Contains(out.String(), "app: acme.com:eng-team (from --app)") {
		t.Errorf("the scope line must fall back to the raw ref: %s", out.String())
	}
	if !strings.Contains(out.String(), "backend-engineer") {
		t.Errorf("the roles must still render: %s", out.String())
	}
}

// The register invariants are the SERVER's; the CLI maps their typed
// refusals — minted/duplicate/exists are state conflicts (TEAM_ROLE_EXISTS
// needs its explicit mapping: the generic suffix rule matches
// The register-invariant refusals this used to cover — TEAM_ROLE_NAME_MINTED,
// TEAM_ROLE_NAME_DUPLICATE, TEAM_ROLE_NAME_OUT_OF_RANGE, TEAM_ROLE_STALE,
// TEAM_ROLE_IN_USE — are all unreachable: hadron-server#1050 removed the
// register that produced them, so a case for any of them would pin an exit
// code no caller can observe. TEAM_ROLE_EXISTS is the one that survived.
func TestTeamRoleWriteServerRefusals(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamAppIdentity": teamAppIdentityJSON,
		"CreateTeamRole":  `{"errors":[{"message":"role backend-engineer already exists - updateTeamRole is the edit path","extensions":{"code":"TEAM_ROLE_EXISTS"}}]}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "role", "create", "backend-engineer",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Errorf("existing role: exit %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
	}
	if err == nil || !strings.Contains(err.Error(), "updateTeamRole is the edit path") {
		t.Errorf("server message must surface verbatim: %v", err)
	}
}

// #404: --dry-run routes to the castWorkerPreview QUERY — the mutation is
// absent from the fake, so any cast call fails loudly. The receipt carries
// the not-a-reservation caveat; the preview creates nothing.
func TestTeamWorkerCastDryRun(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"CastWorkerPreview": `{"data":{"castWorkerPreview":{"name":"Joe","role":"backend-engineer",
			"agentId":"agt1","agentName":"backend-engineer",
			"prompt":"You are Joe.","hasNamePlaceholder":true}}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "worker", "cast", "--dry-run", "--app", "acme.com:eng-team",
		"--role", "backend-engineer", "--name", "Joe", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var vars map[string]any
	_ = json.Unmarshal(captured["CastWorkerPreview"], &vars)
	if vars["appRef"] != "acme.com:eng-team" || vars["role"] != "backend-engineer" || vars["name"] != "Joe" {
		t.Errorf("preview vars: %v", vars)
	}
	// teamAgentRef is not in the operation at all now (hadron-cli#496).
	for _, k := range []string{"agentRef", "teamAgentRef", "promptOverride"} {
		if _, present := vars[k]; present {
			t.Errorf("unset %q must be omitted, got %v", k, vars[k])
		}
	}
	for _, want := range []string{"would cast Joe", "You are Joe.", "NOT reserved"} {
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
		"--role", "backend-engineer", "--name", "Joe", "--json", "--server", gql.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var dto struct {
		Name               string  `json:"name"`
		Role               *string `json:"role"`
		AgentID            string  `json:"agentId"`
		Prompt             *string `json:"prompt"`
		HasNamePlaceholder *bool   `json:"hasNamePlaceholder"`
		Reserved           bool    `json:"reserved"`
	}
	if err := json.Unmarshal([]byte(out2.String()), &dto); err != nil {
		t.Fatalf("--json: %v (%s)", err, out2.String())
	}
	if dto.Name != "Joe" || dto.AgentID != "agt1" ||
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
	// The Team Agent keys are gone: they named the register holder, and casting
	// reads no system memory now (hadron-cli#496).
	var raw map[string]any
	_ = json.Unmarshal([]byte(out2.String()), &raw)
	for _, gone := range []string{"teamAgentId", "teamAgentName"} {
		if _, present := raw[gone]; present {
			t.Errorf("%q named the removed register holder: %s", gone, out2.String())
		}
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

// #472: one worktree per worker. The already-bound guard was always right;
// the remedy was not. Which remedy is correct depends entirely on whether the
// bound session is still ALIVE — for a live one, --force relabels who gets
// blamed while leaving two agents on one index, and clears the only signal
// that anything was wrong.
func TestTeamSessionStartAlreadyBoundPicksTheRemedyByLiveness(t *testing.T) {
	bind := func(t *testing.T) {
		t.Helper()
		dir := teamGitDir(t)
		if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
			t.Fatalf("write binding: %v", err)
		}
	}

	for _, tc := range []struct {
		name           string
		session        string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:    "live session leads with a separate worktree",
			session: `{"data":{"session":` + startedSessionJSON + `}}`,
			wantContains: []string{
				"still active",
				"git worktree add -b <new-branch> ../<name>",
				// --force is named only so the reader knows it is the WRONG
				// tool here, never as the remedy.
				"WITHOUT separating the working trees",
				"only to take over an abandoned binding",
			},
			wantNotContain: []string{"--force to replace the binding"},
		},
		{
			name:    "an ended session is exactly what --force is for",
			session: `{"data":{"session":` + endedSessionJSON + `}}`,
			wantContains: []string{
				"whose session ended",
				"--force replaces the abandoned binding",
			},
			// The worktree advice would be noise: nobody is driving.
			wantNotContain: []string{"git worktree add"},
		},
		{
			name:    "unknown liveness leads with the safe remedy, not the convenient one",
			session: `{"data":{"session":null}}`,
			wantContains: []string{
				"could not be checked",
				"git worktree add -b <new-branch> ../<name>",
				"does NOT separate the working trees",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bind(t)
			gql, captured := captureGraphQL(t, map[string]string{"GetTeamSession": tc.session})
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "session", "start", "--as", "Dara", "--server", gql.URL})
			err := root.Execute()
			if code := exitcode.FromError(err); code != exitcode.Conflict {
				t.Fatalf("exit code = %d, want %d (Conflict); err: %v", code, exitcode.Conflict, err)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message missing %q:\n%s", want, err)
				}
			}
			for _, not := range tc.wantNotContain {
				if strings.Contains(err.Error(), not) {
					t.Errorf("message must not contain %q:\n%s", not, err)
				}
			}
			// The refusal happens before anything is bound or started.
			if _, started := captured["StartTeamSession"]; started {
				t.Error("a refused start must not reach the mutation")
			}
			var vars map[string]any
			_ = json.Unmarshal(captured["GetTeamSession"], &vars)
			if vars["id"] != "s-new" {
				t.Errorf("liveness must be checked on the BOUND session, got %v", vars)
			}
		})
	}
}

// PR #478 review (Codex P2). Liveness needs a GraphQL client, but the REFUSAL
// must not: before #472 this guard ran before any client construction, so it
// answered for a signed-out caller too. Hoisting the client above it would
// swap the documented exit-5 conflict for an auth error — and drop the safe
// worktree remedy at exactly the moment the caller cannot resolve the
// situation any other way. A client that cannot be built is unknown liveness.
func TestTeamSessionStartAlreadyBoundStillRefusesWhenSignedOut(t *testing.T) {
	dir := teamGitDir(t)
	if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
		t.Fatalf("write binding: %v", err)
	}
	gql, captured := captureGraphQL(t, map[string]string{})
	f, _ := testFactory(t)
	t.Setenv("HADRON_TOKEN", "") // signed out: GraphQLClient() fails before any request
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Dara", "--server", gql.URL})
	err := root.Execute()
	if code := exitcode.FromError(err); code != exitcode.Conflict {
		t.Fatalf("exit code = %d, want %d (Conflict) — the binding conflict outranks the auth error here; err: %v",
			code, exitcode.Conflict, err)
	}
	for _, want := range []string{"could not be checked", "git worktree add -b <new-branch> ../<name>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("a signed-out caller must still get the safe remedy, missing %q:\n%s", want, err)
		}
	}
	if len(captured) != 0 {
		t.Errorf("nothing should have reached the server: %v", captured)
	}
}

// configuredApp writes an App into the (temp-dir) config, producing the one
// scope that is ambient WITHOUT a worktree binding. Must run after
// testFactory, which is what points XDG_CONFIG_HOME at the temp dir.
func configuredApp(t *testing.T, ref string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "hadron")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("app = \""+ref+"\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// failingWriter rejects every write — stands in for a broken pipe or an
// embedded caller's failing writer.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func teamGitDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(team.GitDirEnv, dir)
	return dir
}

func TestTeamSessionStartWritesBinding(t *testing.T) {
	dir := teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":          staffJSON,
		"WorkersRoster":    staffJSON,
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

// The NAME path's briefing, pinned separately from the id path's (#459).
//
// `--as wkr1` resolves through GetWorker; `--as Iris` resolves through the
// shared Workers scan, which is the projection `worker list` stopped using.
// Trimming `prompt` from THAT scan — the obvious next "optimisation" — would
// leave a bound session with no briefing and nothing failing, so the two paths
// need separate assertions. Ada's issue named this as a done-criterion: the
// session-start briefing path must be unaffected.
func TestTeamSessionStartByNameStillPrintsBriefing(t *testing.T) {
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"Workers":          staffJSON,
		"TeamSessions":     `{"data":{"sessions":[]}}`,
		"StartTeamSession": `{"data":{"startSession":` + startedSessionJSON + `}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "session", "start", "--as", "Iris", "--tool", "claude-code",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "You are Iris.") {
		t.Errorf("a name-resolved start must still print the briefing: %s", out.String())
	}
	// It came from the prompt-BEARING scan, not the roster one.
	if _, roster := captured["WorkersRoster"]; roster {
		t.Error("session start must not resolve through the roster projection — it needs the prompt")
	}
	if _, full := captured["Workers"]; !full {
		t.Error("session start resolves through the prompt-bearing Workers scan")
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
		"WorkersRoster":    staffJSON,
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
// #470: an empty `chat read` was byte-identical to reading the wrong team.
// The render is a bare loop, so zero messages printed nothing at all — and
// `--since <watermark>` returning nothing is the NORMAL steady state, so the
// failure hid inside the expected case. The header is unconditional.
func TestTeamChatReadNamesTheAppEvenWhenEmpty(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
		"TeamAppIdentity":  teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--since", "42",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(out.String(), "app: hrn:app:acme.com:eng-team — Eng Team (from --app)\n") {
		t.Errorf("an empty read must still say which chat was empty: %q", out.String())
	}
}

// The pre-flight (#470, @Ada's call: options 1+3). A receipt can only diagnose
// a bad post — the message is live and the mentions have fired — and there is
// no removal surface, so the signal has to come BEFORE the write. It is gated
// on ambient-AND-unbound: with a binding, the App comes from the same binding
// that supplies sessionRef, so scope and authorship agree by construction.
func TestTeamChatPostPreflightOnlyWhenScopeIsAmbientAndUnbound(t *testing.T) {
	post := map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatMsgJSON + `}}`,
		"TeamAppIdentity":       teamAppIdentityJSON,
	}

	t.Run("--app names it, so no pre-flight", func(t *testing.T) {
		teamGitDir(t) // no binding file written
		gql, _ := captureGraphQL(t, post)
		f, _ := testFactory(t)
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "post", "hello",
			"--app", "acme.com:eng-team", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if strings.Contains(errOut.String(), "note:") {
			t.Errorf("an explicitly named App needs no warning: %q", errOut.String())
		}
	})

	t.Run("an ambient App context with no binding DOES warn, before the write", func(t *testing.T) {
		teamGitDir(t)
		gql, captured := captureGraphQL(t, post)
		f, _ := testFactory(t)
		configuredApp(t, "acme.com:eng-team")
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "post", "hello", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		// Named, sourced, and on stderr — so --json stdout is untouched. NOT
		// suppressed by --json: an agent posting from an ambient context is
		// exactly the exposed caller.
		want := "note: no --app and no worker session binding — posting to " +
			"hrn:app:acme.com:eng-team — Eng Team (from the App context)"
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("want the pre-flight on stderr, got %q", errOut.String())
		}
		if _, posted := captured["CreateTeamChatMessage"]; !posted {
			t.Error("the pre-flight warns, it never refuses — the post must still go out")
		}
	})

	t.Run("a session on the wire lets the server check, so no pre-flight", func(t *testing.T) {
		dir := teamGitDir(t)
		if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
			t.Fatalf("write binding: %v", err)
		}
		gql, _ := captureGraphQL(t, post)
		f, _ := testFactory(t)
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "post", "hello", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if strings.Contains(errOut.String(), "note:") {
			t.Errorf("a session-bound post is server-checked — no warning: %q", errOut.String())
		}
	})

	// PR #473 review (Codex P1). Gating on "a binding file exists" was wrong:
	// --as-me deliberately drops the session, and an ambient App CONTEXT
	// overrides the binding's App — so the post lands irreversibly in an App
	// nobody named, with the session that would have let the server catch it
	// deliberately withheld. The binding's existence proves nothing here.
	t.Run("--as-me drops the session, so the pre-flight returns", func(t *testing.T) {
		dir := teamGitDir(t)
		if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(bindingFixture), 0o600); err != nil {
			t.Fatalf("write binding: %v", err)
		}
		gql, captured := captureGraphQL(t, post)
		f, _ := testFactory(t)
		configuredApp(t, "acme.com:eng-team")
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "post", "hello", "--as-me", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(errOut.String(), "note: no --app and no worker session") {
			t.Errorf("a binding that is not on the wire must not suppress the warning: %q", errOut.String())
		}
		// And the session really is absent — the premise of the warning.
		var vars map[string]any
		_ = json.Unmarshal(captured["CreateTeamChatMessage"], &vars)
		if _, sent := vars["sessionRef"]; sent {
			t.Errorf("--as-me must send no sessionRef: %v", vars)
		}
	})
}

// The receipt named the author and the seq — the two facts never in doubt —
// and not the chat, so a post into the wrong team came back with a ✓ on it.
func TestTeamChatPostReceiptNamesTheChat(t *testing.T) {
	teamGitDir(t)
	gql, _ := captureGraphQL(t, map[string]string{
		"CreateTeamChatMessage": `{"data":{"createTeamChatMessage":` + teamChatMsgJSON + `}}`,
		"TeamAppIdentity":       teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "post", "hello",
		"--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "✓ posted as Iris (seq 8)") {
		t.Errorf("the existing receipt must survive: %s", out.String())
	}
	if !strings.Contains(out.String(), "  app: hrn:app:acme.com:eng-team — Eng Team (from --app)") {
		t.Errorf("the receipt must name the chat it landed in: %s", out.String())
	}
}

// The read header and the post receipt are renders, so `--json` keeps its
// shape and pays for no identity read. The PRE-FLIGHT is deliberately NOT
// suppressed under --json — it is a safety signal on an unrecallable write,
// and an agent posting from an ambient context is the exposed caller — but it
// goes to stderr, so the --json stdout contract is untouched either way.
func TestTeamChatJSONKeepsItsShape(t *testing.T) {
	// SANDBOXED even though this test writes no binding: without it the command
	// reads whatever binding the developer's own checkout happens to hold, and
	// `chat read` behaves differently bound than unbound. It passed on CI and
	// failed on a bound worktree — the worst way round, since CI is the copy
	// nobody watches for false greens (found while fixing PR #493).
	teamGitDir(t)
	gql, captured := captureGraphQL(t, map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":1,"items":[` + teamChatMsgJSON + `]}}}`,
		"TeamAppIdentity":  teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--app", "acme.com:eng-team", "--json", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured["TeamAppIdentity"]; called {
		t.Error("a read under --json must not pay for a render-only identity read")
	}
	var result struct {
		Messages  []map[string]any `json:"messages"`
		NextSince int              `json:"nextSince"`
	}
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatalf("--json must stay {messages, nextSince}: %v (%s)", err, out.String())
	}
	if len(result.Messages) != 1 || result.NextSince != 8 {
		t.Errorf("the documented shape must not move: %s", out.String())
	}
}

func TestTeamChatReadEmitsAuthorAliasAndKind(t *testing.T) {
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":2,"items":[` +
			teamChatHumanMsgJSON + `,` + teamChatMsgJSON + `]}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
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
// A binding that HAS read the team chat, watermark at seq 90.
const bindingChatSeenFixture = `{"sessionId":"s-new","workerId":"wkr1","workerName":"Iris","workerRole":"backend-engineer",
	"agentId":"agt1","appId":"app1","startedAt":"2026-08-11T10:00:00Z","appBound":true,
	"teamMemory":"hrn:mem:acme.com:eng-team","tool":"claude-code","chatSeenSeq":90,
	"repo":"hadron-memory/hadron-cli","prNumbers":[]}`

// #474: `session log` fires right before a worker publishes something durable,
// so it is where a missed decision can still change what they do. The role
// prompts already say "read everything" — the rule existed and did not hold,
// because nothing interrupts a focused worker to apply it.
// The other half of #474's loop: `chat read` records the watermark, or
// `session log` has nothing to compare against and the signal never advances
// past "you have not read".
func TestTeamChatReadRecordsTheWatermark(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":1,"items":[` + teamChatMsgJSON + `]}}}`,
		"TeamAppIdentity":  teamAppIdentityJSON,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "chat", "read", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read binding: %v", err)
	}
	var b struct {
		ChatSeenSeq int `json:"chatSeenSeq"`
	}
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("binding: %v", err)
	}
	// teamChatMsgJSON is seq 8 — the watermark is what was actually returned.
	if b.ChatSeenSeq != 8 {
		t.Errorf("chat read must record the watermark it returned, got %d", b.ChatSeenSeq)
	}
}

// The watermark is a claim about what the reader has SEEN, and three ways of
// getting it wrong survived the first round of #474 (PR #493 review). Each
// subtest below fails if its guard is removed — the whole value of the nudge
// is that it is never wrong, so a watermark that overstates is worse than none.
func TestTeamChatReadWatermarkOnlyRecordsWhatItCanClaim(t *testing.T) {
	bind := func(t *testing.T) string {
		t.Helper()
		dir := teamGitDir(t)
		path := filepath.Join(dir, "hadron-team-session.json")
		if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// seq, or -1 for "absent" — the two are different answers (never read vs
	// read a chat that was empty) and the field is a pointer to keep them apart.
	watermark := func(t *testing.T, path string) int {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read binding: %v", err)
		}
		var b struct {
			ChatSeenSeq *int `json:"chatSeenSeq"`
		}
		if err := json.Unmarshal(data, &b); err != nil {
			t.Fatalf("binding: %v", err)
		}
		if b.ChatSeenSeq == nil {
			return -1
		}
		return *b.ChatSeenSeq
	}
	oneMessage := map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":1,"items":[` + teamChatMsgJSON + `]}}}`,
		"TeamAppIdentity":  teamAppIdentityJSON,
	}
	read := func(t *testing.T, stubs map[string]string, args ...string) {
		t.Helper()
		gql, _ := captureGraphQL(t, stubs)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"team", "chat", "read", "--server", gql.URL}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("chat read: %v", err)
		}
	}

	// A FILTERED read sees only matching messages, so its highest seq says
	// nothing about the ones in between. Recording it would mark them read
	// forever — the reader is then told they are caught up on messages they
	// were never shown, which is the one failure this feature exists to prevent.
	t.Run("a mentions-filtered read records nothing", func(t *testing.T) {
		path := bind(t)
		read(t, oneMessage, "--mentions-me")
		if got := watermark(t, path); got != -1 {
			t.Errorf("a --mentions-me read must not claim the whole chat, got watermark %d", got)
		}
	})
	t.Run("an explicit --mentions read records nothing", func(t *testing.T) {
		path := bind(t)
		read(t, oneMessage, "--mentions", "wkr9")
		if got := watermark(t, path); got != -1 {
			t.Errorf("a --mentions read must not claim the whole chat, got watermark %d", got)
		}
	})

	// The binding holds ONE App's cursor. Reading a different team through the
	// same worktree would file that team's seq under this one.
	t.Run("another App's read stays out of this binding", func(t *testing.T) {
		path := bind(t)
		read(t, map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":1,"items":[` + teamChatMsgJSON + `]}}}`,
			// Resolves to a DIFFERENT id than the binding's app1 — the whole
			// question the guard asks. A fixture that resolved everything to
			// app1 would pass no matter what the guard did.
			"TeamAppIdentity": `{"data":{"app":{"id":"app2","urn":"hrn:app:acme.com:other-team","name":"Other Team"}}}`,
		}, "--app", "app2")
		if got := watermark(t, path); got != -1 {
			t.Errorf("app2's seq must not land in app1's binding, got %d", got)
		}
	})
	// ...and the guard is "a DIFFERENT App", not "any --app": naming the
	// binding's own App explicitly is the same read and must still record.
	t.Run("naming the bound App explicitly still records", func(t *testing.T) {
		path := bind(t)
		read(t, oneMessage, "--app", "app1")
		if got := watermark(t, path); got != 8 {
			t.Errorf("--app app1 is the bound App — want watermark 8, got %d", got)
		}
	})
	// The same App SPELLED differently is still the same App. `--app <urn>` and
	// `hadron app set-active <app-urn>` are the documented ways to name one, and
	// a raw string compare against the binding's server id calls them a
	// different team — pinning the watermark forever, so `session log` claims on
	// every run that this worktree has never read the chat. A nudge that is
	// always wrong is ignored, which costs more than the case it guards.
	t.Run("the bound App named by URN still records", func(t *testing.T) {
		path := bind(t)
		read(t, oneMessage, "--app", "hrn:app:acme.com:eng-team")
		if got := watermark(t, path); got != 8 {
			t.Errorf("that URN resolves to app1, the bound App — want 8, got %d", got)
		}
	})

	// `nextSince` is a PAGING cursor and falls back to whatever the caller
	// passed; the watermark is a claim about what was SEEN. Conflating them let
	// a typo'd or stale `--since` mark the team's next year of messages read.
	t.Run("a --since past the end records nothing", func(t *testing.T) {
		path := bind(t)
		read(t, map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
			"TeamAppIdentity":  teamAppIdentityJSON,
		}, "--since", "999")
		if got := watermark(t, path); got != -1 {
			t.Errorf("the server returned no seq 999 — nothing was seen, got watermark %d", got)
		}
	})
	t.Run("a --since past the end does not move an existing watermark", func(t *testing.T) {
		dir := teamGitDir(t)
		path := filepath.Join(dir, "hadron-team-session.json")
		if err := os.WriteFile(path, []byte(bindingChatSeenFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		read(t, map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
			"TeamAppIdentity":  teamAppIdentityJSON,
		}, "--since", "999")
		if got := watermark(t, path); got != 90 {
			t.Errorf("want the watermark left at 90, got %d", got)
		}
	})

	// The watermark says the reader HAS SEEN these messages. A closed pipe or a
	// full disk means they have not, and marking them read would bury them.
	t.Run("a failed render records nothing", func(t *testing.T) {
		path := bind(t)
		gql, _ := captureGraphQL(t, oneMessage)
		f, _ := testFactory(t)
		f.IOStreams.Out = brokenWriter{}
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "read", "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Fatal("a failed render must surface as an error")
		}
		if got := watermark(t, path); got != -1 {
			t.Errorf("the messages were never delivered — got watermark %d", got)
		}
	})

	// A `--since` AHEAD of the watermark is a window, not a prefix. The seqs it
	// returns are real, which is what makes this one slip past a
	// "server-verified" rule — but everything between the old watermark and the
	// --since was never rendered, and recording the top of the window buries it
	// while reporting the reader as caught up.
	t.Run("a --since ahead of the watermark does not jump the gap", func(t *testing.T) {
		dir := teamGitDir(t)
		path := filepath.Join(dir, "hadron-team-session.json")
		if err := os.WriteFile(path, []byte(bindingChatSeenFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		// Watermark 90, reading from 100: seq 101 comes back, 91–100 never do.
		read(t, map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":1,"items":[` +
				strings.Replace(teamChatMsgJSON, `"seq":8`, `"seq":101`, 1) + `]}}}`,
			"TeamAppIdentity": teamAppIdentityJSON,
		}, "--since", "100")
		if got := watermark(t, path); got != 90 {
			t.Errorf("91-100 were never shown — the watermark must stay at 90, got %d", got)
		}
	})
	// …but a --since BEHIND the watermark is a prefix: it re-reads ground already
	// claimed, so what it returns above the watermark really has been seen.
	t.Run("a --since behind the watermark still advances", func(t *testing.T) {
		dir := teamGitDir(t)
		path := filepath.Join(dir, "hadron-team-session.json")
		if err := os.WriteFile(path, []byte(bindingChatSeenFixture), 0o600); err != nil {
			t.Fatal(err)
		}
		read(t, map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":1,"items":[` +
				strings.Replace(teamChatMsgJSON, `"seq":8`, `"seq":95`, 1) + `]}}}`,
			"TeamAppIdentity": teamAppIdentityJSON,
		}, "--since", "50")
		if got := watermark(t, path); got != 95 {
			t.Errorf("50 is behind the watermark, so 95 was genuinely seen — got %d", got)
		}
	})

	// App ids are unique WITHIN a deployment, not across them — a clone or a
	// restore carries them over — so the id check alone lets a second server's
	// seq into this binding. Reading another deployment is legitimate (`chat
	// post` and the session mutations refuse it; a read does not), so it is only
	// the bookkeeping that has to stay out.
	t.Run("a cross-server read stays out of this binding", func(t *testing.T) {
		dir := teamGitDir(t)
		path := filepath.Join(dir, "hadron-team-session.json")
		// Bound to a server that is NOT the fake this read will target.
		bound := strings.Replace(bindingWithTeamFixture, `"appBound":true`,
			`"appBound":true,"server":"https://elsewhere.example"`, 1)
		if err := os.WriteFile(path, []byte(bound), 0o600); err != nil {
			t.Fatal(err)
		}
		read(t, oneMessage)
		if got := watermark(t, path); got != -1 {
			t.Errorf("another deployment's seq must not land here, got %d", got)
		}
	})

	// The watermark write lands AFTER a paginated fetch and a full render, so
	// the snapshot it started from can be seconds old — and two agents in one
	// worktree is the normal case here. Writing the whole snapshot back would
	// undo whatever landed in between. These two interleave a real concurrent
	// mutation by mutating the binding from inside the GraphQL handler, which is
	// exactly the window that matters.
	concurrently := func(t *testing.T, during func()) {
		t.Helper()
		gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OperationName string `json:"operationName"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			during() // another agent, mid-fetch
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(oneMessage[body.OperationName]))
		}))
		t.Cleanup(gql.Close)
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "read", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("chat read: %v", err)
		}
	}

	t.Run("a concurrent binding edit is not clobbered", func(t *testing.T) {
		path := bind(t)
		concurrently(t, func() {
			// A `session log --pr` in the sibling agent's session.
			edited := strings.Replace(bindingWithTeamFixture, `"prNumbers":[]`, `"prNumbers":[371]`, 1)
			_ = os.WriteFile(path, []byte(edited), 0o600)
		})
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			ChatSeenSeq *int  `json:"chatSeenSeq"`
			PRNumbers   []int `json:"prNumbers"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		if got.ChatSeenSeq == nil || *got.ChatSeenSeq != 8 {
			t.Errorf("the watermark must still land: %v", got.ChatSeenSeq)
		}
		if len(got.PRNumbers) != 1 || got.PRNumbers[0] != 371 {
			t.Errorf("the concurrent PR number must survive, got %v", got.PRNumbers)
		}
	})

	// The worse half: a wholesale write would RESURRECT a binding that
	// `session end` removed, leaving a worktree bound to a closed session.
	t.Run("a binding ended mid-read is not resurrected", func(t *testing.T) {
		path := bind(t)
		concurrently(t, func() { _ = os.Remove(path) })
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("`session end` removed this binding — it must stay removed (stat err: %v)", err)
		}
	})

	// …and a render that gets PART way is the same story. The header write was
	// checked; the message loop discarded its error, so a pipe closing after the
	// first line left `output.Write` returning nil and the messages marked read.
	t.Run("a render that fails mid-way records nothing", func(t *testing.T) {
		path := bind(t)
		gql, _ := captureGraphQL(t, oneMessage)
		f, _ := testFactory(t)
		f.IOStreams.Out = &flakyWriter{ok: 1} // the "app:" header lands, the message does not
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "chat", "read", "--server", gql.URL})
		if err := root.Execute(); err == nil {
			t.Fatal("a message that could not be written must surface as an error")
		}
		if got := watermark(t, path); got != -1 {
			t.Errorf("the messages were never delivered — got watermark %d", got)
		}
	})

	// An empty chat is READ, not unread. On a bare int, 0 meant both, so a team
	// whose chat had nothing in it yet could never be marked read and every
	// later `session log` nagged about it.
	t.Run("reading an empty chat is recorded as seq 0", func(t *testing.T) {
		path := bind(t)
		read(t, map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
			"TeamAppIdentity":  teamAppIdentityJSON,
		})
		if got := watermark(t, path); got != 0 {
			t.Errorf("an empty chat was still read — want recorded 0, got %d", got)
		}
	})
}

// The SEAM between the two halves (#474, reported live at team-chat seq 102 by
// a worker who read the chat and was then told it had not). Both halves were
// tested in isolation; the handover between them was not, which is exactly
// where the reported failure lives.
func TestTeamChatReadThenSessionLogSeesTheWatermark(t *testing.T) {
	dir := teamGitDir(t)
	path := filepath.Join(dir, "hadron-team-session.json")
	if err := os.WriteFile(path, []byte(bindingWithTeamFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	gql, _ := captureGraphQL(t, map[string]string{
		"TeamChatMessages": `{"data":{"teamChatMessages":{"total":1,"items":[` + teamChatMsgJSON + `]}}}`,
		"TeamAppIdentity":  teamAppIdentityJSON,
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	})

	// 1. read the chat, exactly as the procedure's step 4 says
	f1, _ := testFactory(t)
	r1 := NewRootCmd(f1)
	r1.SetArgs([]string{"team", "chat", "read", "--since", "0", "--server", gql.URL})
	if err := r1.Execute(); err != nil {
		t.Fatalf("chat read: %v", err)
	}

	// 2. log a milestone in the SAME binding
	f2, _ := testFactory(t)
	errOut := f2.IOStreams.ErrOut.(*strings.Builder)
	r2 := NewRootCmd(f2)
	r2.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
	if err := r2.Execute(); err != nil {
		t.Fatalf("session log: %v", err)
	}
	if strings.Contains(errOut.String(), "no record of reading the team chat") {
		t.Errorf("a read in this same worker session must count — false nudge:\n%s", errOut.String())
	}
}

func TestTeamSessionLogNotesUnreadTeamChat(t *testing.T) {
	logStubs := map[string]string{
		"UpdateTeamSession": `{"data":{"updateSession":{"id":"s-new","agentId":"agt1","workerId":"wkr1","userId":"u1",
			"type":"DEVELOPER","repo":null,"branch":null,"prNumber":371,
			"startedAt":"2026-08-11T10:00:00Z","endedAt":null,"host":null,"tool":null,
			"transcriptPath":null,"llmModel":null}}}`,
		"RecordTeamWork": `{"data":{"recordTeamWork":{"nodeId":"w1","sessionId":"s-new","workerId":"wkr1","workerName":"Iris",
			"tool":"claude-code","kind":"pr","ref":"hadron-memory/hadron-cli#371","action":"worked-on",
			"at":"2026-08-13T10:00:00Z","detail":null}}}`,
	}
	bind := func(t *testing.T, fixture string) {
		t.Helper()
		dir := teamGitDir(t)
		if err := os.WriteFile(filepath.Join(dir, "hadron-team-session.json"), []byte(fixture), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubs := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range logStubs {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	t.Run("never read is a louder state than nothing new", func(t *testing.T) {
		bind(t, bindingWithTeamFixture) // no chatSeenSeq
		gql, captured := captureGraphQL(t, stubs(nil))
		f, _ := testFactory(t)
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(errOut.String(), "this worktree has no record of reading the team chat") {
			t.Errorf("want the never-read note, got %q", errOut.String())
		}
		// Claims only what the CLI knows — an MCP-side read is invisible here,
		// so asserting the worker never read would be a false nudge (seq 102).
		if !strings.Contains(errOut.String(), "MCP tools is not visible here") {
			t.Errorf("the note must name its own blind spot: %q", errOut.String())
		}
		// And it costs nothing: no count query is worth issuing for that answer.
		if _, called := captured["TeamChatMessages"]; called {
			t.Error("the never-read branch must not query the chat")
		}
	})

	t.Run("counts what landed since the watermark", func(t *testing.T) {
		bind(t, bindingChatSeenFixture)
		gql, captured := captureGraphQL(t, stubs(map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":8,"items":[]}}}`,
		}))
		f, out := testFactory(t)
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--json", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(errOut.String(), "8 new messages in the team chat since you last read") {
			t.Errorf("want the count, got %q", errOut.String())
		}
		if !strings.Contains(errOut.String(), "chat read --since 90") {
			t.Errorf("the remedy must carry the watermark: %q", errOut.String())
		}
		// Counted FROM the watermark, not from zero.
		var vars map[string]any
		_ = json.Unmarshal(captured["TeamChatMessages"], &vars)
		if vars["sinceSeq"] != float64(90) {
			t.Errorf("must count since the read watermark: %v", vars)
		}
		// stderr only — the --json stdout contract is untouched.
		var dto map[string]any
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatalf("--json must stay parseable: %v (%s)", err, out.String())
		}
		if dto["ref"] != "hadron-memory/hadron-cli#371" {
			t.Errorf("the receipt must survive: %s", out.String())
		}
	})

	t.Run("silent when caught up", func(t *testing.T) {
		bind(t, bindingChatSeenFixture)
		gql, _ := captureGraphQL(t, stubs(map[string]string{
			"TeamChatMessages": `{"data":{"teamChatMessages":{"total":0,"items":[]}}}`,
		}))
		f, _ := testFactory(t)
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if strings.Contains(errOut.String(), "team chat") {
			t.Errorf("nothing new must say nothing: %q", errOut.String())
		}
	})

	// "none mentioning you" is the clause that decides whether the reader stops
	// to look. If the mentions query FAILED, the honest answer is "unknown", and
	// stating the reassuring one as fact is how a worker walks past the message
	// addressed to them (PR #493 review). The count query succeeded, so the
	// count is still worth saying — the clause is dropped, not the note.
	t.Run("a failed mentions query says nothing rather than none", func(t *testing.T) {
		bind(t, bindingChatSeenFixture)
		// Both halves are the same operation, so this fake keys on the variable
		// that tells them apart rather than on the operation name.
		gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OperationName string `json:"operationName"`
				Variables     struct {
					MentionsRef *string `json:"mentionsRef"`
				} `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			switch {
			case body.OperationName != "TeamChatMessages":
				_, _ = w.Write([]byte(logStubs[body.OperationName]))
			case body.Variables.MentionsRef != nil:
				_, _ = w.Write([]byte(`{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`))
			default:
				_, _ = w.Write([]byte(`{"data":{"teamChatMessages":{"total":8,"items":[]}}}`))
			}
		}))
		t.Cleanup(gql.Close)
		f, _ := testFactory(t)
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(errOut.String(), "8 new messages in the team chat since you last read") {
			t.Errorf("the count query succeeded — its answer must survive: %q", errOut.String())
		}
		if strings.Contains(errOut.String(), "mentioning you") {
			t.Errorf("the mentions query failed — the answer is unknown, not none: %q", errOut.String())
		}
	})

	// The two counts are separate round trips against an append-only sequence,
	// so a message arriving between them can make the mentions count exceed the
	// total. "1 new message (2 mentioning you)" is not merely stale — it is
	// impossible, and an impossible receipt is the kind readers stop believing.
	t.Run("an impossible pair of counts drops the clause", func(t *testing.T) {
		bind(t, bindingChatSeenFixture)
		gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				OperationName string `json:"operationName"`
				Variables     struct {
					MentionsRef *string `json:"mentionsRef"`
				} `json:"variables"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			switch {
			case body.OperationName != "TeamChatMessages":
				_, _ = w.Write([]byte(logStubs[body.OperationName]))
			case body.Variables.MentionsRef != nil:
				// Two mentions, against a total of one taken a moment earlier.
				_, _ = w.Write([]byte(`{"data":{"teamChatMessages":{"total":2,"items":[]}}}`))
			default:
				_, _ = w.Write([]byte(`{"data":{"teamChatMessages":{"total":1,"items":[]}}}`))
			}
		}))
		t.Cleanup(gql.Close)
		f, _ := testFactory(t)
		errOut := f.IOStreams.ErrOut.(*strings.Builder)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(errOut.String(), "1 new message in the team chat") {
			t.Errorf("the total still stands: %q", errOut.String())
		}
		if strings.Contains(errOut.String(), "mentioning you") {
			t.Errorf("2 of 1 is impossible — say nothing rather than nonsense: %q", errOut.String())
		}
	})

	// The milestone is already recorded server-side by the time this runs, so a
	// failed courtesy read must never turn a successful write into a failure.
	t.Run("a failing chat read does not fail the log", func(t *testing.T) {
		bind(t, bindingChatSeenFixture)
		gql, _ := captureGraphQL(t, stubs(map[string]string{
			"TeamChatMessages": `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}]}`,
		}))
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs([]string{"team", "session", "log", "--pr", "371", "--server", gql.URL})
		if err := root.Execute(); err != nil {
			t.Fatalf("the milestone was already recorded — the note must not fail it: %v", err)
		}
		if !strings.Contains(out.String(), "✓ logged pr") {
			t.Errorf("the receipt must still print: %s", out.String())
		}
	})
}

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
		case "TeamAppIdentity":
			_, _ = w.Write([]byte(teamAppIdentityJSON))
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
		case "TeamAppIdentity":
			_, _ = w.Write([]byte(teamAppIdentityJSON))
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
// #469: `team init` WRITES, and its receipt names the target memory only after
// the schemas have landed — so the one moment a reader could catch a wrong
// ambient App was the moment that had already passed. The scope line comes
// BEFORE the converge, and carries where the scope came from.
func TestTeamInitNamesTheAppBeforeConverging(t *testing.T) {
	shared := `{"data":{"app":{"id":"app1","sharedMemory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team",
		"class":"app","schema":{"objectTypes":{"worklog":{"fields":{"ref":{"type":"text","required":true}}}}}}}}}`
	converged := `{"data":{"updateTeamCollections":{"memoryId":"m1","collections":["worklog"],"changed":false}}}`

	gql, _ := captureGraphQL(t, map[string]string{
		"GetAppSharedMemory": shared, "UpdateTeamCollections": converged,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Leads the output: it has to be readable before the receipt, not with it.
	if !strings.HasPrefix(out.String(), "app: hrn:app:acme.com:eng-team — Eng Team (from --app)\n") {
		t.Errorf("the scope line must come first: %q", out.String())
	}
	// And the existing receipt still answers its own, different question —
	// WHICH MEMORY the collections landed in, which can differ from the App's.
	if !strings.Contains(out.String(), "collection(s) unchanged in hrn:mem:acme.com:eng-team") {
		t.Errorf("the receipt must survive: %s", out.String())
	}

	// Render-only: --json keeps its shape and pays for no identity read.
	gql2, captured2 := captureGraphQL(t, map[string]string{
		"GetAppSharedMemory": shared, "UpdateTeamCollections": converged,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f2, out2 := testFactory(t)
	root2 := NewRootCmd(f2)
	root2.SetArgs([]string{"team", "init", "--app", "acme.com:eng-team", "--json", "--server", gql2.URL})
	if err := root2.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, called := captured2["TeamAppIdentity"]; called {
		t.Error("--json must not pay for a render-only identity read")
	}
	var dto map[string]any
	if err := json.Unmarshal([]byte(out2.String()), &dto); err != nil {
		t.Fatalf("--json must stay parseable — the scope line must not leak into stdout: %v (%s)", err, out2.String())
	}
	for _, k := range []string{"memory", "collections", "status"} {
		if _, ok := dto[k]; !ok {
			t.Errorf("--json key %q must survive: %s", k, out2.String())
		}
	}
}

// The scope line's contract is that it PRECEDES the mutation, so a failure to
// write it must stop the converge rather than proceed without it (PR #488
// review). Otherwise the App is mutated with the signal absent — which is the
// failure this whole family exists to remove, wearing a new costume.
func TestTeamInitDoesNotConvergeIfTheScopeLineCannotBeWritten(t *testing.T) {
	gql, captured := captureGraphQL(t, map[string]string{
		"GetAppSharedMemory": `{"data":{"app":{"id":"app1","sharedMemory":{"id":"m1","urn":"hrn:mem:acme.com:eng-team",
			"class":"app","schema":{"objectTypes":{"worklog":{"fields":{"ref":{"type":"text","required":true}}}}}}}}}`,
		"UpdateTeamCollections": `{"data":{"updateTeamCollections":{"memoryId":"m1",
			"collections":["worklog"],"changed":false}}}`,
		"TeamAppIdentity": teamAppIdentityJSON,
	})
	f, _ := testFactory(t)
	f.IOStreams.Out = failingWriter{}
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "init", "--app", "acme.com:eng-team", "--server", gql.URL})
	if err := root.Execute(); err == nil {
		t.Fatal("a failed scope-line write must surface, not be swallowed")
	}
	if _, converged := captured["UpdateTeamCollections"]; converged {
		t.Error("the App must NOT be mutated when the line that should precede the mutation never landed")
	}
}

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
		case "TeamAppIdentity":
			_, _ = w.Write([]byte(teamAppIdentityJSON))
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

// brokenWriter is stdout that cannot be written — a closed pipe, a full disk.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// flakyWriter accepts `ok` writes, then fails — a pipe that closes partway
// through a render, which is not the same case as one that was never open.
type flakyWriter struct{ ok int }

func (w *flakyWriter) Write(p []byte) (int, error) {
	if w.ok <= 0 {
		return 0, errors.New("broken pipe")
	}
	w.ok--
	return len(p), nil
}
