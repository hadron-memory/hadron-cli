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
	"time"

	"github.com/hadron-memory/hadron-cli/internal/delivery"
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
const noOps = `{"data":{"collectHostOperations":[]}}`

const noBatches = `{"data":{"deliveryBatches":{"batches":[],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}`

// cli#907: with delivery on, each step also reads this host's batches, with
// the host key and no session, and the status snapshot records the read.
func TestTeamHostRunOnceReadsBatchesWithTheHostKey(t *testing.T) {
	cfg := writeHostConfig(t, deliveryOn)
	srv, calls := hostServerFake(t, map[string]string{
		"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": noOps, "DeliveryBatches": noBatches,
	})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("run --once: %v", err)
	}
	var read *hostCall
	for i, c := range calls() {
		if c.auth != "Bearer hk_host_key" || c.session != "" {
			t.Errorf("%s: auth %q session %q", c.op, c.auth, c.session)
		}
		if c.op == "DeliveryBatches" {
			read = &calls()[i]
		}
		if c.op == "AcknowledgeDeliveryBatches" {
			t.Errorf("an ack with nothing typed: %+v", c)
		}
	}
	if read == nil {
		t.Fatalf("no batch read: %+v", calls())
	}
	d := hostStatusJSON(t, cfg).Delivery
	if d.Mode != "enabled" || d.LastRead == nil || *d.LastRead != "read" || d.Stale {
		t.Fatalf("delivery %+v", d)
	}
}

// A key without attention:deliver switches delivery off for the run; the
// host's operations carry on and the run succeeds.
func TestTeamHostRunOnceScopeRefusalTurnsDeliveryOff(t *testing.T) {
	cfg := writeHostConfig(t, deliveryOn)
	srv, _ := hostServerFake(t, map[string]string{
		"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": noOps,
		"DeliveryBatches": `{"errors":[{"message":"scope","extensions":{"code":"APP_KEY_SCOPE_REQUIRED"}}],"data":null}`,
	})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("run --once: %v", err)
	}
	if !strings.Contains(out.String(), "delivery-off") {
		t.Errorf("the operator is told delivery is off:\n%s", out.String())
	}
	if d := hostStatusJSON(t, cfg).Delivery; d.Mode != "off-by-scope" {
		t.Fatalf("delivery %+v", d)
	}
}

// Rodolfo's §10 P2 on #939, end to end: a refusal that echoes the host key
// never reaches the operator's output, in either format.
func TestTeamHostRunNeverPrintsUpstreamDeliveryText(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		cfg := writeHostConfig(t, deliveryOn)
		srv, _ := hostServerFake(t, map[string]string{
			"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": noOps,
			"DeliveryBatches": `{"errors":[{"message":"echo hk_host_key PRIVATE-CHAT-TEXT","extensions":{"code":"FORBIDDEN"}}],"data":null}`,
		})
		f, out := testFactory(t)
		root := NewRootCmd(f)
		args := []string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL}
		if asJSON {
			args = append(args, "--json")
		}
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("run --once: %v", err)
		}
		if strings.Contains(out.String(), "hk_host_key") || strings.Contains(out.String(), "PRIVATE") {
			t.Fatalf("json=%v output carries upstream text:\n%s", asJSON, out.String())
		}
		if !strings.Contains(out.String(), "FORBIDDEN") {
			t.Fatalf("json=%v output lacks the code:\n%s", asJSON, out.String())
		}
	}
}

// With delivery off, nothing reads batches.
func TestTeamHostRunOnceWithoutDeliveryReadsNoBatches(t *testing.T) {
	cfg := writeHostConfig(t, "")
	srv, calls := hostServerFake(t, map[string]string{"RegisterHost": hostRegistrationAnswer, "CollectHostOperations": noOps})
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "run", "--once", "--config", cfg, "--server", srv.URL})
	if err := root.Execute(); err != nil {
		t.Fatalf("run --once: %v", err)
	}
	for _, c := range calls() {
		if c.op == "DeliveryBatches" {
			t.Fatalf("read batches with delivery off: %+v", calls())
		}
	}
}

// cli#906 row 4: two workers whose worktree tokens name one directory
// through a symlink are refused at start, before anything reaches the server.
func TestTeamHostRunRefusesTwoWorkersOnOneDirectory(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	cfg := writeHostConfig(t, "    worktrees: [repo/a]\n  dara:\n    family: codex\n    launch: [x]\n    worktrees: [repo/b]\n")
	trees := filepath.Join(filepath.Dir(cfg), "trees", "repo")
	if err := os.MkdirAll(filepath.Join(trees, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(trees, "a"), filepath.Join(trees, "b")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"--server", srv.URL, "team", "host", "run", "--once", "--config", cfg})
	err := root.Execute()
	if code := exitCodeFor(err); code != exitcode.Usage || !strings.Contains(err.Error(), "one directory") {
		t.Fatalf("exit %d: %v", code, err)
	}
	if calls != 0 {
		t.Fatalf("%d server calls before the refusal", calls)
	}
}

// cli#906 row 3: `team host status` adds the poller's local delivery
// snapshot. It is observational, reads no server state for it, and shows
// codes only.
type hostDeliveryStatus struct {
	Delivery struct {
		Enabled    bool    `json:"enabled"`
		Mode       string  `json:"mode"`
		OrcaBin    string  `json:"orcaBin"`
		LastStepAt *string `json:"lastStepAt"`
		LastRead   *string `json:"lastRead"`
		Stale      bool    `json:"stale"`
		Error      *string `json:"error"`
		// Wakes (cli#906 row 1).
		WakeRead          *string `json:"wakeRead"`
		BudgetExhaustedAt *string `json:"budgetExhaustedAt"`
		Dormant           int     `json:"dormant"`
		Withheld          int     `json:"withheld"`
		Workers           []struct {
			Worker           string  `json:"worker"`
			WorkerID         string  `json:"workerId"`
			LastKind         string  `json:"lastKind"`
			LastReason       string  `json:"lastReason"`
			LastAt           *string `json:"lastAt"`
			ConfirmedUnacked int     `json:"confirmedUnacked"`
			ResolvedBy       string  `json:"resolvedBy"`
			LastFor          string  `json:"lastFor"`
			TicksUnacked     int     `json:"ticksUnacked"`
			Unreported       bool    `json:"unreported"`
		} `json:"workers"`
	} `json:"delivery"`
}

func runHostStatus(t *testing.T, cfg string, args ...string) (string, error) {
	t.Helper()
	srv, _ := hostServerFake(t, map[string]string{"HostRegistrations": `{"data":{"hostRegistrations":[]}}`})
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"team", "host", "status", "--config", cfg, "--server", srv.URL}, args...))
	err := root.Execute()
	return out.String(), err
}

func hostStatusJSON(t *testing.T, cfg string) hostDeliveryStatus {
	t.Helper()
	out, err := runHostStatus(t, cfg, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st hostDeliveryStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return st
}

const deliveryOn = "delivery:\n  enabled: true\n  orca_bin: /opt/orca/bin/orca\n"

func TestTeamHostStatusShowsTheDeliveryBlock(t *testing.T) {
	cfg := writeHostConfig(t, deliveryOn)
	stateDir := filepath.Dir(cfg)
	now := time.Now().UTC()
	book := delivery.NewStatusBook(stateDir)
	book.Observe(delivery.Event{Time: now, Worker: "iris", WorkerID: "wkr1", Kind: delivery.Held, Reason: delivery.HoldDraft, ResolvedBy: "screen"})
	if err := book.Step(now, delivery.StepRead); err != nil {
		t.Fatal(err)
	}
	recs := &delivery.Records{Dir: filepath.Join(stateDir, "delivery", "records")}
	if err := recs.Put(delivery.Batch{WorkerID: "wkr1", BatchKey: "k1", Ranges: []delivery.Range{{ChannelID: "ch", Incarnation: 1, FromSeq: 5, ThroughSeq: 5, Seqs: []int{5}}}}, now); err != nil {
		t.Fatal(err)
	}
	d := hostStatusJSON(t, cfg).Delivery
	if !d.Enabled || d.Mode != "enabled" || d.OrcaBin != "/opt/orca/bin/orca" || d.Stale || d.Error != nil || d.LastStepAt == nil || d.LastRead == nil || *d.LastRead != "read" {
		t.Fatalf("delivery %+v", d)
	}
	if len(d.Workers) != 1 {
		t.Fatalf("workers %+v", d.Workers)
	}
	w := d.Workers[0]
	if w.Worker != "iris" || w.WorkerID != "wkr1" || w.LastKind != "held" || w.LastReason != "draft" || w.ConfirmedUnacked != 1 || w.ResolvedBy != "screen" || w.LastAt == nil {
		t.Fatalf("worker %+v", w)
	}
	human, err := runHostStatus(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"delivery: enabled", "orca /opt/orca/bin/orca", "iris", "held draft", "confirmed-unacked 1", "resolved by screen"} {
		if !strings.Contains(human, want) {
			t.Errorf("human status lacks %q:\n%s", want, human)
		}
	}
	if strings.Contains(human, "STALE") {
		t.Errorf("a fresh snapshot is not stale:\n%s", human)
	}
}

// cli#940: a hold the server was not told about says so, in both forms,
// and never claims a server hold, alarm or escalation.
func TestTeamHostStatusShowsAnUnreportedHold(t *testing.T) {
	cfg := writeHostConfig(t, deliveryOn)
	stateDir := filepath.Dir(cfg)
	now := time.Now().UTC()
	book := delivery.NewStatusBook(stateDir)
	book.Observe(delivery.Event{Time: now, Worker: "iris", WorkerID: "wkr1", Kind: delivery.Held, Reason: delivery.HoldDraft, Unreported: true})
	book.Observe(delivery.Event{Time: now, Worker: "dara", WorkerID: "wkr2", Kind: delivery.Held, Reason: delivery.HoldQuestion})
	if err := book.Step(now, delivery.StepRead); err != nil {
		t.Fatal(err)
	}
	d := hostStatusJSON(t, cfg).Delivery
	if len(d.Workers) != 2 || !d.Workers[0].Unreported || d.Workers[1].Unreported {
		t.Fatalf("workers %+v", d.Workers)
	}
	human, err := runHostStatus(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human, "held draft (batch), UNREPORTED: no server hold, alarm or escalation") {
		t.Errorf("human status:\n%s", human)
	}
	if strings.Count(human, "UNREPORTED") != 1 {
		t.Errorf("only iris's hold is unreported:\n%s", human)
	}
}

// cli#906 row 1: the wake read, the budget, and each worker's typed ticks
// whose ack has not landed. Counts and codes only: never a reason key.
func TestTeamHostStatusShowsTheWakeFields(t *testing.T) {
	cfg := writeHostConfig(t, deliveryOn)
	stateDir := filepath.Dir(cfg)
	now := time.Now().UTC()
	book := delivery.NewStatusBook(stateDir)
	book.Observe(delivery.Event{Time: now, Worker: "iris", WorkerID: "wkr1", Kind: delivery.Delivered, Reason: "ack-held", Wake: true})
	book.ObserveWakes(delivery.WakeRead{At: now, Read: delivery.WakeBudgetExhausted, Dormant: 2, Withheld: 3})
	if err := book.Step(now, delivery.StepRead); err != nil {
		t.Fatal(err)
	}
	wakes := &delivery.WakeRecords{Dir: filepath.Join(stateDir, "delivery", "wakes")}
	tick := delivery.WakeReason{Kind: delivery.WakeTick, TickerID: "tk-SECRETKEY", TickerName: "Check-in", LastTickAt: now}
	task := delivery.WakeReason{Kind: delivery.WakeTask, Task: &delivery.TaskReason{EventID: "ev1", Event: "assigned", ID: "t1", Title: "T"}}
	if err := wakes.Typed("wkr1", []delivery.WakeReason{tick, task}, now); err != nil {
		t.Fatal(err)
	}
	if err := wakes.Acked("wkr1", []string{task.Key()}, now); err != nil {
		t.Fatal(err)
	}
	d := hostStatusJSON(t, cfg).Delivery
	if d.WakeRead == nil || *d.WakeRead != "budget-exhausted" || d.BudgetExhaustedAt == nil || d.Dormant != 2 || d.Withheld != 3 {
		t.Fatalf("delivery %+v", d)
	}
	if len(d.Workers) != 1 || d.Workers[0].LastFor != "wake" || d.Workers[0].TicksUnacked != 1 {
		t.Fatalf("workers %+v", d.Workers)
	}
	human, err := runHostStatus(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wakes: budget-exhausted", "budget exhausted at", "dormant 2, withheld 3", "(wake)", "ticks typed-unacked 1"} {
		if !strings.Contains(human, want) {
			t.Errorf("human status lacks %q:\n%s", want, human)
		}
	}
	out, _ := runHostStatus(t, cfg, "--json")
	if strings.Contains(human+out, "SECRETKEY") || strings.Contains(human+out, "ev1") {
		t.Fatal("status shows a reason key")
	}
}

func TestTeamHostStatusDeliveryIsStaleWhenMissingCorruptOrOld(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		d := hostStatusJSON(t, writeHostConfig(t, deliveryOn)).Delivery
		if !d.Stale || d.Error == nil || *d.Error != "status-missing" || d.Workers == nil {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		cfg := writeHostConfig(t, deliveryOn)
		dir := filepath.Join(filepath.Dir(cfg), "delivery")
		_ = os.MkdirAll(dir, 0o700)
		_ = os.WriteFile(filepath.Join(dir, "status.json"), []byte("{"), 0o600)
		d := hostStatusJSON(t, cfg).Delivery
		if !d.Stale || d.Error == nil || *d.Error != "status-unreadable" {
			t.Fatalf("%+v", d)
		}
	})
	t.Run("old", func(t *testing.T) {
		cfg := writeHostConfig(t, deliveryOn)
		// poll_interval_seconds is 30: older than three intervals is stale.
		if err := delivery.NewStatusBook(filepath.Dir(cfg)).Step(time.Now().Add(-2*time.Minute), delivery.StepRead); err != nil {
			t.Fatal(err)
		}
		d := hostStatusJSON(t, cfg).Delivery
		if !d.Stale || d.Error != nil {
			t.Fatalf("%+v", d)
		}
		human, _ := runHostStatus(t, cfg)
		if !strings.Contains(human, "STALE") {
			t.Errorf("human status should say STALE:\n%s", human)
		}
	})
	t.Run("off by scope", func(t *testing.T) {
		cfg := writeHostConfig(t, deliveryOn)
		_ = delivery.NewStatusBook(filepath.Dir(cfg)).Step(time.Now(), delivery.StepOffByScope)
		if d := hostStatusJSON(t, cfg).Delivery; d.Mode != "off-by-scope" {
			t.Fatalf("%+v", d)
		}
	})
}

func TestTeamHostStatusDeliveryDisabled(t *testing.T) {
	d := hostStatusJSON(t, writeHostConfig(t, "")).Delivery
	if d.Enabled || d.Mode != "disabled" || d.Stale || d.Error != nil || d.Workers == nil || len(d.Workers) != 0 {
		t.Fatalf("%+v", d)
	}
}
