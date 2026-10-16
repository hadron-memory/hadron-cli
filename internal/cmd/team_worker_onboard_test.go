package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/teamrouter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTeamWorkerOnboardHelp837(t *testing.T) {
	_, out, err := runTeamTask(t, hostResponses(), "team", "worker", "onboard", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Orca-hosted workers only", "--apply", "--host", "orca1", "dry-run"} {
		if !strings.Contains(out, want) {
			t.Fatalf("onboard help missing %q:\n%s", want, out)
		}
	}
}

// A real fake queue at the HTTP boundary, with real executable Orca fixtures.
// The fixture refuses every Orca verb except reads and the one display update.
func TestTeamWorkerOnboardApplyQueueOnlyAndRetry837(t *testing.T) {
	r, args, log, configPath := onboardFixture837(t, true)
	dir := filepath.Dir(configPath)
	worktree := filepath.Join(dir, "hadron-cli", "iris")
	worktreeJSON, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"worktree": map[string]any{"path": worktree, "displayName": "Iris [backend-engineer]", "branch": "refs/heads/iris/work"}}})
	terminalJSON, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": []map[string]any{{"handle": "term1", "worktreePath": worktree, "agentIdentity": "claude", "connected": true, "writable": true}}}})
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncase \"$1 $2\" in\n'status --json') echo '{\"ok\":true,\"result\":{\"runtime\":{\"reachable\":true}}}';;\n'worktree show') echo '" + string(worktreeJSON) + "';;\n'worktree set') echo '{\"ok\":true}';;\n'terminal list') echo '" + string(terminalJSON) + "';;\n*) exit 19;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "orca"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	var issued []map[string]any
	var stored []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var q struct {
			Op    string                     `json:"operationName"`
			Query string                     `json:"query"`
			Vars  map[string]json.RawMessage `json:"variables"`
		}
		if err := json.NewDecoder(req.Body).Decode(&q); err != nil {
			t.Error(err)
			return
		}
		if strings.HasPrefix(strings.TrimSpace(q.Query), "mutation") && q.Op != "IssueHostOperation" {
			t.Errorf("forbidden onboard mutation %s", q.Op)
			_, _ = w.Write([]byte(`{"errors":[{"message":"forbidden mutation"}]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch q.Op {
		case "HostOperations":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"hostOperations": map[string]any{"items": stored, "hasNextPage": false}}})
		case "IssueHostOperation":
			var in map[string]any
			_ = json.Unmarshal(q.Vars["input"], &in)
			issued = append(issued, in)
			a := in["args"].(map[string]any)
			a["workerId"] = "wkr1"
			op := map[string]any{"id": fmt.Sprint("hop", len(issued)), "appId": in["appRef"], "hostId": in["hostId"], "workerId": "wkr1", "type": in["type"], "args": a, "status": "SUCCEEDED"}
			if in["type"] == "worktree.create" {
				op["result"] = map[string]any{"worktree": "hadron-cli/iris"}
			}
			stored = append(stored, op)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"issueHostOperation": op}})
		default:
			if response, ok := r[q.Op]; ok {
				_, _ = w.Write([]byte(response))
			} else {
				t.Errorf("unexpected GraphQL %s", q.Op)
				_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
			}
		}
	}))
	defer srv.Close()
	writeTeamBinding(t)
	setTeamBindingServer(t, srv.URL)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
				cfg, err := teamrouter.LoadConfig(configPath)
				if err == nil {
					_ = teamrouter.RecordReload(cfg)
				}
			}
		}
	}()
	defer func() { close(done); <-stopped }()
	for attempt := 0; attempt < 2; attempt++ {
		f, out := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append(append([]string{"--server", srv.URL}, args...), "--apply", "--timeout", "5s"))
		if err := root.Execute(); err != nil {
			t.Fatalf("attempt%d: %v\n%s", attempt, err, out.String())
		}
		var dto struct {
			DryRun   bool     `json:"dryRun"`
			Verified []string `json:"verified"`
		}
		if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
			t.Fatal(err)
		}
		if dto.DryRun || len(dto.Verified) != 8 {
			t.Fatalf("incomplete verification %s", out.String())
		}
	}
	if len(issued) != 2 {
		t.Fatalf("retry enqueued duplicate operations: %v", issued)
	}
	for i, want := range []struct {
		typ  string
		args map[string]any
	}{
		{"worktree.create", map[string]any{"workerId": "wkr1", "repo": "hadron-memory/hadron-cli", "baseBranch": "main", "branchName": "iris/work", "label": "iris"}},
		{"worker.start", map[string]any{"workerId": "wkr1", "worktree": "hadron-cli/iris", "family": "claude"}},
	} {
		if issued[i]["type"] != want.typ || !reflect.DeepEqual(issued[i]["args"], want.args) {
			t.Fatalf("typed queue drift: %v", issued[i])
		}
	}
	got, _ := os.ReadFile(log)
	for _, line := range strings.Split(strings.TrimSpace(string(got)), "\n") {
		if line != "status --json" && !strings.HasPrefix(line, "worktree show --worktree path:") && !strings.HasPrefix(line, "worktree set --worktree path:") && !strings.HasPrefix(line, "terminal list --worktree path:") {
			t.Fatalf("direct privileged Orca call: %s", line)
		}
	}
	cfg, err := teamrouter.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Roster) != 1 || cfg.Roster[0].Worker != "wkr1" || cfg.Roster[0].Worktree != worktree {
		t.Fatalf("roster not idempotent: %+v", cfg.Roster)
	}
}

func onboardFixture837(t *testing.T, reachable bool) (map[string]string, []string, string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "orca.log")
	bin := filepath.Join(dir, "orca")
	status := `{"ok":true,"result":{"runtime":{"reachable":true}}}`
	if !reachable {
		status = `{"ok":false}`
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + log + "'\ncase \"$1 $2\" in\n'status --json') printf '%s\\n' '" + status + "';;\n*) exit 19;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "router.json")
	config, _ := json.Marshal(map[string]any{"app": "capp100000000000000000000", "channel": "chan1", "stateDir": filepath.Join(dir, "state"), "orcaCli": bin, "roster": []any{}})
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	r := hostResponses()
	worker := strings.Replace(irisWorkerJSON, `"retiredAt":null`, `"heldByUserId":"u-holger","hasLiveSession":true,"retiredAt":null`, 1)
	r["GetWorker"] = `{"data":{"worker":` + worker + `}}`
	r["RouterRegister"] = `{"data":{"attendeeRegister":[{"namedBy":"WORKER","channel":{"id":"chan1"},"entry":{"mentionOnly":true}}]}}`
	args := []string{"team", "worker", "onboard", "--name", "hrn:worker:acme.com:eng-team:iris", "--host", "orca1", "--repo", "hadron-memory/hadron-cli", "--branch", "iris/work", "--label", "iris", "--family", "claude", "--worktrees-root", dir, "--config", configPath, "--json"}
	return r, args, log, configPath
}

func TestTeamWorkerOnboardDryRun837(t *testing.T) {
	r, args, log, configPath := onboardFixture837(t, true)
	before, _ := os.ReadFile(configPath)
	calls, out, err := runTeamTask(t, r, args...)
	if err != nil {
		t.Fatal(err)
	}
	var dto struct {
		DryRun     bool `json:"dryRun"`
		Operations []struct {
			Type string            `json:"type"`
			Args map[string]string `json:"args"`
		} `json:"operations"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"queue": []`) {
		t.Fatalf("queue must be an empty array: %s", out)
	}
	if !dto.DryRun || len(dto.Operations) != 2 || dto.Operations[0].Type != "worktree.create" || dto.Operations[1].Type != "worker.start" {
		t.Fatalf("unexpected plan %s", out)
	}
	for _, c := range *calls {
		if c.Op == "IssueHostOperation" || strings.Contains(c.Op, "Session") || strings.Contains(c.Op, "Cast") {
			t.Fatalf("dry-run submitted %s", c.Op)
		}
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != string(before) {
		t.Fatal("dry-run changed config")
	}
	got, _ := os.ReadFile(log)
	if string(got) != "status --json\n" {
		t.Fatalf("unexpected Orca calls %s", got)
	}
}

func TestTeamWorkerOnboardOutsideOrca837(t *testing.T) {
	r, args, log, _ := onboardFixture837(t, false)
	calls, _, err := runTeamTask(t, r, append(args, "--apply")...)
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "Orca") || !strings.Contains(err.Error(), "nothing was queued or changed") {
		t.Fatalf("wrong refusal %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("outside-Orca made server calls: %v", *calls)
	}
	got, _ := os.ReadFile(log)
	if string(got) != "status --json\n" {
		t.Fatalf("unexpected Orca calls %s", got)
	}
}

func TestTeamWorkerOnboardBroadRegisterRefused837(t *testing.T) {
	r, args, _, _ := onboardFixture837(t, true)
	r["RouterRegister"] = strings.Replace(r["RouterRegister"], `"mentionOnly":true`, `"mentionOnly":false`, 1)
	calls, _, err := runTeamTask(t, r, append(args, "--apply")...)
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "mention-only") {
		t.Fatalf("wrong refusal %v", err)
	}
	for _, c := range *calls {
		if c.Op == "IssueHostOperation" {
			t.Fatal("queued before register prerequisite")
		}
	}
}

func TestTeamWorkerOnboardFailurePrintsPartialQueue837(t *testing.T) {
	r, args, log, configPath := onboardFixture837(t, true)
	r["HostOperations"] = `{"data":{"hostOperations":{"items":[{"id":"failed-op","hostId":"orca1","workerId":"wkr1","type":"worktree.create","args":{"workerId":"wkr1","repo":"hadron-memory/hadron-cli","baseBranch":"main","branchName":"iris/work","label":"iris"},"status":"FAILED"}],"hasNextPage":false}}}`
	before, _ := os.ReadFile(configPath)
	calls, out, err := runTeamTask(t, r, append(args, "--apply")...)
	if err == nil || !strings.Contains(err.Error(), "failed-op") {
		t.Fatalf("wrong failure %v", err)
	}
	var dto struct {
		Error *string                       `json:"error"`
		Queue []struct{ ID, Status string } `json:"queue"`
	}
	if err := json.Unmarshal([]byte(out), &dto); err != nil {
		t.Fatalf("missing partial JSON: %v %s", err, out)
	}
	if dto.Error == nil || len(dto.Queue) != 1 || dto.Queue[0].ID != "failed-op" || dto.Queue[0].Status != "FAILED" {
		t.Fatalf("lost queue result %s", out)
	}
	for _, c := range *calls {
		if c.Op == "IssueHostOperation" {
			t.Fatal("retried failed op")
		}
	}
	after, _ := os.ReadFile(configPath)
	if string(after) != string(before) {
		t.Fatal("queue failure changed roster")
	}
	got, _ := os.ReadFile(log)
	if string(got) != "status --json\n" {
		t.Fatalf("queue failure made direct calls %s", got)
	}
}
