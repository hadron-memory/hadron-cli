package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#2061 (C6): `team host import` is a dry run unless --apply, and
// --apply is refused (nothing written) until the leave-alone write exists.

const importRoster2061 = `{"data":{"teamRoster":{"appId":"capp100000000000000000000","asOf":"2026-11-15T10:00:00Z","workers":[
 {"worker":{"id":"w1","kind":"WORKER","name":"Jonas","urn":"hrn:worker:t:t:jonas"}},
 {"worker":{"id":"w2","kind":"WORKER","name":"Iris","urn":"hrn:worker:t:t:iris"}},
 {"worker":{"id":"w3","kind":"WORKER","name":"Unlisted","urn":"hrn:worker:t:t:unlisted"}}]}}}`

func importResponses2061() map[string]string {
	r := responses2080()
	r["TeamRoster"] = importRoster2061
	// The server already holds Iris exactly as the file says.
	r["TeamHostSetup"] = `{"data":{"hostSetup":{"source":"HOST","assignments":[{"id":"a9","hostId":"orca1","workerId":"w2","worker":{"name":"Iris"},"workdir":"iris-1","family":"claude","model":null,"createdAt":"2026-11-15T10:00:00Z","updatedAt":"2026-11-15T10:00:00Z"}]}}}`
	return r
}

func hostYAML2061(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	y := "server: http://127.0.0.1:1\napp: hrn:app:t:t\nstate_file: " + filepath.Join(dir, "s.json") + "\nworktrees_root: " + dir + `
workers:
  Jonas:
    family: openai
    launch: ["/usr/bin/codex", "--api-key", "SECRET_LAUNCH_ARG"]
    worktrees: [jonas-1]
  Iris:
    family: claude
    launch: [claude]
    worktrees: [iris-1]
  Ghost:
    family: claude
    launch: [claude]
    worktrees: [ghost-1]
`
	p := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTeamHostImport2061DryRunWritesNothingAndReports(t *testing.T) {
	calls, out, err := runTeamTask(t, importResponses2061(), "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"TeamHostAssign", "TeamHostAssignInstructed", "TeamHostSetSource", "TeamHostUnassign"} {
		noCall2080(t, calls, op)
	}
	if strings.Contains(out, "SECRET_LAUNCH_ARG") {
		t.Fatalf("a launch argument was printed: %s", out)
	}
	var got struct {
		Applied bool             `json:"applied"`
		Counts  map[string]int   `json:"counts"`
		Missing []string         `json:"missing"`
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.Applied || got.Counts["create"] != 1 || got.Counts["unchanged"] != 1 || got.Counts["skip"] != 1 || len(got.Missing) != 1 || got.Missing[0] != "Unlisted" {
		t.Fatalf("report = %+v", got)
	}
	for _, e := range got.Entries {
		if e["profileStatus"] != "unmapped" || e["profile"] != nil {
			t.Errorf("entry %v: launch lines are never mapped before profiles exist", e)
		}
	}
}

func TestTeamHostImport2061ApplyRefusedBeforeAnyWrite(t *testing.T) {
	calls, _, err := runTeamTask(t, importResponses2061(), "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--apply")
	if err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Fatalf("err = %v, want a usage refusal", err)
	}
	for _, op := range []string{"TeamHostAssign", "TeamHostAssignInstructed", "TeamHostSetSource"} {
		noCall2080(t, calls, op)
	}
	if !strings.Contains(err.Error(), "nothing written") {
		t.Fatalf("refusal must say nothing was written: %v", err)
	}
}

func TestTeamHostImport2061RequiresFromAndHost(t *testing.T) {
	for _, args := range [][]string{{"team", "host", "import", "--host", "orca1"}, {"team", "host", "import", "--from", "x.yaml"}} {
		if _, _, err := runTeamTask(t, importResponses2061(), args...); err == nil || exitCodeFor(err) != exitcode.Usage {
			t.Errorf("%v: err = %v, want usage", args, err)
		}
	}
	if _, _, err := runTeamTask(t, importResponses2061(), "team", "host", "import", "--from", "/no/such/host.yaml", "--host", "orca1"); err == nil || exitCodeFor(err) != exitcode.Usage {
		t.Errorf("a missing file must be a usage error: %v", err)
	}
}

// Today's production server has no hostSetup (hadron-server#4241): the dry run
// still works and says so.
func TestTeamHostImport2061OldServerWithoutHostSetup(t *testing.T) {
	r := importResponses2061()
	r["TeamHostSetup"] = `{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`
	calls, out, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1", "--json")
	if err != nil {
		t.Fatalf("a dry run must not fail on a server without hostSetup: %v", err)
	}
	noCall2080(t, calls, "TeamHostAssign", "TeamHostAssignInstructed", "TeamHostSetSource")
	var got struct {
		HostSetup string         `json:"hostSetup"`
		Counts    map[string]int `json:"counts"`
		Entries   []struct {
			Ref, Action string
			Reason      *string
		} `json:"entries"`
	}
	if e := json.Unmarshal([]byte(out), &got); e != nil {
		t.Fatalf("not JSON: %v\n%s", e, out)
	}
	if got.HostSetup != "missing" || got.Counts["unchecked"] != 2 || got.Counts["create"] != 0 {
		t.Fatalf("report = %+v", got)
	}
	for _, e := range got.Entries {
		if e.Action == "unchecked" && (e.Reason == nil || !strings.Contains(*e.Reason, "server lacks host setup")) {
			t.Errorf("%s: reason %v", e.Ref, e.Reason)
		}
	}
	// A different failure is still an error.
	r["TeamHostSetup"] = `{"errors":[{"message":"nope","extensions":{"code":"FORBIDDEN"}}]}`
	if _, _, err := runTeamTask(t, r, "team", "host", "import", "--from", hostYAML2061(t), "--host", "orca1"); err == nil {
		t.Fatal("a FORBIDDEN read must not be reported as a missing server feature")
	}
}
