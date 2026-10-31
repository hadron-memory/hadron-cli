package orca

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// sentinelKey is the host key every custody pin looks for in places it must
// never reach.
const sentinelKey = "hk_SENTINEL_9f3c1b7a5d2e48c6"

// hostCfg writes a host configuration and its key file (0600) and returns the
// config path. extraTop is spliced at the top level (server:, delivery:).
func hostCfg(t *testing.T, extraTop string) string {
	t.Helper()
	dir := t.TempDir()
	key := filepath.Join(dir, "host.key")
	if err := os.WriteFile(key, []byte(sentinelKey+"\n"), 0o600); err != nil {
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
  hrn:worker:acme.com:eng-team:ada:
    family: claude
    launch: [claude-worker]
` + extraTop
	p := filepath.Join(dir, "host.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// hostRig is a rig whose --config seams record what they were given.
type hostRig struct {
	*rig
	bins    []string
	hostCfg []*hostpoller.Config
	keys    []string
}

func newHostRig(t *testing.T) *hostRig {
	t.Helper()
	h := &hostRig{rig: newRig(t)}
	h.env.OrcaExec = func(bin string) orcaterm.Runner {
		h.bins = append(h.bins, bin)
		return h.orca.run
	}
	h.env.HostReads = func(_ *cmdutil.Factory, cfg *hostpoller.Config, key string) (Reads, error) {
		h.hostCfg = append(h.hostCfg, cfg)
		h.keys = append(h.keys, key)
		return h.reads, nil
	}
	h.f.AppFlag = "" // --config's App is the file's, never the ambient one
	return h
}

func TestStatusConfigPicksOrcaBin(t *testing.T) {
	h := newHostRig(t)
	h.reads.workers = []Worker{{ID: "w_i", Name: "Iris"}}
	code, out := h.run(t, true, "--all", "--config", hostCfg(t, "delivery:\n  orca_bin: /opt/orca/bin/orca\n"))
	if code != 0 || len(h.bins) != 1 || h.bins[0] != "/opt/orca/bin/orca" {
		t.Fatalf("exit %d bins %v out %s", code, h.bins, out)
	}
}

func TestStatusConfigWithoutOrcaBinUsesPath(t *testing.T) {
	h := newHostRig(t)
	h.reads.workers = []Worker{{ID: "w_i", Name: "Iris"}}
	code, out := h.run(t, true, "--all", "--config", hostCfg(t, ""))
	if code != 0 || len(h.bins) != 1 || h.bins[0] != "orca" {
		t.Fatalf("exit %d bins %v out %s", code, h.bins, out)
	}
}

func TestStatusConfigRelativeOrcaBinIsUsage(t *testing.T) {
	for _, delivery := range []string{"delivery:\n  orca_bin: orca\n", "delivery:\n  enabled: false\n  orca_bin: bin/orca\n"} {
		h := newHostRig(t)
		code, out := h.run(t, true, "--all", "--config", hostCfg(t, delivery))
		if code != exitcode.Usage || len(h.bins) != 0 || len(h.orca.commands) != 0 || len(h.keys) != 0 {
			t.Fatalf("%q: exit %d bins %v keys %v out %s", delivery, code, h.bins, h.keys, out)
		}
	}
}

func TestStatusConfigEmptyPathIsUsage(t *testing.T) {
	h := newHostRig(t)
	code, _ := h.run(t, true, "--all", "--config", "")
	if code != exitcode.Usage || len(h.orca.commands) != 0 || len(h.keys) != 0 {
		t.Fatalf("exit %d", code)
	}
}

func TestStatusConfigBadConfigIsUsage(t *testing.T) {
	for name, body := range map[string]string{"bad yaml": "app: [", "unknown field": "app: x\nbogus: 1\n"} {
		h := newHostRig(t)
		p := filepath.Join(t.TempDir(), "host.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		code, _ := h.run(t, true, "--all", "--config", p)
		if code != exitcode.Usage || len(h.orca.commands) != 0 {
			t.Fatalf("%s: exit %d", name, code)
		}
	}
}

// B6: the key is read, and refused, before Orca is listed.
func TestStatusConfigKeyRefusalsHappenBeforeAnyOrcaCall(t *testing.T) {
	loose := func(t *testing.T, p string) {
		keyPath := filepath.Join(filepath.Dir(p), "host.key")
		if err := os.Chmod(keyPath, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	empty := func(t *testing.T, p string) {
		if err := os.WriteFile(filepath.Join(filepath.Dir(p), "host.key"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	missing := func(t *testing.T, p string) {
		if err := os.Remove(filepath.Join(filepath.Dir(p), "host.key")); err != nil {
			t.Fatal(err)
		}
	}
	for name, mutate := range map[string]func(*testing.T, string){"mode 0644": loose, "empty": empty, "missing": missing} {
		h := newHostRig(t)
		p := hostCfg(t, "")
		mutate(t, p)
		code, out := h.run(t, true, "--all", "--config", p)
		if code != exitcode.AuthRequired || len(h.orca.commands) != 0 || len(h.bins) != 0 || len(h.keys) != 0 {
			t.Fatalf("%s: exit %d commands %v out %s", name, code, h.orca.commands, out)
		}
		if strings.Contains(out, sentinelKey) {
			t.Fatalf("%s: key in output: %s", name, out)
		}
		if name == "mode 0644" && !strings.Contains(h.errs.String()+out, "readable by group or others") {
			t.Fatalf("mode refusal text: %s%s", h.errs.String(), out)
		}
	}
}

func TestStatusConfigNoKeyFileSettingIsAuthRequired(t *testing.T) {
	h := newHostRig(t)
	p := hostCfg(t, "")
	raw, _ := os.ReadFile(p)
	lines := []string{}
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(l, "key_file:") {
			lines = append(lines, l)
		}
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _ := h.run(t, true, "--all", "--config", p)
	if code != exitcode.AuthRequired || len(h.orca.commands) != 0 {
		t.Fatalf("exit %d", code)
	}
}

// B3: --app with --config is a conflict; nothing is read.
func TestStatusConfigWithAppFlagIsUsage(t *testing.T) {
	h := newHostRig(t)
	h.f.AppFlag = "hrn:app:x:other"
	code, _ := h.run(t, true, "--all", "--config", hostCfg(t, ""))
	if code != exitcode.Usage || len(h.orca.commands) != 0 || len(h.keys) != 0 || h.reads.calls["workers"] != 0 {
		t.Fatalf("exit %d commands %v", code, h.orca.commands)
	}
}

func TestStatusConfigReadsTheConfigsAppWithTheHostKey(t *testing.T) {
	h := newHostRig(t)
	var gotApp string
	inner := h.env.HostReads
	h.env.HostReads = func(f *cmdutil.Factory, cfg *hostpoller.Config, key string) (Reads, error) {
		gotApp = cfg.App
		return inner(f, cfg, key)
	}
	code, _ := h.run(t, false, "--all", "--config", hostCfg(t, ""))
	if code != 0 || gotApp != "hrn:app:acme.com:eng-team" || len(h.keys) != 1 || h.keys[0] != sentinelKey {
		t.Fatalf("exit %d app %q keys %v", code, gotApp, h.keys)
	}
	if !strings.Contains(h.errs.String(), "scope: App hrn:app:acme.com:eng-team (from --config") {
		t.Fatalf("scope line: %q", h.errs.String())
	}
	if strings.Contains(h.errs.String(), sentinelKey) {
		t.Fatalf("key in the scope line: %q", h.errs.String())
	}
}

// B1: --all under --config is the App's active workers that match a
// configured ref (by name, URN or id), in the App's order.
func TestStatusConfigAllCoversConfiguredWorkersOnly(t *testing.T) {
	h := newHostRig(t)
	h.reads.workers = []Worker{
		{ID: "w_z", Name: "Zed"},
		{ID: "w_a", URN: "hrn:worker:acme.com:eng-team:ada", Name: "Ada"},
		{ID: "w_i", Name: "IRIS"},
	}
	code, out := h.run(t, true, "--all", "--config", hostCfg(t, ""))
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 {
		t.Fatalf("exit %d: %v\n%s", code, err, out)
	}
	if len(got) != 2 || got[0]["worker"] != "Ada" || got[1]["worker"] != "IRIS" {
		t.Fatalf("workers: %v", got)
	}
}

// R1 interim: the server refuses a host key the ticker reads (APP_NOT_FOUND):
// idle fails closed, busy and locked are untouched, exit 0, detail text-only.
func TestStatusConfigTickerRefusalFailsIdleClosed(t *testing.T) {
	h := newHostRig(t)
	h.reads.workers = []Worker{{ID: "w_i", Name: "Iris"}, {ID: "w_a", URN: "hrn:worker:acme.com:eng-team:ada", Name: "Ada"}}
	h.reads.subsErr = errors.New("APP_NOT_FOUND")
	h.orca.terms = append(h.orca.terms, term("term_i", "/w/iris", "claude"), term("term_a", "/w/ada", "claude"))
	h.orca.screens["term_i"], h.orca.screens["term_a"] = idleScreen, busyScreen
	h.env.BoundAt = func(_ context.Context, wt string) (Bound, bool) {
		switch wt {
		case "/w/iris":
			return Bound{WorkerID: "w_i", WorkerName: "Iris", AppID: "app1"}, true
		case "/w/ada":
			return Bound{WorkerID: "w_a", WorkerName: "Ada", AppID: "app1"}, true
		}
		return Bound{}, false
	}
	code, out := h.run(t, true, "--all", "--config", hostCfg(t, ""))
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 {
		t.Fatalf("exit %d: %v\n%s", code, err, out)
	}
	if got[0]["status"] != "locked" || got[0]["reason"] != "unrecognized" || got[1]["status"] != "busy" {
		t.Fatalf("statuses: %v", got)
	}
	if strings.Contains(out, "APP_NOT_FOUND") {
		t.Fatalf("detail leaked into JSON: %s", out)
	}
	code, out = h.run(t, false, "--all", "--config", hostCfg(t, ""))
	if code != 0 || !strings.Contains(out, "due-wake evidence unavailable") {
		t.Fatalf("text: exit %d %s", code, out)
	}
}

// B2: dueWakes refused APP_KEY_SCOPE_REQUIRED (no attention:deliver).
func TestStatusConfigDueRefusalFailsIdleClosed(t *testing.T) {
	h := newHostRig(t)
	h.reads.workers = []Worker{{ID: "w_i", Name: "Iris"}}
	h.reads.dueErr = errors.New("APP_KEY_SCOPE_REQUIRED")
	h.orca.terms = append(h.orca.terms, term("term_i", "/w/iris", "claude"))
	h.orca.screens["term_i"] = idleScreen
	h.env.BoundAt = func(context.Context, string) (Bound, bool) {
		return Bound{WorkerID: "w_i", WorkerName: "Iris", AppID: "app1"}, true
	}
	code, out := h.run(t, true, "--all", "--config", hostCfg(t, ""))
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got[0]["status"] != "locked" {
		t.Fatalf("exit %d: %v\n%s", code, err, out)
	}
}

// B4: a terminal bound to another App is judged against cfg.App (the
// binding's App is ignored), and fails closed.
func TestStatusConfigIgnoresTheTerminalBindingsApp(t *testing.T) {
	h := newHostRig(t)
	h.reads.workers = []Worker{{ID: "w_i", Name: "Iris"}} // cfg.App does not list the bound worker
	h.orca.terms = append(h.orca.terms, term("term_x", "/w/other", "claude"))
	h.orca.screens["term_x"] = idleScreen
	h.env.BoundAt = func(context.Context, string) (Bound, bool) {
		return Bound{WorkerID: "w_other", WorkerName: "Other", AppID: "app-OTHER"}, true
	}
	var app string
	inner := h.env.HostReads
	h.env.HostReads = func(f *cmdutil.Factory, cfg *hostpoller.Config, key string) (Reads, error) {
		app = cfg.App
		return inner(f, cfg, key)
	}
	code, out := h.run(t, true, "term_x", "--config", hostCfg(t, ""))
	m := decode(t, out)
	if code != 0 || app != "hrn:app:acme.com:eng-team" || m["status"] != "locked" {
		t.Fatalf("exit %d app %q: %v", code, app, m)
	}
	if strings.Contains(h.errs.String(), "app-OTHER") {
		t.Fatalf("binding App named in scope: %s", h.errs.String())
	}
}

// Without --config nothing about host configuration is consulted.
func TestStatusWithoutConfigNeverTouchesHostSeams(t *testing.T) {
	h := newHostRig(t)
	h.f.AppFlag = "hrn:app:x:team"
	h.env.OrcaExec = func(string) orcaterm.Runner { t.Fatal("OrcaExec called without --config"); return nil }
	h.env.HostReads = func(*cmdutil.Factory, *hostpoller.Config, string) (Reads, error) {
		t.Fatal("HostReads called without --config")
		return nil, nil
	}
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "host.yaml"), []byte("app: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.joshua(idleScreen)
	if code, out := h.run(t, true, "Joshua"); code != 0 {
		t.Fatalf("exit %d %s", code, out)
	}
}

// hostGraph records every request a real host client sends.
type hostGraph struct {
	mu    sync.Mutex
	calls []struct{ op, auth, session string }
}

func newHostGraph(t *testing.T, answers map[string]string) (*httptest.Server, *hostGraph) {
	t.Helper()
	g := &hostGraph{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.calls = append(g.calls, struct{ op, auth, session string }{body.OperationName, r.Header.Get("Authorization"), r.Header.Get("X-Hadron-Session")})
		g.mu.Unlock()
		resp, ok := answers[body.OperationName]
		if !ok {
			resp = `{"errors":[{"message":"unexpected ` + body.OperationName + `"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return srv, g
}

// P4: through the real factory the host key is the only credential sent: no
// session header, never HADRON_TOKEN or a stored token.
func TestStatusConfigAuthenticatesWithTheHostKeyOnly(t *testing.T) {
	srv, g := newHostGraph(t, map[string]string{
		"Workers": `{"data":{"workers":{"total":1,"items":[{"id":"w_i","name":"Iris","retiredAt":null}]}}}`,
	})
	t.Setenv("HADRON_TOKEN", "ambient-token-NOT-THE-KEY")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_APP", "")
	io, _, _ := output.Test()
	out, errs := &strings.Builder{}, &strings.Builder{}
	io.Out, io.ErrOut = output.Tracked(out), errs
	f := &cmdutil.Factory{IOStreams: io, ConfigFn: config.Load, HTTPClient: srv.Client(), JSON: true, ServerFlag: srv.URL}
	orca := &fakeOrca{screens: map[string][]string{}}
	env := Env{Orca: orca.run, Now: func() time.Time { return clock }, BoundAt: func(context.Context, string) (Bound, bool) { return Bound{}, false },
		OrcaExec: func(string) orcaterm.Runner { return orca.run }}
	cmd := newCmdStatus(f, env)
	cmd.SetArgs([]string{"--all", "--config", hostCfg(t, "")})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exit %d: %v", exitcode.FromError(err), err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.calls) == 0 {
		t.Fatal("no request reached the server")
	}
	for _, c := range g.calls {
		if c.auth != "Bearer "+sentinelKey || c.session != "" {
			t.Fatalf("%s: auth %q session %q", c.op, c.auth, c.session)
		}
	}
	for _, s := range []string{out.String(), errs.String()} {
		if strings.Contains(s, sentinelKey) {
			t.Fatalf("key in output: %s", s)
		}
	}
}

// The server's real refusal shape for a host key reading tickers
// (hadron-server#2931): the read errors, it does not return empty.
func TestGraphReadsTickerRefusalIsAnError(t *testing.T) {
	srv, _ := newHostGraph(t, map[string]string{
		"ListTickers": `{"errors":[{"message":"App not found","extensions":{"code":"APP_NOT_FOUND"}}],"data":null}`,
	})
	client, err := api.NewClient(srv.URL, sentinelKey, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (graphReads{client: client, app: "hrn:app:acme.com:eng-team"}).Subscriptions(context.Background()); err == nil {
		t.Fatal("a refused ticker read must be an error so idle fails closed")
	}
}

// fakeOrcaScript writes an executable stand-in for `orca` that logs its argv,
// its environment and its open fds to log, and answers `terminal list` with no
// terminals. It is a shell script, so it is unix-only.
func fakeOrcaScript(t *testing.T, dir, log string) string {
	t.Helper()
	envBin, err := exec.LookPath("env")
	if err != nil {
		t.Skip("no env binary")
	}
	lsBin, err := exec.LookPath("ls")
	if err != nil {
		t.Skip("no ls binary")
	}
	script := "#!/bin/sh\n" +
		"{ echo ARGV; printf '%s\\n' \"$@\"; echo ENV; " + envBin + "; echo FDS; " + lsBin + " -l /proc/self/fd 2>/dev/null; } >> " + log + "\n" +
		"echo '{\"ok\":true,\"result\":{\"terminals\":[]}}'\n"
	p := filepath.Join(dir, "fake-orca")
	if err := os.WriteFile(p, []byte(script), 0o700); err != nil { // #nosec G306 — a test executable
		t.Fatal(err)
	}
	return p
}

// P1+P7: the real Exec runs the configured binary with no PATH to find `orca`
// on, and nothing the child can see (argv, environment, fds) or print carries
// the key.
func TestStatusConfigRealExecHonorsOrcaBinAndKeepsKeyOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "child.log")
	bin := fakeOrcaScript(t, dir, log)
	srv, _ := newHostGraph(t, map[string]string{
		"Workers": `{"data":{"workers":{"total":1,"items":[{"id":"w_i","name":"Iris","retiredAt":null}]}}}`,
	})
	t.Setenv("PATH", t.TempDir()) // no orca anywhere on PATH
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_APP", "")
	io, _, _ := output.Test()
	out, errs := &strings.Builder{}, &strings.Builder{}
	io.Out, io.ErrOut = output.Tracked(out), errs
	f := &cmdutil.Factory{IOStreams: io, ConfigFn: config.Load, HTTPClient: srv.Client(), JSON: true, ServerFlag: srv.URL}
	env := Env{Now: func() time.Time { return clock }, BoundAt: func(context.Context, string) (Bound, bool) { return Bound{}, false }}
	cmd := newCmdStatus(f, env)
	cmd.SetArgs([]string{"--all", "--config", hostCfg(t, "delivery:\n  orca_bin: "+bin+"\n")})
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("exit %d: %v\n%s", exitcode.FromError(err), err, errs.String())
	}
	child, err := os.ReadFile(log) // #nosec G304 — the test's own temp file
	if err != nil {
		t.Fatalf("the configured orca_bin never ran: %v", err)
	}
	for name, s := range map[string]string{"child log": string(child), "stdout": out.String(), "stderr": errs.String()} {
		if strings.Contains(s, sentinelKey) {
			t.Fatalf("key in %s:\n%s", name, s)
		}
	}
	if strings.Contains(string(child), "HADRON_TOKEN") {
		t.Fatalf("a credential variable reached the child:\n%s", child)
	}
}
