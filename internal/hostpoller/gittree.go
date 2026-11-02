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
	status, errOut, err := runGitTree(ctx, dir, "status", "--porcelain")
	if err != nil {
		return st, fmt.Errorf("git status: %s", firstNonEmpty(errOut, err.Error()))
	}
	st.Dirty = status != ""
	up, _, err := runGitTree(ctx, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return st, nil // no upstream: Upstream stays empty, which refuses
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
