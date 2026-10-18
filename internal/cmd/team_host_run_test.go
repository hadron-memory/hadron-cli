package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// hadron-cli#831: `team host run|status` against a fake server. What is pinned
// here is the wire: the host key is the only credential on every host call
// (never HADRON_TOKEN), no session rides along, the workers list is always
// sent, and an operation outside the closed set is reported FAILED.

type hostCall struct {
	op, auth, session string
	vars              json.RawMessage
}

func hostServerFake(t *testing.T, responses map[string]string) (*httptest.Server, func() []hostCall) {
	t.Helper()
	var mu sync.Mutex
	var calls []hostCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, hostCall{body.OperationName, r.Header.Get("Authorization"), r.Header.Get("X-Hadron-Session"), body.Variables})
		mu.Unlock()
		resp, ok := responses[body.OperationName]
		if !ok {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []hostCall { mu.Lock(); defer mu.Unlock(); return append([]hostCall{}, calls...) }
}

func writeHostConfig(t *testing.T, extra string) string {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte("hk_host_key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := `app: hrn:app:acme.com:eng-team
label: box1
poll_interval_seconds: 30
key_file: ` + key + `
worktrees_root: ` + filepath.Join(dir, "trees") + `
workers:
  iris:
    family: claude
    launch: [claude-worker]
` + extra
	p := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const hostRegistrationAnswer = `{"data":{"registerHost":{"hostId":"h1","label":"box1","pollIntervalSeconds":30,
	"registeredAt":"2026-10-14T12:00:00Z","refreshedAt":"2026-10-14T12:00:00Z","live":true,
	"workers":[{"workerId":"wkr1","worker":{"id":"wkr1","urn":"hrn:worker:acme.com:eng-team:iris","name":"Iris"},"family":"claude","worktrees":[]}]}}}`

func polledOpJSON(id, typ, status string) string {
	return `{"id":"` + id + `","appId":"app1","hostId":"h1","type":"` + typ + `","args":{},"workerId":"wkr1",
		"worker":{"id":"wkr1","name":"Iris"},"status":"` + status + `","issuedBy":"USER","issuedByUserId":"u1",
		"issuedByWorkerId":null,"issuedAt":"2026-10-14T12:00:00Z","collectedAt":"2026-10-14T12:00:01Z",
		"finishedAt":null,"result":null,"failureReason":null}`
}

func TestTeamHostRunOnceUsesOnlyTheHostKey(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, calls := hostServerFake(t, map[string]string{
		"RegisterHost":          hostRegistrationAnswer,
		"CollectHostOperations": `{"data":{"collectHostOperations":[` + strings.Replace(polledOpJSON("o1", "shell.exec", "COLLECTED"), `"failureReason":null}`, `"failureReason":null,"launchGrant":null}`, 1) + `]}}`,
		"ReportHostOperation":   `{"data":{"reportHostOperation":` + polledOpJSON("o1", "shell.exec", "FAILED") + `}}`,
	})
	f, out := testFactory(t) // sets HADRON_TOKEN to a person's token
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("run --once: %v", err)
	}
	got := calls()
	if len(got) != 3 {
		t.Fatalf("want register, collect, report; got %+v", got)
	}
	for _, c := range got {
		if c.auth != "Bearer hk_host_key" {
			t.Errorf("%s authenticated with %q: the host key is the only credential on a host call", c.op, c.auth)
		}
		if c.session != "" {
			t.Errorf("%s carried a session (%q): refused APP_KEY_SESSION_BOUND", c.op, c.session)
		}
	}
	var reg struct {
		Input struct {
			AppRef  string           `json:"appRef"`
			Label   string           `json:"label"`
			Workers []map[string]any `json:"workers"`
		} `json:"input"`
	}
	if err := json.Unmarshal(got[0].vars, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.Input.AppRef != "hrn:app:acme.com:eng-team" || reg.Input.Label != "box1" || len(reg.Input.Workers) != 1 {
		t.Errorf("registration: %s", got[0].vars)
	}
	if wt, ok := reg.Input.Workers[0]["worktrees"].([]any); !ok || len(wt) != 0 {
		t.Errorf("worktrees must be sent as [], never null: %s", got[0].vars)
	}
	var rep struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(got[2].vars, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Input["outcome"] != "FAILED" || !strings.Contains(rep.Input["failureReason"].(string), "unknown operation type") {
		t.Errorf("an unknown type is reported FAILED: %s", got[2].vars)
	}
	if _, sent := rep.Input["worktree"]; sent {
		t.Errorf("worktree is sent only on a create's success: %s", got[2].vars)
	}
	if !strings.Contains(out.String(), "reported") {
		t.Errorf("the operator sees what happened:\n%s", out.String())
	}
}

func TestTeamHostRunRefusesWithoutAHostKey(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(cfg, []byte("app: a\nworktrees_root: /w\nworkers:\n  iris:\n    family: claude\n    launch: [x]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HADRON_HOST_KEY", "")
	f, _ := testFactory(t) // HADRON_TOKEN is set, and must not be used
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", "http://127.0.0.1:1"})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.AuthRequired {
		t.Errorf("exit = %d, want %d: %v", code, exitcode.AuthRequired, err)
	}
	if err == nil || !strings.Contains(err.Error(), "key_file") {
		t.Errorf("the refusal names where the key goes: %v", err)
	}
}

func TestTeamHostRunRefusesABadConfig(t *testing.T) {
	cases := map[string]string{
		"unknown field": "app: a\nworktrees_root: /w\nshell: bash\nworkers:\n  iris:\n    family: claude\n    launch: [x]\n",
		"bad family":    "app: a\nworktrees_root: /w\nworkers:\n  iris:\n    family: Claude Code\n    launch: [x]\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := filepath.Join(t.TempDir(), "host.yaml")
			if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			f, _ := testFactory(t)
			root := NewRootCmd(f)
			root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg})
			if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
				t.Errorf("exit = %d, want %d", code, exitcode.Usage)
			}
		})
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once"})
	if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
		t.Errorf("no --config: exit = %d, want %d", code, exitcode.Usage)
	}
}

func TestTeamHostRunMapsARefusedHostKey(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, _ := hostServerFake(t, map[string]string{
		"RegisterHost": `{"errors":[{"message":"no host scope","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}],"data":null}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if code := exitCodeFor(root.Execute()); code != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d (Forbidden)", code, exitcode.Forbidden)
	}
}

func TestTeamHostStatusShowsConfigAndRegistration(t *testing.T) {
	cfg := writeHostConfig(t, "    worktrees: [repo/iris]\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), "hadron-host-state.json"), []byte(`{"created":{},"keep":{},"hostId":"h1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	srv, calls := hostServerFake(t, map[string]string{
		"HostRegistrations": `{"data":{"hostRegistrations":[{"hostId":"h1","label":"box1","pollIntervalSeconds":30,
			"registeredAt":"2026-10-14T12:00:00Z","refreshedAt":"2026-10-14T12:05:00Z","live":true,
			"workers":[{"workerId":"wkr1","worker":{"id":"wkr1","name":"Iris"},"family":"claude","worktrees":["repo/iris"]}]},
			{"hostId":"h2","label":"box1","pollIntervalSeconds":30,"registeredAt":"2026-10-14T12:00:00Z","refreshedAt":"2026-10-14T12:05:00Z","live":false,"workers":[]}]}}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "status", "--config", cfg, "--server", srv.URL, "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var dto struct {
		Workers []struct {
			Worker    string   `json:"worker"`
			Worktrees []string `json:"worktrees"`
		} `json:"workers"`
		Kept         map[string]string `json:"kept"`
		Registration *struct {
			HostID string `json:"hostId"`
			Live   bool   `json:"live"`
		} `json:"registration"`
	}
	if err := json.Unmarshal([]byte(out.String()), &dto); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(dto.Workers) != 1 || dto.Workers[0].Worktrees[0] != "repo/iris" {
		t.Errorf("workers: %+v", dto.Workers)
	}
	if dto.Kept == nil {
		t.Error("kept renders as {}, not null")
	}
	if dto.Registration == nil || dto.Registration.HostID != "h1" || !dto.Registration.Live {
		t.Errorf("registration: %+v", dto.Registration)
	}
	for _, c := range calls() {
		if c.op != "HostRegistrations" {
			t.Errorf("status changes nothing; it called %s", c.op)
		}
		if c.auth != "Bearer hk_host_key" {
			t.Errorf("status reads with the host key: %q", c.auth)
		}
	}
}

// A revoked key does not heal by retrying: run exits with its code instead of
// looping on it.
func TestTeamHostRunExitsOnARefusedKey(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, _ := hostServerFake(t, map[string]string{
		"RegisterHost": `{"errors":[{"message":"revoked","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}],"data":null}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--config", cfg, "--server", srv.URL})
	if code := exitCodeFor(root.Execute()); code != exitcode.Forbidden {
		t.Errorf("exit = %d, want %d", code, exitcode.Forbidden)
	}
}

func TestTeamHostRunOnceFailsWhenAReportDidNotLand(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, _ := hostServerFake(t, map[string]string{
		"RegisterHost":          hostRegistrationAnswer,
		"CollectHostOperations": `{"data":{"collectHostOperations":[` + strings.Replace(polledOpJSON("o1", "worker.deliver", "COLLECTED"), `"failureReason":null}`, `"failureReason":null,"launchGrant":null}`, 1) + `]}}`,
		"ReportHostOperation":   `{"errors":[{"message":"boom","extensions":{"code":"INTERNAL_SERVER_ERROR"}}],"data":null}`,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if code := exitCodeFor(root.Execute()); code != exitcode.Unavailable {
		t.Errorf("exit = %d, want %d: an undelivered report is not a clean poll", code, exitcode.Unavailable)
	}
}

// Until S1's batch query is on server main (hadron-server#2017), a host
// config that turns delivery on must fail loudly, not load and do nothing.
func TestTeamHostRunRefusesDeliveryUntilTheBatchSourceShips(t *testing.T) {
	cfg := writeHostConfig(t, "delivery:\n  enabled: true\n  orca_bin: /usr/local/bin/orca\n")
	srv, calls := hostServerFake(t, map[string]string{})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage {
		t.Fatalf("exit = %d, want %d: %v", code, exitcode.Usage, err)
	}
	if err == nil || !strings.Contains(err.Error(), "hadron-server#2017") {
		t.Errorf("the refusal names what it waits on: %v", err)
	}
	if len(calls()) != 0 {
		t.Errorf("no host call before the refusal: %+v", calls())
	}
}
