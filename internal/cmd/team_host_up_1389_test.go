package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gofrs/flock"
	"github.com/hadron-memory/hadron-cli/internal/cmd/agentic"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// cli#1389: `team host up` against a fake server (a stateful host-operation
// queue), a fake Orca (an executable that answers only reads) and a fake
// desired-state source (the --desired-state-file).

type hostUpWorker1389 struct {
	ID, Name, Desired, Reason string
	Session                   string
	NoRepo                    bool
}

type hostUpFixture1389 struct {
	t       *testing.T
	root    string // worktrees root
	desired string // desired-state file
	orcaLog string
	orcaBin string
	srv     *httptest.Server
	mu      sync.Mutex
	calls   []attnCall
	issued  []map[string]any
	stored  []map[string]any
	refuse  string // IssueHostOperation answers this error code (FORBIDDEN, UNAUTHENTICATED)
	regErr  string // HostRegistrations answers this error code
	async   bool   // an issued operation is ISSUED; it completes on the next queue read
	// cli#2069: per-operation queue scripts. On each queue read an ISSUED or
	// COLLECTED operation takes its next scripted status (SUCCEEDED runs the
	// host's half); a stalled one never moves.
	transitions map[string][]string
	stall       map[string]bool
	// cli#1389 server adapter: when desiredSrv is set the command reads its
	// desired state from the fake server (no --desired-state-file).
	desiredSrv  map[string]any
	desiredErr  string
	desiredRaw  string // answered verbatim when set (a malformed success)
	desiredVars []map[string]json.RawMessage
	// cli#2085: the hostSetup answer (verbatim when set; unset = an old server's
	// schema-skew refusal) and what each read carried.
	setupRaw  string
	setupVars []map[string]json.RawMessage
	// cli#2085: Worker.repos by worker id (unset = []), an error code for the
	// read, and which workers' repos were read.
	repos      map[string][]string
	reposErr   string
	reposReads []string
	onIssue    func()   // runs after an issued operation is stored
	registry   []string // worker ids the host registration lists
	// cli#2652: the App's server sweep mode that --apply reads before it acts.
	// newHostUpFixture1389 starts it at APPLY, so every older --apply test keeps
	// its meaning; sweepErr is an error code the read answers instead.
	sweepMode  string
	sweepErr   string
	sweepReads int
}

func (fx *hostUpFixture1389) dir(name string) string {
	return filepath.Join(fx.root, "hadron-cli", strings.ToLower(name))
}

// writeDesired writes the fake source's document.
func (fx *hostUpFixture1389) writeDesired(sweepMode string, workers ...hostUpWorker1389) {
	fx.t.Helper()
	list := []map[string]any{}
	for _, w := range workers {
		setup := map[string]any{"worktree": "hadron-cli/" + strings.ToLower(w.Name), "family": "claude"}
		if !w.NoRepo {
			setup["repo"], setup["baseBranch"], setup["branch"] = "hadron-memory/hadron-cli", "main", strings.ToLower(w.Name)+"/work"
		}
		row := map[string]any{"workerId": w.ID, "name": w.Name, "desired": w.Desired, "reason": w.Reason, "wouldBe": nil, "sessionId": nil, "setup": setup}
		if w.Session != "" {
			row["sessionId"] = w.Session
		}
		list = append(list, row)
	}
	raw, _ := json.Marshal(map[string]any{"hostId": "orca1", "sweepMode": sweepMode, "workers": list})
	if err := os.WriteFile(fx.desired, raw, 0o600); err != nil {
		fx.t.Fatal(err)
	}
}

// present makes a worker's worktree exist, with n live agent terminals in it.
func (fx *hostUpFixture1389) present(name string, terminals int) {
	fx.t.Helper()
	fx.presentTerminals(name, terminals, true)
}

func (fx *hostUpFixture1389) presentTerminals(name string, terminals int, connected bool) {
	fx.t.Helper()
	dir := fx.dir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fx.t.Fatal(err)
	}
	rows := []map[string]any{}
	for i := 0; i < terminals; i++ {
		rows = append(rows, map[string]any{"handle": fmt.Sprint("term", i), "worktreePath": dir, "agentIdentity": "claude", "connected": connected, "writable": true})
	}
	raw, _ := json.Marshal(map[string]any{"ok": true, "result": map[string]any{"terminals": rows}})
	if err := os.WriteFile(filepath.Join(dir, ".terminals.json"), raw, 0o600); err != nil {
		fx.t.Fatal(err)
	}
}

func newHostUpFixture1389(t *testing.T) *hostUpFixture1389 {
	t.Helper()
	base := t.TempDir()
	fx := &hostUpFixture1389{t: t, root: filepath.Join(base, "workspaces"), desired: filepath.Join(base, "desired.json"),
		orcaLog: filepath.Join(base, "orca.log"), orcaBin: filepath.Join(base, "orca"), registry: []string{"wkr-iris", "wkr-jonas"}, sweepMode: "APPLY"}
	if err := os.MkdirAll(fx.root, 0o700); err != nil {
		t.Fatal(err)
	}
	// Orca answers status and `terminal list` only; any other verb exits 19.
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + fx.orcaLog + "'\ncase \"$1 $2\" in\n" +
		"'status --json') echo '{\"ok\":true,\"result\":{\"runtime\":{\"reachable\":true}}}';;\n" +
		"'terminal list') d=\"${4#path:}\"; if [ -f \"$d/.terminals.json\" ]; then cat \"$d/.terminals.json\"; else echo '{\"ok\":true,\"result\":{\"terminals\":[]}}'; fi;;\n" +
		"*) exit 19;;\nesac\n"
	if err := os.WriteFile(fx.orcaBin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	canned := hostResponses()
	fx.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var q struct {
			Op    string                     `json:"operationName"`
			Query string                     `json:"query"`
			Vars  map[string]json.RawMessage `json:"variables"`
		}
		raw, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(raw, &q)
		fx.mu.Lock()
		defer fx.mu.Unlock()
		if q.Op != "GetBoundSessionEnd" {
			fx.calls = append(fx.calls, attnCall{Op: q.Op, Session: req.Header.Get("X-Hadron-Session"), Query: q.Query})
		}
		w.Header().Set("Content-Type", "application/json")
		switch q.Op {
		case "HostRegistrations":
			if fx.regErr != "" {
				_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + fx.regErr + `"}}]}`))
				return
			}
			workers := []map[string]any{}
			for _, id := range fx.registry {
				workers = append(workers, map[string]any{"workerId": id, "worker": map[string]any{"id": id, "name": id}, "family": "claude", "worktrees": []string{}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"hostRegistrations": []any{map[string]any{
				"hostId": "orca1", "label": "Orca box 1", "pollIntervalSeconds": 30, "registeredAt": "2026-10-14T00:00:00Z",
				"refreshedAt": "2026-10-14T01:00:00Z", "live": true, "workers": workers}}}})
		case "HostOperations", "HostOperationsWithReason":
			var worker string
			_ = json.Unmarshal(q.Vars["worker"], &worker)
			items := []map[string]any{}
			for _, op := range fx.stored {
				if op["workerId"] == worker {
					items = append(items, op)
				}
			}
			b, _ := json.Marshal(map[string]any{"data": map[string]any{"hostOperations": map[string]any{"items": items, "hasNextPage": false}}})
			_, _ = w.Write(b)
			for _, op := range fx.stored {
				if op["status"] != "ISSUED" && op["status"] != "COLLECTED" {
					continue
				}
				id, _ := op["id"].(string)
				if fx.stall[id] {
					continue
				}
				if seq := fx.transitions[id]; len(seq) > 0 {
					fx.transitions[id] = seq[1:]
					switch seq[0] {
					case "SUCCEEDED":
						fx.complete(op)
					case "FAILED":
						op["status"], op["failureReason"] = "FAILED", "git worktree add failed"
					default:
						op["status"] = seq[0]
					}
					continue
				}
				fx.complete(op)
			}
		case "GetAppSweepMode":
			fx.sweepReads++
			if fx.sweepErr != "" {
				_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + fx.sweepErr + `"}}]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"app": map[string]any{"sweepMode": fx.sweepMode}}})
		case "HostUpWorkerRepos":
			var ref string
			_ = json.Unmarshal(q.Vars["ref"], &ref)
			fx.reposReads = append(fx.reposReads, ref)
			if fx.reposErr != "" {
				_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + fx.reposErr + `"}}]}`))
				return
			}
			repos := fx.repos[ref]
			if repos == nil {
				repos = []string{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"worker": map[string]any{"id": ref, "repos": repos}}})
		case "TeamHostSetup":
			// cli#2085: unset, the server predates hostSetup (schema skew), so
			// every older test keeps meaning "HOST behaviour".
			fx.setupVars = append(fx.setupVars, q.Vars)
			if fx.setupRaw != "" {
				_, _ = w.Write([]byte(fx.setupRaw))
				return
			}
			_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field \"hostSetup\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}]}`))
		case "HostDesiredState":
			fx.desiredVars = append(fx.desiredVars, q.Vars)
			if fx.desiredRaw != "" {
				_, _ = w.Write([]byte(fx.desiredRaw))
				return
			}
			if fx.desiredErr != "" {
				_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + fx.desiredErr + `"}}]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"hostDesiredState": fx.desiredSrv}})
		case "IssueHostOperation":
			if fx.refuse != "" {
				_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"refused","extensions":{"code":"` + fx.refuse + `"}}]}`))
				return
			}
			var in map[string]any
			_ = json.Unmarshal(q.Vars["input"], &in)
			fx.issued = append(fx.issued, in)
			worker := in["worker"].(string)
			args := in["args"].(map[string]any)
			args["workerId"] = worker
			op := map[string]any{"id": fmt.Sprint("hop", len(fx.stored)+1), "appId": "capp100000000000000000000", "hostId": in["hostId"], "workerId": worker, "type": in["type"], "args": args, "status": "ISSUED"}
			if !fx.async {
				fx.complete(op)
			}
			fx.stored = append(fx.stored, op)
			if fx.onIssue != nil {
				fx.onIssue()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"issueHostOperation": op}})
		default:
			if strings.HasPrefix(strings.TrimSpace(q.Query), "mutation") {
				t.Errorf("host up sent a mutation it must never send: %s", q.Op)
			}
			if response, ok := canned[q.Op]; ok {
				_, _ = w.Write([]byte(response))
			} else if response, ok := unstubbedDefault(q.Op); ok {
				_, _ = w.Write([]byte(response))
			} else {
				t.Errorf("unexpected GraphQL %s", q.Op)
				_, _ = w.Write([]byte(`{"errors":[{"message":"unexpected"}]}`))
			}
		}
	}))
	t.Cleanup(fx.srv.Close)
	return fx
}

// complete is the host poller's half of an operation: the worktree appears,
// or the agent starts and binds; the operation SUCCEEDS.
func (fx *hostUpFixture1389) complete(op map[string]any) {
	args := op["args"].(map[string]any)
	switch op["type"] {
	case "worktree.create":
		op["result"] = map[string]any{"worktree": "hadron-cli/" + args["label"].(string)}
		if err := os.MkdirAll(filepath.Join(fx.root, "hadron-cli", args["label"].(string)), 0o700); err != nil {
			fx.t.Error(err)
		}
	case "worker.start":
		fx.present(filepath.Base(args["worktree"].(string)), 1)
		fx.bindInDesired(op["workerId"].(string))
	}
	op["status"] = "SUCCEEDED"
}

// bindInDesired is the server's half of a start: the worker now has a session.
func (fx *hostUpFixture1389) bindInDesired(worker string) {
	if fx.desiredSrv != nil {
		for _, w := range fx.desiredSrv["workers"].([]map[string]any) {
			if w["workerId"] == worker {
				w["openSessionId"] = "s-" + worker
			}
		}
		return
	}
	raw, err := os.ReadFile(fx.desired)
	if err != nil {
		fx.t.Error(err)
		return
	}
	var doc map[string]any
	_ = json.Unmarshal(raw, &doc)
	for _, w := range doc["workers"].([]any) {
		if row := w.(map[string]any); row["workerId"] == worker {
			row["sessionId"] = "s-" + worker
		}
	}
	raw, _ = json.Marshal(doc)
	if err := os.WriteFile(fx.desired, raw, 0o600); err != nil {
		fx.t.Error(err)
	}
}

type hostUpItem1389 struct {
	WorkerID    *string           `json:"workerId"`
	Worker      string            `json:"worker"`
	Action      *string           `json:"action"`
	Reason      string            `json:"reason"`
	Args        map[string]string `json:"args"`
	OperationID *string           `json:"operationId"`
	Detail      *string           `json:"detail"`
}

type hostUpOut1389 struct {
	AppRef    string  `json:"appRef"`
	HostID    string  `json:"hostId"`
	DryRun    bool    `json:"dryRun"`
	SweepMode *string `json:"sweepMode"`
	// cli#2085: the App's hostSetupSource; null when it was not read.
	SetupSource *string          `json:"hostSetupSource"`
	Planned     []hostUpItem1389 `json:"planned"`
	Applied     []hostUpItem1389 `json:"applied"`
	Skipped     []hostUpItem1389 `json:"skipped"`
	Failed      []hostUpItem1389 `json:"failed"`
	Error       *string          `json:"error"`
	Source      string           `json:"source"`
}

// run executes `team host up --json` from a bound worktree and decodes stdout.
func (fx *hostUpFixture1389) run(extra ...string) (hostUpOut1389, string, error) {
	fx.t.Helper()
	writeTeamBinding(fx.t)
	setTeamBindingServer(fx.t, fx.srv.URL)
	f, out := testFactory(fx.t)
	root := NewRootCmd(f)
	args := []string{"--server", fx.srv.URL, "team", "host", "up", "--host", "orca1", "--worktrees-root", fx.root,
		"--orca-bin", fx.orcaBin, "--timeout", "5s", "--json"}
	if fx.desiredSrv == nil && fx.desiredErr == "" && fx.desiredRaw == "" {
		args = append(args, "--desired-state-file", fx.desired)
	}
	root.SetArgs(append(args, extra...))
	err := root.Execute()
	var dto hostUpOut1389
	if out.Len() > 0 {
		if jerr := json.Unmarshal([]byte(out.String()), &dto); jerr != nil {
			fx.t.Fatalf("stdout is not the host up document: %v\n%s", jerr, out.String())
		}
	}
	return dto, out.String(), err
}

func (fx *hostUpFixture1389) mutations() []string {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	var got []string
	for _, c := range fx.calls {
		if strings.HasPrefix(strings.TrimSpace(c.Query), "mutation") {
			got = append(got, c.Op)
		}
	}
	return got
}

func summary1389(items []hostUpItem1389) []string {
	got := []string{}
	for _, it := range items {
		action := "-"
		if it.Action != nil {
			action = *it.Action
		}
		got = append(got, it.Worker+" "+action+" "+it.Reason)
	}
	return got
}

func wantItems1389(t *testing.T, what string, got []hostUpItem1389, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if s := summary1389(got); !reflect.DeepEqual(s, want) {
		t.Fatalf("%s = %q, want %q", what, s, want)
	}
}

// The standard pair: Iris fully running, Jonas missing his worktree.
func (fx *hostUpFixture1389) irisRunningJonasMissing() {
	fx.present("Iris", 1)
	fx.writeDesired("APPLY",
		hostUpWorker1389{ID: "wkr-iris", Name: "Iris", Desired: "RUNNING", Reason: "LONG_LIVED", Session: "s-iris"},
		hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "HOLDS_TASK"})
}

// Acceptance 1 and 2: one fully running worker and one missing its worktree
// plan exactly worktree.create + worker.start for the second; a dry run sends
// reads only, and no read carries the bound worker's session.
func TestTeamHostUpDryRunPlansOnlyWhatIsMissing1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	for _, flags := range [][]string{nil, {"--dry-run"}} {
		dto, raw, err := fx.run(flags...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", flags, err, raw)
		}
		if !dto.DryRun || dto.HostID != "orca1" || dto.SweepMode == nil || *dto.SweepMode != "APPLY" || dto.Error != nil {
			t.Fatalf("%v: header = %+v", flags, dto)
		}
		wantItems1389(t, "planned", dto.Planned, "Jonas worktree.create WORKTREE_MISSING", "Jonas worker.start NO_SESSION")
		wantItems1389(t, "skipped", dto.Skipped, "Iris - IN_SYNC")
		wantItems1389(t, "applied", dto.Applied)
		wantItems1389(t, "failed", dto.Failed)
		if got, want := dto.Planned[0].Args, map[string]string{"repo": "hadron-memory/hadron-cli", "baseBranch": "main", "branchName": "jonas/work", "label": "jonas"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("worktree.create args = %v", got)
		}
		if got, want := dto.Planned[1].Args, map[string]string{"worktree": "hadron-cli/jonas", "family": "claude"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("worker.start args = %v", got)
		}
		// Empty lists are [], never null.
		for _, key := range []string{`"applied": []`, `"failed": []`} {
			if !strings.Contains(raw, key) {
				t.Fatalf("stdout lacks %s:\n%s", key, raw)
			}
		}
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("a dry run sent mutations: %v", m)
	}
	for _, c := range fx.calls {
		if c.Session != "" {
			t.Fatalf("%s carried X-Hadron-Session %q; host up reads never carry the worker session", c.Op, c.Session)
		}
	}
	if _, err := os.Stat(fx.dir("Jonas")); !os.IsNotExist(err) {
		t.Fatalf("a dry run created the worktree: %v", err)
	}
	log, _ := os.ReadFile(fx.orcaLog)
	for _, line := range strings.Split(strings.TrimSpace(string(log)), "\n") {
		if line != "status --json" && !strings.HasPrefix(line, "terminal list --worktree path:") {
			t.Fatalf("Orca call beyond a read: %s", line)
		}
	}
}

// Acceptance 1 and 5: apply issues exactly the two typed operations, and a
// second run right after plans nothing and issues nothing.
func TestTeamHostUpApplyThenIdempotent1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	// From a bound worktree, with no flag: every call, the issuing
	// mutation included, goes out as the person (cli#2064 §10 P2).
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	for _, c := range fx.calls {
		if c.Session != "" {
			t.Fatalf("%s carried X-Hadron-Session %q from the bound worktree", c.Op, c.Session)
		}
	}
	if dto.DryRun {
		t.Fatal("dryRun is true on --apply")
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worktree.create WORKTREE_MISSING", "Jonas worker.start NO_SESSION")
	wantItems1389(t, "failed", dto.Failed)
	if dto.Applied[0].OperationID == nil || *dto.Applied[0].OperationID != "hop1" || dto.Applied[1].OperationID == nil || *dto.Applied[1].OperationID != "hop2" {
		t.Fatalf("applied operation ids = %+v", dto.Applied)
	}
	want := []map[string]any{
		{"appRef": "capp100000000000000000000", "hostId": "orca1", "worker": "wkr-jonas", "type": "worktree.create", "args": map[string]any{"workerId": "wkr-jonas", "repo": "hadron-memory/hadron-cli", "baseBranch": "main", "branchName": "jonas/work", "label": "jonas"}},
		{"appRef": "capp100000000000000000000", "hostId": "orca1", "worker": "wkr-jonas", "type": "worker.start", "args": map[string]any{"workerId": "wkr-jonas", "worktree": "hadron-cli/jonas", "family": "claude"}},
	}
	if !reflect.DeepEqual(fx.issued, want) {
		t.Fatalf("issued = %v", fx.issued)
	}
	for _, flags := range [][]string{{"--dry-run"}, {"--apply"}} {
		again, raw, err := fx.run(flags...)
		if err != nil {
			t.Fatalf("second run %v: %v\n%s", flags, err, raw)
		}
		wantItems1389(t, "second run planned", again.Planned)
		wantItems1389(t, "second run skipped", again.Skipped, "Iris - IN_SYNC", "Jonas - IN_SYNC")
	}
	if len(fx.issued) != 2 {
		t.Fatalf("the second run issued again: %v", fx.issued)
	}
}

// The reconcile matrix beyond the missing worktree: a missing terminal plans
// only the start, and a finished identical operation is not reused for it.
func TestTeamHostUpMissingTerminalPlansOnlyStart1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Jonas", 0)
	fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED"})
	// Last week's start of the same worker, long finished.
	fx.stored = append(fx.stored, map[string]any{"id": "hop-old", "appId": "capp100000000000000000000", "hostId": "orca1", "workerId": "wkr-jonas",
		"type": "worker.start", "args": map[string]any{"workerId": "wkr-jonas", "worktree": "hadron-cli/jonas", "family": "claude"}, "status": "SUCCEEDED"})
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
	if len(fx.issued) != 1 || fx.issued[0]["type"] != "worker.start" {
		t.Fatalf("issued = %v, want one new worker.start", fx.issued)
	}
}

// Acceptance 3 (as the review addendum amends it): an open session is never
// started again or rebound. With its terminal gone it is reported, since the
// host-local relaunch is a later slice (cli#2060).
func TestTeamHostUpOpenSessionIsNeverStarted1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.present("Iris", 0)
	fx.writeDesired("APPLY",
		hostUpWorker1389{ID: "wkr-iris", Name: "Iris", Desired: "RUNNING", Reason: "LONG_LIVED", Session: "s-iris"},
		hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED", Session: "s-jonas"})
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "planned", dto.Planned)
	wantItems1389(t, "skipped", dto.Skipped, "Iris worker.relaunch RELAUNCH_UNPROVEN", "Jonas - SESSION_WITHOUT_WORKTREE")
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations for an open session: %v", m)
	}
}

// The addendum's additions: the platform's stop reasons are reported and never
// acted on, and nothing is planned unless the sweep mode is APPLY.
func TestTeamHostUpNeverActs1389(t *testing.T) {
	for _, reason := range []string{"LEAVE_ALONE", "HALTED", "PAUSED", "TEAM_PAUSED", "RESETTING", "RETIRED", "SWEEP_OFF"} {
		t.Run(reason, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: reason})
			dto, raw, err := fx.run("--apply")
			if err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			wantItems1389(t, "planned", dto.Planned)
			wantItems1389(t, "skipped", dto.Skipped, "Jonas - "+reason)
			if m := fx.mutations(); len(m) != 0 {
				t.Fatalf("mutations: %v", m)
			}
		})
	}
	for _, mode := range []string{"OFF", "OBSERVE", ""} {
		t.Run("sweep mode "+mode, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.writeDesired(mode, hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED"})
			dto, raw, err := fx.run("--apply")
			if err != nil {
				t.Fatalf("%v\n%s", err, raw)
			}
			wantItems1389(t, "planned", dto.Planned)
			wantItems1389(t, "skipped", dto.Skipped, "Jonas worktree.create SWEEP_NOT_APPLY")
			if m := fx.mutations(); len(m) != 0 {
				t.Fatalf("mutations: %v", m)
			}
		})
	}
}

// Acceptance 4: never casts. A worker the host is not meant to run, or that
// its registration does not list, is reported and nothing is created.
func TestTeamHostUpNeverCasts1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.registry = []string{"wkr-iris"}
	fx.irisRunningJonasMissing()

	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Iris - IN_SYNC", "Jonas - NOT_REGISTERED")

	dto, raw, err = fx.run("--apply", "--worker", "Nobody")
	if exitCodeFor(err) != exitcode.NotFound {
		t.Fatalf("--worker Nobody: exit %d (%v), want %d\n%s", exitCodeFor(err), err, exitcode.NotFound, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Nobody - NOT_ON_HOST")
	if dto.Skipped[0].WorkerID != nil || dto.Error == nil {
		t.Fatalf("NOT_ON_HOST row = %+v, error = %v", dto.Skipped[0], dto.Error)
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}

// --worker narrows the run to one worker, by name or id, in any case.
func TestTeamHostUpOneWorker1389(t *testing.T) {
	for _, ref := range []string{"jonas", "wkr-jonas"} {
		fx := newHostUpFixture1389(t)
		fx.irisRunningJonasMissing()
		dto, raw, err := fx.run("--worker", ref)
		if err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		wantItems1389(t, "planned", dto.Planned, "Jonas worktree.create WORKTREE_MISSING", "Jonas worker.start NO_SESSION")
		wantItems1389(t, "skipped", dto.Skipped)
	}
}

// Acceptance 6: the server's refusal comes back with the CLI's mapped exit
// code, and the run stops there: one attempt, nothing issued, the rest of the
// plan reported as not attempted.
func TestTeamHostUpRefusalStopsTheRun1389(t *testing.T) {
	for _, tc := range []struct {
		code string
		exit int
	}{{"FORBIDDEN", exitcode.Forbidden}, {"UNAUTHENTICATED", exitcode.AuthRequired}} {
		t.Run(tc.code, func(t *testing.T) {
			fx := newHostUpFixture1389(t)
			fx.present("Iris", 0)
			fx.writeDesired("APPLY",
				hostUpWorker1389{ID: "wkr-iris", Name: "Iris", Desired: "RUNNING", Reason: "LONG_LIVED"},
				hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED"})
			fx.refuse = tc.code
			dto, raw, err := fx.run("--apply")
			if exitCodeFor(err) != tc.exit {
				t.Fatalf("exit %d (%v), want %d\n%s", exitCodeFor(err), err, tc.exit, raw)
			}
			wantItems1389(t, "failed", dto.Failed, "Iris worker.start REFUSED", "Jonas worktree.create NOT_ATTEMPTED", "Jonas worker.start NOT_ATTEMPTED")
			wantItems1389(t, "applied", dto.Applied)
			if m := fx.mutations(); !reflect.DeepEqual(m, []string{"IssueHostOperation"}) {
				t.Fatalf("mutations = %v, want one refused attempt", m)
			}
			if len(fx.issued) != 0 || dto.Error == nil {
				t.Fatalf("issued = %v, error = %v", fx.issued, dto.Error)
			}
		})
	}
}

// §10 note on cli#2064: a refused registration read ends the run with its
// code, read once, before anything is issued.
func TestTeamHostUpRefusedRegistrationReadStopsTheRun2064(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	fx.regErr = "FORBIDDEN"
	_, raw, err := fx.run("--apply")
	if exitCodeFor(err) != exitcode.Forbidden {
		t.Fatalf("exit %d (%v), want %d\n%s", exitCodeFor(err), err, exitcode.Forbidden, raw)
	}
	reads := 0
	for _, c := range fx.calls {
		if c.Op == "HostRegistrations" {
			reads++
		}
	}
	if reads != 1 {
		t.Fatalf("HostRegistrations read %d times, want once", reads)
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}

// The per-host lock: a second applying run exits 5 at once and sends nothing.
// A dry run does not take the lock.
func TestTeamHostUpLock1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	// testFactory points XDG_CONFIG_HOME at a fresh dir on every run, so hold
	// the lock where the command will look: pin the config home first.
	home := t.TempDir()
	hold := func() {
		t.Setenv("XDG_CONFIG_HOME", home)
	}
	hold()
	// The lock's place is part of the contract a timer and a hand run share:
	// <config dir>/host-up.<sha256(host id)[:8] in hex>.lock.
	sum := sha256.Sum256([]byte("orca1"))
	if err := os.MkdirAll(filepath.Join(home, "hadron"), 0o700); err != nil {
		t.Fatal(err)
	}
	held := flock.New(filepath.Join(home, "hadron", "host-up."+hex.EncodeToString(sum[:8])+".lock"))
	if ok, err := held.TryLock(); err != nil || !ok {
		t.Fatalf("holding the lock: %v %v", ok, err)
	}
	defer func() { _ = held.Unlock() }()

	run := func(extra ...string) (string, error) {
		writeTeamBinding(t)
		setTeamBindingServer(t, fx.srv.URL)
		f, out := testFactory(t)
		hold()
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"--server", fx.srv.URL, "team", "host", "up", "--host", "orca1", "--worktrees-root", fx.root,
			"--desired-state-file", fx.desired, "--orca-bin", fx.orcaBin, "--json"}, extra...))
		return out.String(), root.Execute()
	}
	out, err := run("--apply")
	if exitCodeFor(err) != exitcode.Conflict || !strings.Contains(err.Error(), "holds the lock") {
		t.Fatalf("exit %d (%v), want %d\n%s", exitCodeFor(err), err, exitcode.Conflict, out)
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("a locked-out run sent mutations: %v", m)
	}
	if out, err := run(); err != nil {
		t.Fatalf("a dry run must not need the lock: %v\n%s", err, out)
	}
}

// Usage refusals, before anything is read from the server.
func TestTeamHostUpUsage1389(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.irisRunningJonasMissing()
	// cobra refuses the pair; main maps that message to exit 2 (isUsageError).
	if _, _, err := fx.run("--dry-run", "--apply"); err == nil || !isUsageError(err) {
		t.Fatalf("--dry-run --apply: %v, want a usage refusal", err)
	}
	for name, doc := range map[string]string{
		"unknown key":     `{"hostId":"orca1","sweepMode":"APPLY","workers":[],"command":"rm -rf /"}`,
		"another host":    `{"hostId":"orca2","sweepMode":"APPLY","workers":[]}`,
		"bad token":       `{"hostId":"orca1","sweepMode":"APPLY","workers":[{"workerId":"w","name":"W","desired":"RUNNING","reason":"LONG_LIVED","setup":{"worktree":"../etc","family":"claude"}}]}`,
		"duplicate":       `{"hostId":"orca1","sweepMode":"APPLY","workers":[{"workerId":"w","name":"W","desired":"RUNNING","reason":"LONG_LIVED"},{"workerId":"w","name":"W2","desired":"RUNNING","reason":"LONG_LIVED"}]}`,
		"worker no name":  `{"hostId":"orca1","sweepMode":"APPLY","workers":[{"workerId":"w","desired":"RUNNING","reason":"LONG_LIVED"}]}`,
		"not a document":  `nope`,
		"second document": `{"hostId":"orca1","sweepMode":"APPLY","workers":[]} {}`,
		// cli#2064 blind P2: a stray closing delimiter or garbage after the
		// document passed dec.More.
		"trailing ]":    `{"hostId":"orca1","sweepMode":"APPLY","workers":[]}]`,
		"trailing }":    `{"hostId":"orca1","sweepMode":"APPLY","workers":[]}}`,
		"trailing junk": `{"hostId":"orca1","sweepMode":"APPLY","workers":[]} x`,
	} {
		if err := os.WriteFile(fx.desired, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, raw, err := fx.run("--apply"); exitCodeFor(err) != exitcode.Usage {
			t.Fatalf("%s: exit %d (%v), want usage\n%s", name, exitCodeFor(err), err, raw)
		}
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}

// Acceptance 7: the --json shape is documented in agentic-usage.
func TestTeamHostUpAgenticUsage1389(t *testing.T) {
	doc := agentic.Doc()
	start := strings.Index(doc, "\n## Host up\n")
	if start < 0 {
		t.Fatal("agentic-usage has no `## Host up` section")
	}
	section := doc[start+1:]
	if end := strings.Index(section[3:], "\n## "); end >= 0 {
		section = section[:end+3]
	}
	for _, want := range []string{"hadron team host up", "--dry-run", "--apply", "--worker", "--desired-state-file", `"planned"`, `"applied"`, `"skipped"`, `"failed"`, `"operationId"`, `"sweepMode"`,
		"WORKTREE_MISSING", "NO_SESSION", "IN_SYNC", "SWEEP_NOT_APPLY", "NOT_ON_HOST", "NOT_REGISTERED", "RELAUNCH_UNPROVEN", "LEAVE_ALONE", "REFUSED", "NOT_ATTEMPTED", "OBSERVE_FAILED"} {
		if !strings.Contains(section, want) {
			t.Errorf("the Host up section lacks %q", want)
		}
	}
}

// cli#2064 local P1: an operation that is ISSUED when issued and completes
// later is followed to its outcome, not lost as "disappeared".
func TestTeamHostUpFollowsAnOperationToItsOutcome2064(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.async = true
	fx.irisRunningJonasMissing()
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "applied", dto.Applied, "Jonas worktree.create WORKTREE_MISSING", "Jonas worker.start NO_SESSION")
	wantItems1389(t, "failed", dto.Failed)
	if len(fx.issued) != 2 {
		t.Fatalf("issued = %v", fx.issued)
	}
}

// cli#2064 blind P3: an identical operation already in flight is adopted and
// waited on, with no new mutation; a conflicting one in flight refuses.
func TestTeamHostUpAdoptsAnInFlightOperation2064(t *testing.T) {
	inFlight := func(fx *hostUpFixture1389, family string) {
		fx.stored = append(fx.stored, map[string]any{"id": "hop-live", "appId": "capp100000000000000000000", "hostId": "orca1", "workerId": "wkr-jonas",
			"type": "worker.start", "args": map[string]any{"workerId": "wkr-jonas", "worktree": "hadron-cli/jonas", "family": family}, "status": "ISSUED"})
	}
	t.Run("identical is adopted", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		fx.present("Jonas", 0)
		fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED"})
		inFlight(fx, "claude")
		dto, raw, err := fx.run("--apply")
		if err != nil {
			t.Fatalf("%v\n%s", err, raw)
		}
		wantItems1389(t, "applied", dto.Applied, "Jonas worker.start NO_SESSION")
		if id := dto.Applied[0].OperationID; id == nil || *id != "hop-live" {
			t.Fatalf("operationId = %v, want the adopted hop-live", id)
		}
		if m := fx.mutations(); len(m) != 0 {
			t.Fatalf("adoption sent mutations: %v", m)
		}
	})
	t.Run("conflicting refuses", func(t *testing.T) {
		fx := newHostUpFixture1389(t)
		fx.present("Jonas", 0)
		fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED"})
		inFlight(fx, "codex")
		dto, raw, err := fx.run("--apply")
		if exitCodeFor(err) != exitcode.Error {
			t.Fatalf("exit %d (%v)\n%s", exitCodeFor(err), err, raw)
		}
		wantItems1389(t, "failed", dto.Failed, "Jonas worker.start ACTION_FAILED")
		if dto.Failed[0].Detail == nil || !strings.Contains(*dto.Failed[0].Detail, "conflicting active worker.start") {
			t.Fatalf("detail = %v", dto.Failed[0].Detail)
		}
		if m := fx.mutations(); len(m) != 0 {
			t.Fatalf("mutations: %v", m)
		}
	})
}

// cli#2064 blind P3: a disconnected agent terminal still counts as an agent,
// so no second one is started beside it.
func TestTeamHostUpDisconnectedTerminalCountsAsAnAgent2064(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.presentTerminals("Jonas", 1, false)
	fx.writeDesired("APPLY", hostUpWorker1389{ID: "wkr-jonas", Name: "Jonas", Desired: "RUNNING", Reason: "LONG_LIVED"})
	dto, raw, err := fx.run("--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, raw)
	}
	wantItems1389(t, "skipped", dto.Skipped, "Jonas - TERMINAL_WITHOUT_SESSION")
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}

// cli#2064 blind P2: --worker never selects two workers.
func TestTeamHostUpAmbiguousWorkerRefuses2064(t *testing.T) {
	fx := newHostUpFixture1389(t)
	fx.writeDesired("APPLY",
		hostUpWorker1389{ID: "wkr-iris", Name: "Iris", Desired: "RUNNING", Reason: "LONG_LIVED"},
		hostUpWorker1389{ID: "wkr-iris2", Name: "IRIS", Desired: "RUNNING", Reason: "LONG_LIVED"})
	_, raw, err := fx.run("--apply", "--worker", "iris")
	if exitCodeFor(err) != exitcode.Usage || !strings.Contains(err.Error(), "more than one worker") {
		t.Fatalf("exit %d (%v), want usage\n%s", exitCodeFor(err), err, raw)
	}
	if m := fx.mutations(); len(m) != 0 {
		t.Fatalf("mutations: %v", m)
	}
}
