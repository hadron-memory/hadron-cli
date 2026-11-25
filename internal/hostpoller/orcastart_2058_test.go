package hostpoller

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// cli#2058 C3 at the poller: worker.start of a launch_via: orca worker goes
// through the Orca launcher with the profile's argv, the explicit environment
// and the fixed first prompt; every other worker keeps the exec path.

type recordedOrca struct {
	specs []OrcaLaunchSpec
	err   error
}

func (r *recordedOrca) launch(_ context.Context, spec OrcaLaunchSpec) (Agent, error) {
	r.specs = append(r.specs, spec)
	if r.err != nil {
		return nil, r.err
	}
	return &fakeAgent{done: make(chan struct{}), pid: 4242}, nil
}

func orcaWorker(p *Poller, disk string, prof LauncherProfile, acct AccountProfile) {
	w := p.startCfg.Workers["iris"]
	w.LaunchVia, w.Profile, w.DiskTmp = LaunchViaOrca, &prof, disk
	p.startCfg.Workers["iris"] = w
	p.startCfg.Accounts = map[string]AccountProfile{prof.Account: acct}
	p.startCfg.OrcaLaunch = OrcaLaunchConfig{OrcaBin: "/usr/bin/orca"}
}

func TestWorkerStartLaunchesThroughOrcaFromTheProfile2058(t *testing.T) {
	p, srv, host := testPoller(t)
	disk := filepath.Join(t.TempDir(), "disk")
	orcaWorker(p, disk, LauncherProfile{Family: "codex", Mode: ModeShort, Model: "gpt-6-luna", Account: "acct1"},
		AccountProfile{Bin: "codex", CodexHome: "/acct/home"})
	p.startCfg.Workers["iris"] = func() WorkerConfig { w := p.startCfg.Workers["iris"]; w.Family = "codex"; return w }()
	rec := &recordedOrca{}
	p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.startCfg})
	srv.notDone["s1"] = 2
	op := startOp()
	op.Args["family"] = "codex"
	srv.batches = [][]Operation{{op}}
	step(t, p)
	if r := only(t, srv); r.outcome != Succeeded {
		t.Fatalf("start must report SUCCEEDED once bound: %+v", r)
	}
	if len(host.launches) != 0 {
		t.Fatalf("the exec path launched too: %v", host.launches)
	}
	if len(rec.specs) != 1 {
		t.Fatalf("orca launches = %d", len(rec.specs))
	}
	s := rec.specs[0]
	if want := []string{"codex", "--no-daemon", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt-6-luna", PromptArg}; !reflect.DeepEqual(s.Argv, want) {
		t.Fatalf("argv = %q", s.Argv)
	}
	if s.Dir != p.startCfg.WorktreePath("repo/iris") || s.Title != "iris" {
		t.Fatalf("dir %q title %q", s.Dir, s.Title)
	}
	want := []string{"HADRON_LAUNCH_GRANT=hlg_grant", "TMPDIR=" + disk, "CLAUDE_CODE_TMPDIR=" + disk,
		"NODE_COMPILE_CACHE=" + filepath.Join(disk, "node-compile-cache"), "CODEX_HOME=/acct/home"}
	if !reflect.DeepEqual(s.Env, want) {
		t.Fatalf("env = %q", s.Env)
	}
	// Only these, never the poller's own environment (no host key, no token).
	for _, kv := range s.Env {
		if strings.Contains(kv, hostKey) {
			t.Fatalf("the host key reached the launch env: %q", kv)
		}
	}
	if !strings.Contains(s.Prompt, "hadron team task start t1") || !strings.Contains(s.Prompt, "--tool codex") || strings.Contains(s.Prompt, "--resume") {
		t.Fatalf("prompt = %q", s.Prompt)
	}
	if _, ok := p.agentWorktree("id-iris"); !ok {
		t.Fatal("the Orca-launched agent is not tracked")
	}
}

// A worker configured for Orca never falls back to the exec path: no
// launcher wired, or an Orca failure, launches nothing and says why.
func TestWorkerStartThroughOrcaNeverFallsBack2058(t *testing.T) {
	for name, setup := range map[string]func(p *Poller){
		"no launcher wired": func(p *Poller) { p.orcaLaunch = nil }, // testPoller wires the fake launcher: unwire it
		"orca refuses": func(p *Poller) {
			rec := &recordedOrca{err: errors.New("orca did not create the terminal (invalid_argument)")}
			p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.startCfg})
		},
	} {
		t.Run(name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			orcaWorker(p, filepath.Join(t.TempDir(), "disk"), LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}, AccountProfile{Bin: "claude-holger"})
			setup(p)
			srv.notDone["s1"] = 1
			srv.batches = [][]Operation{{startOp()}}
			step(t, p)
			r := only(t, srv)
			if r.outcome != Failed || !strings.Contains(r.reason, "through Orca failed") || len(host.launches) != 0 {
				t.Fatalf("report %+v, exec launches %d", r, len(host.launches))
			}
		})
	}
}

// The first prompt is a template: a task id that is not a plain token is not
// interpolated; a start with no task points at the briefing's routine.
func TestFirstPrompt2058(t *testing.T) {
	if p := FirstPrompt("iris", "claude", "AT0.75", true); !strings.Contains(p, "hadron team task start AT0.75") || !strings.Contains(p, "--tool claude-code") {
		t.Fatalf("%q", p)
	}
	for _, bad := range []string{"", "x; rm -rf /", "a\nb", "$(id)"} {
		p := FirstPrompt("iris", "claude", bad, true)
		if strings.Contains(p, "task start") {
			t.Fatalf("task id %q was interpolated: %q", bad, p)
		}
		// With no usable task it is a relaunch: the recovery routine (C7), never a resume.
		if !strings.Contains(p, "hadron team session recover") {
			t.Fatalf("no-task prompt is not the recovery prompt: %q", p)
		}
	}
}

// Load-time validation: an Orca worker's prerequisites are checked when the
// config loads, never discovered at a start.
func TestOrcaWorkerValidation2058(t *testing.T) {
	base := func() *Config {
		return &Config{App: "hrn:app:acme.com:team", Label: "box1", PollIntervalSeconds: 30, WorktreesRoot: "/w", StateFile: "/w/s.json",
			Workers: map[string]WorkerConfig{"iris": {Family: "claude", LaunchVia: LaunchViaOrca, DiskTmp: "/disk/iris", Worktrees: []string{"r/iris"},
				Profile: &LauncherProfile{Family: "glm", Mode: ModeLong, Account: "a"}}},
			Accounts:   map[string]AccountProfile{"a": {Bin: "claude-glm"}},
			OrcaLaunch: OrcaLaunchConfig{OrcaBin: "/usr/bin/orca"},
		}
	}
	if err := base().validate(); err != nil {
		t.Fatalf("a valid Orca worker (claude-family profile on a claude worker) refused: %v", err)
	}
	for name, mutate := range map[string]func(c *Config){
		"unknown launch_via": func(c *Config) { w := c.Workers["iris"]; w.LaunchVia = "systemd"; c.Workers["iris"] = w },
		"no profile":         func(c *Config) { w := c.Workers["iris"]; w.Profile = nil; c.Workers["iris"] = w },
		"unknown account":    func(c *Config) { c.Accounts = nil },
		"family mismatch":    func(c *Config) { w := c.Workers["iris"]; w.Profile.Family = "codex"; c.Workers["iris"] = w },
		"relative disk_tmp":  func(c *Config) { w := c.Workers["iris"]; w.DiskTmp = "tmp"; c.Workers["iris"] = w },
		"no disk_tmp":        func(c *Config) { w := c.Workers["iris"]; w.DiskTmp = ""; c.Workers["iris"] = w },
		"relative orca_bin":  func(c *Config) { c.OrcaLaunch.OrcaBin = "orca" },
		"bad group":          func(c *Config) { c.OrcaLaunch.Group = "in cus" },
		"bad argv":           func(c *Config) { w := c.Workers["iris"]; w.Profile.Effort = "-c"; c.Workers["iris"] = w },
		"relative codex_home": func(c *Config) {
			c.Accounts = map[string]AccountProfile{"a": {Bin: "claude-glm", CodexHome: "home"}}
		},
	} {
		c := base()
		mutate(c)
		if err := c.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// C5 entry (#2060): a relaunch under a still-open session has no grant, so
// the spec carries no grant and a prompt that recovers without binding again.
func TestOrcaLaunchSpecForARelaunch2058(t *testing.T) {
	p, _, _ := testPoller(t)
	orcaWorker(p, filepath.Join(t.TempDir(), "disk"), LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}, AccountProfile{Bin: "claude-holger"})
	p.EnableOrcaLaunch((&recordedOrca{}).launch, ConfigProfiles{Cfg: p.startCfg})
	spec, err := p.orcaLaunchSpec("iris", p.startCfg.Workers["iris"], p.startCfg.WorktreePath("repo/iris"), "", "", Resolved{})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range spec.Env {
		if strings.HasPrefix(kv, launchGrantEnvVar+"=") {
			t.Fatalf("a relaunch carries a grant: %q", kv)
		}
	}
	if strings.Contains(spec.Prompt, "session start") || !strings.Contains(spec.Prompt, "hadron team session recover") || !strings.Contains(spec.Prompt, "still open") {
		t.Fatalf("relaunch prompt %q", spec.Prompt)
	}
	// A grant-bearing start with no task binds first, then recovers (G2).
	spec, err = p.orcaLaunchSpec("iris", p.startCfg.Workers["iris"], p.startCfg.WorktreePath("repo/iris"), "hlg_g", "", Resolved{})
	if err != nil || !strings.Contains(spec.Prompt, "session start") || !strings.Contains(spec.Prompt, "session recover") || spec.Env[0] != launchGrantEnvVar+"=hlg_g" {
		t.Fatalf("G2 start spec %+v %v", spec, err)
	}
}
