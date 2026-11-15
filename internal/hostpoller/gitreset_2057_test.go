package hostpoller

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hadron-cli#2057 C2b: the workspace reset, on real Git repositories.

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir,
		"-c", "user.name=T", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
		"-c", "protocol.file.allow=always"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// resetRepo builds a bare "origin" with one commit on main and a clone of it
// (origin/HEAD set), the shape of a real worktree.
func resetRepo(t *testing.T) (origin, clone string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := t.TempDir()
	origin = filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")
	clone = filepath.Join(root, "clone")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", origin)
	gitIn(t, root, "init", "-q", "-b", "main", seed)
	if err := os.WriteFile(filepath.Join(seed, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, seed, "add", ".")
	gitIn(t, seed, "commit", "-q", "-m", "one")
	gitIn(t, seed, "remote", "add", "origin", origin)
	gitIn(t, seed, "push", "-q", "origin", "main")
	gitIn(t, root, "clone", "-q", origin, clone)
	return origin, clone
}

// advanceOrigin pushes a new commit to origin/main from a scratch clone.
func advanceOrigin(t *testing.T, origin string) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	if err := os.WriteFile(filepath.Join(other, "b.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-q", "-m", "two")
	gitIn(t, other, "push", "-q", "origin", "main")
	return gitIn(t, other, "rev-parse", "HEAD")
}

var resetNow = time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)

func TestResetWorktreeMovesACleanPushedTreeToTheRemoteDefaultBranch(t *testing.T) {
	origin, clone := resetRepo(t)
	tip := advanceOrigin(t, origin) // the remote moved on since the clone
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset || out.Refused() {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if head := gitIn(t, clone, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("HEAD %s, want the fetched remote tip %s", head, tip)
	}
	if br := gitIn(t, clone, "branch", "--show-current"); br != "" {
		t.Fatalf("the tree must be detached at the base, on branch %q", br)
	}
	if _, err := os.Stat(filepath.Join(clone, "b.txt")); err != nil {
		t.Fatalf("the new base's files must be checked out: %v", err)
	}
}

func TestResetWorktreeIsIdempotentFromADetachedBase(t *testing.T) {
	_, clone := resetRepo(t)
	for i := 0; i < 2; i++ {
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || !out.Reset {
			t.Fatalf("run %d: out=%+v err=%v (a detached HEAD at a remote branch's commit is pushed)", i, out, err)
		}
	}
}

func TestResetWorktreeRefusesDirtyAndUntrackedWithoutMovingAnything(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, clone string){
		"modified tracked file": func(t *testing.T, c string) { _ = os.WriteFile(filepath.Join(c, "a.txt"), []byte("edited\n"), 0o644) },
		"untracked file":        func(t *testing.T, c string) { _ = os.WriteFile(filepath.Join(c, "new.txt"), []byte("x\n"), 0o644) },
	} {
		origin, clone := resetRepo(t)
		advanceOrigin(t, origin)
		before := gitIn(t, clone, "rev-parse", "HEAD")
		mutate(t, clone)
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || out.Code != CodeWorktreeDirty || out.Reset {
			t.Errorf("%s: out=%+v err=%v", name, out, err)
		}
		if after := gitIn(t, clone, "rev-parse", "HEAD"); after != before {
			t.Errorf("%s: a refused reset moved HEAD", name)
		}
	}
}

func TestResetWorktreeRefusesUnpushedCommitsAndKeepsThemAsRescueRefs(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "checkout", "-q", "-b", "work")
	_ = os.WriteFile(filepath.Join(clone, "w.txt"), []byte("w\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "unpushed work")
	work := gitIn(t, clone, "rev-parse", "HEAD")
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeWorktreeUnpushed || out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if head := gitIn(t, clone, "rev-parse", "HEAD"); head != work {
		t.Fatal("a refused reset must leave HEAD where it was")
	}
	ref := "refs/hadron/rescue/20261115T140000Z/heads/work"
	if got := gitIn(t, clone, "rev-parse", ref); got != work {
		t.Fatalf("rescue ref %s = %s, want the unpushed commit %s", ref, got, work)
	}
	if len(out.Rescued) != 1 || !strings.Contains(out.Rescued[0], ref) {
		t.Fatalf("rescued = %v", out.Rescued)
	}
}

// Unpushed work on ANOTHER branch survives the reset as a rescue ref, and the
// branch itself is left alone.
func TestResetWorktreeKeepsAnotherBranchsUnpushedCommitsReachable(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "checkout", "-q", "-b", "side")
	_ = os.WriteFile(filepath.Join(clone, "s.txt"), []byte("s\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "side work")
	side := gitIn(t, clone, "rev-parse", "HEAD")
	gitIn(t, clone, "checkout", "-q", "main") // HEAD is on a pushed branch
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if got := gitIn(t, clone, "rev-parse", "refs/hadron/rescue/20261115T140000Z/heads/side"); got != side {
		t.Fatalf("rescue ref for the side branch = %s, want %s", got, side)
	}
	if got := gitIn(t, clone, "rev-parse", "side"); got != side {
		t.Fatal("the reset must never move or delete a branch")
	}
}

func TestResetWorktreeLeavesAWorkerWithoutGitAlone(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "notes.md"), []byte("n\n"), 0o644)
	out, err := ResetWorktree(context.Background(), dir, resetNow)
	if err != nil || out.Reset || out.Refused() {
		t.Fatalf("out=%+v err=%v: a plain directory has no tree to reset and nothing may be refused", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.md")); err != nil {
		t.Fatal("a plain directory must be untouched")
	}
}

func TestResetWorktreeRefusesATreeWithNoOriginAndAFailedFetch(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "fetch") {
		t.Fatalf("a failed fetch must refuse, not guess: out=%+v err=%v", out, err)
	}
	gitIn(t, clone, "remote", "remove", "origin")
	out, err = ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "origin") {
		t.Fatalf("no origin: out=%+v err=%v", out, err)
	}
}

// Blind P2: a local branch called origin/main must not be taken for the remote
// one: the base is a commit id resolved from the fully qualified remote ref.
func TestResetWorktreeIgnoresALocalBranchNamedLikeTheRemoteOne(t *testing.T) {
	origin, clone := resetRepo(t)
	tip := advanceOrigin(t, origin)
	gitIn(t, clone, "branch", "origin/main", "HEAD") // a decoy: the OLD commit, under the remote's short name
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if head := gitIn(t, clone, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("HEAD %s: the decoy branch was taken for the remote (want %s)", head, tip)
	}
}

// Blind P2: rescue refs never overwrite an earlier rescue, and a branch can be
// named like the synthetic detached entry.
func TestResetWorktreeRescueRefsAreCreateOnlyAndDoNotCollide(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "checkout", "-q", "-b", "work")
	commit := func(name string) string {
		_ = os.WriteFile(filepath.Join(clone, name), []byte(name+"\n"), 0o644)
		gitIn(t, clone, "add", ".")
		gitIn(t, clone, "commit", "-q", "-m", name)
		return gitIn(t, clone, "rev-parse", "HEAD")
	}
	first := commit("one.txt")
	if out, _ := ResetWorktree(context.Background(), clone, resetNow); out.Code != CodeWorktreeUnpushed {
		t.Fatalf("%+v", out)
	}
	second := commit("two.txt") // the branch moved on; the SAME second
	if out, _ := ResetWorktree(context.Background(), clone, resetNow); out.Code != CodeWorktreeUnpushed {
		t.Fatalf("%+v", out)
	}
	base := "refs/hadron/rescue/20261115T140000Z/heads/work"
	if got := gitIn(t, clone, "rev-parse", base); got != first {
		t.Fatalf("the earlier rescue was overwritten: %s, want %s", got, first)
	}
	if got := gitIn(t, clone, "rev-parse", base+"-2"); got != second {
		t.Fatalf("the second rescue = %s, want %s", got, second)
	}
}

func TestResetWorktreeDetachedUnpushedHeadAndABranchNamedDetachedDoNotCollide(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "checkout", "-q", "-b", "detached")
	_ = os.WriteFile(filepath.Join(clone, "d.txt"), []byte("d\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "on the branch")
	onBranch := gitIn(t, clone, "rev-parse", "HEAD")
	gitIn(t, clone, "checkout", "-q", "--detach")
	_ = os.WriteFile(filepath.Join(clone, "e.txt"), []byte("e\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "detached work")
	loose := gitIn(t, clone, "rev-parse", "HEAD")
	out, _ := ResetWorktree(context.Background(), clone, resetNow)
	if out.Code != CodeWorktreeUnpushed {
		t.Fatalf("%+v", out)
	}
	if got := gitIn(t, clone, "rev-parse", "refs/hadron/rescue/20261115T140000Z/heads/detached"); got != onBranch {
		t.Fatalf("branch rescue = %s, want %s", got, onBranch)
	}
	if got := gitIn(t, clone, "rev-parse", "refs/hadron/rescue/20261115T140000Z/detached"); got != loose {
		t.Fatalf("a detached HEAD's commits are not rescued: %s, want %s", got, loose)
	}
}

// Local P2: starting again does not multiply rescue refs for commits an earlier
// rescue already keeps.
func TestResetWorktreeDoesNotRecordTheSameCommitAgain(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "checkout", "-q", "-b", "work")
	_ = os.WriteFile(filepath.Join(clone, "w.txt"), []byte("w\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "work")
	gitIn(t, clone, "checkout", "-q", "main")
	for i := 0; i < 3; i++ {
		out, err := ResetWorktree(context.Background(), clone, resetNow.Add(time.Duration(i)*time.Minute))
		if err != nil || !out.Reset {
			t.Fatalf("run %d: %+v %v", i, out, err)
		}
		if i > 0 && len(out.Rescued) != 0 {
			t.Fatalf("run %d recorded %v again", i, out.Rescued)
		}
	}
	if n := len(strings.Fields(gitIn(t, clone, "for-each-ref", "--format=%(refname)", "refs/hadron/rescue"))); n != 1 {
		t.Fatalf("%d rescue refs for one commit across three starts, want 1", n)
	}
	if got, want := gitIn(t, clone, "rev-parse", "refs/hadron/rescue/20261115T140000Z/heads/work"), gitIn(t, clone, "rev-parse", "work"); got != want {
		t.Fatalf("the rescue ref must exist and point at the unpushed tip: %s, want %s", got, want)
	}
}

// Blind P2: the remote's CURRENT default branch is used; a cached origin/HEAD
// is not refreshed by a fetch and would leave every start on the old branch.
func TestResetWorktreeFollowsTheRemotesCurrentDefaultBranch(t *testing.T) {
	origin, clone := resetRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	gitIn(t, other, "checkout", "-q", "-b", "trunk")
	_ = os.WriteFile(filepath.Join(other, "t.txt"), []byte("t\n"), 0o644)
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-q", "-m", "trunk")
	gitIn(t, other, "push", "-q", "origin", "trunk")
	trunk := gitIn(t, other, "rev-parse", "HEAD")
	gitIn(t, filepath.Dir(origin), "--git-dir", origin, "symbolic-ref", "HEAD", "refs/heads/trunk") // the server's default moved
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if head := gitIn(t, clone, "rev-parse", "HEAD"); head != trunk {
		t.Fatalf("HEAD %s: still on the former default branch (want trunk %s)", head, trunk)
	}
}

// Security (§10): a hook or config line planted in the worker-writable repo
// never runs as the poller.
func TestResetWorktreeRunsNoRepositoryHooks(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin) // so the checkout actually moves HEAD
	hooks := filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "ran")
	hook := "#!/bin/sh\ntouch " + marker + "\n"
	for _, name := range []string{"post-checkout", "reference-transaction", "post-merge"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, clone, "config", "core.hooksPath", hooks)
	gitIn(t, clone, "config", "core.fsmonitor", "touch "+marker+"; true")
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook or fsmonitor command from the worker-writable repository ran as the poller")
	}
}

func TestResetWorktreeNeedsAnOriginRemoteSpecifically(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "remote", "rename", "origin", "upstream")
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "origin") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

// ---- §10: every command-running class of repository config (one test each) ----

func markerScript(t *testing.T, body string) (script, marker string) {
	t.Helper()
	d := t.TempDir()
	marker = filepath.Join(d, "ran")
	script = filepath.Join(d, "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script, marker
}

func mustNotHaveRun(t *testing.T, marker, what string) {
	t.Helper()
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("%s ran as the poller", what)
	}
}

// A checkout filter (smudge) the worker configures and .gitattributes selects.
func TestResetWorktreeRunsNoCheckoutFilter(t *testing.T) {
	origin, clone := resetRepo(t)
	script, marker := markerScript(t, "cat\n")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	_ = os.WriteFile(filepath.Join(other, ".gitattributes"), []byte("*.dat filter=evil\n"), 0o644)
	_ = os.WriteFile(filepath.Join(other, "x.dat"), []byte("payload\n"), 0o644)
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-q", "-m", "filtered file")
	gitIn(t, other, "push", "-q", "origin", "main")
	gitIn(t, clone, "config", "filter.evil.smudge", script)
	gitIn(t, clone, "config", "filter.evil.clean", script)
	gitIn(t, clone, "config", "filter.evil.process", script)
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if _, err := os.Stat(filepath.Join(clone, "x.dat")); err != nil {
		t.Fatal("the matching file must have been materialized for the probe to mean anything")
	}
	mustNotHaveRun(t, marker, "a checkout filter")
}

// remote.<name>.uploadpack names a program the fetch runs on a local origin. Git
// keeps the FIRST value, so a -c cannot replace it: the reset refuses, before
// fetching, and the program never runs.
func TestResetWorktreeRefusesAConfiguredUploadPack(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	before := gitIn(t, clone, "rev-parse", "HEAD")
	script, marker := markerScript(t, "exec git-upload-pack \"$@\"\n")
	for _, key := range []string{"uploadpack", "receivepack", "proxy", "vcs"} {
		gitIn(t, clone, "config", "remote.origin."+key, script)
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "remote.origin."+key) {
			t.Fatalf("%s: out=%+v err=%v", key, out, err)
		}
		gitIn(t, clone, "config", "--unset", "remote.origin."+key)
	}
	if after := gitIn(t, clone, "rev-parse", "HEAD"); after != before {
		t.Fatal("a refused reset moved HEAD")
	}
	mustNotHaveRun(t, marker, "remote.origin.uploadpack")
}

// An ext:: remote runs a command; the transport allowlist refuses it even when
// the repository's config says protocol.ext.allow=always.
func TestResetWorktreeRunsNoExtTransport(t *testing.T) {
	_, clone := resetRepo(t)
	script, marker := markerScript(t, "")
	gitIn(t, clone, "config", "protocol.ext.allow", "always")
	gitIn(t, clone, "remote", "set-url", "origin", "ext::"+script)
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	mustNotHaveRun(t, marker, "an ext:: remote")
}

// core.sshCommand is replaced by plain ssh for the fetch.
func TestResetWorktreeRunsNoConfiguredSSHCommand(t *testing.T) {
	_, clone := resetRepo(t)
	script, marker := markerScript(t, "exit 1\n")
	gitIn(t, clone, "config", "core.sshCommand", script)
	gitIn(t, clone, "remote", "set-url", "origin", "ssh://127.0.0.1:1/none.git")
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	mustNotHaveRun(t, marker, "core.sshCommand")
}

// core.askPass is cleared: it runs even with terminal prompts off.
func TestResetWorktreeRunsNoConfiguredAskPass(t *testing.T) {
	_, clone := resetRepo(t)
	script, marker := markerScript(t, "echo x\n")
	gitIn(t, clone, "config", "core.askPass", script)
	gitIn(t, clone, "config", "credential.helper", "") // none; the askpass is the only prompt source
	gitIn(t, clone, "remote", "set-url", "origin", "https://127.0.0.1:1/none.git")
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil {
		t.Fatal(err)
	}
	_ = out
	mustNotHaveRun(t, marker, "core.askPass")
}

// A credential helper in the repository's own config cannot be neutralised
// without resetting the user's own, so the reset refuses before touching
// anything and the helper never runs.
func TestResetWorktreeRefusesARepositoryCredentialHelper(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	before := gitIn(t, clone, "rev-parse", "HEAD")
	script, marker := markerScript(t, "")
	gitIn(t, clone, "config", "credential.helper", "!"+script)
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "credential helper") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if after := gitIn(t, clone, "rev-parse", "HEAD"); after != before {
		t.Fatal("a refused reset moved HEAD")
	}
	mustNotHaveRun(t, marker, "a repository credential helper")
}

// The poller user's OWN global credential helper is not cleared (a private
// remote's credentials live there): only the repository's config is distrusted.
func TestGitEnvKeepsTheUsersOwnConfig(t *testing.T) {
	for _, kv := range hardenedGit() {
		if strings.HasPrefix(kv, "credential.helper") {
			t.Fatalf("hardenedGit clears credential.helper (%q): it would drop the user's own helper", kv)
		}
	}
	for _, kv := range gitEnv() {
		if strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") || strings.HasPrefix(kv, "GIT_CONFIG_NOSYSTEM=") {
			t.Fatalf("the user's own git config must stay: %q", kv)
		}
	}
}

// A credential prompt cannot be provoked from a local fixture, so the askpass
// neutralisation is pinned structurally: git skips an empty GIT_ASKPASS and
// SSH_ASKPASS and falls to the terminal, which GIT_TERMINAL_PROMPT=0 forbids;
// core.askPass is cleared as well.
func TestGitRunsNeverReachAnAskPassProgram(t *testing.T) {
	env := strings.Join(gitEnv(), "\n")
	for _, want := range []string{"GIT_ASKPASS=\n", "SSH_ASKPASS=\n", "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=file:http:https:ssh:git"} {
		if !strings.Contains(env+"\n", want) {
			t.Errorf("gitEnv lacks %q", want)
		}
	}
	if !strings.Contains(strings.Join(hardenedGit(), " "), "core.askPass=") {
		t.Error("core.askPass is not cleared")
	}
}
