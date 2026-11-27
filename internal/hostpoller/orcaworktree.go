package hostpoller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/hostfacts"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
)

// worktree.create on a host that runs from the platform (hadron-cli#3237, U3 of hadron-server#5210): the tree is
// made by Orca (`orca worktree create --repo name:<displayName>`), not by `git worktree add`, and lands at
// <Orca workspace root>/<repo>/<name>, which is the token <displayName>/<label> hostfacts resolves. The outcome
// is judged on Orca's worktree list read after the create, never on the create's own answer: `--branch` takes
// a numeric suffix when the name is taken, so a create can succeed on a branch nobody asked for.

// OrcaWorktreeFacts is what a platform worktree.create reads from Orca (hostfacts.Facts in production).
type OrcaWorktreeFacts interface {
	Repos(ctx context.Context) ([]hostfacts.Repo, *hostfacts.Missing)
	Worktrees(ctx context.Context) ([]hostfacts.Worktree, *hostfacts.Missing)
}

// SetOrcaWorktrees gives a platform poller what worktree.create needs: Orca's reads and the runner the create
// goes through. The runner is closed over `worktree create` here (orcaterm.WorktreeCreateGuard), whatever the
// caller passes. Without it (or given a nil half) a platform worktree.create fails and creates nothing.
func (p *Poller) SetOrcaWorktrees(facts OrcaWorktreeFacts, run orcaterm.Runner) {
	if facts == nil || run == nil {
		p.orcaFacts, p.orcaCreate = nil, nil
		return
	}
	p.orcaFacts = facts
	p.orcaCreate = orcaterm.WorktreeCreateGuard(run)
}

// orcaWorktreeCreate decides a platform worktree.create. It returns the token on success, or the failure
// reason. Nothing it reports carries Orca's own output; the one path it names is an ambiguous token's
// candidates, as a start's ambiguity refusal does (#3237: the refusal names the candidates and the fix).
func (p *Poller) orcaWorktreeCreate(ctx context.Context, repo, label, branch, base string) (string, string) {
	if p.orcaCreate == nil {
		return "", "this host cannot create a worktree: Orca is not wired for worktree.create; nothing was created"
	}
	if branch == "" {
		return "", "worktree.create names no branch; nothing was created"
	}
	repos, miss := p.orcaFacts.Repos(ctx)
	if miss != nil {
		return "", "Orca's repositories could not be read (" + miss.Reason + "); nothing was created"
	}
	n := 0
	for _, r := range repos {
		if r.DisplayName == repo {
			n++
		}
	}
	switch {
	case n == 0:
		return "", fmt.Sprintf("repo %q is not registered with Orca on this host; register it with `orca repo add`; nothing was created", repo)
	case n > 1:
		return "", fmt.Sprintf("repo %q names %d Orca repositories on this host; rename all but one in Orca so the name is one repository; nothing was created", repo, n)
	}
	token := repo + "/" + label
	// A re-delivered create whose tree is already there on the branch succeeds, as under --config.
	switch tree, why := p.orcaTree(ctx, repos, token); {
	case why != "":
		return "", why + "; nothing was created"
	case tree != nil:
		if tree.Branch != "refs/heads/"+branch {
			return "", fmt.Sprintf("%s already exists and is not a worktree on %s", token, branch)
		}
		return token, ""
	}
	argv := []string{"worktree", "create", "--repo", "name:" + repo, "--name", label, "--branch", branch}
	if base != "" {
		argv = append(argv, "--base-branch", base)
	}
	// --no-parent: Orca would otherwise record the tree as a child of whatever it infers from the poller's own
	// directory. --setup skip: a repo's setup hooks are not the host's to run.
	argv = append(argv, "--no-parent", "--setup", "skip", "--json")
	res := p.orcaCreate(ctx, argv)
	if res.Code != 0 || !orcaOK(res.Stdout) {
		// A bound or a lost answer reads the same as a refusal, so whether the tree exists is not known here.
		return "", fmt.Sprintf("Orca did not confirm creating %s (`orca worktree create` exit %d); nothing is recorded, and whether the tree exists is unknown: check `orca worktree list`", token, res.Code)
	}
	tree, why := p.orcaTree(ctx, nil, token)
	switch {
	case why != "":
		return "", "Orca's create answered, but " + why + "; the tree may exist: check `orca worktree list`"
	case tree == nil:
		return "", fmt.Sprintf("Orca's create answered, but %s is not in its worktree list (the name may have been taken); check `orca worktree list`", token)
	case tree.Branch != "refs/heads/"+branch:
		return "", fmt.Sprintf("Orca created %s on %s, not on %s (the branch name was taken); remove it with Orca and retry with a free branch name", token, strings.TrimPrefix(tree.Branch, "refs/heads/"), branch)
	}
	return token, ""
}

// orcaTree is the one live Orca worktree token names: nil when there is none, a reason when the list cannot be
// read or the token names more than one. repos nil reads them again.
func (p *Poller) orcaTree(ctx context.Context, repos []hostfacts.Repo, token string) (*hostfacts.Worktree, string) {
	if repos == nil {
		var miss *hostfacts.Missing
		if repos, miss = p.orcaFacts.Repos(ctx); miss != nil {
			return nil, "Orca's repositories could not be read (" + miss.Reason + ")"
		}
	}
	trees, miss := p.orcaFacts.Worktrees(ctx)
	if miss != nil {
		return nil, "Orca's worktrees could not be read (" + miss.Reason + ")"
	}
	path, cands, res := hostfacts.ResolveWorkdir(token, repos, trees)
	switch res {
	case hostfacts.Ambiguous:
		return nil, fmt.Sprintf("%s: %s matches %d Orca worktrees on this host (%s); archive or rename all but one", hostfacts.CodeWorkdirAmbiguousOnHost, token, len(cands), strings.Join(cands, ", "))
	case hostfacts.Found:
		for i := range trees {
			if trees[i].Path == path && !trees[i].Archived {
				return &trees[i], ""
			}
		}
	}
	return nil, ""
}

// orcaOK reads Orca's JSON envelope: only {"ok":true,…} is a success.
func orcaOK(stdout string) bool {
	var env struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal([]byte(strings.TrimSpace(stdout)), &env) == nil && env.OK
}

// platformRemoveTree finds token on Orca's live list for a platform worktree.remove whose token no assignment
// resolved at this poll: its path, "" when Orca lists no such tree, or the reason it cannot be told.
func (p *Poller) platformRemoveTree(ctx context.Context, token string) (string, string) {
	if p.orcaFacts == nil {
		return "", fmt.Sprintf("%s: worktree %q is not an assigned workdir on this host and Orca is not wired to look it up", hostfacts.CodeWorkdirNotOnHost, token)
	}
	tree, why := p.orcaTree(ctx, nil, token)
	if why != "" || tree == nil {
		return "", why
	}
	return tree.Path, ""
}
