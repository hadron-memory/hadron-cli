package hostfacts

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// cli#3236 (host-file C2): a host's facts are detected when needed, never read from a file.
// These tables run over an injected runner and recorded Orca JSON (measured on orca1,
// Orca 1.4.247, hadron-server#5210); no Orca is needed.

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type recorded struct{ calls [][]string }

func (r *recorded) runner(answers map[string]orcaterm.Result) Runner {
	return func(_ context.Context, argv []string) orcaterm.Result {
		r.calls = append(r.calls, append([]string{}, argv...))
		if a, ok := answers[strings.Join(argv, " ")]; ok {
			return a
		}
		return orcaterm.Result{Code: 1, Stderr: "no such fixture: " + strings.Join(argv, " ")}
	}
}

func newFacts(run Runner) *Facts {
	f := New(run)
	f.GOOS = "linux"
	f.Home = "/home/u"
	f.Getenv = func(string) string { return "" }
	f.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	f.IsExecutable = func(string) bool { return false }
	f.FSType = func(string) (string, error) { return "ext4", nil }
	return f
}

func TestOrcaBinSearchOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		goos  string
		path  map[string]string // name -> found path
		exec  map[string]bool
		want  string
		found bool
	}{
		{"on PATH", "linux", map[string]string{"orca": "/usr/local/bin/orca"}, nil, "/usr/local/bin/orca", true},
		{"orca's own cli dir on Linux", "linux", nil, map[string]bool{"/home/u/.config/orca/cli/bin/orca": true}, "/home/u/.config/orca/cli/bin/orca", true},
		{"PATH wins over the cli dir", "linux", map[string]string{"orca": "/opt/orca"}, map[string]bool{"/home/u/.config/orca/cli/bin/orca": true}, "/opt/orca", true},
		{"macOS app bundle", "darwin", nil, map[string]bool{"/Applications/Orca.app/Contents/Resources/bin/orca": true}, "/Applications/Orca.app/Contents/Resources/bin/orca", true},
		{"nowhere", "linux", nil, nil, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFacts(nil)
			f.GOOS = tc.goos
			f.LookPath = func(n string) (string, error) {
				if p, ok := tc.path[n]; ok {
					return p, nil
				}
				return "", os.ErrNotExist
			}
			f.IsExecutable = func(p string) bool { return tc.exec[p] }
			got, miss := f.OrcaBin()
			if tc.found != (miss == nil) || got != tc.want {
				t.Fatalf("OrcaBin() = %q, %v; want %q found=%v", got, miss, tc.want, tc.found)
			}
			if !tc.found {
				if !miss.Unreachable || miss.Fact != "orca" ||
					!strings.Contains(miss.Reason, "Orca") || !strings.Contains(miss.Fix, "install Orca") || !strings.Contains(miss.Fix, "PATH") {
					t.Errorf("a missing Orca names Orca, the install step and PATH, and reads as unreachable: %+v", miss)
				}
			}
		})
	}
}

func TestOrcaVersion(t *testing.T) {
	var rec recorded
	f := newFacts(rec.runner(map[string]orcaterm.Result{"--version": {Stdout: "1.4.247\n"}}))
	if v, miss := f.OrcaVersion(context.Background()); miss != nil || v != "1.4.247" {
		t.Fatalf("OrcaVersion() = %q, %v", v, miss)
	}
	for name, res := range map[string]orcaterm.Result{
		"non-zero exit": {Code: 1, Stderr: "boom"},
		"empty answer":  {Stdout: "  \n"},
	} {
		f := newFacts((&recorded{}).runner(map[string]orcaterm.Result{"--version": res}))
		if _, miss := f.OrcaVersion(context.Background()); miss == nil || !miss.Unreachable || miss.Fix == "" {
			t.Errorf("%s: want an unreachable Missing with a fix, got %+v", name, miss)
		}
	}
}

func TestReposAndWorktreesParseOrcaJSON(t *testing.T) {
	var rec recorded
	f := newFacts(rec.runner(map[string]orcaterm.Result{
		"repo list --json":                  {Stdout: fixture(t, "orca_repo_list.linux.json")},
		"worktree list --json --limit 5000": {Stdout: fixture(t, "orca_worktree_list.linux.json")},
	}))
	repos, miss := f.Repos(context.Background())
	if miss != nil || len(repos) != 3 || repos[0].ID != "repo-hs" || repos[0].DisplayName != "hadron-server" || repos[2].Kind != "folder" {
		t.Fatalf("Repos() = %+v, %v", repos, miss)
	}
	wts, miss := f.Worktrees(context.Background())
	if miss != nil || len(wts) != 7 || wts[5].Archived != true || wts[1].RepoID != "repo-hs" {
		t.Fatalf("Worktrees() = %+v, %v", wts, miss)
	}
	// Orca returns 200 rows by default and says truncated:true past that, so the call names a limit.
	if !reflect.DeepEqual(rec.calls[1], []string{"worktree", "list", "--json", "--limit", "5000"}) {
		t.Errorf("the worktree read must ask for every row: %v", rec.calls[1])
	}
}

func TestFailClosedOnABadOrIncompleteOrcaAnswer(t *testing.T) {
	trunc := strings.Replace(fixture(t, "orca_worktree_list.linux.json"), `"truncated":false`, `"truncated":true`, 1)
	for name, tc := range map[string]struct {
		argv string
		res  orcaterm.Result
	}{
		"repos not ok":             {"repo list --json", orcaterm.Result{Stdout: `{"ok":false,"error":{"message":"x"}}`}},
		"repos not json":           {"repo list --json", orcaterm.Result{Stdout: "<html>"}},
		"repos missing the list":   {"repo list --json", orcaterm.Result{Stdout: `{"ok":true,"result":{}}`}},
		"repos non-zero":           {"repo list --json", orcaterm.Result{Code: 2}},
		"worktrees truncated":      {"worktree list --json --limit 5000", orcaterm.Result{Stdout: trunc}},
		"worktrees not ok":         {"worktree list --json --limit 5000", orcaterm.Result{Stdout: `{"ok":false}`}},
		"worktrees missing a list": {"worktree list --json --limit 5000", orcaterm.Result{Stdout: `{"ok":true,"result":{"totalCount":0,"truncated":false}}`}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFacts((&recorded{}).runner(map[string]orcaterm.Result{tc.argv: tc.res}))
			var miss *Missing
			if strings.HasPrefix(tc.argv, "repo") {
				_, miss = f.Repos(context.Background())
			} else {
				_, miss = f.Worktrees(context.Background())
			}
			if miss == nil || !miss.Unreachable || miss.Fix == "" {
				t.Fatalf("a bad or incomplete answer must read as unreachable with a fix, never as an empty list: %+v", miss)
			}
		})
	}
}

// The runner is closed over the three reads. Nothing else of Orca's, never an account, terminal or send call.
func TestGuardAllowsOnlyTheThreeReads(t *testing.T) {
	var rec recorded
	g := Guard(rec.runner(map[string]orcaterm.Result{}))
	for _, ok := range [][]string{{"--version"}, {"repo", "list", "--json"}, {"worktree", "list", "--json", "--limit", "5000"}} {
		before := len(rec.calls)
		g(context.Background(), ok)
		if len(rec.calls) != before+1 {
			t.Errorf("%v must pass the guard", ok)
		}
	}
	for _, bad := range [][]string{
		{"terminal", "list", "--json"}, {"account", "list", "--json"}, {"terminal", "send"}, {"repo", "add"},
		{"worktree", "list", "--json"}, {"worktree", "list", "--json", "--limit", "x"}, {"worktree", "list", "--json", "--limit", "5000", "--repo", "x"},
		{"repo", "list"}, {}, {"--version", "--json"},
	} {
		before := len(rec.calls)
		res := g(context.Background(), bad)
		if len(rec.calls) != before || res.Code == 0 {
			t.Errorf("%v must be refused without running", bad)
		}
	}
}

func TestScrubbedEnvKeepsWhatOrcaNeedsAndNothingOfHadron(t *testing.T) {
	got := ScrubbedEnv([]string{"PATH=/bin", "HOME=/home/u", "HADRON_TOKEN=sekrit", "HADRON_HOST_KEY=k", "ANTHROPIC_API_KEY=a", "XDG_CONFIG_HOME=/c", "USER=u", "OPENAI_API_KEY=o"})
	joined := strings.Join(got, "\n")
	for _, keep := range []string{"PATH=/bin", "HOME=/home/u", "XDG_CONFIG_HOME=/c", "USER=u"} {
		if !strings.Contains(joined, keep) {
			t.Errorf("%s must be kept: %v", keep, got)
		}
	}
	for _, drop := range []string{"HADRON_TOKEN", "HADRON_HOST_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "sekrit"} {
		if strings.Contains(joined, drop) {
			t.Errorf("%s must not reach Orca: %v", drop, got)
		}
	}
}

func TestAgentProgramPerFamily(t *testing.T) {
	f := newFacts(nil)
	onPath := map[string]string{"claude": "/bin/claude", "codex": "/bin/codex", "claude-glm": "/bin/claude-glm"}
	f.LookPath = func(n string) (string, error) {
		if p, ok := onPath[n]; ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}
	for family, want := range map[string]string{"claude": "/bin/claude", "codex": "/bin/codex", "glm": "/bin/claude-glm", "anthropic": "/bin/claude", "openai": "/bin/codex"} {
		if got, miss := f.AgentProgram(family); miss != nil || got != want {
			t.Errorf("AgentProgram(%s) = %q, %v; want %q", family, got, miss, want)
		}
	}
	// kimi's program is claude-kimi, not on PATH: the Missing names the program and the accounts-file key that overrides it.
	_, miss := f.AgentProgram("kimi")
	if miss == nil || !strings.Contains(miss.Reason, "claude-kimi") || !strings.Contains(miss.Fix, "program") || miss.Unreachable {
		t.Errorf("a missing program names it and the accounts-file `program` key: %+v", miss)
	}
	// An unknown family has no default program.
	_, miss = f.AgentProgram("llama")
	if miss == nil || !strings.Contains(miss.Reason, "llama") || !strings.Contains(miss.Fix, "program") {
		t.Errorf("an unknown family is refused naming it: %+v", miss)
	}
	if DefaultProgramName("glm") != "claude-glm" || DefaultProgramName("kimi") != "claude-kimi" || DefaultProgramName("claude") != "claude" || DefaultProgramName("codex") != "codex" || DefaultProgramName("llama") != "" {
		t.Error("the default program names are claude, codex, claude-glm and claude-kimi (hadron-server#5210)")
	}
}

func TestStateDirOnDisk(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    map[string]string
		fstype string
		want   string
		ok     bool
	}{
		{"XDG_STATE_HOME on ext4", map[string]string{"XDG_STATE_HOME": "/var/state"}, "ext4", "/var/state/hadron", true},
		{"default under HOME", nil, "ext4", "/home/u/.local/state/hadron", true},
		{"tmpfs is not durable", nil, "tmpfs", "/home/u/.local/state/hadron", false},
		{"ramfs is not durable", nil, "ramfs", "/home/u/.local/state/hadron", false},
		{"unknown filesystem is found", nil, "", "/home/u/.local/state/hadron", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFacts(nil)
			f.Getenv = func(k string) string { return tc.env[k] }
			f.FSType = func(string) (string, error) { return tc.fstype, nil }
			got, miss := f.StateDirOnDisk()
			if got != tc.want || tc.ok != (miss == nil) {
				t.Fatalf("StateDirOnDisk() = %q, %+v; want %q ok=%v", got, miss, tc.want, tc.ok)
			}
			if miss != nil && (miss.Unreachable || !strings.Contains(miss.Fix, "XDG_STATE_HOME")) {
				t.Errorf("a tmpfs state dir names XDG_STATE_HOME as the fix and is not an Orca problem: %+v", miss)
			}
		})
	}
}

func TestResolveWorkdir(t *testing.T) {
	var rec recorded
	f := newFacts(rec.runner(map[string]orcaterm.Result{
		"repo list --json":                  {Stdout: fixture(t, "orca_repo_list.linux.json")},
		"worktree list --json --limit 5000": {Stdout: fixture(t, "orca_worktree_list.linux.json")},
	}))
	repos, _ := f.Repos(context.Background())
	wts, _ := f.Worktrees(context.Background())
	for _, tc := range []struct {
		token  string
		path   string
		cands  []string
		result Resolution
	}{
		{"hadron-server/antonio", "/home/u/orca/workspaces/hadron-server/antonio", nil, Found},
		{"hadron-cli/judy", "/home/u/orca/workspaces/hadron-cli/judy", nil, Found},
		{"worker/notes", "/home/u/ws/notes", nil, Found},
		{"hadron-server/wt", "", []string{"/home/u/orca/workspaces/hadron-server/wt", "/home/u/scratch/qa/wt"}, Ambiguous},
		{"hadron-cli/old", "", nil, NotOnHost},
		{"hadron-server/nobody", "", nil, NotOnHost},
		{"Antonio [Server Engineer, Sonnet]", "", nil, NotOnHost},
		{"hadron-server", "", nil, NotOnHost},
	} {
		path, cands, res := ResolveWorkdir(tc.token, repos, wts)
		if res != tc.result || path != tc.path || !reflect.DeepEqual(cands, tc.cands) {
			t.Errorf("ResolveWorkdir(%q) = %q, %v, %v; want %q, %v, %v", tc.token, path, cands, res, tc.path, tc.cands, tc.result)
		}
	}
}

// macOS: where Orca's CLI lives is covered by the search-order table above; the measured Orca JSON for macOS
// needs a Mac (hadron-server#5210 U5, Holger's Mac steps), so the recorded-JSON cases stay pending.
func TestMacOSRecordedOrcaJSONIsPending(t *testing.T) {
	t.Skip("pending: macOS Orca JSON fixtures need a Mac (hadron-server#5210 U5)")
}
