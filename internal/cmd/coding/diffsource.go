package coding

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Where `coding review run` gets its list of changed files.
//
// Two sources, and the explicit one wins: a diff handed in on stdin or in a file
// (so the command works in CI, on a PR fetched by other means, or with no
// checkout at all), otherwise git in the current repository.

// reDiffPath matches the paths in a unified diff header. Both forms are read —
// `diff --git a/x b/y` and the `+++ b/x` line — because a diff produced with
// `--no-prefix`, or by a forge, may carry one and not the other.
var (
	reDiffGit  = regexp.MustCompile(`^diff --git (?:a/)?(\S+) (?:b/)?(\S+)`)
	reDiffPlus = regexp.MustCompile(`^\+\+\+ (?:b/)?(\S+)`)
)

// pathsFromDiff reads a unified diff and returns the files it touches.
//
// /dev/null is skipped: it is how a diff spells "this side does not exist" for
// an add or a delete, and taking it literally would put a file named
// `dev/null` in the changed set and match a check on it.
func pathsFromDiff(r io.Reader) ([]string, error) {
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	// A diff line can be long (a minified file, a data fixture); the default
	// 64K token limit would error mid-read and report a SHORT file list, which
	// is the silent-undercount direction — a check would go missing because a
	// line elsewhere was wide.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" || p == "/dev/null" || p == "dev/null" {
			return
		}
		seen[p] = true
	}
	for sc.Scan() {
		line := sc.Text()
		if m := reDiffGit.FindStringSubmatch(line); m != nil {
			add(m[1])
			add(m[2])
			continue
		}
		if m := reDiffPlus.FindStringSubmatch(line); m != nil {
			add(m[1])
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// gitLines runs a git command and returns its non-empty output lines.
func gitLines(ctx context.Context, args ...string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

// defaultBase picks what to diff against when the caller gives no --base: the
// merge base with the repository's default branch, so the answer is "everything
// this branch changed" rather than "everything since the last commit".
//
// Best-effort at every step. A repo with no origin, a detached HEAD or a shallow
// clone simply yields "", and the caller falls back to HEAD — a smaller change
// set, never a wrong one, and the command says which base it used.
func defaultBase(ctx context.Context) string {
	branch := "main"
	if lines, err := gitLines(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && len(lines) > 0 {
		branch = lines[0] // e.g. origin/main
	} else if lines, err := gitLines(ctx, "rev-parse", "--verify", "--quiet", "origin/main"); err == nil && len(lines) > 0 {
		branch = "origin/main"
	}
	if lines, err := gitLines(ctx, "merge-base", "HEAD", branch); err == nil && len(lines) > 0 {
		return lines[0]
	}
	return ""
}

// changedFiles resolves the diff's file list from git.
//
// UNTRACKED FILES ARE INCLUDED when the head is the working tree, and that is
// not a detail: a brand-new file is the single most likely thing to fire a
// check ("applies when a NEW command is added"), and `git diff` does not report
// one. Omitting them would make the command quietest exactly where a reviewer
// most needs it.
func changedFiles(ctx context.Context, base, head string) (files []string, usedBase string, err error) {
	if base == "" {
		base = defaultBase(ctx)
	}
	args := []string{"diff", "--name-only"}
	switch {
	case base != "" && head != "":
		args = append(args, base, head)
	case base != "":
		args = append(args, base)
	case head != "":
		args = append(args, head)
	default:
		args = append(args, "HEAD")
		base = "HEAD"
	}
	tracked, err := gitLines(ctx, args...)
	if err != nil {
		return nil, base, exitcode.Newf(exitcode.Usage,
			"could not read the diff from git (base %q, head %q) — pass --diff to supply one instead: %v", base, head, err)
	}
	seen := map[string]bool{}
	for _, f := range tracked {
		seen[f] = true
	}
	if head == "" {
		if untracked, uerr := gitLines(ctx, "ls-files", "--others", "--exclude-standard"); uerr == nil {
			for _, f := range untracked {
				seen[f] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, base, nil
}

// openDiff resolves --diff, where "-" is stdin.
func openDiff(spec string, stdin io.Reader) ([]string, error) {
	if spec == "-" {
		return pathsFromDiff(stdin)
	}
	f, err := os.Open(spec)
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "could not read the diff file %q: %v", spec, err)
	}
	defer func() { _ = f.Close() }()
	return pathsFromDiff(f)
}
