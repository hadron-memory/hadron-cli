package cmd

// cli#1872: `--as-worker` on the six boot-prompt compile and approve commands.
// Default path: no worker session on any request (P20). With the flag: the
// bound session rides the compile/approve mutations only (P21), through a
// strict resolver that never falls back to the person (P22, P23).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmd/team"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

const asWorkerApproveNodeJSON = `{"data":{"approveNode":{"nodeId":"n1","urn":"hrn:node:acme.com:kb:findings:flaky-ci","loc":"findings:flaky-ci","approval":{"revision":4,"approvedAt":"2026-10-01T23:00:00Z","approvedBy":null,"approvedByInfo":"worker:wkr1 (batch grant)","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}}`

// asWorkerCommand is one of the six commands, runnable against its own fake.
type asWorkerCommand struct {
	name string
	op   string // the mutation that may carry the session
	// run executes the command in a bound worktree and returns the ops it sent
	// with their sessions, the stdout and the error. extra is appended after
	// the command's own arguments.
	run func(t *testing.T, prep func(*testing.T), extra ...string) ([]asWorkerCall, string, error)
}

type asWorkerCall struct{ Op, Session string }

// runBoundSingle is runTeamTask with a prep hook that runs after the binding
// is written and pointed at the fake, before the command (cli#1872 P22).
func runBoundSingle(t *testing.T, prep func(*testing.T), responses map[string]string, argv ...string) (*[]attnCall, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, calls := attnServer(t, responses)
	setTeamBindingServer(t, srv.URL)
	if prep != nil {
		prep(t)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL}, argv...))
	err := root.Execute()
	return calls, out.String(), err
}

func fromAttn(calls *[]attnCall) []asWorkerCall {
	out := []asWorkerCall{}
	for _, c := range *calls {
		out = append(out, asWorkerCall{c.Op, c.Session})
	}
	return out
}

func singleCompile(name string, c struct {
	name, op, field string
	args            []string
}) asWorkerCommand {
	return asWorkerCommand{name: name, op: c.op, run: func(t *testing.T, prep func(*testing.T), extra ...string) ([]asWorkerCall, string, error) {
		calls, out, err := runBoundSingle(t, prep, c1Responses(), append(append([]string{}, c.args...), extra...)...)
		return fromAttn(calls), out, err
	}}
}

func batchCompileCommand() asWorkerCommand {
	return asWorkerCommand{name: "team worker compile (batch)", op: "CompileWorkerBootPrompt", run: func(t *testing.T, prep func(*testing.T), extra ...string) ([]asWorkerCall, string, error) {
		fake, out, err := runBoundBatch(t, nil, prep, []batchWorker{{id: "w-iris", name: "Iris", compile: "ok"}}, append([]string{"--apply"}, extra...)...)
		return fromBatch(fake), out, err
	}}
}

func runBoundBatch(t *testing.T, onOp func(string), prep func(*testing.T), workers []batchWorker, args ...string) (*batchFake, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, fake := newBatchFake(t, workers)
	fake.onOp = onOp
	setTeamBindingServer(t, srv.URL)
	if prep != nil {
		prep(t)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "--app", batchApp, "team", "worker", "compile"}, args...))
	err := root.Execute()
	return fake, out.String(), err
}

func fromBatch(b *batchFake) []asWorkerCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []asWorkerCall{}
	for _, c := range b.calls {
		out = append(out, asWorkerCall{c.Op, c.Session})
	}
	return out
}

func runBoundApprove(t *testing.T, onOp func(string), prep func(*testing.T), workers []approveWorker, args ...string) (*approveFake, string, error) {
	t.Helper()
	writeTeamBinding(t)
	srv, fake := newApproveFake(t, workers)
	fake.onOp = onOp
	setTeamBindingServer(t, srv.URL)
	if prep != nil {
		prep(t)
	}
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append([]string{"--server", srv.URL, "--app", batchApp, "team", "worker", "approve"}, args...))
	err := root.Execute()
	return fake, out.String(), err
}

func fromApprove(a *approveFake) []asWorkerCall {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []asWorkerCall{}
	for _, c := range a.calls {
		out = append(out, asWorkerCall{c.Op, c.Session})
	}
	return out
}

func batchApproveCommand() asWorkerCommand {
	return asWorkerCommand{name: "team worker approve (batch)", op: "ApproveNode", run: func(t *testing.T, prep func(*testing.T), extra ...string) ([]asWorkerCall, string, error) {
		fake, out, err := runBoundApprove(t, nil, prep, approveTeam(), append([]string{"--apply", "--json"}, extra...)...)
		return fromApprove(fake), out, err
	}}
}

func nodeApproveCommand() asWorkerCommand {
	return asWorkerCommand{name: "node approve", op: "ApproveNode", run: func(t *testing.T, prep func(*testing.T), extra ...string) ([]asWorkerCall, string, error) {
		calls, out, err := runBoundSingle(t, prep, map[string]string{"ResolveUrn": resolveNodeJSON, "ApproveNode": asWorkerApproveNodeJSON},
			append([]string{"node", "approve", nodeURN, "--revision", "4"}, extra...)...)
		return fromAttn(calls), out, err
	}}
}

func asWorkerCommands() []asWorkerCommand {
	out := []asWorkerCommand{}
	for _, c := range c1Commands {
		out = append(out, singleCompile(c.name+" compile", c))
	}
	return append(out, batchCompileCommand(), nodeApproveCommand(), batchApproveCommand())
}

// P20: the default path in a BOUND worktree sends no session on any request,
// for all six commands. (Green on main on purpose: the regression guard.)
func TestAsWorkerDefaultPathSendsNoSession1872(t *testing.T) {
	for _, c := range asWorkerCommands() {
		t.Run(c.name, func(t *testing.T) {
			calls, _, err := c.run(t, nil)
			if err != nil {
				t.Fatal(err)
			}
			saw := false
			for _, call := range calls {
				if call.Op == c.op {
					saw = true
				}
				if call.Session != "" {
					t.Fatalf("default path sent session %q on %s", call.Session, call.Op)
				}
			}
			if !saw {
				t.Fatalf("%s never sent: %+v", c.op, calls)
			}
		})
	}
}

// P21: with --as-worker the mutation carries the bound session and every read
// in the same run carries none (R13).
func TestAsWorkerMutationsCarrySessionReadsDoNot1872(t *testing.T) {
	for _, c := range asWorkerCommands() {
		t.Run(c.name, func(t *testing.T) {
			calls, _, err := c.run(t, nil, "--as-worker")
			if err != nil {
				t.Fatal(err)
			}
			muts := 0
			for _, call := range calls {
				switch {
				case call.Op == c.op:
					muts++
					if call.Session != "s-new" {
						t.Fatalf("%s carried session %q, want s-new", call.Op, call.Session)
					}
				case call.Session != "":
					t.Fatalf("read %s carried session %q", call.Op, call.Session)
				}
			}
			if muts == 0 {
				t.Fatalf("%s never sent: %+v", c.op, calls)
			}
		})
	}
}

// asWorkerRefusals are the bindings --as-worker must refuse, applied after the
// binding is written and pointed at the fake.
var asWorkerRefusals = []struct {
	name string
	prep func(t *testing.T)
}{
	{"no worktree", func(t *testing.T) {
		t.Setenv(team.GitDirEnv, "")
		t.Chdir(t.TempDir())
	}},
	{"no binding file", func(t *testing.T) {
		_ = os.Remove(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json"))
	}},
	{"malformed binding", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}},
	{"missing session", func(t *testing.T) { rewriteBinding1592(t, func(b map[string]any) { b["sessionId"] = "" }) }},
	{"missing worker", func(t *testing.T) { rewriteBinding1592(t, func(b map[string]any) { b["workerId"] = "" }) }},
	{"missing App", func(t *testing.T) { rewriteBinding1592(t, func(b map[string]any) { b["appId"] = "" }) }},
	{"missing server", func(t *testing.T) { rewriteBinding1592(t, func(b map[string]any) { b["server"] = "" }) }},
	{"binding for another server", func(t *testing.T) {
		rewriteBinding1592(t, func(b map[string]any) { b["server"] = "https://elsewhere.example.test" })
	}},
}

// P22: the strict resolver refuses with exit 2, sends no request at all and
// never falls back to the person, for every binding fault and every command.
func TestAsWorkerStrictResolverRefusesAndSendsNothing1872(t *testing.T) {
	for _, c := range asWorkerCommands() {
		for _, r := range asWorkerRefusals {
			t.Run(c.name+"/"+r.name, func(t *testing.T) {
				calls, _, err := c.run(t, r.prep, "--as-worker")
				if got := exitCodeFor(err); got != exitcode.Usage {
					t.Fatalf("want exit %d, got %d (%v)", exitcode.Usage, got, err)
				}
				if len(calls) != 0 {
					t.Fatalf("a refused --as-worker sent requests: %+v", calls)
				}
			})
		}
	}
}

// P22: an --app naming another App refuses; the only request it may make is
// the identity read that proves the App differs.
func TestAsWorkerAnotherAppRefusesAndSendsNoWrite1872(t *testing.T) {
	resp := c1Responses()
	resp["TeamAppIdentity"] = strings.ReplaceAll(teamAppIdentityJSON, "capp100000000000000000000", "capp900000000000000000000")
	calls, _, err := runBoundSingle(t, nil, resp, append(append([]string{}, c1Commands[0].args...), "--as-worker", "--app", "hrn:app:acme.com:other")...)
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Fatalf("want exit 2, got %d (%v)", got, err)
	}
	for _, c := range *calls {
		if c.Op != "TeamAppIdentity" {
			t.Fatalf("a refused --as-worker sent %s", c.Op)
		}
	}
}

// The binding failure is latched: a binding that changes before item two and
// is restored before item three must not let item three send (blind P2).
func TestAsWorkerBindingFailureIsLatchedForTheRun1872(t *testing.T) {
	writeTeamBinding(t)
	srv, _ := attnServer(t, map[string]string{})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	f.ServerFlag = srv.URL
	send, err := team.WorkerWriteSession(f)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sends := func() bool {
		c, e := send(context.Background())
		return e == nil && c != context.Background()
	}
	p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	orig, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !sends() {
		t.Fatal("item one must send")
	}
	swapSession(t)
	if sends() {
		t.Fatal("item two (binding changed) must be refused")
	}
	if err := os.WriteFile(p, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if sends() {
		t.Fatal("item three (binding restored) must stay refused")
	}
}

func swapSession(t *testing.T) {
	t.Helper()
	p := filepath.Join(os.Getenv(team.GitDirEnv), "hadron-team-session.json")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), "s-new", "replacement")), 0o600); err != nil {
		t.Fatal(err)
	}
}

// P23: the binding is swapped during the App-identity round trip: nothing is
// sent under the old or the new session.
func TestAsWorkerRebindDuringAppLookupSendsNothing1872(t *testing.T) {
	writeTeamBinding(t)
	srv, calls := attnServerHook(t, map[string]string{"TeamAppIdentity": teamAppIdentityJSON, "CompileAgentBootPrompt": c1Responses()["CompileAgentBootPrompt"]}, func(op string) {
		if op == "TeamAppIdentity" {
			swapSession(t)
		}
	})
	setTeamBindingServer(t, srv.URL)
	f, _ := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs(append(append([]string{"--server", srv.URL}, c1Commands[0].args...), "--as-worker", "--app", "hrn:app:acme.com:eng-team"))
	if got := exitCodeFor(root.Execute()); got != exitcode.Usage {
		t.Fatalf("want exit 2, got %d", got)
	}
	for _, c := range *calls {
		if c.Op == "CompileAgentBootPrompt" {
			t.Fatalf("compile was sent after a rebind: %+v", c)
		}
	}
}

// P23 mid-run: a rebind between two mutations of a batch stops the next one.
// The first approval already left under the verified session; the second is
// filed as a failed row (exit 2) and is never sent.
func TestAsWorkerRebindMidBatchStopsTheNextWrite1872(t *testing.T) {
	workers := []approveWorker{
		{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: "ok", serve: "same"},
		{id: "w-dee", name: "Dee", pointer: cptr("w-dee"), revision: 4, state: "NOT_APPROVED", approve: "ok", serve: "same"},
	}
	n := 0
	fake, out, err := runBoundApprove(t, func(op string) {
		if op == "ApproveNode" {
			n++
			if n == 1 {
				swapSession(t)
			}
		}
	}, nil, workers, "--as-worker", "--apply", "--json")
	if got := exitCodeFor(err); got != exitcode.Usage {
		t.Fatalf("want exit 2, got %d (%v)", got, err)
	}
	sent := 0
	for _, c := range fromApprove(fake) {
		if c.Op == "ApproveNode" {
			sent++
			if c.Session != "s-new" {
				t.Fatalf("approval sent under %q", c.Session)
			}
		}
	}
	if sent != 1 {
		t.Fatalf("want exactly the first approval sent, got %d", sent)
	}
	d := decodeApprove(t, out)
	if len(d.Failed) != 1 || d.Failed[0]["exitCode"] != float64(exitcode.Usage) {
		t.Fatalf("second approval must be a failed row with exit 2: %+v", d.Failed)
	}
}

// P24: server refusals under --as-worker print with their codes and exit 8.
func TestAsWorkerServerRefusalsExit8AndBatchRowsKeepCodes1872(t *testing.T) {
	for _, code := range []string{"BOOT_PROMPT_COMPILE_NOT_PERMITTED", "BOOT_PROMPT_GRANT_REQUIRED", "BOOT_PROMPT_WORKER_SESSION_REFUSED"} {
		t.Run("single "+code, func(t *testing.T) {
			r := c1Responses()
			r["CompileAgentBootPrompt"] = `{"errors":[{"message":"the grant does not name this node","extensions":{"code":"` + code + `"}}]}`
			_, _, err := runTeamTask(t, r, append(append([]string{}, c1Commands[0].args...), "--as-worker")...)
			if exitCodeFor(err) != exitcode.Forbidden || err == nil || err.Error() != "the grant does not name this node" {
				t.Fatalf("want exit 8 with the server's message, got %v", err)
			}
		})
		t.Run("approve batch "+code, func(t *testing.T) {
			workers := []approveWorker{{id: "w-ann", name: "Ann", pointer: cptr("w-ann"), revision: 3, state: "NOT_APPROVED", approve: code}}
			_, out, err := runBoundApprove(t, nil, nil, workers, "--as-worker", "--apply", "--json")
			if exitCodeFor(err) != exitcode.Forbidden {
				t.Fatalf("want exit 8, got %v", err)
			}
			d := decodeApprove(t, out)
			if len(d.Failed) != 1 || d.Failed[0]["code"] != code || d.Failed[0]["exitCode"] != float64(exitcode.Forbidden) {
				t.Fatalf("failed row lost its code/exitCode: %+v", d.Failed)
			}
		})
		t.Run("compile batch "+code, func(t *testing.T) {
			_, out, err := runBoundBatch(t, nil, nil, []batchWorker{{id: "w-iris", name: "Iris", compile: code}}, "--as-worker", "--apply", "--json")
			if exitCodeFor(err) != exitcode.Forbidden {
				t.Fatalf("want exit 8, got %v", err)
			}
			if !strings.Contains(out, `"code": "`+code+`"`) && !strings.Contains(out, `"code":"`+code+`"`) {
				t.Fatalf("failed row lost its code: %s", out)
			}
		})
	}
}

// P25: the printed approve line goes out the way the batch compiled.
func TestAsWorkerBatchCompilePrintsMatchingApproveLine1872(t *testing.T) {
	for _, as := range []bool{false, true} {
		args := []string{"--apply", "--json"}
		if as {
			args = append(args, "--as-worker")
		}
		_, out, err := runBoundBatch(t, nil, nil, []batchWorker{{id: "w-iris", name: "Iris", compile: "ok"}}, args...)
		if err != nil {
			t.Fatal(err)
		}
		var d struct {
			Compiled []struct {
				ApproveCommand string `json:"approveCommand"`
			} `json:"compiled"`
		}
		if err := json.Unmarshal([]byte(out), &d); err != nil || len(d.Compiled) != 1 {
			t.Fatalf("bad output (%v): %s", err, out)
		}
		if got := strings.HasSuffix(d.Compiled[0].ApproveCommand, " --as-worker"); got != as {
			t.Fatalf("as-worker=%v but approve line is %q", as, d.Compiled[0].ApproveCommand)
		}
	}
}

// P26: the authority text matches the path; the default stays byte-identical.
func TestAsWorkerApproveAuthorityMatchesThePath1872(t *testing.T) {
	_, out, err := runBoundApprove(t, nil, nil, approveTeam(), "--json")
	if err != nil {
		t.Fatal(err)
	}
	def := decodeApprove(t, out).Authority
	if !strings.Contains(def, "acting as themselves") || !strings.Contains(def, "a register grant is not checked") {
		t.Fatalf("default authority moved: %q", def)
	}
	_, out, err = runBoundApprove(t, nil, nil, approveTeam(), "--json", "--as-worker")
	if err != nil {
		t.Fatal(err)
	}
	as := decodeApprove(t, out).Authority
	if as == def || strings.Contains(as, "acting as themselves") || !strings.Contains(as, "bootprompt.compile.approve") {
		t.Fatalf("worker authority must name the grant path: %q", as)
	}
}

// P27: the approver renders from approvedByInfo when approvedBy is null.
func TestAsWorkerNodeApproveRendersTheBatchGrantApprover1872(t *testing.T) {
	_, out, err := runTeamTask(t, map[string]string{"ResolveUrn": resolveNodeJSON, "ApproveNode": asWorkerApproveNodeJSON},
		"node", "approve", nodeURN, "--revision", "4", "--as-worker")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "by worker:wkr1 (batch grant)") {
		t.Fatalf("approver not rendered from approvedByInfo: %s", out)
	}
}

// P28: --as-worker adds no client-side setPointer; the server forces it.
func TestAsWorkerSendsNoSetPointer1872(t *testing.T) {
	for _, c := range c1Commands {
		t.Run(c.name, func(t *testing.T) {
			calls, _, err := runTeamTask(t, c1Responses(), append(append([]string{}, c.args...), "--as-worker")...)
			if err != nil {
				t.Fatal(err)
			}
			if _, present := c2Mutation(t, calls, c.op).Vars["setPointer"]; present {
				t.Fatal("--as-worker must not send setPointer")
			}
		})
	}
}

// P29: help truth. The old "never sends" sentence is gone; the default and the
// opt-in are both stated; the flag is on all six commands.
func TestAsWorkerHelpIsTrue1872(t *testing.T) {
	f, out := testFactory(t)
	for _, argv := range [][]string{
		{"agent", "compile", "--help"}, {"app", "agent", "compile", "--help"}, {"team", "worker", "compile", "--help"},
		{"node", "approve", "--help"}, {"team", "worker", "approve", "--help"},
	} {
		out.Reset()
		root := NewRootCmd(f)
		root.SetArgs(argv)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		h := out.String()
		if strings.Contains(h, "never sends this worktree's worker-session header") {
			t.Errorf("%v: stale help sentence", argv)
		}
		flat := strings.Join(strings.Fields(h), " ")
		for _, want := range []string{"--as-worker", "bootprompt.compile.approve", "does not cover signing"} {
			if !strings.Contains(flat, want) {
				t.Errorf("%v: help lacks %q", argv, want)
			}
		}
	}
}
