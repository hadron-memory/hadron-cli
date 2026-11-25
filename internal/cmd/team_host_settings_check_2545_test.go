package cmd

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostpoller/hostyaml"
)

// cli#2545: `team host settings-check` is local and read-only.
type settingsFixture struct{ root, home, trees, managed, bin, cfg string }

func (x settingsFixture) put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newSettingsFixture(t *testing.T, version, workerExtra string) settingsFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hosts are Linux; the fixture uses symlinks")
	}
	r, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	x := settingsFixture{r, filepath.Join(r, "home"), filepath.Join(r, "trees"), filepath.Join(r, "managed"), filepath.Join(r, "claude"), filepath.Join(r, "host.yaml")}
	x.put(t, filepath.Join(r, "versions", version), "")
	if err := os.Symlink(filepath.Join(r, "versions", version), x.bin); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(x.trees, "repo", "iris"), 0o755); err != nil {
		t.Fatal(err)
	}
	x.put(t, x.cfg, hostyaml.Supervised("app: a\nworktrees_root: "+x.trees+"\nworkers:\n  iris:\n    family: claude\n    launch: [x]\n    worktrees: [repo/iris]\n"+workerExtra))
	t.Setenv("HOME", x.home)
	return x
}

type settingsOut struct {
	Drift         bool   `json:"drift"`
	ClaudeVersion string `json:"claudeVersion"`
	Accounts      []struct {
		Dir    string `json:"dir"`
		Status string `json:"status"`
	} `json:"accounts"`
	Worktrees []struct {
		Worker    string   `json:"worker"`
		Status    string   `json:"status"`
		Effective *bool    `json:"effective"`
		Source    *string  `json:"source"`
		Reasons   []string `json:"reasons"`
	} `json:"worktrees"`
}

func (x settingsFixture) run(t *testing.T) (settingsOut, string, int) {
	t.Helper()
	f, out := testFactory(t)
	root := NewRootCmd(f)
	// An unreachable server: the check must not need one.
	root.SetArgs([]string{"team", "host", "settings-check", "--config", x.cfg, "--managed-dir", x.managed, "--claude-bin", x.bin, "--server", "http://127.0.0.1:1", "--json"})
	err := root.Execute()
	var o settingsOut
	if e := json.Unmarshal([]byte(out.String()), &o); e != nil {
		t.Fatalf("%v (%v)\n%s", e, err, out.String())
	}
	code := 0
	if err != nil {
		code = exitCodeFor(err)
	}
	return o, out.String(), code
}

func TestTeamHostSettingsCheck2545(t *testing.T) {
	const off, on = `{"idleCompaction":false}`, `{"idleCompaction":true}`
	user := func(x settingsFixture) string { return filepath.Join(x.home, ".claude", "settings.json") }
	user2 := func(x settingsFixture) string { return filepath.Join(x.home, ".claude-b", "settings.json") }
	local := func(x settingsFixture) string {
		return filepath.Join(x.trees, "repo", "iris", ".claude", "settings.local.json")
	}
	shared := func(x settingsFixture) string {
		return filepath.Join(x.trees, "repo", "iris", ".claude", "settings.json")
	}
	managed := func(x settingsFixture) string { return filepath.Join(x.managed, "managed-settings.json") }
	dropin := func(n string) func(settingsFixture) string {
		return func(x settingsFixture) string { return filepath.Join(x.managed, "managed-settings.d", n) }
	}
	mainLocal := func(x settingsFixture) string {
		return filepath.Join(x.root, "main", ".claude", "settings.local.json")
	}
	type put struct {
		path func(settingsFixture) string
		body string
	}
	cases := []struct {
		name, version string
		linked        bool
		puts          []put
		source        func(settingsFixture) string // nil: no file sets it
		effective     string                       // true | false | null
		reasons       string
	}{
		{"unset is the default", "2.1.330", false, nil, nil, "true", "UNSET"},
		{"user off", "2.1.330", false, []put{{user, off}}, user, "false", ""},
		{"user on", "2.1.330", false, []put{{user, on}}, user, "true", "ON"},
		{"local beats user", "2.1.330", false, []put{{user, on}, {local, off}}, local, "false", ""},
		{"local beats shared", "2.1.330", false, []put{{shared, off}, {local, on}}, local, "true", "ON"},
		{"shared beats user", "2.1.330", false, []put{{user, off}, {shared, on}}, shared, "true", "ON"},
		{"managed on beats everything", "2.1.330", false, []put{{managed, on}, {local, off}, {user, off}}, managed, "true", "MANAGED_ON"},
		{"later drop-in wins", "2.1.330", false, []put{{managed, on}, {dropin("10.json"), on}, {dropin("20.json"), off}}, dropin("20.json"), "false", ""},
		{"hidden and non-json drop-ins are ignored", "2.1.330", false, []put{{user, off}, {dropin(".a.json"), on}, {dropin("b.txt"), on}}, user, "false", ""},
		{"a broken managed file is never off", "2.1.330", false, []put{{managed, `{`}, {user, off}}, managed, "null", "INVALID"},
		{"linked worktree: main checkout's local wins", "2.1.330", true, []put{{mainLocal, on}, {local, off}}, mainLocal, "true", "ON"},
		{"linked worktree: its own local is next", "2.1.330", true, []put{{local, off}, {user, on}}, local, "false", ""},
		{"linked worktree: main checkout's local off", "2.1.330", true, []put{{mainLocal, off}}, mainLocal, "false", ""},
		{"linked worktree, unset main local falls through", "2.1.330", true, []put{{mainLocal, `{"hooks":{}}`}, {shared, off}}, shared, "false", ""},
		{"accounts disagree", "2.1.330", false, []put{{user, off}, {user2, on}}, user2, "true", "ON ACCOUNT_UNKNOWN"},
		{"every account off", "2.1.330", false, []put{{user, off}, {user2, off}}, user, "false", ""},
		{"old version", "2.1.289", false, []put{{user, off}}, user, "false", "VERSION_TOO_OLD"},
		{"minimum version", "2.1.290", false, []put{{user, off}}, user, "false", ""},
		{"newer minor", "2.2.0", false, []put{{user, off}}, user, "false", ""},
		{"unknown version", "weird", false, []put{{user, off}}, user, "false", "VERSION_UNKNOWN"},
		{"wrong type", "2.1.330", false, []put{{local, `{"idleCompaction":"false"}`}, {user, off}}, local, "null", "INVALID"},
		{"null is not off", "2.1.330", false, []put{{user, `{"idleCompaction":null}`}}, user, "null", "INVALID"},
		{"trailing comma", "2.1.330", false, []put{{local, `{"idleCompaction":false,}`}, {user, off}}, local, "null", "INVALID"},
		{"key case is exact", "2.1.330", false, []put{{user, `{"idlecompaction":false}`}}, nil, "true", "UNSET"},
		{"byte order mark accepted", "2.1.330", false, []put{{user, "\xef\xbb\xbf" + off}}, user, "false", ""},
		{"a higher off decides over a broken lower file", "2.1.330", false, []put{{local, off}, {user, `{`}}, local, "false", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := newSettingsFixture(t, c.version, "")
			if err := os.MkdirAll(filepath.Join(x.home, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			if c.linked {
				gd := filepath.Join(x.root, "main", ".git", "worktrees", "iris")
				x.put(t, filepath.Join(gd, "commondir"), "../..\n")
				x.put(t, filepath.Join(x.trees, "repo", "iris", ".git"), "gitdir: "+gd+"\n")
			}
			for _, p := range c.puts {
				x.put(t, p.path(x), p.body)
			}
			o, raw, code := x.run(t)
			if len(o.Worktrees) != 1 {
				t.Fatalf("rows: %s", raw)
			}
			w := o.Worktrees[0]
			eff := "null"
			if w.Effective != nil {
				eff = map[bool]string{true: "true", false: "false"}[*w.Effective]
			}
			src, want := "", ""
			if w.Source != nil {
				src = *w.Source
			}
			if c.source != nil {
				want = c.source(x)
			}
			status, exit := "OFF", 0
			if c.reasons != "" {
				status, exit = "DRIFT", exitcode.Conflict
			}
			if eff != c.effective || src != want || strings.Join(w.Reasons, " ") != c.reasons || w.Status != status || code != exit || o.Drift != (exit != 0) {
				t.Errorf("effective %s source %q reasons %v status %s exit %d drift %v\n%s", eff, src, w.Reasons, w.Status, code, o.Drift, raw)
			}
		})
	}
}

// A worker that names its claude_config_dir is judged by that directory alone.
func TestTeamHostSettingsCheckNamedConfigDir2545(t *testing.T) {
	x := newSettingsFixture(t, "2.1.330", "    claude_config_dir: /nowhere-2545\n")
	x.put(t, filepath.Join(x.home, ".claude", "settings.json"), `{"idleCompaction":false}`)
	o, raw, code := x.run(t)
	if code != exitcode.Conflict || strings.Join(o.Worktrees[0].Reasons, " ") != "UNSET" {
		t.Errorf("exit %d\n%s", code, raw)
	}
}

// Nothing is verified for a tree that is not there, a worker with no tree or
// a file that is not a regular one; a Codex worker has no such setting.
func TestTeamHostSettingsCheckUnverifiable2545(t *testing.T) {
	x := newSettingsFixture(t, "2.1.330", "  ada:\n    family: claude\n    launch: [x]\n  bo:\n    family: claude\n    launch: [x]\n    worktrees: [repo/gone]\n  cy:\n    family: codex\n    launch: [x]\n    worktrees: [repo/cy]\n")
	if err := os.MkdirAll(filepath.Join(x.home, ".claude", "settings.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	o, raw, code := x.run(t)
	got := map[string]string{}
	for _, w := range o.Worktrees {
		got[w.Worker] = w.Status + " " + strings.Join(w.Reasons, " ")
	}
	want := map[string]string{"ada": "DRIFT NO_WORKTREE", "bo": "DRIFT WORKTREE_MISSING", "cy": "NOT_APPLICABLE ", "iris": "DRIFT UNREADABLE"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q", k, got[k], v)
		}
	}
	if code != exitcode.Conflict {
		t.Errorf("exit %d\n%s", code, raw)
	}
}

// A worktree the poller created is checked like a configured one.
func TestTeamHostSettingsCheckCreatedWorktree2545(t *testing.T) {
	x := newSettingsFixture(t, "2.1.330", "")
	x.put(t, filepath.Join(x.root, "hadron-host-state.json"), `{"created":{"iris":["repo/made","repo/iris"]},"keep":{}}`)
	x.put(t, filepath.Join(x.home, ".claude", "settings.json"), `{"idleCompaction":false}`)
	x.put(t, filepath.Join(x.trees, "repo", "made", ".claude", "settings.json"), `{"idleCompaction":true}`)
	o, raw, code := x.run(t)
	if len(o.Worktrees) != 2 || o.Worktrees[0].Status != "OFF" || o.Worktrees[1].Status != "DRIFT" || code != exitcode.Conflict {
		t.Errorf("exit %d\n%s", code, raw)
	}
}

// It writes nothing and shows nothing of a settings file but the one key.
func TestTeamHostSettingsCheckReadsOnlyTheKey2545(t *testing.T) {
	x := newSettingsFixture(t, "2.1.330", "")
	x.put(t, filepath.Join(x.home, ".claude", "settings.json"), `{"env":{"TOKEN":"s3cr3t-valid"},"idleCompaction":true}`)
	x.put(t, filepath.Join(x.home, ".claude-b", "settings.json"), `{"idleCompaction":"s3cr3t-type"}`)
	x.put(t, filepath.Join(x.home, ".claude-c", "settings.json"), `s3cr3t-invalid`)
	snap := func() string {
		var b strings.Builder
		_ = filepath.WalkDir(x.root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				t.Fatal(err)
			}
			i, _ := d.Info()
			b.WriteString(p + i.Mode().String() + i.ModTime().String())
			if i.Mode().IsRegular() {
				raw, _ := os.ReadFile(p)
				b.Write(raw)
			}
			return nil
		})
		return b.String()
	}
	before := snap()
	_, raw, _ := x.run(t)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "settings-check", "--config", x.cfg, "--managed-dir", x.managed, "--claude-bin", x.bin})
	err := root.Execute()
	if exitCodeFor(err) != exitcode.Conflict {
		t.Fatalf("human form: %v", err)
	}
	if all := raw + out.String() + err.Error(); strings.Contains(all, "s3cr3t") || strings.Contains(all, "TOKEN") {
		t.Errorf("file contents shown:\n%s", all)
	}
	if !strings.Contains(out.String(), "iris") || !strings.Contains(out.String(), "DRIFT") {
		t.Errorf("human report:\n%s", out.String())
	}
	if snap() != before {
		t.Error("the check changed a file")
	}
}

// An empty directory would read managed settings from the current one.
func TestTeamHostSettingsCheckRefusesEmptyManagedDir2545(t *testing.T) {
	x := newSettingsFixture(t, "2.1.330", "")
	x.put(t, filepath.Join(x.home, ".claude", "settings.json"), `{"idleCompaction":false}`)
	f, out := testFactory(t)
	root := NewRootCmd(f)
	root.SetArgs([]string{"team", "host", "settings-check", "--config", x.cfg, "--claude-bin", x.bin, "--managed-dir", ""})
	if code := exitCodeFor(root.Execute()); code != exitcode.Usage || strings.Contains(out.String(), "ACCOUNT FILE") {
		t.Errorf("exit %d: %s", code, out.String())
	}
}

func TestTeamHostSettingsCheckNeedsConfig2545(t *testing.T) {
	for _, args := range [][]string{nil, {"--config", "/nowhere-2545.yaml"}} {
		f, _ := testFactory(t)
		root := NewRootCmd(f)
		root.SetArgs(append([]string{"team", "host", "settings-check"}, args...))
		if code := exitCodeFor(root.Execute()); code != exitcode.Usage {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}
