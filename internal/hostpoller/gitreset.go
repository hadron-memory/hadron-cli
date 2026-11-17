package hostpoller

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// resetAfterFetch and resetBeforeCheckout are test seams, called right after the
// fetch and right before the checkout's final config check.
var resetAfterFetch, resetBeforeCheckout func()

// resetFetchTimeout bounds the fetch: a stalled remote must not hold a start.
const resetFetchTimeout = 2 * time.Minute

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
	// The worker's directory must itself be a worktree root. A plain directory
	// inside some enclosing repository is "inside a work tree" to git, and a reset
	// there would move the ENCLOSING repository: such a worker has no tree of its
	// own, so it is treated as a worker without Git (no reset, nothing refused).
	if root, err := isWorktreeRoot(ctx, dir); err != nil {
		return refuse(CodeResetFailed, err.Error()+"; nothing was moved"), nil
	} else if !root {
		return ResetOutcome{}, nil
	}
	// The worktree's git config is untrusted: any key outside the allowlist
	// refuses before a single command runs on the tree.
	if why := untrustedRepoConfig(ctx, dir); why != "" {
		return refuse(CodeResetFailed, why), nil
	}
	// The directory has a .git entry of its own, so it is not "a worker without
	// Git": git must resolve it to a valid repository whose work tree IS this
	// directory. An empty or broken .git directory is skipped by git's discovery,
	// which answers "not a repository" or falls back to an ENCLOSING repository
	// (the reset would then move that one): both refuse (hadron-cli#2119).
	if why := ownRepoProblem(ctx, dir); why != "" {
		return refuse(CodeResetFailed, why+"; nothing was moved"), nil
	}
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
	if url, _, err := runGitTree(ctx, dir, "config", "--get", "remote."+remote+".url"); err != nil || !resetURLOK(url) {
		return refuse(CodeResetFailed, fmt.Sprintf("remote %s is not an https://, ssh:// or user@host: URL (%q); nothing was fetched or moved", remote, url)), nil
	}
	fctx, cancel := context.WithTimeout(ctx, resetFetchTimeout)
	_, errOut, err := runGitTree(fctx, dir, "fetch", "--quiet", "--prune", "--no-recurse-submodules", "--upload-pack=git-upload-pack", remote)
	cancel()
	if err != nil {
		return refuse(CodeResetFailed, fmt.Sprintf("git fetch %s failed: %s", remote, firstNonEmpty(errOut, err.Error()))), nil
	}
	if resetAfterFetch != nil {
		resetAfterFetch() // test seam: what a ref moved between the fetch and the read would look like
	}
	// The config is read again right before the commands that matter: a key a
	// same-user actor planted after the first read (or during the fetch) refuses
	// here, not after the checkout has run its filters.
	if why := untrustedRepoConfig(ctx, dir); why != "" {
		return refuse(CodeResetFailed, why), nil
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
	base, sha, err := resetBase(ctx, dir, remote)
	if err != nil {
		return refuse(CodeResetFailed, err.Error()), nil
	}
	if resetBeforeCheckout != nil {
		resetBeforeCheckout() // test seam: a config change after the last check
	}
	if why := untrustedRepoConfig(ctx, dir); why != "" {
		o := refuse(CodeResetFailed, why)
		o.Rescued = rescued
		return o, nil
	}
	// By commit id, never by a name: a local branch called origin/main cannot
	// be taken for the remote one.
	if _, errOut, err := runGitTree(ctx, dir, "checkout", "--quiet", "--detach", sha); err != nil {
		return refuse(CodeResetFailed, fmt.Sprintf("git checkout %s failed: %s", base, firstNonEmpty(errOut, err.Error()))), nil
	}
	return ResetOutcome{Reset: true, Base: base + " (" + sha[:min(len(sha), 12)] + ")", Rescued: rescued}, nil
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

// recordRescueRefs gives every local branch tip and a detached HEAD that no
// remote branch contains a ref of its own, so the commits stay reachable
// whatever happens to the branch afterwards. It never moves or deletes a branch.
//
// One git call finds every unpushed tip (a shared clone holds hundreds of
// branches; a call per branch is seconds per start). A commit already kept by an
// earlier rescue ref is not recorded again, so repeated starts do not multiply
// refs. Branch tips go under .../heads/<branch>, a detached HEAD under
// .../detached, so no branch name can collide with the synthetic one; and every
// ref is created with an all-zero old value, so an existing ref is never
// replaced: a name already taken gets a numeric suffix.
func recordRescueRefs(ctx context.Context, dir string, now time.Time) ([]string, error) {
	stamp := now.UTC().Format("20060102T150405Z")
	unpushed, errOut, err := runGitTree(ctx, dir, "rev-list", "--no-walk=unsorted", "--branches", "--not", "--remotes")
	if err != nil {
		return nil, fmt.Errorf("git rev-list failed: %s", firstNonEmpty(errOut, err.Error()))
	}
	want := map[string]bool{}
	for _, sha := range strings.Fields(unpushed) {
		want[sha] = true
	}
	type tip struct{ name, sha string }
	var tips []tip
	if len(want) > 0 {
		out, errOut, err := runGitTree(ctx, dir, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
		if err != nil {
			return nil, fmt.Errorf("git for-each-ref failed: %s", firstNonEmpty(errOut, err.Error()))
		}
		for _, l := range strings.Split(out, "\n") {
			if f := strings.Fields(l); len(f) == 2 && want[f[1]] {
				tips = append(tips, tip{"heads/" + f[0], f[1]})
			}
		}
	}
	if br, _, _ := runGitTree(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD"); br == "" {
		if head, _, err := runGitTree(ctx, dir, "rev-parse", "--verify", "-q", "HEAD"); err == nil && head != "" {
			if onRemote, err := onRemoteBranch(ctx, dir, head); err != nil {
				return nil, err
			} else if !onRemote {
				tips = append(tips, tip{"detached", head})
			}
		}
	}
	kept := map[string]bool{}
	if out, _, err := runGitTree(ctx, dir, "for-each-ref", "--format=%(objectname)", "refs/hadron/rescue"); err == nil {
		for _, sha := range strings.Fields(out) {
			kept[sha] = true
		}
	}
	var made []string
	for _, t := range tips {
		if kept[t.sha] {
			continue // an earlier rescue already keeps this commit reachable
		}
		ref, err := createRescueRef(ctx, dir, "refs/hadron/rescue/"+stamp+"/"+t.name, t.sha)
		if err != nil {
			return nil, err
		}
		kept[t.sha] = true
		made = append(made, ref+" "+t.sha[:min(len(t.sha), 12)])
	}
	return made, nil
}

// zeroOID is git's "this ref must not exist yet" old value for update-ref.
const zeroOID = "0000000000000000000000000000000000000000"

// createRescueRef creates ref at sha and never replaces an existing ref: a
// taken name gets -2, -3, ... (bounded).
func createRescueRef(ctx context.Context, dir, ref, sha string) (string, error) {
	name := ref
	for i := 2; ; i++ {
		_, errOut, err := runGitTree(ctx, dir, "update-ref", name, sha, zeroOID)
		if err == nil {
			return name, nil
		}
		if i > 50 {
			return "", fmt.Errorf("recording rescue ref %s failed: %s", ref, firstNonEmpty(errOut, err.Error()))
		}
		name = fmt.Sprintf("%s-%d", ref, i)
	}
}

// resetBase is the remote's CURRENT default branch, learned from the remote
// (`ls-remote --symref`), not from the cached origin/HEAD, which a fetch never
// refreshes: a server whose default changed would otherwise leave every start on
// the old branch. It returns the qualified ref and the commit id it resolves to,
// after the fetch; a default that cannot be established or was not fetched is
// refused rather than guessed.
func resetBase(ctx context.Context, dir, remote string) (ref, sha string, err error) {
	lctx, cancel := context.WithTimeout(ctx, resetFetchTimeout)
	defer cancel()
	out, errOut, lerr := runGitTree(lctx, dir, "ls-remote", "--upload-pack=git-upload-pack", "--symref", remote, "HEAD")
	if lerr != nil {
		return "", "", fmt.Errorf("could not read %s's default branch: %s", remote, firstNonEmpty(errOut, lerr.Error()))
	}
	branch := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "ref: refs/heads/") {
			if f := strings.Fields(strings.TrimPrefix(l, "ref: refs/heads/")); len(f) > 0 {
				branch = f[0]
			}
		}
	}
	if branch == "" {
		return "", "", fmt.Errorf("%s reports no default branch", remote)
	}
	ref = "refs/remotes/" + remote + "/" + branch
	sha, _, verr := runGitTree(ctx, dir, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if verr != nil || sha == "" {
		return "", "", fmt.Errorf("%s's default branch %s was not fetched", remote, branch)
	}
	// What was fetched must be what the remote says its default branch is: the
	// ls-remote answer names the tip, so a tracking ref that points elsewhere (a
	// refspec or a ref moved by someone else) never gets checked out. A remote
	// that moved between the fetch and this read refuses too: try again.
	var head string
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) == 2 && f[1] == "HEAD" && !strings.HasPrefix(l, "ref:") {
			head = f[0]
		}
	}
	if head == "" || head != sha {
		return "", "", fmt.Errorf("the fetched %s/%s (%.12s) is not the tip %s reports for its default branch (%.12s); nothing was moved", remote, branch, sha, remote, head)
	}
	return remote + "/" + branch, sha, nil
}

// ownRepoProblem says why dir, which has a .git entry of its own, is not a usable
// repository whose work tree is dir itself ("" when it is). It runs after the
// config allowlist, so a repository whose config redirects its work tree
// (core.worktree) has already been refused by name.
func ownRepoProblem(ctx context.Context, dir string) string {
	top, errOut, err := runGitTree(ctx, dir, "rev-parse", "--show-toplevel")
	switch {
	case err != nil:
		return "the .git entry in " + dir + " is not a usable repository (" + firstNonEmpty(errOut, err.Error()) + ")"
	case !samePath(top, dir):
		return "git resolves " + dir + " to the work tree " + top + ", not to this directory: its .git entry is not a usable repository and git fell back to an enclosing one"
	}
	return ""
}

// isWorktreeRoot reports whether dir is the top level of a git work tree: it has
// a .git entry of its own (the directory of a repository's own work tree, or the
// pointer file of a linked worktree). A plain directory, or a subdirectory, inside
// some enclosing repository has none, so it is a worker without Git. The root is
// decided by this entry, not by `rev-parse --show-toplevel`, which honours the
// repository's own core.worktree: a tree whose config redirects its work tree
// elsewhere would read as "not a root" and slip past silently; with the entry
// present it goes through the config allowlist, which refuses core.worktree.
func isWorktreeRoot(_ context.Context, dir string) (bool, error) {
	_, err := lstatGit(filepath.Join(dir, ".git"))
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR):
		return false, nil // a definite "no such entry": no tree of its own
	}
	// Anything else (a permission or I/O failure) says nothing about whether
	// there is a tree: it REFUSES, with the error, and is never a worker without Git.
	return false, fmt.Errorf("could not examine %s: %w", filepath.Join(dir, ".git"), err)
}

// lstatGit is the metadata lookup behind isWorktreeRoot; a test injects errors.
var lstatGit = os.Lstat
