package hostpoller

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// cli#2199 C8: a worker.start's family, model or tier, and effort override the
// worker's launcher profile for the run, and the bind reports what applied.

// orcaStartWith runs one worker.start through the Orca path for a worker whose
// profile is prof/acct, with extra op args merged over startOp's.
func orcaStartWith(t *testing.T, prof LauncherProfile, acct AccountProfile, args map[string]any) (report, *recordedOrca, *fakeHost) {
	t.Helper()
	p, srv, host := testPoller(t)
	orcaWorker(p, filepath.Join(t.TempDir(), "disk"), prof, acct)
	rec := &recordedOrca{}
	p.EnableOrcaLaunch(rec.launch, ConfigProfiles{Cfg: p.cfg})
	srv.notDone["s1"] = 2
	op := startOp()
	for k, v := range args {
		op.Args[k] = v
	}
	srv.batches = [][]Operation{{op}}
	step(t, p)
	return only(t, srv), rec, host
}

var (
	claudeProf = LauncherProfile{Family: "claude", Mode: ModeLong, Model: "opus", Account: "a"}
	claudeAcct = AccountProfile{Bin: "claude-holger"}
	codexAcct  = AccountProfile{Bin: "codex", CodexHome: "/acct/home"}
	// an account that explicitly has a template for both families
	bothAcct    = AccountProfile{Bin: "codex", CodexHome: "/acct/home", Families: []string{"claude", "codex"}}
	codexOnClds = LauncherProfile{Family: "claude", Mode: ModeShort, Account: "a"}
)

func TestStartLaunchesAnotherFamilyOnACapableAccount2199(t *testing.T) {
	r, rec, _ := orcaStartWith(t, codexOnClds, bothAcct, map[string]any{"family": "codex"})
	if r.outcome != Succeeded || len(rec.specs) != 1 {
		t.Fatalf("report %+v, launches %d", r, len(rec.specs))
	}
	if want := []string{"codex", "--no-daemon", "--dangerously-bypass-approvals-and-sandbox", PromptArg}; !reflect.DeepEqual(rec.specs[0].Argv, want) {
		t.Fatalf("argv = %q", rec.specs[0].Argv)
	}
	if !strings.Contains(rec.specs[0].Prompt, "--tool codex --family codex") {
		t.Fatalf("prompt = %q", rec.specs[0].Prompt)
	}
}

func TestStartRefusesAFamilyTheAccountCannotLaunch2199(t *testing.T) {
	for name, c := range map[string]struct {
		acct AccountProfile
		fam  string
	}{
		"codex on a claude account": {claudeAcct, "codex"},
		"claude on a codex account": {codexAcct, "claude"},
		"family with no template":   {claudeAcct, "gemini"},
		"malformed family":          {claudeAcct, "Claude Code"},
	} {
		t.Run(name, func(t *testing.T) {
			prof := claudeProf
			if c.acct.CodexHome != "" {
				prof = LauncherProfile{Family: "codex", Mode: ModeShort, Account: "a"}
			}
			r, rec, host := orcaStartWith(t, prof, c.acct, map[string]any{"family": c.fam})
			if r.outcome != Failed || len(rec.specs) != 0 || len(host.launches) != 0 {
				t.Fatalf("report %+v, launches %d", r, len(rec.specs))
			}
			if name != "malformed family" && (!strings.Contains(r.reason, c.fam) || !strings.Contains(r.reason, `"a"`)) {
				t.Fatalf("the reason must name the family and the account: %q", r.reason)
			}
		})
	}
}

func TestStartModelOverridesTheProfileAndIsBound2199(t *testing.T) {
	r, rec, _ := orcaStartWith(t, claudeProf, claudeAcct, map[string]any{"model": "claude-sonnet-5-5"})
	if r.outcome != Succeeded {
		t.Fatalf("%+v", r)
	}
	if want := []string{"claude-holger", "--dangerously-skip-permissions", "--model", "claude-sonnet-5-5", PromptArg}; !reflect.DeepEqual(rec.specs[0].Argv, want) {
		t.Fatalf("argv = %q", rec.specs[0].Argv)
	}
	if !strings.Contains(rec.specs[0].Prompt, "--family claude --model claude-sonnet-5-5 --effort NOT_APPLICABLE") {
		t.Fatalf("prompt = %q", rec.specs[0].Prompt)
	}
}

func TestStartTierMapsToTheClaudeAlias2199(t *testing.T) {
	for tier, alias := range map[string]string{"high": "opus", "mid": "sonnet", "low": "haiku"} {
		prof := claudeProf
		prof.Model = "something-else"
		_, rec, _ := orcaStartWith(t, prof, claudeAcct, map[string]any{"tier": tier})
		if len(rec.specs) != 1 || !reflect.DeepEqual(rec.specs[0].Argv, []string{"claude-holger", "--dangerously-skip-permissions", "--model", alias, PromptArg}) {
			t.Fatalf("tier %s: %+v", tier, rec.specs)
		}
		if !strings.Contains(rec.specs[0].Prompt, "--model "+alias) {
			t.Fatalf("tier %s prompt = %q", tier, rec.specs[0].Prompt)
		}
	}
}

func TestStartTierOnAFamilyWithoutATableLaunchesWithTheDefaultModel2199(t *testing.T) {
	prof := LauncherProfile{Family: "codex", Mode: ModeShort, Model: "gpt-x", Account: "a"}
	r, rec, _ := orcaStartWith(t, prof, codexAcct, map[string]any{"family": "codex", "tier": "mid"})
	if r.outcome != Succeeded || len(rec.specs) != 1 {
		t.Fatalf("%+v", r)
	}
	if want := []string{"codex", "--no-daemon", "--dangerously-bypass-approvals-and-sandbox", PromptArg}; !reflect.DeepEqual(rec.specs[0].Argv, want) {
		t.Fatalf("argv = %q (no -m: the account default runs)", rec.specs[0].Argv)
	}
	res, err := ResolveRun(prof, codexAcct, RunSettings{Family: "codex", Tier: "mid"}, "a")
	if err != nil || !strings.Contains(res.Note, "not mapped") {
		t.Fatalf("the unmapped tier must be reported: %+v %v", res, err)
	}
}

func TestStartMalformedShapesLaunchNothing2199(t *testing.T) {
	for name, args := range map[string]map[string]any{
		"model and tier":        {"model": "m1", "tier": "high"},
		"effort NOT_APPLICABLE": {"effort": "NOT_APPLICABLE"},
		"tier outside scale":    {"tier": "ultra"},
		"model is a flag":       {"model": "--resume"},
	} {
		t.Run(name, func(t *testing.T) {
			r, rec, host := orcaStartWith(t, claudeProf, claudeAcct, args)
			if r.outcome != Failed || !strings.Contains(r.reason, "malformed") || len(rec.specs) != 0 || len(host.launches) != 0 {
				t.Fatalf("report %+v, launches %d", r, len(rec.specs))
			}
		})
	}
}

func TestStartEffortMapsToCodexAndIsNotRefusedOnClaude2199(t *testing.T) {
	codexProf := LauncherProfile{Family: "codex", Mode: ModeLong, Account: "a"}
	for effort, want := range map[string]string{"HIGH": "high", "MAX": "xhigh", "XHIGH": "xhigh", "LOW": "low", "MEDIUM": "medium", "UNSET": ""} {
		_, rec, _ := orcaStartWith(t, codexProf, codexAcct, map[string]any{"family": "codex", "effort": effort})
		got := strings.Join(rec.specs[0].Argv, " ")
		if has := strings.Contains(got, "model_reasoning_effort="+want); want != "" && !has || want == "" && strings.Contains(got, "model_reasoning_effort") {
			t.Fatalf("effort %s: argv %q", effort, got)
		}
	}
	_, rec, _ := orcaStartWith(t, claudeProf, claudeAcct, map[string]any{"effort": "HIGH"})
	if len(rec.specs) != 1 || strings.Contains(strings.Join(rec.specs[0].Argv, " "), "effort") {
		t.Fatalf("claude must launch without an effort flag: %+v", rec.specs)
	}
	if !strings.Contains(rec.specs[0].Prompt, "--effort NOT_APPLICABLE") {
		t.Fatalf("prompt = %q", rec.specs[0].Prompt)
	}
}

func TestStartWithNoRunSettingsIsUnchanged2199(t *testing.T) {
	_, rec, _ := orcaStartWith(t, claudeProf, claudeAcct, nil)
	if len(rec.specs) != 1 {
		t.Fatal("no launch")
	}
	if want := []string{"claude-holger", "--dangerously-skip-permissions", "--model", "opus", PromptArg}; !reflect.DeepEqual(rec.specs[0].Argv, want) {
		t.Fatalf("argv = %q", rec.specs[0].Argv)
	}
	if want := FirstPrompt("iris", "claude", "t1", true); rec.specs[0].Prompt != want {
		t.Fatalf("prompt changed:\n%q\n%q", rec.specs[0].Prompt, want)
	}
}

// The exec path has one fixed argv: it keeps the same-family guard.
func TestStartOnTheExecPathKeepsTheFamilyGuard2199(t *testing.T) {
	r, launches := startWith(t, "claude", "codex")
	if r.outcome != Failed || launches != 0 {
		t.Fatalf("%+v %d", r, launches)
	}
}

// Review fix (#2204 LOCAL P2, BLIND P2): the account must be able to launch
// exactly the family the start names, switched or not, and a Claude-family
// launcher script never "switches" to another Claude-family backend.
func TestStartRefusesAnIncompatibleAccountEvenForTheProfilesOwnFamily2199(t *testing.T) {
	for name, c := range map[string]struct {
		prof LauncherProfile
		acct AccountProfile
		args map[string]any
		fam  string
	}{
		"claude request on a codex account, model given":  {claudeProf, codexAcct, map[string]any{"family": "claude", "model": "sonnet"}, "claude"},
		"claude request on a codex account, family only":  {claudeProf, codexAcct, map[string]any{"family": "claude"}, "claude"},
		"codex request on a claude account, effort given": {LauncherProfile{Family: "codex", Mode: ModeLong, Account: "a"}, claudeAcct, map[string]any{"family": "codex", "effort": "HIGH"}, "codex"},
		"claude launcher switched to glm":                 {claudeProf, claudeAcct, map[string]any{"family": "glm"}, "glm"},
		"glm launcher switched to claude":                 {LauncherProfile{Family: "glm", Mode: ModeLong, Account: "a"}, AccountProfile{Bin: "claude-glm"}, map[string]any{"family": "claude"}, "claude"},
	} {
		t.Run(name, func(t *testing.T) {
			r, rec, host := orcaStartWith(t, c.prof, c.acct, c.args)
			if r.outcome != Failed || len(rec.specs) != 0 || len(host.launches) != 0 {
				t.Fatalf("report %+v, launches %d", r, len(rec.specs))
			}
			if !strings.Contains(r.reason, c.fam) || !strings.Contains(r.reason, `"a"`) {
				t.Fatalf("the reason must name the family and the account: %q", r.reason)
			}
		})
	}
}

// Wolfgang's ruling (#110836): the exec path launches its own fixed command,
// never refuses the start for settings it cannot apply, and states they were
// not applied, in the log and in the agent's environment (the carrier for the
// bind, hadron-server#4306).
func TestStartOnTheExecPathStatesSettingsNotApplied2199(t *testing.T) {
	for name, c := range map[string]struct {
		args map[string]any
		want bool
	}{
		"model":  {map[string]any{"model": "sonnet"}, true},
		"tier":   {map[string]any{"tier": "low"}, true},
		"effort": {map[string]any{"effort": "HIGH"}, true},
		"none":   {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			p, srv, host := testPoller(t)
			var logged []string
			p.emit = func(e Event) { logged = append(logged, e.Kind+": "+e.Detail) }
			srv.notDone["s1"] = 1
			op := startOp()
			for k, v := range c.args {
				op.Args[k] = v
			}
			srv.batches = [][]Operation{{op}}
			step(t, p)
			if len(host.launches) != 1 || !reflect.DeepEqual(host.launches[0].Argv, []string{"claude-worker", "--model", "opus"}) {
				t.Fatalf("the fixed command must launch unchanged: %+v", host.launches)
			}
			has := false
			for _, kv := range host.launches[0].Env {
				has = has || kv == StartSettingsAppliedEnvVar+"=false"
			}
			said := false
			for _, l := range logged {
				said = said || strings.Contains(l, "not applied")
			}
			if has != c.want || said != c.want {
				t.Fatalf("env statement %v, log %v, want %v (env %q)", has, said, c.want, host.launches[0].Env)
			}
		})
	}
}

// An account's capability is explicit: its families list, never its name.
func TestAccountFamiliesAreExplicit2199(t *testing.T) {
	prof := LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}
	both := AccountProfile{Bin: "claude-holger", Families: []string{"claude", "glm"}}
	if r, err := ResolveRun(prof, both, RunSettings{Family: "glm"}, "a"); err != nil || r.Profile.Family != "glm" {
		t.Fatalf("an account that lists glm may switch to it: %+v %v", r, err)
	}
	if _, err := ResolveRun(prof, both, RunSettings{Family: "codex"}, "a"); err == nil {
		t.Fatal("codex is not in the account's list")
	}
	// a codex-looking launcher with no explicit list still launches only its profile family
	if _, err := ResolveRun(prof, AccountProfile{Bin: "codex"}, RunSettings{Family: "codex"}, "a"); err == nil {
		t.Fatal("a launcher's name must not grant a family")
	}
}

// A Codex account without a codex_home (the field is optional) is valid and
// launches its own family (#2204 LOCAL delta P2).
func TestCodexAccountWithoutAHomeLaunchesCodex2199(t *testing.T) {
	prof := LauncherProfile{Family: "codex", Mode: ModeShort, Account: "a"}
	r, rec, _ := orcaStartWith(t, prof, AccountProfile{Bin: "codex"}, map[string]any{"family": "codex", "model": "gpt-6"})
	if r.outcome != Succeeded || len(rec.specs) != 1 {
		t.Fatalf("%+v launches %d", r, len(rec.specs))
	}
	if _, err := ResolveRun(claudeProf, AccountProfile{Bin: "codex"}, RunSettings{Family: "claude"}, "a"); err == nil {
		t.Fatal("a Claude profile on a codex launcher must still be refused")
	}
}

// The families list is an exhaustive allowlist (#2204 BLIND P3): a list that
// leaves out the profile's own family refuses even that family.
func TestAccountFamiliesListIsExhaustive2199(t *testing.T) {
	prof := LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}
	acct := AccountProfile{Bin: "claude-holger", Families: []string{"glm"}}
	if _, err := ResolveRun(prof, acct, RunSettings{Family: "claude"}, "a"); err == nil {
		t.Fatal("a list without the profile family must refuse it")
	}
}

// Load-time validation of account families (#2204 BLIND P3): an unsupported
// family is refused; canonical aliases are accepted.
func TestAccountFamiliesLoadValidation2199(t *testing.T) {
	cfg := func(families ...string) *Config {
		return &Config{App: "hrn:app:acme.com:team", Label: "box1", PollIntervalSeconds: 30, WorktreesRoot: "/w", StateFile: "/w/s.json",
			Workers: map[string]WorkerConfig{"iris": {Family: "claude", LaunchVia: LaunchViaOrca, DiskTmp: "/disk/iris", Worktrees: []string{"r/iris"},
				Profile: &LauncherProfile{Family: "claude", Mode: ModeLong, Account: "a"}}},
			Accounts:   map[string]AccountProfile{"a": {Bin: "claude-holger", Families: families}},
			OrcaLaunch: OrcaLaunchConfig{OrcaBin: "/usr/bin/orca"},
		}
	}
	if err := cfg("claude", "gemini").validate(); err == nil {
		t.Fatal("a family with no launch template must be refused at load")
	}
	if err := cfg("anthropic", "openai", "glm", "kimi").validate(); err != nil {
		t.Fatalf("canonical aliases and supported families load: %v", err)
	}
}
