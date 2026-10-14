package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// #871 over server #1876 (#1709 T6 slice 1): the host-operation reader and
// manager surfaces — `team host list|ops|issue` and `app key scope
// grant|revoke` (cor:agt:070:01–:05, cor:aut:010:04). Every test runs in a
// worktree bound to a worker (session s-new), so the attribution of each
// write is pinned in the state where it can go wrong.

const hostOpJSON = `{"id":"hop1","appId":"capp100000000000000000000","hostId":"orca1",
	"type":"worktree.create","args":{"workerId":"wkr1","repo":"hadron-cli","baseBranch":"main","branchName":"jonas/871","label":"jonas-3"},
	"workerId":"wkr1","worker":{"id":"wkr1","name":"Jonas"},"status":"SUCCEEDED","issuedBy":"WORKER",
	"issuedByUserId":"u1","issuedByWorkerId":"wkr2","issuedAt":"2026-10-14T01:00:00Z",
	"collectedAt":"2026-10-14T01:00:30Z","finishedAt":"2026-10-14T01:01:00Z",
	"result":{"worktree":"jonas-3"},"failureReason":null}`

const issuedHostOpJSON = `{"id":"hop2","appId":"capp100000000000000000000","hostId":"orca1",
	"type":"worktree.remove","args":{"workerId":"wkr1","worktree":"jonas-3"},
	"workerId":"wkr1","worker":{"id":"wkr1","name":"Jonas"},"status":"ISSUED","issuedBy":"USER",
	"issuedByUserId":"u1","issuedByWorkerId":null,"issuedAt":"2026-10-14T02:00:00Z",
	"collectedAt":null,"finishedAt":null,"result":null,"failureReason":null}`

const hostRegJSON = `{"hostId":"orca1","label":"Orca box 1","pollIntervalSeconds":30,
	"registeredAt":"2026-10-14T00:00:00Z","refreshedAt":"2026-10-14T01:00:00Z","live":true,
	"workers":[{"workerId":"wkr1","worker":{"id":"wkr1","name":"Jonas"},"family":"claude","worktrees":["jonas-2","jonas-3"]}]}`

const appKeyScopesJSON = `{"id":"key1","appId":"capp100000000000000000000","label":"orca1 host","revokedAt":null,
	"scopes":[{"scope":"host:operate","hostId":"orca1","grantedAt":"2026-10-14T00:00:00Z","grantedBy":"u1"}]}`

func hostResponses() map[string]string {
	r := taskResponses()
	r["HostRegistrations"] = `{"data":{"hostRegistrations":[` + hostRegJSON + `]}}`
	r["HostOperations"] = `{"data":{"hostOperations":{"items":[` + hostOpJSON + `],"hasNextPage":true,"endCursor":"hop1"}}}`
	r["IssueHostOperation"] = `{"data":{"issueHostOperation":` + issuedHostOpJSON + `}}`
	r["GrantAppKeyScope"] = `{"data":{"grantAppKeyScope":` + appKeyScopesJSON + `}}`
	r["RevokeAppKeyScope"] = `{"data":{"revokeAppKeyScope":{"id":"key1","appId":"capp100000000000000000000","label":"orca1 host","revokedAt":null,"scopes":[]}}}`
	return r
}

// findCall returns the one call of op, failing when there is none — a test
// must never pass because the mutation was never sent.
func findCall(t *testing.T, calls *[]attnCall, op string) attnCall {
	t.Helper()
	var found []attnCall
	for _, c := range *calls {
		if c.Op == op {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %s call, got %d", op, len(found))
	}
	return found[0]
}

// ---- ls ---------------------------------------------------------------------

func TestTeamHostLsRenders(t *testing.T) {
	calls, out, err := runTeamTask(t, hostResponses(), "team", "host", "list")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "HostRegistrations")
	if c.Vars["appRef"] != "capp100000000000000000000" || c.Session != "" {
		t.Fatalf("appRef %v session %q", c.Vars["appRef"], c.Session)
	}
	for _, want := range []string{"app: ", "orca1", "Orca box 1", "yes", "30s", "Jonas/claude[jonas-2,jonas-3]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

func TestTeamHostLsJSON(t *testing.T) {
	_, out, err := runTeamTask(t, hostResponses(), "team", "host", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("hosts %v", got)
	}
	h := got[0]
	for _, k := range []string{"hostId", "label", "pollIntervalSeconds", "registeredAt", "refreshedAt", "live", "workers"} {
		if _, ok := h[k]; !ok {
			t.Fatalf("missing key %s in %v", k, h)
		}
	}
	if len(h) != 7 {
		t.Fatalf("keys %v", h)
	}
	ws := h["workers"].([]any)
	w := ws[0].(map[string]any)
	if w["workerId"] != "wkr1" || w["workerName"] != "Jonas" || w["family"] != "claude" || len(w["worktrees"].([]any)) != 2 || len(w) != 4 {
		t.Fatalf("worker %v", w)
	}
}

// An App with no hosts renders [] in JSON, never null.
func TestTeamHostLsEmptyJSONIsArray(t *testing.T) {
	r := hostResponses()
	r["HostRegistrations"] = `{"data":{"hostRegistrations":[]}}`
	_, out, err := runTeamTask(t, r, "team", "host", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Fatalf("empty list renders %q, want []", out)
	}
}

// ---- ops --------------------------------------------------------------------

func TestTeamHostOpsFiltersAndCursor(t *testing.T) {
	calls, out, err := runTeamTask(t, hostResponses(), "team", "host", "ops",
		"--host", "orca1", "--status", "succeeded", "--worker", "Jonas", "--limit", "10", "--after", "hop9")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "HostOperations")
	want := map[string]any{"appRef": "capp100000000000000000000", "hostId": "orca1", "status": "SUCCEEDED",
		"worker": "Jonas", "first": 10.0, "after": "hop9"}
	if len(c.Vars) != len(want) {
		t.Fatalf("vars %v, want %v", c.Vars, want)
	}
	for k, v := range want {
		if c.Vars[k] != v {
			t.Fatalf("%s = %v, want %v", k, c.Vars[k], v)
		}
	}
	for _, want := range []string{"hop1", "worktree.create", "orca1", "Jonas", "SUCCEEDED", "WORKER", `{"worktree":"jonas-3"}`,
		"more follow — continue with --after hop1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

// Unset filters are omitted from the wire, not sent as null.
func TestTeamHostOpsOmitsUnsetFilters(t *testing.T) {
	calls, _, err := runTeamTask(t, hostResponses(), "team", "host", "ops")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "HostOperations")
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(c.RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	if len(vars) != 2 || string(vars["first"]) != "50" {
		t.Fatalf("wire variables %s, want appRef and first only", c.RawVars)
	}
}

func TestTeamHostOpsJSON(t *testing.T) {
	_, out, err := runTeamTask(t, hostResponses(), "team", "host", "ops", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["hasNextPage"] != true || got["endCursor"] != "hop1" || len(got) != 3 {
		t.Fatalf("page %v", got)
	}
	items := got["items"].([]any)
	op := items[0].(map[string]any)
	keys := []string{"id", "appId", "hostId", "type", "args", "workerId", "workerName", "status", "issuedBy",
		"issuedByUserId", "issuedByWorkerId", "issuedAt", "collectedAt", "finishedAt", "result", "failureReason"}
	if len(op) != len(keys) {
		t.Fatalf("op keys %v, want %v", op, keys)
	}
	for _, k := range keys {
		if _, ok := op[k]; !ok {
			t.Fatalf("missing %s in %v", k, op)
		}
	}
	if op["result"].(map[string]any)["worktree"] != "jonas-3" || op["args"].(map[string]any)["repo"] != "hadron-cli" {
		t.Fatalf("op %v", op)
	}
}

func TestTeamHostOpsLocalRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"bad status", []string{"--status", "done"}, "--status takes issued, collected, succeeded or failed"},
		{"limit zero", []string{"--limit", "0"}, "--limit takes 1 to 200"},
		{"limit over cap", []string{"--limit", "201"}, "--limit takes 1 to 200"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, hostResponses(), append([]string{"team", "host", "ops"}, tc.argv...)...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want usage refusal %q, got %v (exit %d)", tc.want, err, exitCodeFor(err))
			}
			for _, c := range *calls {
				if c.Op == "HostOperations" {
					t.Fatal("a local refusal must not reach the server")
				}
			}
		})
	}
}

// ---- issue ------------------------------------------------------------------

// In a bound worktree, the operation is the worker's (cor:agt:070:05: the
// worker the target reports to) — the session rides the request.
func TestTeamHostIssueBoundIsTheWorkers(t *testing.T) {
	calls, out, err := runTeamTask(t, hostResponses(), "team", "host", "issue", "worktree.create",
		"--host", "orca1", "--worker", "Jonas",
		"--arg", "repo=hadron-cli", "--arg", "label=jonas-3", "--arg", "baseBranch=main", "--arg", "branchName=jonas/871")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "IssueHostOperation")
	if c.Session != "s-new" {
		t.Fatalf("bound issue carried session %q, want the worker's s-new", c.Session)
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal(c.RawVars, &vars); err != nil {
		t.Fatal(err)
	}
	var in map[string]json.RawMessage
	if err := json.Unmarshal(vars["input"], &in); err != nil {
		t.Fatal(err)
	}
	if got := string(in["args"]); got != `{"baseBranch":"main","branchName":"jonas/871","label":"jonas-3","repo":"hadron-cli"}` {
		t.Fatalf("wire args %s", got)
	}
	for k, v := range map[string]string{"appRef": `"capp100000000000000000000"`, "hostId": `"orca1"`, "worker": `"Jonas"`, "type": `"worktree.create"`} {
		if string(in[k]) != v {
			t.Fatalf("%s = %s, want %s", k, in[k], v)
		}
	}
	if !strings.Contains(out, "✓ issued worktree.remove hop2 to host orca1 for Jonas (ISSUED, by USER)") {
		t.Fatalf("receipt %q", out)
	}
}

// --as-person issues it as the caller — the target's holder or the App's
// manager — with no worker session, even in a bound worktree.
func TestTeamHostIssueAsPersonCarriesNoSession(t *testing.T) {
	calls, _, err := runTeamTask(t, hostResponses(), "team", "host", "issue", "worktree.remove",
		"--host", "orca1", "--worker", "Jonas", "--arg", "worktree=jonas-3", "--as-person")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "IssueHostOperation")
	if c.Session != "" {
		t.Fatalf("--as-person carried worker session %q", c.Session)
	}
}

func TestTeamHostIssueJSON(t *testing.T) {
	_, out, err := runTeamTask(t, hostResponses(), "team", "host", "issue", "worktree.remove",
		"--host", "orca1", "--worker", "Jonas", "--arg", "worktree=jonas-3", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var op map[string]any
	if err := json.Unmarshal([]byte(out), &op); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if op["id"] != "hop2" || op["status"] != "ISSUED" || op["result"] != nil || len(op) != 16 {
		t.Fatalf("op %v", op)
	}
	if v, ok := op["result"]; !ok || v != nil {
		t.Fatal("result must be present as null")
	}
	if strings.Contains(out, "✓") {
		t.Fatalf("--json carries the human receipt:\n%s", out)
	}
}

// With no --arg, args is null on the wire: the server reads it as {}.
func TestTeamHostIssueNoArgsSendsNull(t *testing.T) {
	calls, _, err := runTeamTask(t, hostResponses(), "team", "host", "issue", "worker.start", "--host", "orca1", "--worker", "Jonas")
	if err != nil {
		t.Fatal(err)
	}
	in := findCall(t, calls, "IssueHostOperation").Vars["input"].(map[string]any)
	if v, ok := in["args"]; ok && v != nil {
		t.Fatalf("args %v", v)
	}
}

func TestTeamHostIssueLocalRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"no host", []string{"worktree.create", "--worker", "Jonas"}, "--host is required"},
		{"no worker", []string{"worktree.create", "--host", "orca1"}, "--worker is required"},
		{"bad arg", []string{"worktree.create", "--host", "orca1", "--worker", "Jonas", "--arg", "repo"}, "--arg takes key=value"},
		{"empty key", []string{"worktree.create", "--host", "orca1", "--worker", "Jonas", "--arg", "=x"}, "--arg takes key=value"},
		{"dup arg", []string{"worktree.create", "--host", "orca1", "--worker", "Jonas", "--arg", "repo=a", "--arg", "repo=b"}, "--arg repo is given twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, hostResponses(), append([]string{"team", "host", "issue"}, tc.argv...)...)
			if err == nil || exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want usage refusal %q, got %v (exit %d)", tc.want, err, exitCodeFor(err))
			}
			for _, c := range *calls {
				if c.Op == "IssueHostOperation" {
					t.Fatal("a local refusal must not reach the server")
				}
			}
		})
	}
}

// The typed refusals the server owns surface with their exit codes and no
// success receipt.
func TestTeamHostIssueServerRefusals(t *testing.T) {
	for _, tc := range []struct {
		code string
		want int
	}{
		{"HOST_OPERATION_TYPE_UNKNOWN", exitcode.Usage},
		{"HOST_OPERATION_NOT_ISSUABLE", exitcode.Usage},
		{"BAD_HOST_OPERATION_ARGS", exitcode.Usage},
		{"HOST_OPERATION_NOT_PERMITTED", exitcode.Forbidden},
		{"HOST_NOT_FOUND", exitcode.NotFound},
		{"WORKER_NOT_FOUND", exitcode.NotFound},
		{"WORKER_RETIRED", exitcode.Conflict},
	} {
		t.Run(tc.code, func(t *testing.T) {
			r := hostResponses()
			r["IssueHostOperation"] = `{"errors":[{"message":"refused ` + tc.code + `.","extensions":{"code":"` + tc.code + `"}}]}`
			_, out, err := runTeamTask(t, r, "team", "host", "issue", "shell", "--host", "orca1", "--worker", "Jonas")
			if err == nil || exitCodeFor(err) != tc.want || !strings.Contains(err.Error(), "refused "+tc.code) {
				t.Fatalf("want exit %d with the server's message, got %v (exit %d)", tc.want, err, exitCodeFor(err))
			}
			if strings.Contains(out, "✓") {
				t.Fatalf("receipt before refusal: %q", out)
			}
		})
	}
}

// ---- app key scope ----------------------------------------------------------

// The grant is the App manager's, as themselves: the server refuses a worker
// session, so even a bound worktree's grant carries none.
func TestAppKeyScopeGrantHostID(t *testing.T) {
	calls, out, err := runTeamTask(t, hostResponses(), "app", "key", "scope", "grant", "key1", "host:operate", "--host-id", "orca1")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "GrantAppKeyScope")
	if c.Session != "" {
		t.Fatalf("grant carried worker session %q", c.Session)
	}
	if c.Vars["id"] != "key1" || c.Vars["scope"] != "host:operate" || c.Vars["hostId"] != "orca1" {
		t.Fatalf("vars %v", c.Vars)
	}
	for _, want := range []string{"✓ key key1 holds host:operate for host orca1", "host:operate", "orca1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

// Without --host-id, hostId is omitted from the wire (not null).
func TestAppKeyScopeGrantOmitsHostID(t *testing.T) {
	calls, _, err := runTeamTask(t, hostResponses(), "app", "key", "scope", "grant", "key1", "attention:deliver")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "GrantAppKeyScope")
	if strings.Contains(string(c.RawVars), "hostId") {
		t.Fatalf("wire variables %s carry hostId", c.RawVars)
	}
}

// The --json shape: the key's id, label and scopes — no preview, no key material.
func TestAppKeyScopeGrantJSON(t *testing.T) {
	calls, out, err := runTeamTask(t, hostResponses(), "app", "key", "scope", "grant", "key1", "host:operate", "--host-id", "orca1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if q := findCall(t, calls, "GrantAppKeyScope").Query; strings.Contains(q, "keyPreview") {
		t.Fatal("the grant selects the key's preview")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got["id"] != "key1" || got["label"] != "orca1 host" || len(got) != 5 {
		t.Fatalf("key %v", got)
	}
	s := got["scopes"].([]any)[0].(map[string]any)
	if s["scope"] != "host:operate" || s["hostId"] != "orca1" || len(s) != 4 {
		t.Fatalf("scope %v", s)
	}
}

func TestAppKeyScopeGrantHostConflict(t *testing.T) {
	r := hostResponses()
	r["GrantAppKeyScope"] = `{"errors":[{"message":"This key already operates as host orca2.","extensions":{"code":"APP_KEY_SCOPE_HOST_CONFLICT"}}]}`
	_, out, err := runTeamTask(t, r, "app", "key", "scope", "grant", "key1", "host:operate", "--host-id", "orca1")
	if err == nil || exitCodeFor(err) != exitcode.Conflict || !strings.Contains(err.Error(), "already operates as host orca2") {
		t.Fatalf("want exit 5, got %v (exit %d)", err, exitCodeFor(err))
	}
	if strings.Contains(out, "✓") {
		t.Fatalf("receipt before refusal: %q", out)
	}
}

func TestAppKeyScopeRevokeEmptyScopesJSON(t *testing.T) {
	calls, out, err := runTeamTask(t, hostResponses(), "app", "key", "scope", "revoke", "key1", "host:operate", "--json")
	if err != nil {
		t.Fatal(err)
	}
	c := findCall(t, calls, "RevokeAppKeyScope")
	if c.Session != "" || c.Vars["id"] != "key1" || c.Vars["scope"] != "host:operate" {
		t.Fatalf("session %q vars %v", c.Session, c.Vars)
	}
	if !strings.Contains(out, `"scopes": []`) {
		t.Fatalf("empty scopes must render []:\n%s", out)
	}
}
