package hostpoller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// GitTreeState is the reset handler's Git determination (ruling 0012,
// cor:agt:050:27 rule 7; hadron-cli#1043 amendment 6586883519).
type GitTreeState struct {
	// InGit: `git -C <dir> rev-parse --is-inside-work-tree` printed true.
	InGit bool
	// The rest is read only when InGit.
	Dirty    bool
	Upstream string
	Ahead    int
}

// GitTree decides whether dir is inside a Git work tree, and only then reads
// its cleanliness and push state. A missing directory, a non-Git directory
// and a host without git are all "not in a Git tree". Any other git failure
// is an error, so the caller refuses: an unreadable tree is never taken for
// a non-Git one.
func GitTree(ctx context.Context, dir string) (GitTreeState, error) {
	var st GitTreeState
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return st, nil
	} else if err != nil {
		return st, err
	}
	out, errOut, err := runGitTree(ctx, dir, "rev-parse", "--is-inside-work-tree")
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return st, nil
	case err != nil && strings.Contains(strings.ToLower(errOut), "not a git repository"): // Git before 2.36 capitalizes it
		return st, nil
	case err != nil:
		return st, fmt.Errorf("git rev-parse --is-inside-work-tree: %s", firstNonEmpty(errOut, err.Error()))
	case out != "true":
		return st, nil
	}
	st.InGit = true
	// --untracked-files=all: a status.showUntrackedFiles=no config would
	// otherwise hide untracked work from the cleanliness check.
	status, errOut, err := runGitTree(ctx, dir, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return st, fmt.Errorf("git status: %s", firstNonEmpty(errOut, err.Error()))
	}
	st.Dirty = status != ""
	up, _, err := runGitTree(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return st, nil // no upstream: Upstream stays empty, which refuses
	}
	// A branch may track a local branch (branch.<name>.remote = .), or a
	// remote that is no longer configured: neither proves HEAD was pushed
	// anywhere, so neither is a remote upstream.
	if remote, err := upstreamRemote(ctx, dir); err != nil {
		return st, err
	} else if remote == "" {
		return st, nil
	}
	st.Upstream = up
	n, errOut, err := runGitTree(ctx, dir, "rev-list", "--count", "@{upstream}..HEAD")
	if err != nil {
		return st, fmt.Errorf("git rev-list: %s", firstNonEmpty(errOut, err.Error()))
	}
	if st.Ahead, err = strconv.Atoi(n); err != nil {
		return st, fmt.Errorf("reading commits ahead of %s: %w", up, err)
	}
	return st, nil
}

// upstreamRemote is the configured remote HEAD's branch tracks, or "" when
// it tracks a local branch or a remote that is not configured.
func upstreamRemote(ctx context.Context, dir string) (string, error) {
	branch, _, err := runGitTree(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil || branch == "" {
		return "", nil // detached: no branch, so no upstream
	}
	remote, _, err := runGitTree(ctx, dir, "config", "--get", "branch."+branch+".remote")
	if err != nil || remote == "" { // "." (a local branch) is never a configured remote
		return "", nil
	}
	remotes, errOut, err := runGitTree(ctx, dir, "remote")
	if err != nil {
		return "", fmt.Errorf("git remote: %s", firstNonEmpty(errOut, err.Error()))
	}
	for _, r := range strings.Split(remotes, "\n") {
		if strings.TrimSpace(r) == remote {
			return remote, nil
		}
	}
	return "", nil
}

// hardenedGit is the configuration EVERY git run in a tree the worker can write
// gets, so a hook, a config line or a transport the worker (or an agent) planted
// in the repository never runs as the poller's user (hadron-cli#2057 §10). It is
// the fixed part of a systematic rule: the user's own global and system config
// (outside the worktree, not worker-writable) is kept, because a private remote's
// credential helper lives there; what the REPOSITORY's config adds is neutralised
// by repoOverrides, class by class.
func hardenedGit() []string {
	base := hardenedGitBase()
	if localRemotesForTests {
		// Test-only seam: the fixtures fetch from a local path.
		base = append(base, "-c", "protocol.file.allow=always")
	}
	return base
}

// localRemotesForTests lets a test fetch from a local path. It is false in
// production: a team remote is https or ssh (the redirection and transport
// allowlist of hadron-cli#2057 §10); only a _test.go file ever sets it.
var localRemotesForTests bool

func hardenedGitBase() []string {
	return []string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "core.sshCommand=ssh",
		"-c", "core.askPass=",
		"-c", "core.gitProxy=",
		"-c", "protocol.allow=never",
		"-c", "protocol.https.allow=always",
		"-c", "protocol.ssh.allow=always",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "submodule.recurse=false",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
	}
}

// gitEnv is the environment of every git run on a worker-writable tree: no
// prompt, and only the transports a team remote uses (ext:: runs a command).
func gitEnv() []string {
	protos := "https:ssh"
	if localRemotesForTests {
		protos = "file:https:ssh"
	}
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL="+protos,
		"GIT_ASKPASS=", "SSH_ASKPASS=",
	)
}

// untrustedRepoConfig is the repository-config ALLOWLIST (hadron-cli#2057 §10,
// Wolfgang #108206): the worktree's git config is untrusted, so instead of
// chasing the keys that can run a command (hooks, filters, helpers, proxies,
// includes, drivers, transports...) every key in the worker-writable scopes of
// dir's config (local, and worktree for a linked worktree) must be one of a
// small set of inert ones. It returns the first key outside the set, or an
// explanation when the config cannot be read (nothing unread is trusted); ""
// means the config is trusted. Reading config executes nothing. Includes are
// expanded for the read, and include.* is itself outside the set.
//
// The set is what a real clone carries: core.repositoryformatversion, filemode,
// bare, logallrefupdates, ignorecase, precomposeunicode, symlinks;
// remote.<name>.url and fetch, remote.pushdefault; branch.<name>.remote, merge,
// base, pushremote; push.autosetupremote; user.name, user.email;
// extensions.worktreeconfig. (base, pushremote, symlinks, pushdefault and
// push.autosetupremote are additions to the ruled list: every orca1 clone
// carries them, and refusing them would refuse every reset.)
func untrustedRepoConfig(ctx context.Context, dir string) string {
	if _, errOut, err := runGitRaw(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		if strings.Contains(strings.ToLower(errOut), "not a git repository") || errors.Is(err, exec.ErrNotFound) {
			return "" // no repository, no repository config
		}
		return "could not read the repository (" + firstNonEmpty(strings.TrimSpace(errOut), err.Error()) + "), so its config could not be checked"
	}
	// config.worktree is applied by git only when extensions.worktreeConfig is
	// on; without it `config --worktree` is an error in a linked worktree, and
	// there is no such scope to check. With it, the scope is checked like local.
	worktreeScope := false
	for _, scope := range []string{"--local", "--worktree"} {
		if scope == "--worktree" && !worktreeScope {
			break
		}
		out, errOut, err := runGitRaw(ctx, dir, "config", scope, "--includes", "--list", "-z")
		if err != nil {
			// extensions.worktreeConfig on, no config.worktree yet: nothing in
			// that scope. Anything else fails closed.
			if scope == "--worktree" && (strings.Contains(errOut, "unable to read config file") || strings.Contains(errOut, "No such file")) {
				continue
			}
			return "could not read the repository's " + strings.TrimPrefix(scope, "--") + " config (" + firstNonEmpty(strings.TrimSpace(errOut), err.Error()) + "), so it could not be checked"
		}
		for _, entry := range strings.Split(out, "\x00") {
			key, value, _ := strings.Cut(entry, "\n")
			if key == "" {
				continue
			}
			if !inertConfigKey(key, value) {
				return "the repository's own config sets " + key + ", which is not on the allowlist of inert keys; nothing was run, fetched or moved"
			}
			if scope == "--local" && key == "extensions.worktreeconfig" && !gitBoolFalse(value) {
				worktreeScope = true
			}
		}
	}
	return ""
}

// gitBoolFalse reports whether v is one of git's spellings of false: false, no,
// off, 0, or the empty string.
func gitBoolFalse(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "no", "off", "0", "":
		return true
	}
	return false
}

// inertConfigKey reports whether key (as git lists it: lowercase section and
// variable, case-preserved subsection) is one a clone carries and that can run
// nothing.
func inertConfigKey(key, value string) bool {
	section, rest, ok := strings.Cut(key, ".")
	if !ok {
		return false
	}
	sub, name := "", rest
	if i := strings.LastIndex(rest, "."); i >= 0 {
		sub, name = rest[:i], rest[i+1:]
	}
	switch section {
	case "core":
		switch name {
		case "repositoryformatversion", "filemode", "bare", "logallrefupdates", "ignorecase", "precomposeunicode", "symlinks":
			return sub == ""
		}
	case "remote":
		if sub != "" {
			// The fetch refspec decides which remote ref lands on the tracking
			// ref the reset checks out: only the standard mapping is inert.
			if name == "fetch" {
				return standardRefspec(sub, value)
			}
			return name == "url"
		}
		return name == "pushdefault"
	case "branch":
		return sub != "" && (name == "remote" || name == "merge" || name == "base" || name == "pushremote")
	case "push":
		return sub == "" && name == "autosetupremote"
	case "user":
		return sub == "" && (name == "name" || name == "email")
	case "extensions":
		return sub == "" && name == "worktreeconfig"
	}
	return false
}

// standardRefspec reports whether v maps the remote's branches onto that same
// remote's tracking namespace and nothing else: +refs/heads/*:refs/remotes/<r>/*,
// or the single-branch form +refs/heads/<b>:refs/remotes/<r>/<b>. A refspec that
// lands another branch on refs/remotes/<r>/main would make the reset check out
// the wrong commit while reporting the default branch.
func standardRefspec(remote, v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "+")
	src, dst, ok := strings.Cut(v, ":")
	if !ok {
		return false
	}
	wantDst := "refs/remotes/" + remote + "/"
	if src == "refs/heads/*" {
		return dst == wantDst+"*"
	}
	b, isHead := strings.CutPrefix(src, "refs/heads/")
	return isHead && b != "" && !strings.Contains(b, "*") && dst == wantDst+b
}

func runGitTree(ctx context.Context, dir string, args ...string) (string, string, error) {
	out, errOut, err := runGitRaw(ctx, dir, args...)
	return strings.TrimSpace(out), strings.TrimSpace(errOut), err
}

// runGitRaw runs git with the hardened configuration and returns its output
// untrimmed.
func runGitRaw(ctx context.Context, dir string, args ...string) (string, string, error) {
	argv := append(append([]string{}, hardenedGit()...), "-C", dir)
	cmd := exec.CommandContext(ctx, "git", append(argv, args...)...)
	cmd.Env = gitEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// resetURLOK says whether a remote URL is one a team remote uses: https, ssh, or
// user@host:path (scp-like). Anything else (a local path, file://, ext::, git://,
// http://) is refused before the fetch.
func resetURLOK(url string) bool {
	u := strings.TrimSpace(url)
	switch {
	case strings.HasPrefix(u, "https://"), strings.HasPrefix(u, "ssh://"):
		return true
	case localRemotesForTests && (strings.HasPrefix(u, "/") || strings.HasPrefix(u, "file://")):
		return true
	}
	// scp-like: user@host:path, with no scheme and no slash before the colon.
	at := strings.Index(u, "@")
	colon := strings.Index(u, ":")
	return at > 0 && colon > at+1 && !strings.Contains(u[:colon], "/") && !strings.Contains(u, "::")
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
