package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	authflow "github.com/hadron-memory/hadron-cli/internal/auth"
	"github.com/hadron-memory/hadron-cli/internal/auth/store"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/config"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/hadron-memory/hadron-cli/internal/skilldoc"
	"github.com/zalando/go-keyring"
)

type login508Call struct {
	op               string
	host, credential string
	force            bool
}
type login508Env struct {
	f           *cmdutil.Factory
	home        string
	out, errOut *strings.Builder
	// mu guards calls, orphans and failExport: the handler goroutine writes
	// them while the test goroutine reads. Completing a response normally
	// orders the two, but the failExport knob ABORTS a response mid-request
	// (hadron-cli#558), which removes exactly that ordering — so every access
	// goes through the lock.
	mu         *sync.Mutex
	calls      *[]login508Call
	orphans    *[]any
	failExport *bool
}

func setupLogin508(t *testing.T, tty, jsonMode bool, answer string, dirs, bins []string, enabled bool) *login508Env {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HADRON_TOKEN", envTok)
	keyring.MockInit()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	oldHome, oldLook, oldBrowser := loginSkillsHome, loginSkillsLookPath, browserLogin
	t.Cleanup(func() { loginSkillsHome, loginSkillsLookPath, browserLogin = oldHome, oldLook, oldBrowser })
	loginSkillsHome = func() (string, error) { return home, nil }
	loginSkillsLookPath = func(name string) (string, error) {
		for _, b := range bins {
			if b == name {
				return "/synthetic/bin/" + name, nil
			}
		}
		return "", fmt.Errorf("not found")
	}
	browserLogin = func(context.Context, authflow.LoginOptions) (*authflow.Token, error) {
		return &authflow.Token{AccessToken: newTok}, nil
	}
	calls := []login508Call{}
	orphans := []any{}
	failExport := false
	mu := &sync.Mutex{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Input struct {
					Host     string          `json:"host"`
					Force    json.RawMessage `json:"force"`
					ScopeRef json.RawMessage `json:"scopeRef"`
					Memories json.RawMessage `json:"memories"`
				} `json:"input"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.OperationName == "AuthContext" {
			fmt.Fprint(w, `{"data":{"authContext":{"principalType":"USER","user":{"id":"new","handle":"new"}}}}`)
			return
		}
		if req.OperationName != "SkillExportPlan" && req.OperationName != "LoginSkillAvailability" {
			t.Errorf("unexpected operation %q", req.OperationName)
			http.Error(w, "unexpected", 500)
			return
		}
		if len(req.Variables.Input.Force) > 0 || len(req.Variables.Input.ScopeRef) > 0 || len(req.Variables.Input.Memories) > 0 {
			t.Errorf("login must omit force, scopeRef and memories, not send null or overrides")
		}
		mu.Lock()
		calls = append(calls, login508Call{req.OperationName, req.Variables.Input.Host, r.Header.Get("Authorization"), string(req.Variables.Input.Force) == "true"})
		abort := failExport && req.OperationName == "SkillExportPlan"
		orphansNow := append([]any{}, orphans...)
		mu.Unlock()
		if abort {
			panic(http.ErrAbortHandler)
		}
		entries := []any{}
		if enabled {
			entries = append(entries, map[string]any{"urn": "hrn:node:example.com:demo:task", "nodeId": "node-508", "name": "demo-508", "class": "never-exported", "findings": []any{}, "renderedBody": "---\nname: demo-508\ndescription: Synthetic test skill\n---\nTest only.\n", "exportPlan": map[string]any{"action": "WRITE", "reasons": []any{}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"skillPlan": map[string]any{"scanned": len(entries), "judged": len(entries), "entries": entries, "orphans": orphansNow, "unrecognized": []any{}}}})
	}))
	t.Cleanup(srv.Close)
	streams, _, _ := output.Test()
	if tty {
		streams, _, _ = output.TestTTY(answer)
	}
	out, errOut := &strings.Builder{}, &strings.Builder{}
	streams.Out, streams.ErrOut = output.Tracked(out), errOut
	f := cmdutil.NewFactory()
	f.IOStreams = streams
	f.JSON = jsonMode
	f.ServerFlag = srv.URL
	f.HTTPClient = srv.Client()
	f.TokenStoreFn = func() store.Store { return store.Keyring{} }
	return &login508Env{f, home, out, errOut, mu, &calls, &orphans, &failExport}
}

// snapshotCalls returns a copy of the recorded calls under the lock, so test
// goroutines never read the handler's append in flight.
func (e *login508Env) snapshotCalls() []login508Call {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]login508Call{}, (*e.calls)...)
}

// setFailExport arms the abort knob under the lock (the handler reads it).
func (e *login508Env) setFailExport(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	*e.failExport = v
}

// addOrphan seeds one orphan entry under the lock (the handler serves them).
func (e *login508Env) addOrphan(o any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	*e.orphans = append(*e.orphans, o)
}
func (e *login508Env) run(args ...string) error {
	cmd := newCmdLogin(e.f)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	return cmd.Execute()
}
func (e *login508Env) assertInstalled(t *testing.T, claude, codex bool) {
	t.Helper()
	for _, h := range []struct {
		dir  string
		want bool
	}{{".claude", claude}, {".agents", codex}} {
		_, err := os.Stat(filepath.Join(e.home, h.dir, "skills", "demo-508", "SKILL.md"))
		if (err == nil) != h.want {
			t.Errorf("%s installed=%v want%v (%v)", h.dir, err == nil, h.want, err)
		}
	}
}

// countRequests pins the request budget: how many operations of one kind were
// sent to one host (#2500). Availability queries are the preflight the offer
// costs; export plans are the writes consent gates.
func (e *login508Env) countRequests(op, host string) int {
	n := 0
	for _, c := range e.snapshotCalls() {
		if c.op == op && c.host == host {
			n++
		}
	}
	return n
}

// assertRequests asserts each detected host got exactly one availability query
// and — only with consent — exactly one export plan, and that no other host was
// contacted at all.
func (e *login508Env) assertRequests(t *testing.T, claude, codex, export bool) {
	t.Helper()
	for key, detected := range map[string]bool{"claudeSkill": claude, "codexSkill": codex} {
		wantAvail, wantExport := 0, 0
		if detected {
			wantAvail = 1
			if export {
				wantExport = 1
			}
		}
		if got := e.countRequests("LoginSkillAvailability", key); got != wantAvail {
			t.Errorf("%s: %d availability queries, want %d", key, got, wantAvail)
		}
		if got := e.countRequests("SkillExportPlan", key); got != wantExport {
			t.Errorf("%s: %d export plans, want %d (writes happen only with consent)", key, got, wantExport)
		}
	}
}

// Both explicit acceptance constraints use the real login + export HTTP/file
// path, never a replacement installer that would hide wrong host or token use.
func TestLogin508DetectedHostsAndStoredAccount(t *testing.T) {
	for _, c := range []struct {
		name          string
		dirs, bins    []string
		claude, codex bool
	}{
		{"claude-directory", []string{".claude"}, nil, true, false},
		{"agents-directory", []string{".agents"}, nil, false, true},
		{"codex-directory", []string{".codex"}, nil, false, true},
		{"claude-binary", nil, []string{"claude"}, true, false},
		{"codex-binary", nil, []string{"codex"}, false, true},
		{"both", []string{".claude", ".agents", ".codex"}, []string{"claude", "codex"}, true, true},
		{"none", nil, nil, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := setupLogin508(t, false, false, "", c.dirs, c.bins, true)
			if err := e.run("--install-skills"); err != nil {
				t.Fatal(err)
			}
			e.assertInstalled(t, c.claude, c.codex)
			for _, call := range e.snapshotCalls() {
				if call.credential != "Bearer "+newTok {
					t.Errorf("export did not use the newly stored login credential")
				}
				if call.force {
					t.Error("login passed force")
				}
				if call.host == "claudeSkill" && !c.claude || call.host == "codexSkill" && !c.codex {
					t.Errorf("undetected host queried: %s", call.host)
				}
			}
			// Per-host request budget (#2500): one availability query and one
			// export plan per DETECTED host, nothing for an undetected one.
			e.assertRequests(t, c.claude, c.codex, c.claude || c.codex)
			if strings.Contains(e.out.String()+e.errOut.String(), newTok) || strings.Contains(e.out.String()+e.errOut.String(), envTok) {
				t.Error("credential in output")
			}
		})
	}
}
func TestLogin508PromptAndSilence(t *testing.T) {
	for _, c := range []struct {
		name, answer                       string
		tty, jsonMode, enabled             bool
		args                               []string
		configOff, prompt, installed, hint bool
		// avail/export: the request budget for the one detected host
		// (codexSkill, from the .agents dir below). Every case pins that the
		// hint, config-off and --no-install-skills paths send NOTHING, and
		// that an export plan follows only an accepted offer or the explicit
		// flag (#2500).
		avail, export int
	}{
		{name: "accept", answer: "y\n", tty: true, enabled: true, prompt: true, installed: true, avail: 1, export: 1},
		{name: "decline", answer: "n\n", tty: true, enabled: true, prompt: true, avail: 1},
		{name: "default-no", answer: "\n", tty: true, enabled: true, prompt: true, avail: 1},
		{name: "pipe", enabled: true, hint: true},
		{name: "json", tty: true, jsonMode: true, enabled: true, hint: true},
		{name: "explicit-json", jsonMode: true, enabled: true, args: []string{"--install-skills"}, installed: true, avail: 1, export: 1},
		{name: "no-flag", tty: true, enabled: true, args: []string{"--no-install-skills"}},
		{name: "config-off", tty: true, enabled: true, configOff: true},
		{name: "flag-over-config", tty: true, enabled: true, configOff: true, args: []string{"--install-skills"}, installed: true, avail: 1, export: 1},
		{name: "nothing-enabled", tty: true, enabled: false, avail: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := setupLogin508(t, c.tty, c.jsonMode, c.answer, []string{".agents"}, nil, c.enabled)
			if c.configOff {
				cfg, err := config.Load()
				if err != nil {
					t.Fatal(err)
				}
				if err = cfg.Set("skills.installPrompt", "false"); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.run(c.args...); err != nil {
				t.Fatal(err)
			}
			e.assertInstalled(t, false, c.installed)
			text := e.out.String() + e.errOut.String()
			if got := strings.Count(text, "Install now?"); got != btoi508(c.prompt) {
				t.Errorf("prompt count %d: %s", got, text)
			}
			if c.hint && (!strings.Contains(e.errOut.String(), "hadron auth login --install-skills") || !strings.Contains(e.errOut.String(), "--server "+e.f.ServerFlag)) {
				t.Errorf("missing noninteractive command hint: %s", text)
			}
			if c.jsonMode && !json.Valid([]byte(e.out.String())) {
				t.Errorf("not one JSON document: %s", e.out.String())
			}
			if !c.enabled && !strings.Contains(text, "No skills are enabled") {
				t.Errorf("missing nothing-enabled message: %s", text)
			}
			if got := e.countRequests("LoginSkillAvailability", "codexSkill"); got != c.avail || e.countRequests("SkillExportPlan", "codexSkill") != c.export {
				snapshot := e.snapshotCalls()
				t.Errorf("request budget: %d availability queries (want %d), %d export plans (want %d) — an export plan must follow only consent (calls: %+v)",
					e.countRequests("LoginSkillAvailability", "codexSkill"), c.avail, e.countRequests("SkillExportPlan", "codexSkill"), c.export, snapshot)
			}
		})
	}
}
func btoi508(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestLogin508FlagsRefuseBeforeLogin(t *testing.T) {
	e := setupLogin508(t, true, false, "y\n", []string{".agents"}, nil, true)
	browserCalls := 0
	browserLogin = func(context.Context, authflow.LoginOptions) (*authflow.Token, error) {
		browserCalls++
		return &authflow.Token{AccessToken: newTok}, nil
	}
	if err := e.run("--install-skills", "--no-install-skills"); err == nil {
		t.Fatal("accepted incompatible flags")
	}
	if browserCalls != 0 || len(e.snapshotCalls()) != 0 {
		t.Fatal("sent requests before flag refusal")
	}
	e.assertInstalled(t, false, false)
}

func TestLogin508WithTokenStillUsesStoredAccount(t *testing.T) {
	e := setupLogin508(t, false, true, "", []string{".claude"}, nil, true)
	e.f.IOStreams.In = strings.NewReader(newTok + "\n")
	if err := e.run("--with-token", "--install-skills"); err != nil {
		t.Fatal(err)
	}
	e.assertInstalled(t, true, false)
	for _, call := range e.snapshotCalls() {
		if call.credential != "Bearer "+newTok {
			t.Fatal("wrong export account")
		}
	}
	var dto struct {
		Skills struct {
			Hosts []struct {
				Host string `json:"host"`
			} `json:"hosts"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(e.out.String()), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Skills.Hosts) != 1 || dto.Skills.Hosts[0].Host != "claudeSkill" {
		t.Fatalf("missing selected-host report: %s", e.out.String())
	}
}

func TestLogin508FailedLoginNeverInstalls(t *testing.T) {
	e := setupLogin508(t, true, false, "y\n", []string{".agents"}, nil, true)
	browserLogin = func(context.Context, authflow.LoginOptions) (*authflow.Token, error) {
		return nil, fmt.Errorf("synthetic cancelled login")
	}
	if err := e.run("--install-skills"); err == nil {
		t.Fatal("expected login failure")
	}
	if len(e.snapshotCalls()) != 0 {
		t.Fatal("export after failed login")
	}
	e.assertInstalled(t, false, false)
}

func TestLogin508JSONRefusalPreservesForeignFile(t *testing.T) {
	e := setupLogin508(t, false, true, "", []string{".agents"}, nil, true)
	file := filepath.Join(e.home, ".agents", "skills", "demo-508", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	const body = "---\nname: demo-508\ndescription: My own skill\n---\nKeep my work.\n"
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.run("--install-skills"); err == nil {
		t.Fatal("expected installation refusal")
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != body {
		t.Fatal("foreign file changed")
	}
	var dto struct {
		Skills struct {
			Hosts []struct {
				Refused []json.RawMessage `json:"refused"`
			} `json:"hosts"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(e.out.String()), &dto); err != nil {
		t.Fatalf("not one JSON document: %v: %s", err, e.out.String())
	}
	if len(dto.Skills.Hosts) != 1 || len(dto.Skills.Hosts[0].Refused) != 1 {
		t.Fatalf("refusal report missing: %s", e.out.String())
	}
}

func TestLogin508NeverPrunes(t *testing.T) {
	e := setupLogin508(t, false, false, "", []string{".agents"}, nil, true)
	file := filepath.Join(e.home, ".agents", "skills", "orphan-508", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	body, err := skilldoc.Render("orphan-508", "orphan-508", "hrn:node:example.com:old:gone", "Owned orphan fixture", "Keep.")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	e.addOrphan(map[string]any{"dirName": "orphan-508", "nodeId": "orphan-508", "sourceUrn": "hrn:node:example.com:old:gone"})
	if err := e.run("--install-skills"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != body {
		t.Fatal("login pruned or changed the orphan")
	}
	e.assertInstalled(t, false, true)
}

// #2500 pin: the automatic offer accepted, then the export fails — a
// SUCCESSFUL login is preserved. The command exits 0, states the sign-in on
// stdout, keeps the token stored, prints the retry note with the exact
// reinstall command, and installs nothing. The note is the one at
// login_skills.go's reportLoginSkills: load-bearing behavior with zero
// coverage before this test.
func TestLogin508AcceptedOfferExportFailureKeepsLogin(t *testing.T) {
	e := setupLogin508(t, true, false, "y\n", []string{".agents"}, nil, true)
	e.setFailExport(true)
	if err := e.run(); err != nil {
		t.Fatalf("an accepted offer whose export fails must keep exit 0, got %v", err)
	}
	if !strings.Contains(e.out.String(), "✓ Signed in to ") {
		t.Errorf("stdout must still report the successful login:\n%s", e.out.String())
	}
	note := e.errOut.String()
	if !strings.Contains(note, "note: signed in, but skill installation could not finish; run ") ||
		!strings.Contains(note, "hadron auth login --install-skills --server "+e.f.ServerFlag) || !strings.Contains(note, "to retry") {
		t.Errorf("missing retry note with the exact reinstall command:\n%s", note)
	}
	if tok, err := (store.Keyring{}).Get(authflow.Host(e.f.ServerFlag)); err != nil || tok != newTok {
		t.Errorf("the login token must stay stored after the export failure (%v)", err)
	}
	e.assertInstalled(t, false, false)
	// The offer ran: one availability query, then exactly one (failed) export
	// attempt — and nothing else.
	e.assertRequests(t, false, true, true)
}

// #2500 pin (binding condition): the explicit --install-skills path REPORTS
// the installation failure instead of swallowing it — non-zero exit, the error
// naming that the sign-in succeeded, stdout's login line already printed, and
// the token stored. The automatic path swallowing the same failure is pinned
// above; the two must never converge.
func TestLogin508ExplicitInstallFailureStatesLogin(t *testing.T) {
	e := setupLogin508(t, true, false, "", []string{".agents"}, nil, true)
	e.setFailExport(true)
	err := e.run("--install-skills")
	if err == nil {
		t.Fatal("an explicit installation failure must not exit 0")
	}
	if !strings.Contains(err.Error(), "signed in, but skill installation failed") {
		t.Errorf("the error must state the login succeeded while returning the installation failure: %v", err)
	}
	if !strings.Contains(e.out.String(), "✓ Signed in to ") {
		t.Errorf("stdout must have reported the login before the failure:\n%s", e.out.String())
	}
	if tok, gerr := (store.Keyring{}).Get(authflow.Host(e.f.ServerFlag)); gerr != nil || tok != newTok {
		t.Errorf("the login token must be stored despite the installation failure (%v)", gerr)
	}
	e.assertInstalled(t, false, false)
}
