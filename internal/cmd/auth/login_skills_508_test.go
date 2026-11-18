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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			OperationName string `json:"operationName"`
			Variables     struct {
				Input struct {
					Host  string `json:"host"`
					Force bool   `json:"force"`
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
		if req.OperationName != "SkillExportPlan" {
			t.Errorf("unexpected operation %q", req.OperationName)
			http.Error(w, "unexpected", 500)
			return
		}
		calls = append(calls, login508Call{req.Variables.Input.Host, r.Header.Get("Authorization"), req.Variables.Input.Force})
		entries := []any{}
		if enabled {
			entries = append(entries, map[string]any{"urn": "hrn:node:example.com:demo:task", "nodeId": "node-508", "name": "demo-508", "class": "never-exported", "findings": []any{}, "renderedBody": "---\nname: demo-508\ndescription: Synthetic test skill\n---\nTest only.\n", "exportPlan": map[string]any{"action": "WRITE", "reasons": []any{}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"skillPlan": map[string]any{"scanned": len(entries), "judged": len(entries), "entries": entries, "orphans": []any{}, "unrecognized": []any{}}}})
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
	return &login508Env{f, home, out, errOut, &calls}
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
