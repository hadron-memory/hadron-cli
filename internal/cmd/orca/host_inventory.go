package orca

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

type hostBinding struct {
	Server    string `json:"server"`
	WorkerID  string `json:"workerId"`
	AppID     string `json:"appId"`
	SessionID string `json:"sessionId"`
}
type hostTree struct {
	Path, Common string
	Binding      *hostBinding
}
type hostTerminal struct{ Handle, Path, Family string }
type hostInventory struct {
	Repos     map[string]string
	Trees     []hostTree
	Terminals []hostTerminal
}
type hostInventoryError struct {
	Reason string
	Err    error
}

func (e *hostInventoryError) Error() string { return e.Err.Error() }
func (e *hostInventoryError) Unwrap() error { return e.Err }

type hostReadRunner func(context.Context, string, []string) ([]byte, error)

// Host discovery has its own closed argv set, not the delivery runner (which
// permits send). Errors never contain command output, a path or environment.
func hostReadAllowed(bin string, args []string) bool {
	if bin == "orca" {
		return slices.Equal(args, []string{"terminal", "list", "--json"})
	}
	if bin != "git" || len(args) < 4 || args[0] != "-C" || !filepath.IsAbs(args[1]) {
		return false
	}
	tail := args[2:]
	return slices.Equal(tail, []string{"worktree", "list", "--porcelain", "-z"}) ||
		slices.Equal(tail, []string{"rev-parse", "--show-toplevel"}) ||
		slices.Equal(tail, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}) ||
		slices.Equal(tail, []string{"rev-parse", "--absolute-git-dir"})
}

type hostReadBuffer struct {
	buf      bytes.Buffer
	overflow bool
}

func (b *hostReadBuffer) Write(p []byte) (int, error) {
	const max = 8 << 20
	n := len(p)
	left := max - b.buf.Len()
	if len(p) > left {
		p = p[:left]
		b.overflow = true
	}
	_, _ = b.buf.Write(p)
	return n, nil
}
func runHostRead(ctx context.Context, bin string, args []string) ([]byte, error) {
	if !hostReadAllowed(bin, args) {
		return nil, errors.New("refused host discovery operation")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = time.Second
	// No inherited server token, provider credential or GIT_CONFIG_COUNT/-c
	// injection reaches the child. Host-local discovery needs no network auth.
	for _, k := range []string{"PATH", "HOME", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "USER"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	var out hostReadBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if err != nil || out.overflow {
		return nil, errors.New("host discovery failed")
	}
	return out.buf.Bytes(), nil
}
func readHostInventory(ctx context.Context, rows []hostPlanWorker) (hostInventory, error) {
	return discoverHostInventory(ctx, rows, runHostRead)
}
func discoverHostInventory(ctx context.Context, rows []hostPlanWorker, run hostReadRunner) (hostInventory, error) {
	inv := hostInventory{Repos: map[string]string{}, Trees: []hostTree{}, Terminals: []hostTerminal{}}
	badGit := func() (hostInventory, error) {
		return inv, &hostInventoryError{"git_inventory_unavailable", exitcode.Newf(exitcode.Error, "git inventory unavailable; plan incomplete")}
	}
	// Validate the Orca envelope ourselves: the legacy status client's permissive
	// nil-list decoding is not evidence of an empty host inventory.
	raw, err := run(ctx, "orca", []string{"terminal", "list", "--json"})
	if err != nil {
		return inv, exitcode.Newf(exitcode.OrcaUnreachable, "Orca inventory unavailable")
	}
	var env struct {
		OK     bool `json:"ok"`
		Result *struct {
			Terminals []*struct {
				Handle string  `json:"handle"`
				Path   string  `json:"worktreePath"`
				Family *string `json:"agentIdentity"`
			} `json:"terminals"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &env) != nil || !env.OK || env.Result == nil || env.Result.Terminals == nil {
		return inv, exitcode.Newf(exitcode.OrcaUnreachable, "Orca inventory unavailable")
	}
	seenHandles := map[string]bool{}
	for _, t := range env.Result.Terminals {
		if t == nil || t.Handle == "" || seenHandles[t.Handle] {
			return inv, exitcode.Newf(exitcode.OrcaUnreachable, "Orca inventory unavailable")
		}
		seenHandles[t.Handle] = true
		if t.Path == "" {
			return inv, exitcode.Newf(exitcode.OrcaUnreachable, "Orca inventory unavailable")
		} // An unknown path must not turn into an absent worker resource.
		p, err := canonicalHostPath(t.Path)
		if err != nil {
			return inv, &hostInventoryError{"terminal_path_unresolved", exitcode.Newf(exitcode.Error, "terminal path unavailable; plan incomplete")}
		}
		inv.Terminals = append(inv.Terminals, hostTerminal{t.Handle, p, deref(t.Family)})
	}
	paths := map[string]bool{}
	for _, row := range rows {
		if _, ok := inv.Repos[row.Repo]; ok {
			continue
		}
		root, err := hostGitPath(ctx, run, row.Repo, "--show-toplevel")
		if err != nil || root != row.Repo {
			return badGit()
		}
		common, err := hostGitPath(ctx, run, row.Repo, "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return badGit()
		}
		inv.Repos[row.Repo] = common
		data, err := run(ctx, "git", []string{"-C", row.Repo, "worktree", "list", "--porcelain", "-z"})
		if err != nil {
			return badGit()
		}
		entries := 0
		// Porcelain -z separates records with an empty field. A prunable record
		// whose directory is absent is positive stale metadata, not an unknown
		// live worktree. Never prune it or skip an unmarked/unreadable path.
		for _, record := range strings.Split(string(data), "\x00\x00") {
			fields := strings.Split(record, "\x00")
			if len(fields) == 0 || !strings.HasPrefix(fields[0], "worktree ") {
				continue
			}
			prunable := false
			for _, field := range fields[1:] {
				if field == "prunable" || strings.HasPrefix(field, "prunable ") {
					prunable = true
				}
			}
			p, err := canonicalHostPath(strings.TrimPrefix(fields[0], "worktree "))
			if err != nil {
				if prunable && os.IsNotExist(err) {
					continue
				}
				return badGit()
			}
			paths[p] = true
			entries++
		}
		if entries == 0 {
			return badGit()
		}
	}
	for _, t := range inv.Terminals {
		// A shell outside git has no worktree binding to associate. Do not run git
		// there, and never treat its label/family as a worker identity.
		if _, err := os.Stat(filepath.Join(t.Path, ".git")); err == nil {
			paths[t.Path] = true
		} else if !os.IsNotExist(err) {
			return badGit()
		}
	}
	wanted := map[string]bool{}
	for _, row := range rows {
		wanted[row.ID] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		common, err := hostGitPath(ctx, run, path, "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return badGit()
		}
		gitdir, err := hostGitPath(ctx, run, path, "--absolute-git-dir")
		if err != nil {
			return badGit()
		}
		tree := hostTree{Path: path, Common: common}
		data, err := readHostFile(filepath.Join(gitdir, bindingFileName), 64<<10)
		if err != nil && !os.IsNotExist(err) {
			return badGit()
		}
		if err == nil {
			var b hostBinding
			if json.Unmarshal(data, &b) != nil || b.WorkerID == "" {
				return badGit()
			}
			// A successfully parsed different worker id cannot satisfy any row.
			// Legacy missing metadata for a requested worker still fails closed.
			if wanted[b.WorkerID] {
				if b.AppID == "" || b.SessionID == "" {
					return badGit()
				}
				tree.Binding = &b
			}
		}
		inv.Trees = append(inv.Trees, tree)
	}
	sort.Slice(inv.Terminals, func(i, j int) bool { return inv.Terminals[i].Handle < inv.Terminals[j].Handle })
	return inv, nil
}
func canonicalHostPath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("relative discovery path")
	}
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(p), nil
}
func hostGitPath(ctx context.Context, run hostReadRunner, repo string, args ...string) (string, error) {
	argv := append([]string{"-C", repo, "rev-parse"}, args...)
	raw, err := run(ctx, "git", argv)
	if err != nil {
		return "", err
	}
	// rev-parse adds exactly one newline; embedded newlines remain path data.
	path := strings.TrimSuffix(string(raw), "\n")
	if path == "" {
		return "", errors.New("empty git identity")
	}
	return canonicalHostPath(path)
}
func assembleHostPlan(dto *hostPlanDTO, appID, server string, inv hostInventory) {
	for i := range dto.Workers {
		w := &dto.Workers[i]
		conflict := false
		mark := func(reason string) {
			dto.Conflicts = append(dto.Conflicts, hostPlanProblem{w.Worker, reason})
			conflict = true
		}
		common, ok := inv.Repos[w.Repo]
		if !ok || common == "" {
			dto.Unavailable = append(dto.Unavailable, hostPlanProblem{w.Worker, "repository_identity_unavailable"})
			continue
		}
		for _, tree := range inv.Trees {
			if tree.Binding == nil || tree.Binding.WorkerID != w.ID {
				continue
			}
			bindingServer, err := hostServerIdentity(tree.Binding.Server)
			if err != nil {
				dto.Unavailable = append(dto.Unavailable, hostPlanProblem{w.Worker, "binding_server_unavailable"})
				conflict = true
				continue
			}
			if bindingServer != server {
				mark("binding_server_mismatch")
				continue
			}
			if tree.Binding.AppID != appID {
				mark("binding_app_mismatch")
				continue
			}
			if tree.Common != common {
				mark("binding_repository_mismatch")
				continue
			}
			w.Worktrees = append(w.Worktrees, tree.Path)
			w.Sessions = append(w.Sessions, tree.Binding.SessionID)
			for _, t := range inv.Terminals {
				if t.Path == tree.Path {
					w.Terminals = append(w.Terminals, t.Handle)
					if t.Family != "" && t.Family != w.Family {
						mark("terminal_family_mismatch")
					}
				}
			}
		}
		if len(w.Worktrees) > 1 {
			mark("multiple_bound_worktrees")
		}
		if len(w.Terminals) > 1 {
			mark("multiple_bound_terminals")
		}
		for _, problem := range dto.Conflicts {
			if problem.Worker == w.Worker {
				conflict = true
			}
		}
		if conflict {
			continue
		}
		action := func(kind string) { dto.Actions = append(dto.Actions, hostPlanAction{w.Worker, kind, w.Repo}) }
		if len(w.Worktrees) == 0 {
			action("create-worktree")
			action("open-terminal")
			action("authorize-session")
			continue
		}
		if len(w.Terminals) == 0 {
			action("open-terminal")
			continue
		}
		for _, t := range inv.Terminals {
			if slices.Contains(w.Terminals, t.Handle) && t.Family == "" {
				action("inspect-agent")
			}
		}
	}
}
