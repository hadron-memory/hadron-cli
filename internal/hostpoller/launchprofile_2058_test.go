package hostpoller

import (
	"reflect"
	"strings"
	"testing"
)

// cli#2058 C3: the argv a worker is started with is built from a typed
// launcher profile and fixed per-family, per-mode templates (G1), never from
// a free-form command, and never resumes a conversation (G9).

func acct(bin string) AccountProfile { return AccountProfile{Bin: bin} }

func TestLaunchArgvTemplates2058(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    LauncherProfile
		a    AccountProfile
		want []string
	}{
		{"claude long", LauncherProfile{Family: "claude", Mode: ModeLong, Model: "opus"}, acct("claude-holger"),
			[]string{"claude-holger", "--dangerously-skip-permissions", "--model", "opus", PromptArg}},
		{"claude short", LauncherProfile{Family: "claude", Mode: ModeShort, Model: "sonnet"}, acct("claude-worker"),
			[]string{"claude-worker", "--dangerously-skip-permissions", "--model", "sonnet", PromptArg}},
		{"glm long, no model", LauncherProfile{Family: "glm", Mode: ModeLong}, acct("claude-glm"),
			[]string{"claude-glm", "--dangerously-skip-permissions", PromptArg}},
		{"kimi short", LauncherProfile{Family: "kimi", Mode: ModeShort}, acct("claude-kimi"),
			[]string{"claude-kimi", "--dangerously-skip-permissions", PromptArg}},
		{"codex long", LauncherProfile{Family: "codex", Mode: ModeLong, Model: "gpt-6-luna", Effort: "high"}, acct("codex"),
			[]string{"codex", "--no-daemon", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt-6-luna", "-c", "model_reasoning_effort=high", PromptArg}},
		{"codex short", LauncherProfile{Family: "codex", Mode: ModeShort}, acct("codex"),
			[]string{"codex", "--no-daemon", "--dangerously-bypass-approvals-and-sandbox", PromptArg}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LaunchArgv(tc.p, tc.a)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("argv = %q\nwant %q", got, tc.want)
			}
		})
	}
}

// A profile the templates cannot express is refused, never approximated: an
// unknown family or mode,
// and a model or effort that is not a plain token.
func TestLaunchArgvRefusals2058(t *testing.T) {
	for name, tc := range map[string]struct {
		p LauncherProfile
		a AccountProfile
	}{
		"unknown family":     {LauncherProfile{Family: "gemini", Mode: ModeLong}, acct("gemini")},
		"unknown mode":       {LauncherProfile{Family: "claude", Mode: "forever"}, acct("claude-holger")},
		"no mode":            {LauncherProfile{Family: "claude"}, acct("claude-holger")},
		"model with a space": {LauncherProfile{Family: "claude", Mode: ModeLong, Model: "opus --resume"}, acct("claude-holger")},
		"model is a flag":    {LauncherProfile{Family: "codex", Mode: ModeLong, Model: "--resume"}, acct("codex")},
		"effort is a flag":   {LauncherProfile{Family: "codex", Mode: ModeLong, Effort: "-c"}, acct("codex")},
		"no launcher bin":    {LauncherProfile{Family: "claude", Mode: ModeLong}, acct("")},
		"bin with a space":   {LauncherProfile{Family: "claude", Mode: ModeLong}, acct("claude --resume")},
		"bin relative path":  {LauncherProfile{Family: "claude", Mode: ModeLong}, acct("bin/claude")},
	} {
		if argv, err := LaunchArgv(tc.p, tc.a); err == nil {
			t.Errorf("%s: accepted, argv %q", name, argv)
		}
	}
}

// G9: no template, for any family and mode, can carry a resume or continue.
func TestLaunchArgvNeverResumes2058(t *testing.T) {
	for _, fam := range []string{"claude", "glm", "kimi", "codex"} {
		for _, mode := range []string{ModeLong, ModeShort} {
			bin := map[string]string{"claude": "claude-worker", "glm": "claude-glm", "kimi": "claude-kimi", "codex": "codex"}[fam]
			argv, err := LaunchArgv(LauncherProfile{Family: fam, Mode: mode, Model: "m1"}, acct(bin))
			if err != nil {
				t.Fatalf("%s %s: %v", fam, mode, err)
			}
			for _, a := range argv {
				if a == "--resume" || a == "-r" || a == "--continue" || a == "-c" && fam != "codex" || strings.HasPrefix(a, "--resume=") || a == "resume" {
					t.Fatalf("%s %s argv resumes: %q", fam, mode, argv)
				}
			}
		}
	}
}

// The profile comes through one seam: today a host-config fake, the typed
// server field (S1b) later. A worker with no profile has none (the exec path).
func TestConfigProfileSource2058(t *testing.T) {
	cfg := &Config{
		Workers: map[string]WorkerConfig{
			"iris":  {Family: "claude", LaunchVia: LaunchViaOrca, Profile: &LauncherProfile{Family: "claude", Mode: ModeShort, Model: "opus", Account: "holger"}},
			"jonas": {Family: "codex", Launch: []string{"codex"}},
		},
		Accounts: map[string]AccountProfile{"holger": {Bin: "claude-holger"}},
	}
	src := ConfigProfiles{Cfg: cfg}
	p, a, ok, err := src.Profile("iris")
	if err != nil || !ok || p.Model != "opus" || a.Bin != "claude-holger" {
		t.Fatalf("iris: %+v %+v %v %v", p, a, ok, err)
	}
	if _, _, ok, err := src.Profile("jonas"); ok || err != nil {
		t.Fatalf("jonas has no profile: ok=%v err=%v", ok, err)
	}
	cfg.Workers["iris"] = WorkerConfig{Family: "claude", LaunchVia: LaunchViaOrca, Profile: &LauncherProfile{Family: "claude", Mode: ModeShort, Account: "nobody"}}
	if _, _, _, err := src.Profile("iris"); err == nil {
		t.Fatal("an unknown account profile was accepted")
	}
}

// cli#2199: a Claude-family effort is no longer a refusal: the launch runs
// without one and the bind reports NOT_APPLICABLE.
func TestLaunchArgvClaudeEffortIsDropped2199(t *testing.T) {
	got, err := LaunchArgv(LauncherProfile{Family: "claude", Mode: ModeLong, Effort: "high"}, acct("claude-holger"))
	if err != nil || !reflect.DeepEqual(got, []string{"claude-holger", "--dangerously-skip-permissions", PromptArg}) {
		t.Fatalf("argv %q err %v", got, err)
	}
}

// cli#2200 C9: a short-lived run launches the SAME interactive agent as a
// long-lived one (no -p, no exec), so a stalled run is visible and a human can
// take it over. Only the profile's mode differs, never the argv.
func TestLaunchArgvShortIsTheInteractiveAgent2200(t *testing.T) {
	for fam, bin := range map[string]string{"claude": "claude-worker", "glm": "claude-glm", "kimi": "claude-kimi", "codex": "codex"} {
		long, err := LaunchArgv(LauncherProfile{Family: fam, Mode: ModeLong, Model: "m1", Effort: effortFor(fam)}, acct(bin))
		if err != nil {
			t.Fatal(err)
		}
		short, err := LaunchArgv(LauncherProfile{Family: fam, Mode: ModeShort, Model: "m1", Effort: effortFor(fam)}, acct(bin))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(long, short) {
			t.Errorf("%s: short argv %q differs from long %q", fam, short, long)
		}
		for _, a := range short {
			if a == "-p" || a == "exec" {
				t.Errorf("%s: short argv %q still runs headless", fam, short)
			}
		}
	}
}

func effortFor(fam string) string {
	if fam == "codex" {
		return "high"
	}
	return ""
}
