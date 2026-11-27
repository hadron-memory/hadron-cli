package team

// hadron-cli#3237 (C3 of hadron-server#5209): `team host run` with no --config takes its workers from the
// platform. These pins drive the command (newCmdTeamHostRun) against a fake GraphQL server, with the stored
// credentials, the Orca facts and the launch wiring swapped through platformDepsFor. What is pinned: the
// assigned workers are registered with their resolved worktree; no request carries a program, a config
// directory or a local path; every call carries the host key only and no session; a key that does not answer
// for its App, a refused read, a HOST setup source and a server without V1 are refused naming the fix, with no
// key on any stream; two credentials run two pollers, each scrubbing both keys.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostcred"
	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type call3237 struct {
	op, auth, session, body string
	vars                    json.RawMessage
}

// fakeServer3237 answers by operation name; answer(op, vars) returning "" is an unexpected operation.
func fakeServer3237(t *testing.T, answer func(op string, vars json.RawMessage) string) (*httptest.Server, func() []call3237) {
	t.Helper()
	var mu sync.Mutex
	var calls []call3237
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := ioReadAll3237(r)
		var body struct {
			OperationName string          `json:"operationName"`
			Variables     json.RawMessage `json:"variables"`
		}
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		calls = append(calls, call3237{body.OperationName, r.Header.Get("Authorization"), r.Header.Get("X-Hadron-Session"), string(raw), body.Variables})
		mu.Unlock()
		resp := answer(body.OperationName, body.Variables)
		if resp == "" {
			t.Errorf("unexpected operation %q", body.OperationName)
			resp = `{"errors":[{"message":"unexpected"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []call3237 { mu.Lock(); defer mu.Unlock(); return append([]call3237{}, calls...) }
}

func ioReadAll3237(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return []byte(b.String()), nil
		}
	}
}

func testdata3237(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "hostfacts", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// facts3237 is an Orca that answers from the recorded testdata (hadron-cli/judy, hadron-server/antonio, …).
func facts3237(t *testing.T) *hostfacts.Facts {
	repos, trees := testdata3237(t, "orca_repo_list.linux.json"), testdata3237(t, "orca_worktree_list.linux.json")
	f := hostfacts.New(func(_ context.Context, argv []string) orcaterm.Result {
		switch strings.Join(argv, " ") {
		case "repo list --json":
			return orcaterm.Result{Stdout: repos}
		case "worktree list --json --limit 5000":
			return orcaterm.Result{Stdout: trees}
		}
		t.Errorf("unexpected orca call %q", argv)
		return orcaterm.Result{Code: 3}
	})
	f.LookPath = func(string) (string, error) { return "/opt/orca/bin/orca", nil }
	return f
}

type wired3237 struct {
	mu      sync.Mutex
	pollers []*hostpoller.Poller
}

// withDeps3237 swaps the platform seams for the test: these credentials, the fake Orca, a launch wiring that
// records the poller and wires nothing, and no chat delivery.
func withDeps3237(t *testing.T, entries []hostcred.Entry) *wired3237 {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", stateHome3237(t))
	w := &wired3237{}
	old := platformDepsFor
	platformDepsFor = func() platformDeps {
		return platformDeps{
			goos:        "linux",
			credentials: func() ([]hostcred.Entry, error) { return append([]hostcred.Entry{}, entries...), nil },
			facts:       func() *hostfacts.Facts { return facts3237(t) },
			wire: func(p *hostpoller.Poller, _ *hostpoller.Config, _ *hostServer, _ hostpoller.ProfileSource) error {
				w.mu.Lock()
				w.pollers = append(w.pollers, p)
				w.mu.Unlock()
				return nil
			},
			delivery: func(*hostpoller.Poller, *hostpoller.Config, *hostServer, func(hostpoller.Event)) {},
		}
	}
	t.Cleanup(func() { platformDepsFor = old })
	return w
}

// stateHome3237 is a 0700 directory under /tmp: this host's TMPDIR may be group-writable, which the host state
// directory's checks would refuse for reasons that are not the test's.
func stateHome3237(t *testing.T) string {
	d, err := os.MkdirTemp("/tmp", "hadron-3237-state-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func run3237(t *testing.T, json bool, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("HADRON_TOKEN", "hdr_person_must_not_be_used")
	io, _, _ := output.Test()
	out, errOut := &strings.Builder{}, &strings.Builder{}
	io.Out, io.ErrOut = output.Tracked(out), errOut
	f := &cmdutil.Factory{IOStreams: io, JSON: json}
	c := newCmdTeamHostRun(f)
	c.SetArgs(args)
	c.SetOut(out)
	c.SetErr(errOut)
	err := c.Execute()
	return out.String(), errOut.String(), err
}

const (
	key1 = "hk_first_secret_key_3237"
	key2 = "hk_second_secret_key_3237"
)

// platformAnswer3237 is the HostRunPlatform answer: myApp, the host row and two SERVER-source placements, each
// carrying a workdir token that resolves to exactly one Orca worktree.
func platformAnswer3237(app, host, source string) string {
	return fmt.Sprintf(`{"data":{"myApp":{"id":%q},
		"agentTeamHost":{"hostId":%q,"label":"box-%s","pollIntervalSeconds":30,"deliveryEnabled":true},
		"hostSetup":{"source":%q,"assignments":[
			{"hostId":%q,"workerId":"wkr-judy","worker":{"name":"Judy"},"workdir":"hadron-cli/judy","family":"claude","model":"opus","effort":"high","accountProfile":"work"},
			{"hostId":%q,"workerId":"wkr-antonio","worker":{"name":"Antonio"},"workdir":"hadron-server/antonio","family":"codex","model":null,"effort":null,"accountProfile":null}
		]}}}`, app, host, host, source, host, host)
}

func registerAnswer3237(host string) string {
	return fmt.Sprintf(`{"data":{"registerHost":{"hostId":%q,"label":"box","pollIntervalSeconds":30,
		"registeredAt":"2026-11-27T12:00:00Z","refreshedAt":"2026-11-27T12:00:00Z","live":true,"refusedWorkers":[],
		"workers":[{"workerId":"wkr-judy","worker":{"id":"wkr-judy","urn":"hrn:worker:acme.com:t:judy","name":"Judy"},"family":"claude","worktrees":[]},
		{"workerId":"wkr-antonio","worker":{"id":"wkr-antonio","urn":"hrn:worker:acme.com:t:antonio","name":"Antonio"},"family":"codex","worktrees":[]}]}}}`, host)
}

func goodServer3237(t *testing.T, app, host string) (*httptest.Server, func() []call3237) {
	return fakeServer3237(t, func(op string, _ json.RawMessage) string {
		switch op {
		case "HostRunPlatform":
			return platformAnswer3237(app, host, "SERVER")
		case "RegisterHost", "RegisterHostWithRefused":
			return registerAnswer3237(host)
		case "CollectHostOperations":
			return `{"data":{"collectHostOperations":[]}}`
		}
		return ""
	})
}

func TestHostRunPlatformRegistersTheAssignedWorkers3237(t *testing.T) {
	srv, calls := goodServer3237(t, "app-1", "host-1")
	withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
	out, errOut, err := run3237(t, false, "--once")
	if err != nil {
		t.Fatalf("run --once: %v\nstdout:\n%s\nstderr:\n%s", err, out, errOut)
	}
	got := calls()
	ops := []string{}
	for _, c := range got {
		ops = append(ops, c.op)
	}
	if len(got) < 3 || got[0].op != "HostRunPlatform" {
		t.Fatalf("want the platform read first, then register and collect; got %v", ops)
	}
	var reg *call3237
	for i := range got {
		c := got[i]
		if c.auth != "Bearer "+key1 {
			t.Errorf("%s authenticated with %q: the host key is the only credential", c.op, c.auth)
		}
		if c.session != "" {
			t.Errorf("%s carried a session %q", c.op, c.session)
		}
		if strings.HasPrefix(c.op, "RegisterHost") && reg == nil {
			reg = &got[i]
		}
		// Only names come back and only ids go out: nothing local ever reaches the server.
		for _, local := range []string{"/home/u/", "/opt/orca", "CLAUDE_CONFIG_DIR", "CODEX_HOME", "configDir", "program", "hdr_person"} {
			if strings.Contains(c.body, local) {
				t.Errorf("%s carried %q: %s", c.op, local, c.body)
			}
		}
	}
	if reg == nil {
		t.Fatalf("no registration: %v", ops)
	}
	var in struct {
		Input struct {
			AppRef  string `json:"appRef"`
			Label   string `json:"label"`
			Workers []struct {
				Worker    string   `json:"worker"`
				Family    string   `json:"family"`
				Worktrees []string `json:"worktrees"`
			} `json:"workers"`
		} `json:"input"`
	}
	if err := json.Unmarshal(reg.vars, &in); err != nil {
		t.Fatal(err)
	}
	if in.Input.AppRef != "app-1" || in.Input.Label != "box-host-1" || len(in.Input.Workers) != 2 {
		t.Fatalf("registration: %s", reg.vars)
	}
	seen := map[string]string{}
	for _, w := range in.Input.Workers {
		seen[w.Worker] = w.Family + " " + strings.Join(w.Worktrees, ",")
	}
	// Each workdir token resolved to exactly one Orca worktree, so the worker registers with that tree's token.
	if seen["wkr-judy"] != "claude hadron-cli/judy" || seen["wkr-antonio"] != "codex hadron-server/antonio" {
		t.Errorf("both assigned workers, keyed by worker id, with their family: %s", reg.vars)
	}
	for _, s := range []string{out, errOut} {
		if strings.Contains(s, key1) {
			t.Errorf("the host key reached a stream:\n%s", s)
		}
	}
}

func TestHostRunPlatformRefusalsNameTheFixAndNoKey3237(t *testing.T) {
	cases := []struct {
		name   string
		answer func(op string) string
		code   int
		want   string
	}{
		{"myApp is another App", func(op string) string {
			if op == "HostRunPlatform" {
				return platformAnswer3237("app-OTHER", "host-1", "SERVER")
			}
			return ""
		}, exitcode.AuthRequired, "join again with `hadron team host join --replace`"},
		{"host not found", func(op string) string {
			if op == "HostRunPlatform" {
				return `{"errors":[{"message":"host ` + key1 + ` not found","extensions":{"code":"HOST_NOT_FOUND"}}],"data":null}`
			}
			return ""
		}, exitcode.NotFound, "join again with `hadron team host join --replace`"},
		{"setup source HOST", func(op string) string {
			if op == "HostRunPlatform" {
				return platformAnswer3237("app-1", "host-1", "HOST")
			}
			return ""
		}, exitcode.Conflict, "hadron team host setup --source SERVER"},
		{"server without V1", func(op string) string {
			if op == "HostRunPlatform" {
				return `{"errors":[{"message":"Cannot query field \"agentTeamHost\" on type \"Query\".","extensions":{"code":"GRAPHQL_VALIDATION_FAILED"}}],"data":null}`
			}
			return ""
		}, exitcode.Error, "hadron-server 0.47.0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := fakeServer3237(t, func(op string, _ json.RawMessage) string { return tc.answer(op) })
			withDeps3237(t, []hostcred.Entry{{Server: srv.URL, AppID: "app-1", HostID: "host-1", Key: key1}})
			for _, asJSON := range []bool{false, true} {
				out, errOut, err := run3237(t, asJSON, "--once")
				if code := exitcode.FromError(err); code != tc.code {
					t.Errorf("json=%v: exit %d (%v), want %d", asJSON, code, err, tc.code)
				}
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("json=%v: the refusal names the fix %q: %v", asJSON, tc.want, err)
				}
				for _, s := range []string{out, errOut, fmt.Sprint(err)} {
					if strings.Contains(s, key1) {
						t.Errorf("json=%v: the key reached the operator: %s", asJSON, s)
					}
				}
			}
			for _, c := range calls() {
				if c.op != "HostRunPlatform" {
					t.Errorf("a refused start reached %s: nothing is registered or collected", c.op)
				}
			}
		})
	}
}

func TestHostRunPlatformRunsOnePollerPerCredential3237(t *testing.T) {
	srv1, calls1 := goodServer3237(t, "app-1", "host-1")
	srv2, calls2 := goodServer3237(t, "app-2", "host-2")
	w := withDeps3237(t, []hostcred.Entry{
		{Server: srv1.URL, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: srv2.URL, AppID: "app-2", HostID: "host-2", Key: key2},
	})
	if out, errOut, err := run3237(t, false, "--once"); err != nil {
		t.Fatalf("run --once: %v\n%s\n%s", err, out, errOut)
	}
	for i, c := range [][]call3237{calls1(), calls2()} {
		want := []string{key1, key2}[i]
		reg := false
		for _, x := range c {
			if x.auth != "Bearer "+want {
				t.Errorf("server %d: %s carried %q, want its own host's key only", i+1, x.op, x.auth)
			}
			reg = reg || strings.HasPrefix(x.op, "RegisterHost")
		}
		if !reg {
			t.Errorf("server %d: its host did not register", i+1)
		}
	}
	if len(w.pollers) != 2 {
		t.Fatalf("want two pollers, got %d", len(w.pollers))
	}
	// Each poller scrubs every stored key from what it reports and from a launched agent's environment, not
	// only its own: a key one host holds must never reach another host's agent.
	for i, p := range w.pollers {
		got := strings.Join(p.ScrubKeys(), " ")
		for _, k := range []string{key1, key2} {
			if !strings.Contains(got, k) {
				t.Errorf("poller %d does not scrub the key %s holds", i+1, map[string]string{key1: "host-1", key2: "host-2"}[k])
			}
		}
	}
}

func TestHostRunPlatformOneHostFailingLeavesTheOtherRunning3237(t *testing.T) {
	srv1, _ := goodServer3237(t, "app-1", "host-1")
	srv2, calls2 := fakeServer3237(t, func(op string, _ json.RawMessage) string {
		if op == "HostRunPlatform" {
			return platformAnswer3237("app-2", "host-2", "HOST")
		}
		return ""
	})
	withDeps3237(t, []hostcred.Entry{
		{Server: srv1.URL, AppID: "app-1", HostID: "host-1", Key: key1},
		{Server: srv2.URL, AppID: "app-2", HostID: "host-2", Key: key2},
	})
	out, errOut, err := run3237(t, false, "--once")
	if err != nil {
		t.Fatalf("ruling Q4 (a): one host that cannot start is reported and the other runs: %v\n%s\n%s", err, out, errOut)
	}
	if !strings.Contains(out+errOut, "host-not-started") && !strings.Contains(out+errOut, "host host-2") {
		t.Errorf("the host that could not start is reported:\n%s\n%s", out, errOut)
	}
	for _, c := range calls2() {
		if c.op != "HostRunPlatform" {
			t.Errorf("the HOST-source host reached %s", c.op)
		}
	}
}

func TestHostRunPlatformNarrowsByAppAndServer3237(t *testing.T) {
	entries := []hostcred.Entry{
		{Server: "http://a.example/", AppID: "app-1", HostID: "h1", Key: key1},
		{Server: "http://b.example", AppID: "app-2", HostID: "h2", Key: key2},
	}
	cases := []struct {
		app, server string
		want        []string
		err         bool
	}{
		{"", "", []string{"h1", "h2"}, false},
		{"app-2", "", []string{"h2"}, false},
		{"", "http://a.example", []string{"h1"}, false},
		{"app-1", "http://b.example", nil, true},
		{"app-9", "", nil, true},
	}
	for _, tc := range cases {
		f := &cmdutil.Factory{AppFlag: tc.app, ServerFlag: tc.server}
		got, err := narrowEntries(f, entries)
		if tc.err {
			if exitcode.FromError(err) != exitcode.AuthRequired || !strings.Contains(err.Error(), "hadron team host join") {
				t.Errorf("app=%q server=%q: want exit 3 naming join, got %v", tc.app, tc.server, err)
			}
			for _, k := range []string{key1, key2} {
				if err != nil && strings.Contains(err.Error(), k) {
					t.Errorf("the refusal carries a key: %v", err)
				}
			}
			continue
		}
		ids := []string{}
		for _, e := range got {
			ids = append(ids, e.HostID)
		}
		if strings.Join(ids, ",") != strings.Join(tc.want, ",") || err != nil {
			t.Errorf("app=%q server=%q: got %v (%v), want %v", tc.app, tc.server, ids, err, tc.want)
		}
	}
	if _, err := narrowEntries(&cmdutil.Factory{}, nil); exitcode.FromError(err) != exitcode.AuthRequired || !strings.Contains(err.Error(), "hadron team host join") {
		t.Errorf("no credential: want exit 3 naming join, got %v", err)
	}
}

func TestHostRunPlatformStoreRefusalIsExit3WithNoKey3237(t *testing.T) {
	withDeps3237(t, nil)
	old := platformDepsFor
	platformDepsFor = func() platformDeps {
		d := old()
		d.credentials = func() ([]hostcred.Entry, error) {
			return nil, errors.New("host credential store /x/host-credentials.json: mode 0644 is open to other users")
		}
		return d
	}
	_, _, err := run3237(t, false, "--once")
	if exitcode.FromError(err) != exitcode.AuthRequired || !strings.Contains(err.Error(), "host-credentials.json") {
		t.Errorf("a refused store: want exit 3 naming the file, got %v", err)
	}
}
