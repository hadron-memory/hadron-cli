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
	host, credential string
	force            bool
}
type login508Env struct {
	f           *cmdutil.Factory
	home        string
	out, errOut *strings.Builder
	calls       *[]login508Call
	orphans     *[]any
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
		calls = append(calls, login508Call{req.Variables.Input.Host, r.Header.Get("Authorization"), string(req.Variables.Input.Force) == "true"})
		entries := []any{}
		if enabled {
			entries = append(entries, map[string]any{"urn": "hrn:node:example.com:demo:task", "nodeId": "node-508", "name": "demo-508", "class": "never-exported", "findings": []any{}, "renderedBody": "---\nname: demo-508\ndescription: Synthetic test skill\n---\nTest only.\n", "exportPlan": map[string]any{"action": "WRITE", "reasons": []any{}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"skillPlan": map[string]any{"scanned": len(entries), "judged": len(entries), "entries": entries, "orphans": orphans, "unrecognized": []any{}}}})
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
	return &login508Env{f, home, out, errOut, &calls, &orphans}
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
			for _, call := range *e.calls {
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
			if (c.claude || c.codex) && len(*e.calls) == 0 {
				t.Error("no exporter calls")
			}
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
	}{
		{name: "accept", answer: "y\n", tty: true, enabled: true, prompt: true, installed: true},
		{name: "decline", answer: "n\n", tty: true, enabled: true, prompt: true},
		{name: "default-no", answer: "\n", tty: true, enabled: true, prompt: true},
		{name: "pipe", enabled: true, hint: true},
		{name: "json", tty: true, jsonMode: true, enabled: true, hint: true},
		{name: "explicit-json", jsonMode: true, enabled: true, args: []string{"--install-skills"}, installed: true},
		{name: "no-flag", tty: true, enabled: true, args: []string{"--no-install-skills"}},
		{name: "config-off", tty: true, enabled: true, configOff: true},
		{name: "flag-over-config", tty: true, enabled: true, configOff: true, args: []string{"--install-skills"}, installed: true},
		{name: "nothing-enabled", tty: true, enabled: false},
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
			if c.hint && !strings.Contains(e.errOut.String(), "hadron auth login --install-skills") {
				t.Errorf("missing noninteractive command hint: %s", text)
			}
			if c.jsonMode && !json.Valid([]byte(e.out.String())) {
				t.Errorf("not one JSON document: %s", e.out.String())
			}
			if !c.enabled && !strings.Contains(text, "No skills are enabled") {
				t.Errorf("missing nothing-enabled message: %s", text)
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
	if browserCalls != 0 || len(*e.calls) != 0 {
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
	for _, call := range *e.calls {
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
	if len(*e.calls) != 0 {
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
	*e.orphans = append(*e.orphans, map[string]any{"dirName": "orphan-508", "nodeId": "orphan-508", "sourceUrn": "hrn:node:example.com:old:gone"})
	if err := e.run("--install-skills"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != body {
		t.Fatal("login pruned or changed the orphan")
	}
	e.assertInstalled(t, false, true)
}
