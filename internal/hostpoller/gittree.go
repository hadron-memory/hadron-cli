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
	case err != nil && strings.Contains(errOut, "not a git repository"):
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

func runGitTree(ctx context.Context, dir string, args ...string) (string, string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return strings.TrimSpace(out.String()), strings.TrimSpace(errb.String()), err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
