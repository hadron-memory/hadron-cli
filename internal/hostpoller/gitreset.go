package hostpoller

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The refusal codes a workspace reset reports (hadron-cli#2057 C2b; the same
// rule as cor:agt:050:27 part 7 and ruling 0012: a tree with work to lose is
// never moved). They lead the failure reason a refused worker.start reports,
// so the task waits with the not-started alarm and a person sees why.
const (
	CodeWorktreeDirty    = "WORKTREE_DIRTY"
	CodeWorktreeUnpushed = "WORKTREE_UNPUSHED"
	CodeResetFailed      = "WORKTREE_RESET_FAILED"
)

// ResetOutcome is what a workspace reset did.
type ResetOutcome struct {
	// Reset is false when nothing was done: the directory is not a Git work
	// tree (a worker without Git has no tree to reset), or the reset was
	// refused (then Code is set).
	Reset bool
	// Base is the commit the tree was checked out at, with the ref it came from.
	Base string
	// Rescued lists the rescue refs recorded for commits on no remote branch.
	Rescued []string
	// Code and Detail describe a refusal: Code is one of the codes above.
	Code   string
	Detail string
}

// Refused reports whether the reset was refused.
func (o ResetOutcome) Refused() bool { return o.Code != "" }

// ResetWorktree brings an EXISTING Git worktree back to a clean base at task
// start (G6): it refuses a dirty tree and a HEAD on no remote branch, records a
// rescue ref for every commit no remote branch has, fetches, and checks out the
// remote default branch detached. Nothing about the tree is created or removed
// (cor:agt:070:06 stays): the worktree, its branches and its remote are kept; a
// plain directory with no Git is left alone.
//
// Order matters: the refusals come first, from the tree as it stands, so a
// refused start moves nothing; the fetch only updates remote-tracking refs.
func ResetWorktree(ctx context.Context, dir string, now time.Time) (ResetOutcome, error) {
	st, err := GitTree(ctx, dir)
	if err != nil {
		return ResetOutcome{}, err
	}
	if !st.InGit {
		return ResetOutcome{}, nil // no Git, no reset
	}
	if st.Dirty {
		return refuse(CodeWorktreeDirty, "the worktree has uncommitted or untracked changes; nothing was moved"), nil
	}
	remote, err := resetRemote(ctx, dir)
	if err != nil {
		return refuse(CodeResetFailed, err.Error()), nil
	}
	if _, errOut, err := runGitTree(ctx, dir, "fetch", "--quiet", "--prune", remote); err != nil {
		return refuse(CodeResetFailed, fmt.Sprintf("git fetch %s failed: %s", remote, firstNonEmpty(errOut, err.Error()))), nil
	}
	rescued, err := recordRescueRefs(ctx, dir, now)
	if err != nil {
		return refuse(CodeResetFailed, err.Error()), nil
	}
	head, _, herr := runGitTree(ctx, dir, "rev-parse", "--verify", "-q", "HEAD")
	if herr == nil && head != "" {
		onRemote, err := onRemoteBranch(ctx, dir, head)
		if err != nil {
			return refuse(CodeResetFailed, err.Error()), nil
		}
		if !onRemote {
			o := refuse(CodeWorktreeUnpushed, "HEAD is on no remote branch, so moving the tree could lose commits; they are kept as rescue refs and nothing was moved")
			o.Rescued = rescued
			return o, nil
		}
	}
	base, err := resetBase(ctx, dir, remote)
	if err != nil {
		return refuse(CodeResetFailed, err.Error()), nil
	}
	if _, errOut, err := runGitTree(ctx, dir, "checkout", "--quiet", "--detach", base); err != nil {
		return refuse(CodeResetFailed, fmt.Sprintf("git checkout %s failed: %s", base, firstNonEmpty(errOut, err.Error()))), nil
	}
	sha, _, _ := runGitTree(ctx, dir, "rev-parse", "--short", "HEAD")
	return ResetOutcome{Reset: true, Base: base + " (" + sha + ")", Rescued: rescued}, nil
}

func refuse(code, detail string) ResetOutcome { return ResetOutcome{Code: code, Detail: detail} }

// resetRemote is the remote a reset fetches and bases on: origin. A tree with
// no origin has no remote branch HEAD could be on, so it is refused.
func resetRemote(ctx context.Context, dir string) (string, error) {
	remotes, errOut, err := runGitTree(ctx, dir, "remote")
	if err != nil {
		return "", fmt.Errorf("git remote failed: %s", firstNonEmpty(errOut, err.Error()))
	}
	for _, r := range strings.Fields(remotes) {
		if r == "origin" {
			return r, nil
		}
	}
	return "", fmt.Errorf("the worktree has no remote named origin to reset to")
}

// onRemoteBranch reports whether any remote-tracking branch contains sha.
func onRemoteBranch(ctx context.Context, dir, sha string) (bool, error) {
	out, errOut, err := runGitTree(ctx, dir, "branch", "-r", "--contains", sha)
	if err != nil {
		return false, fmt.Errorf("git branch -r --contains failed: %s", firstNonEmpty(errOut, err.Error()))
	}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		// "origin/HEAD -> origin/main" is an alias, not a branch of its own.
		if l != "" && !strings.Contains(l, "->") {
			return true, nil
		}
	}
	return false, nil
}

// recordRescueRefs gives every local branch tip and the current HEAD that no
// remote branch contains a ref of its own, refs/hadron/rescue/<utc>/<name>, so
// the commits stay reachable whatever happens to the branch afterwards. It never
// moves or deletes a branch.
func recordRescueRefs(ctx context.Context, dir string, now time.Time) ([]string, error) {
	stamp := now.UTC().Format("20060102T150405Z")
	type tip struct{ name, sha string }
	var tips []tip
	out, errOut, err := runGitTree(ctx, dir, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return nil, fmt.Errorf("git for-each-ref failed: %s", firstNonEmpty(errOut, err.Error()))
	}
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			tips = append(tips, tip{f[0], f[1]})
		}
	}
	if head, _, err := runGitTree(ctx, dir, "rev-parse", "--verify", "-q", "HEAD"); err == nil && head != "" {
		if br, _, _ := runGitTree(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD"); br == "" {
			tips = append(tips, tip{"detached-head", head})
		}
	}
	var made []string
	for _, t := range tips {
		onRemote, err := onRemoteBranch(ctx, dir, t.sha)
		if err != nil {
			return nil, err
		}
		if onRemote {
			continue
		}
		ref := "refs/hadron/rescue/" + stamp + "/" + t.name
		if _, errOut, err := runGitTree(ctx, dir, "update-ref", ref, t.sha); err != nil {
			return nil, fmt.Errorf("recording rescue ref %s failed: %s", ref, firstNonEmpty(errOut, err.Error()))
		}
		made = append(made, ref+" "+t.sha[:min(len(t.sha), 12)])
	}
	return made, nil
}

// resetBase is the remote default branch: origin/HEAD when the clone set it,
// else origin/main. Anything else is refused rather than guessed.
func resetBase(ctx context.Context, dir, remote string) (string, error) {
	if ref, _, err := runGitTree(ctx, dir, "symbolic-ref", "--short", "-q", "refs/remotes/"+remote+"/HEAD"); err == nil && ref != "" {
		return ref, nil
	}
	cand := remote + "/main"
	if _, _, err := runGitTree(ctx, dir, "rev-parse", "--verify", "-q", cand+"^{commit}"); err == nil {
		return cand, nil
	}
	return "", fmt.Errorf("could not find the remote default branch (no %s/HEAD, no %s)", remote, cand)
}
