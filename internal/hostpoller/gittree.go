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

type repoOverridesKey struct{}

// withRepoOverrides carries the repository-config overrides of one tree on the
// context, so every git run of one operation applies them.
func withRepoOverrides(ctx context.Context, args []string) context.Context {
	return context.WithValue(ctx, repoOverridesKey{}, args)
}

// repoOverrides reads the WORKER-WRITABLE scopes of dir's git config (local, and
// worktree when it differs) and returns -c arguments that neutralise every
// setting in them that can run a command: a filter driver's smudge, clean and
// process; a diff driver's textconv and external. What cannot be neutralised, or
// would redirect the fetch (so poison the clean base the next task starts from),
// is reported as a refusal instead: a repository credential helper (an empty
// helper clears the whole list, the user's own included), a remote's proxy or
// vcs (git keeps the first value, so an override cannot replace the repository's),
// url.*.insteadOf / pushInsteadOf, an http proxy, and TLS verification turned
// off. A remote's uploadpack is not refused: both network calls pass an explicit
// --upload-pack, which wins. Reading config executes nothing.
func repoOverrides(ctx context.Context, dir string) (args []string, refusal string) {
	if _, errOut, err := runGitRaw(ctx, dir, nil, "rev-parse", "--git-dir"); err != nil {
		if strings.Contains(strings.ToLower(errOut), "not a git repository") || errors.Is(err, exec.ErrNotFound) {
			return nil, "" // no repository, no repository config
		}
		return nil, "could not read the repository (" + firstNonEmpty(strings.TrimSpace(errOut), err.Error()) + "), so its config could not be checked"
	}
	seen := map[string]bool{}
	filters := map[string]bool{}
	last := map[string]string{} // the EFFECTIVE value of a key: the last one read, worktree after local
	hasValue := map[string]bool{}
	for _, scope := range []string{"--local", "--worktree"} {
		// --includes: an explicit scope does not expand include.path /
		// includeIf by default, and an included file is as much the worker's
		// as the config that names it.
		out, errOut, err := runGitRaw(ctx, dir, nil, "config", scope, "--includes", "--list", "-z")
		if err != nil {
			// A repository with extensions.worktreeConfig and no config.worktree
			// has nothing in that scope; anything else fails closed.
			if scope == "--worktree" && (strings.Contains(errOut, "unable to read config file") || strings.Contains(errOut, "No such file")) {
				continue
			}
			return nil, "could not read the repository's " + strings.TrimPrefix(scope, "--") + " config (" + firstNonEmpty(strings.TrimSpace(errOut), err.Error()) + "), so it could not be checked"
		}
		for _, entry := range strings.Split(out, "\x00") {
			key, value, withValue := strings.Cut(entry, "\n")
			if key == "" {
				continue
			}
			last[key], hasValue[key] = value, withValue
			if seen[key] {
				continue
			}
			seen[key] = true
			section, rest, ok := strings.Cut(key, ".")
			if !ok {
				continue
			}
			sub, name := "", rest
			if i := strings.LastIndex(rest, "."); i >= 0 {
				sub, name = rest[:i], rest[i+1:]
			}
			switch {
			case section == "filter" && (name == "smudge" || name == "clean" || name == "process"):
				args = append(args, "-c", key+"=")
				filters[sub] = true
			case section == "diff" && (name == "textconv" || name == "external"):
				args = append(args, "-c", key+"=")
			case section == "credential" && name == "helper":
				refusal = "the repository's own config sets a credential helper (" + key + "), which would run as the poller and cannot be neutralised without resetting the host user's helper"
			case section == "remote" && (name == "proxy" || name == "vcs"):
				// A proxy redirects the fetch; a vcs names a remote helper program.
				// Git keeps the FIRST value of remote.* keys, so a later -c cannot
				// replace the repository's: refuse rather than follow it.
				refusal = "the repository's own config sets " + key + ", which redirects or runs a program for the fetch and cannot be overridden"
			case section == "url" && (name == "insteadof" || name == "pushinsteadof"):
				refusal = "the repository's own config rewrites remote URLs (" + key + "), which could point the fetch at another host"
			case section == "http" && strings.Contains(name, "proxy"):
				refusal = "the repository's own config sets a proxy (" + key + "), which could point the fetch at another host"
			}
		}
	}
	// TLS verification is judged on the EFFECTIVE value of each key (git uses the
	// last one), in any of git's spellings of false.
	for key, v := range last {
		section, rest, _ := strings.Cut(key, ".")
		name := rest
		if i := strings.LastIndex(rest, "."); i >= 0 {
			name = rest[i+1:]
		}
		if section == "http" && name == "sslverify" && hasValue[key] && gitBoolFalse(v) {
			refusal = "the repository's own config turns TLS verification off (" + key + ")"
		}
	}
	for sub := range filters {
		args = append(args, "-c", "filter."+sub+".required=false")
	}
	return args, refusal
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

func runGitTree(ctx context.Context, dir string, args ...string) (string, string, error) {
	out, errOut, err := runGitRaw(ctx, dir, repoArgs(ctx), args...)
	return strings.TrimSpace(out), strings.TrimSpace(errOut), err
}

func repoArgs(ctx context.Context) []string {
	a, _ := ctx.Value(repoOverridesKey{}).([]string)
	return a
}

// runGitRaw runs git with the hardened configuration plus extra -c arguments and
// returns its output untrimmed.
func runGitRaw(ctx context.Context, dir string, extra []string, args ...string) (string, string, error) {
	argv := append(append(append([]string{}, hardenedGit()...), extra...), "-C", dir)
	cmd := exec.CommandContext(ctx, "git", append(argv, args...)...)
	cmd.Env = gitEnv()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
