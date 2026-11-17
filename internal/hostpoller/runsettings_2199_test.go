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
	claudeProf  = LauncherProfile{Family: "claude", Mode: ModeLong, Model: "opus", Account: "a"}
	claudeAcct  = AccountProfile{Bin: "claude-holger"}
	codexAcct   = AccountProfile{Bin: "codex", CodexHome: "/acct/home"}
	codexOnClds = LauncherProfile{Family: "claude", Mode: ModeShort, Account: "a"}
)

func TestStartLaunchesAnotherFamilyOnACapableAccount2199(t *testing.T) {
	r, rec, _ := orcaStartWith(t, codexOnClds, codexAcct, map[string]any{"family": "codex"})
	if r.outcome != Succeeded || len(rec.specs) != 1 {
		t.Fatalf("report %+v, launches %d", r, len(rec.specs))
	}
	if want := []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", PromptArg}; !reflect.DeepEqual(rec.specs[0].Argv, want) {
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
			if c.acct == codexAcct {
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
	if want := []string{"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", PromptArg}; !reflect.DeepEqual(rec.specs[0].Argv, want) {
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
