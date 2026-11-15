package hostpoller

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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

// Production allows only https, ssh and user@host: remotes; the tests' local
// fixtures are a test-only seam, switched OFF here.
func TestResetWorktreeRefusesAnythingButAnHTTPSOrSSHRemoteInProduction(t *testing.T) {
	localRemotesForTests = false
	defer func() { localRemotesForTests = true }()
	for url, ok := range map[string]bool{
		"https://github.com/hadron-memory/hadron-cli.git": true,
		"ssh://git@github.com/hadron-memory/x.git":        true,
		"git@github.com:hadron-memory/hadron-cli.git":     true,
		"http://github.com/x.git":                         false,
		"git://github.com/x.git":                          false,
		"file:///srv/repo.git":                            false,
		"/srv/repo.git":                                   false,
		"ext::sh -c touch /tmp/x":                         false,
		"ftp://example.com/x.git":                         false,
		"../relative.git":                                 false,
		"":                                                false,
	} {
		if got := resetURLOK(url); got != ok {
			t.Errorf("resetURLOK(%q) = %v, want %v", url, got, ok)
		}
	}
	_, clone := resetRepo(t)
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "not an https://") {
		t.Fatalf("a local-path origin must be refused in production: out=%+v err=%v", out, err)
	}
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
	for _, want := range []string{"GIT_ASKPASS=\n", "SSH_ASKPASS=\n", "GIT_TERMINAL_PROMPT=0", "GIT_ALLOW_PROTOCOL=file:https:ssh"} {
		if !strings.Contains(env+"\n", want) {
			t.Errorf("gitEnv lacks %q", want)
		}
	}
	if !strings.Contains(strings.Join(hardenedGit(), " "), "core.askPass=") {
		t.Error("core.askPass is not cleared")
	}
}

// The production transport allowlist is exactly https and ssh (the tests' local
// path is a seam switched off here), and submodule recursion is off.
func TestGitRunsAllowOnlyTheTeamTransportsInProduction(t *testing.T) {
	localRemotesForTests = false
	defer func() { localRemotesForTests = true }()
	var allow string
	for _, kv := range gitEnv() {
		if v, ok := strings.CutPrefix(kv, "GIT_ALLOW_PROTOCOL="); ok {
			allow = v
		}
	}
	if allow != "https:ssh" {
		t.Fatalf("GIT_ALLOW_PROTOCOL = %q, want exactly https:ssh", allow)
	}
	cfg := strings.Join(hardenedGit(), " ")
	for _, want := range []string{"protocol.allow=never", "protocol.https.allow=always", "protocol.ssh.allow=always", "fetch.recurseSubmodules=false", "submodule.recurse=false"} {
		if !strings.Contains(cfg, want) {
			t.Errorf("hardenedGit lacks %q", want)
		}
	}
	if strings.Contains(cfg, "protocol.file.allow") {
		t.Error("the local-path transport leaked into production")
	}
}

// filterRepo advances origin with a .gitattributes selecting filter=evil for *.dat
// and a matching file, so a checkout of the new tip materializes it through the
// filter, and returns the filter script and its marker.
func filterRepo(t *testing.T) (origin, clone, script, marker string) {
	t.Helper()
	origin, clone = resetRepo(t)
	script, marker = markerScript(t, "cat\n")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	_ = os.WriteFile(filepath.Join(other, ".gitattributes"), []byte("*.dat filter=evil\n"), 0o644)
	_ = os.WriteFile(filepath.Join(other, "x.dat"), []byte("payload\n"), 0o644)
	gitIn(t, other, "add", ".")
	gitIn(t, other, "commit", "-q", "-m", "filtered file")
	gitIn(t, other, "push", "-q", "origin", "main")
	return origin, clone, script, marker
}

// A config that cannot be read cannot be checked, so the reset fails closed:
// an include with a syntax error is a refusal, never "no overrides".
func TestResetWorktreeFailsClosedWhenTheRepositoryConfigCannotBeRead(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	before := gitIn(t, clone, "rev-parse", "HEAD")
	bad := filepath.Join(t.TempDir(), "bad.cfg")
	_ = os.WriteFile(bad, []byte("[filter \"evil\"\n\tsmudge = oops\n"), 0o644)
	gitIn(t, clone, "config", "include.path", bad)
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err == nil && (out.Reset || out.Code == "") {
		t.Fatalf("an unreadable config was taken for a clean one: out=%+v", out)
	}
	if after, e := exec.Command("git", "-C", clone, "-c", "include.path=", "rev-parse", "HEAD").Output(); e == nil && strings.TrimSpace(string(after)) != before {
		t.Fatal("a refused reset moved HEAD")
	}
}

// ---- §10: the repository config ALLOWLIST (Wolfgang #108206) ----

// Every key outside the small set of inert ones refuses the reset before a
// single command runs on the tree: nothing is chased by name. One row per class
// of key that can run something or redirect the fetch.
func TestResetWorktreeRefusesAnyRepositoryConfigKeyOutsideTheAllowlist(t *testing.T) {
	script, marker := markerScript(t, "")
	harmless := filepath.Join(t.TempDir(), "harmless.cfg")
	_ = os.WriteFile(harmless, []byte("[user]\n\tname = x\n"), 0o644)
	hooks := t.TempDir()
	_ = os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	for name, kv := range map[string][2]string{
		"hooks path":           {"core.hooksPath", hooks},
		"fsmonitor":            {"core.fsmonitor", "touch " + marker + "; true"},
		"ssh command":          {"core.sshCommand", script},
		"askpass":              {"core.askPass", script},
		"git proxy":            {"core.gitProxy", script},
		"pager":                {"core.pager", script},
		"filter smudge":        {"filter.evil.smudge", script},
		"filter clean":         {"filter.evil.clean", script},
		"filter process":       {"filter.evil.process", script},
		"filter required":      {"filter.evil.required", "true"},
		"diff textconv":        {"diff.evil.textconv", script},
		"merge driver":         {"merge.evil.driver", script},
		"upload pack":          {"remote.origin.uploadpack", script},
		"receive pack":         {"remote.origin.receivepack", script},
		"remote proxy":         {"remote.origin.proxy", "http://evil.example:3128"},
		"remote vcs":           {"remote.origin.vcs", "evil"},
		"credential helper":    {"credential.helper", "!" + script},
		"url rewrite":          {"url.https://evil.example/.insteadOf", "https://github.com/"},
		"push url rewrite":     {"url.https://evil.example/.pushInsteadOf", "https://github.com/"},
		"http proxy":           {"http.proxy", "http://evil.example:3128"},
		"scoped http proxy":    {"http.https://github.com/.proxy", "http://evil.example:3128"},
		"tls verification off": {"http.sslVerify", "false"},
		"tls verification on":  {"http.sslVerify", "true"}, // not on the list either: http.* is refused whole
		"ext transport":        {"protocol.ext.allow", "always"},
		"alias":                {"alias.co", "!" + script},
		"include":              {"include.path", harmless},
		"conditional include":  {"includeIf.gitdir:**.path", harmless},
		"submodule":            {"submodule.recurse", "true"},
		"sparse checkout":      {"core.sparseCheckout", "true"},
		"attributes file":      {"core.attributesFile", script},
		"gc":                   {"gc.auto", "1"},
		"fetch":                {"fetch.recurseSubmodules", "true"},
	} {
		origin, clone := resetRepo(t)
		advanceOrigin(t, origin)
		before := gitIn(t, clone, "rev-parse", "HEAD")
		gitIn(t, clone, "config", kv[0], kv[1])
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || out.Code != CodeResetFailed || out.Reset {
			t.Errorf("%s (%s): out=%+v err=%v", name, kv[0], out, err)
			continue
		}
		if !strings.Contains(out.Detail, "allowlist") || !strings.Contains(strings.ToLower(out.Detail), strings.ToLower(strings.SplitN(kv[0], ".", 2)[0])) {
			t.Errorf("%s: the refusal must name the key: %q", name, out.Detail)
		}
		if after := gitIn(t, clone, "rev-parse", "HEAD"); after != before {
			t.Errorf("%s: a refused reset moved HEAD", name)
		}
		mustNotHaveRun(t, marker, name)
	}
}

// A filter the checkout would actually exercise: the matching file is in the
// fetched commit. The reset refuses before it runs a single command, so the
// file is never materialized and the filter never runs; same through an include.
func TestResetWorktreeNeverReachesACheckoutFilter(t *testing.T) {
	for name, wire := range map[string]func(t *testing.T, clone, script string){
		"direct": func(t *testing.T, clone, script string) {
			gitIn(t, clone, "config", "filter.evil.smudge", script)
			gitIn(t, clone, "config", "filter.evil.clean", script)
		},
		"through include.path": func(t *testing.T, clone, script string) {
			inc := filepath.Join(t.TempDir(), "included.cfg")
			_ = os.WriteFile(inc, []byte("[filter \"evil\"]\n\tsmudge = "+script+"\n"), 0o644)
			gitIn(t, clone, "config", "include.path", inc)
		},
	} {
		_, clone, script, marker := filterRepo(t)
		wire(t, clone, script)
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || out.Code != CodeResetFailed {
			t.Fatalf("%s: out=%+v err=%v", name, out, err)
		}
		if _, err := os.Stat(filepath.Join(clone, "x.dat")); err == nil {
			t.Fatalf("%s: the filtered file was materialized", name)
		}
		mustNotHaveRun(t, marker, "a checkout filter ("+name+")")
	}
}

// What a real clone carries is trusted: the reset must not refuse every repo.
func TestResetWorktreeAcceptsTheConfigRealClonesCarry(t *testing.T) {
	_, clone := resetRepo(t)
	for k, v := range map[string]string{
		"branch.feature.base":       "main",
		"branch.feature.pushremote": "origin",
		"branch.feature.remote":     "origin",
		"branch.feature.merge":      "refs/heads/feature",
		"push.autosetupremote":      "true",
		"remote.pushdefault":        "origin",
		"remote.upstream.url":       "https://github.com/other/repo.git",
		"remote.upstream.fetch":     "+refs/heads/*:refs/remotes/upstream/*",
		"user.name":                 "Someone",
		"user.email":                "someone@example.com",
		"core.symlinks":             "true",
		"core.ignorecase":           "false",
		"core.precomposeunicode":    "true",
	} {
		gitIn(t, clone, "config", k, v)
	}
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

// config.worktree (extensions.worktreeConfig) is a worker-writable scope git
// applies: the same allowlist covers it. Without the extension there is no such
// scope, and a plain linked worktree must not be refused.
func TestResetWorktreeChecksTheWorktreeConfigScopeToo(t *testing.T) {
	_, clone := resetRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	gitIn(t, clone, "worktree", "add", "-q", "--detach", linked)
	if out, err := ResetWorktree(context.Background(), linked, resetNow); err != nil || !out.Reset {
		t.Fatalf("a linked worktree with no worktreeConfig extension must reset: out=%+v err=%v", out, err)
	}
	gitIn(t, clone, "config", "extensions.worktreeConfig", "true")
	if out, err := ResetWorktree(context.Background(), linked, resetNow); err != nil || !out.Reset {
		t.Fatalf("the extension on and no config.worktree is an empty scope: out=%+v err=%v", out, err)
	}
	script, marker := markerScript(t, "cat\n")
	gitIn(t, linked, "config", "--worktree", "filter.evil.smudge", script)
	out, err := ResetWorktree(context.Background(), linked, resetNow)
	if err != nil || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "filter.evil.smudge") {
		t.Fatalf("a filter in config.worktree must refuse: out=%+v err=%v", out, err)
	}
	mustNotHaveRun(t, marker, "a filter in config.worktree")
}

// Chip's QA intake on ab8925f2 and Wolfgang's rule: the host's OTHER git calls
// go through the same gate. Inspect (the pre-reset snapshot) reports the
// untrusted config without running `git status`; worktree.create and any other
// command are refused likewise.
func TestExecHostGitCallsRefuseAnUntrustedRepositoryConfig(t *testing.T) {
	_, clone, script, marker := filterRepo(t)
	gitIn(t, clone, "pull", "-q", "--ff-only", "origin", "main") // x.dat is tracked and filtered
	gitIn(t, clone, "config", "filter.evil.clean", script)
	gitIn(t, clone, "config", "filter.evil.smudge", script)
	_ = os.Chtimes(filepath.Join(clone, "x.dat"), time.Now().Add(time.Hour), time.Now().Add(time.Hour)) // stat-dirty: status would re-hash it

	_, err := (ExecHost{}).Inspect(context.Background(), clone)
	var untrusted *UntrustedConfigError
	if !errors.As(err, &untrusted) || !strings.Contains(untrusted.Why, "filter.evil.clean") {
		t.Fatalf("Inspect must report the untrusted config, not 'not a worktree': %v", err)
	}
	if err := (ExecHost{}).CreateWorktree(context.Background(), clone, filepath.Join(t.TempDir(), "wt"), "work/new", "main"); err == nil {
		t.Fatal("worktree.create ran in a repository with an untrusted config")
	}
	if _, err := git(context.Background(), clone, "status", "--porcelain"); !errors.As(err, &untrusted) {
		t.Fatalf("git() must refuse every command: %v", err)
	}
	mustNotHaveRun(t, marker, "a filter during the host's git calls")
}

// The host's git calls are allowed on a clean config: the same real clone keys.
func TestExecHostGitCallsRunOnATrustedConfig(t *testing.T) {
	_, clone := resetRepo(t)
	if st, err := (ExecHost{}).Inspect(context.Background(), clone); err != nil || !st.IsWorktree {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

// §10 (Rook, df95c30b): remote.origin.fetch decides which remote ref lands on
// refs/remotes/origin/main, the commit the reset checks out. Only the standard
// mapping is inert; a refspec that maps another branch there is refused, not
// followed (the probe checked out 'evil' while reporting origin/main).
func TestResetWorktreeRefusesANonStandardFetchRefspec(t *testing.T) {
	for refspec, ok := range map[string]bool{
		"+refs/heads/*:refs/remotes/origin/*":        true,
		"refs/heads/*:refs/remotes/origin/*":         true,
		"+refs/heads/main:refs/remotes/origin/main":  true,
		"+refs/heads/evil:refs/remotes/origin/main":  false,
		"+refs/heads/*:refs/remotes/upstream/*":      false,
		"+refs/heads/*:refs/remotes/origin/main":     false,
		"+refs/*:refs/*":                             false,
		"+refs/pull/*/head:refs/remotes/origin/pr/*": false,
		"+refs/heads/evil":                           false,
		"+refs/heads/*:refs/heads/*":                 false,
	} {
		origin, clone := resetRepo(t)
		advanceOrigin(t, origin)
		gitIn(t, clone, "config", "--replace-all", "remote.origin.fetch", refspec)
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil {
			t.Fatalf("%s: %v", refspec, err)
		}
		if ok && !out.Reset {
			t.Errorf("%q must be accepted: %+v", refspec, out)
		}
		if !ok && (out.Reset || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "remote.origin.fetch")) {
			t.Errorf("%q must be refused naming the key: %+v", refspec, out)
		}
	}
}

// The fetched default branch must be the tip the remote reports. A tracking ref
// that points elsewhere (here moved right after the fetch) is never checked out.
func TestResetWorktreeRefusesAFetchedTipThatIsNotWhatTheRemoteReports(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	old := gitIn(t, clone, "rev-parse", "HEAD")
	resetAfterFetch = func() { gitIn(t, clone, "update-ref", "refs/remotes/origin/main", old) }
	defer func() { resetAfterFetch = nil }()
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Reset || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "not the tip") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if head := gitIn(t, clone, "rev-parse", "HEAD"); head != old {
		t.Fatal("a refused reset moved HEAD")
	}
}

// §10 note: an unreadable worktree-scope config fails closed.
func TestResetWorktreeFailsClosedOnAnUnreadableWorktreeScope(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "config", "extensions.worktreeConfig", "true")
	_ = os.WriteFile(filepath.Join(clone, ".git", "config.worktree"), []byte("[filter \"evil\"\n"), 0o644)
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Reset || out.Code != CodeResetFailed {
		t.Fatalf("an unreadable config.worktree must refuse: out=%+v err=%v", out, err)
	}
}

// Jamir's P1 as far as it can be closed: a filter planted AFTER the first config
// read (during the fetch, or just before the checkout) is caught by the re-reads
// right before the commands that would run it. Deterministic: the seams plant it.
func TestResetWorktreeRechecksTheConfigBeforeTheCommandsThatMatter(t *testing.T) {
	for name, set := range map[string]func(fn func()){
		"after the fetch":     func(fn func()) { resetAfterFetch = fn },
		"before the checkout": func(fn func()) { resetBeforeCheckout = fn },
	} {
		_, clone, script, marker := filterRepo(t)
		before := gitIn(t, clone, "rev-parse", "HEAD")
		set(func() {
			gitIn(t, clone, "config", "filter.evil.smudge", script)
			gitIn(t, clone, "config", "filter.evil.clean", script)
		})
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		resetAfterFetch, resetBeforeCheckout = nil, nil
		if err != nil || out.Reset || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "filter.evil") {
			t.Fatalf("%s: out=%+v err=%v", name, out, err)
		}
		if after := gitIn(t, clone, "rev-parse", "HEAD"); after != before {
			t.Errorf("%s: HEAD moved", name)
		}
		if _, err := os.Stat(filepath.Join(clone, "x.dat")); err == nil {
			t.Errorf("%s: the filtered file was materialized", name)
		}
		mustNotHaveRun(t, marker, "a filter planted "+name)
	}
}

// ---- Julius's LOCAL P2 (0c5974b8): a plain directory INSIDE a repository ----

// A worker whose directory is a plain (non-Git) child of some enclosing
// repository has no tree of its own: git says it is "inside a work tree", but a
// reset there would move the ENCLOSING repository. Such a worker is a worker
// without Git: no reset, nothing refused, and the enclosing repository is left
// exactly as it was, whatever state or config it is in.
func TestResetWorktreeNeverTouchesTheRepositoryEnclosingAPlainChild(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, origin, clone string){
		"clean and pushed": func(t *testing.T, origin, clone string) { advanceOrigin(t, origin) },
		"dirty": func(t *testing.T, origin, clone string) {
			_ = os.WriteFile(filepath.Join(clone, "a.txt"), []byte("edited\n"), 0o644)
		},
		"unpushed commits": func(t *testing.T, origin, clone string) {
			gitIn(t, clone, "checkout", "-q", "-b", "work")
			_ = os.WriteFile(filepath.Join(clone, "w.txt"), []byte("w\n"), 0o644)
			gitIn(t, clone, "add", ".")
			gitIn(t, clone, "commit", "-q", "-m", "work")
		},
		"untrusted config": func(t *testing.T, origin, clone string) {
			gitIn(t, clone, "config", "core.hooksPath", "/nowhere")
			gitIn(t, clone, "config", "filter.evil.smudge", "false")
		},
	} {
		origin, clone := resetRepo(t)
		setup(t, origin, clone)
		child := filepath.Join(clone, "agents", "notes") // a plain directory inside the repository (untracked, no .git of its own)
		if err := os.MkdirAll(child, 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(child, "n.md"), []byte("n\n"), 0o644)
		headBefore := gitIn(t, clone, "rev-parse", "HEAD")
		branchBefore := gitIn(t, clone, "branch", "--show-current")
		statusBefore := gitIn(t, clone, "status", "--porcelain")
		out, err := ResetWorktree(context.Background(), child, resetNow)
		if err != nil || out.Reset || out.Refused() {
			t.Errorf("%s: a plain child of a repository is a worker without Git: out=%+v err=%v", name, out, err)
		}
		if got := gitIn(t, clone, "rev-parse", "HEAD"); got != headBefore {
			t.Errorf("%s: the ENCLOSING repository's HEAD moved", name)
		}
		if got := gitIn(t, clone, "branch", "--show-current"); got != branchBefore {
			t.Errorf("%s: the enclosing repository changed branch (%q -> %q)", name, branchBefore, got)
		}
		if got := gitIn(t, clone, "status", "--porcelain"); got != statusBefore {
			t.Errorf("%s: the enclosing repository's working tree changed", name)
		}
		if refs := gitIn(t, clone, "for-each-ref", "refs/hadron/rescue"); refs != "" {
			t.Errorf("%s: rescue refs were written in the enclosing repository: %q", name, refs)
		}
		if _, err := os.Stat(filepath.Join(child, "n.md")); err != nil {
			t.Errorf("%s: the child's own file was touched", name)
		}
	}
}

// A real subdirectory of a work tree is not the root either.
func TestResetWorktreeLeavesASubdirectoryOfAWorkTreeAlone(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	sub := filepath.Join(clone, "sub")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(sub, "f.txt"), []byte("f\n"), 0o644)
	gitIn(t, clone, "add", ".")
	gitIn(t, clone, "commit", "-q", "-m", "sub")
	head := gitIn(t, clone, "rev-parse", "HEAD")
	out, err := ResetWorktree(context.Background(), sub, resetNow)
	if err != nil || out.Reset || out.Refused() {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if got := gitIn(t, clone, "rev-parse", "HEAD"); got != head {
		t.Fatal("the enclosing repository moved")
	}
}

// The root itself still resets, also when reached through a symlink.
func TestResetWorktreeStillResetsTheRootReachedThroughASymlink(t *testing.T) {
	origin, clone := resetRepo(t)
	tip := advanceOrigin(t, origin)
	link := filepath.Join(t.TempDir(), "via-link")
	if err := os.Symlink(clone, link); err != nil {
		t.Skip("no symlinks")
	}
	out, err := ResetWorktree(context.Background(), link, resetNow)
	if err != nil || !out.Reset {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if got := gitIn(t, clone, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("HEAD %s, want %s", got, tip)
	}
}

// The host's other git calls follow the same rule: a plain child of a repository
// is not a worktree, and the enclosing repository's config is not its business.
func TestExecHostInspectOfAPlainChildOfARepositoryIsNotAWorktreeAndNotRefused(t *testing.T) {
	_, clone := resetRepo(t)
	gitIn(t, clone, "config", "filter.evil.smudge", "false")
	child := filepath.Join(clone, "plain")
	_ = os.MkdirAll(child, 0o755)
	st, err := (ExecHost{}).Inspect(context.Background(), child)
	if err != nil || !st.Exists || st.IsWorktree {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

// §10 (Rook, a4e836e8): the root is decided by the directory's own .git entry, not
// by `rev-parse --show-toplevel`, which honours the repository's core.worktree. A
// dirty root whose config redirects its work tree elsewhere used to read as "not a
// root": silently not reset, not refused. It is a root with a config outside the
// allowlist, so it is refused, naming the key, and nothing moves.
func TestResetWorktreeRefusesARootWhoseConfigRedirectsItsWorkTree(t *testing.T) {
	origin, clone := resetRepo(t)
	advanceOrigin(t, origin)
	_ = os.WriteFile(filepath.Join(clone, "a.txt"), []byte("dirty\n"), 0o644)
	before := gitIn(t, clone, "rev-parse", "HEAD")
	gitIn(t, clone, "config", "core.worktree", t.TempDir())
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Reset || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "core.worktree") {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if got, e := exec.Command("git", "--git-dir", filepath.Join(clone, ".git"), "rev-parse", "HEAD").Output(); e != nil || strings.TrimSpace(string(got)) != before {
		t.Fatal("a refused reset moved HEAD")
	}
	if b, _ := os.ReadFile(filepath.Join(clone, "a.txt")); string(b) != "dirty\n" {
		t.Fatal("the dirty file was touched")
	}
}

// A directory with a .git entry but a repository git cannot read is a root that
// fails closed, never a worker without Git (Jamir's P3 at a4e836e8).
func TestResetWorktreeRefusesARootWhoseRepositoryCannotBeRead(t *testing.T) {
	_, clone := resetRepo(t)
	if err := os.WriteFile(filepath.Join(clone, ".git", "config"), []byte("[core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err == nil && (out.Reset || out.Code == "") {
		t.Fatalf("a broken repository was taken for a worker without Git: out=%+v", out)
	}
	var untrusted *UntrustedConfigError
	if st, e := (ExecHost{}).Inspect(context.Background(), clone); !errors.As(e, &untrusted) {
		t.Fatalf("Inspect must refuse a repository it cannot read, not report success or 'not a worktree': st=%+v err=%v", st, e)
	}
}

// Jamir (6d9278d5): only a definite "no such entry" means a worker without Git. A
// .git entry that cannot be examined (a directory with no search permission here)
// is not that: it goes to the config gate, whose read fails and refuses.
func TestResetWorktreeRefusesADirectoryWhoseGitEntryCannotBeExamined(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs Unix directory permissions, and a non-root user")
	}
	_, clone := resetRepo(t)
	if err := os.Chmod(clone, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(clone, 0o755) }()
	if _, err := isWorktreeRoot(context.Background(), clone); err == nil {
		t.Fatal("an unexaminable .git entry was taken for a definite answer")
	}
	out, err := ResetWorktree(context.Background(), clone, resetNow)
	if err != nil || out.Reset || out.Code != CodeResetFailed {
		t.Fatalf("a directory that could not be examined must refuse, not start as a worker without Git: out=%+v err=%v", out, err)
	}
}

func TestIsWorktreeRootIsFalseOnlyForADefiniteMissingEntry(t *testing.T) {
	ctx := context.Background()
	plain := t.TempDir()
	if root, err := isWorktreeRoot(ctx, plain); root || err != nil {
		t.Fatalf("a directory with no .git entry is a definite no tree: root=%v err=%v", root, err)
	}
	file := filepath.Join(t.TempDir(), "file")
	_ = os.WriteFile(file, []byte("x"), 0o644)
	if root, err := isWorktreeRoot(ctx, file); root || err != nil {
		t.Fatalf("a path whose parent is a file (ENOTDIR) has no entry: root=%v err=%v", root, err)
	}
	_, clone := resetRepo(t)
	if root, err := isWorktreeRoot(ctx, clone); !root || err != nil {
		t.Fatalf("a repository with a .git entry is a root: root=%v err=%v", root, err)
	}
}

// Wolfgang #108499: an Lstat error other than not-found REFUSES, with the error:
// pinned with an injected one, for the reset and for Inspect.
func TestResetWorktreeRefusesWithTheErrorWhenTheGitEntryLookupFails(t *testing.T) {
	_, clone := resetRepo(t)
	for name, injected := range map[string]error{
		"permission": &fs.PathError{Op: "lstat", Path: "x/.git", Err: syscall.EACCES},
		"I/O":        &fs.PathError{Op: "lstat", Path: "x/.git", Err: syscall.EIO},
	} {
		setLstatGit(t, func(string) (fs.FileInfo, error) { return nil, injected })
		out, err := ResetWorktree(context.Background(), clone, resetNow)
		if err != nil || out.Reset || out.Code != CodeResetFailed || !strings.Contains(out.Detail, "could not examine") || !strings.Contains(out.Detail, injected.(*fs.PathError).Err.Error()) {
			t.Errorf("%s: the reset must refuse with the error: out=%+v err=%v", name, out, err)
		}
		if _, err := (ExecHost{}).Inspect(context.Background(), clone); err == nil || !strings.Contains(err.Error(), "could not examine") {
			t.Errorf("%s: Inspect must return the error, not 'not a worktree': %v", name, err)
		}
	}
}

// setLstatGit swaps the .git lookup for one test and restores it when the test
// ends (no test here runs in parallel, so the global cannot be observed by another).
func setLstatGit(t *testing.T, fn func(string) (fs.FileInfo, error)) {
	t.Helper()
	was := lstatGit
	lstatGit = fn
	t.Cleanup(func() { lstatGit = was })
}
