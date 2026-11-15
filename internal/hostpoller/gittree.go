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
	return []string{
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		"-c", "core.sshCommand=ssh",
		"-c", "core.askPass=",
		"-c", "core.gitProxy=",
		"-c", "protocol.ext.allow=never",
		"-c", "fetch.recurseSubmodules=false",
		"-c", "submodule.recurse=false",
		"-c", "gc.auto=0",
		"-c", "maintenance.auto=false",
	}
}

// gitEnv is the environment of every git run on a worker-writable tree: no
// prompt, and only the transports a team remote uses (ext:: runs a command).
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=file:http:https:ssh:git",
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
// process; a diff driver's textconv and external. Two classes cannot be
// neutralised and are reported as a refusal instead: a repository credential
// helper (an empty helper clears the whole list, the user's own included) and a
// remote's uploadpack, receivepack, proxy or vcs (git keeps the first value, so
// an override cannot replace the repository's). Reading config executes nothing.
func repoOverrides(ctx context.Context, dir string) (args []string, refusal string) {
	seen := map[string]bool{}
	var filters = map[string]bool{}
	for _, scope := range []string{"--local", "--worktree"} {
		out, _, err := runGitRaw(ctx, dir, nil, "config", scope, "--list", "-z")
		if err != nil {
			continue // not a git tree, or no such scope: nothing to read
		}
		for _, entry := range strings.Split(out, "\x00") {
			key, _, _ := strings.Cut(entry, "\n")
			if key == "" || seen[key] {
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
			case section == "remote" && (name == "uploadpack" || name == "receivepack" || name == "proxy" || name == "vcs"):
				// Git keeps the FIRST value of these ("more than one uploadpack
				// given, using the first"), so a later -c cannot replace the
				// repository's: refuse rather than run it.
				refusal = "the repository's own config sets " + key + ", which runs a program as the poller and cannot be overridden"
			case section == "diff" && (name == "textconv" || name == "external"):
				args = append(args, "-c", key+"=")
			case section == "credential" && name == "helper":
				refusal = "the repository's own config sets a credential helper (" + key + "), which would run as the poller and cannot be neutralised without resetting the host user's helper"
			}
		}
	}
	for sub := range filters {
		args = append(args, "-c", "filter."+sub+".required=false")
	}
	return args, refusal
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
