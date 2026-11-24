package team

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// Every worker on a host shares one OS user, so the cwd's binding says whose
// worktree this is, not who is typing (cli#3091: `mark-read` run from a
// teammate's worktree moved the teammate's cursor). The invoker signal is the
// worktree the launcher opened the invoking terminal in: Orca exports
// ORCA_WORKSPACE_ID as "<id>::<worktree path>" per terminal, and a worker's
// shell keeps it when it cd's elsewhere. A mutation that would carry the cwd
// binding's session while the cwd is another worktree is refused, nothing
// sent. Without the signal (no launcher, no worktree)
// nothing changes.

// ForeignBindingOptInEnv names the explicit opt-in: set to 1 to act through
// another worktree's binding on purpose.
const ForeignBindingOptInEnv = "HADRON_ALLOW_FOREIGN_BINDING"

// invokerWorkspaceEnv is the launcher's per-terminal worktree signal.
const invokerWorkspaceEnv = "ORCA_WORKSPACE_ID"

// GitToplevelRunner asks git for the current directory's worktree root. A seam
// for tests, like GitDirRunner.
type GitToplevelRunner func(ctx context.Context) (string, error)

var runGitToplevel GitToplevelRunner = func(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// SetGitToplevelRunner replaces the git call for a test. Tests only.
func SetGitToplevelRunner(r GitToplevelRunner) (restore func()) {
	prev := runGitToplevel
	runGitToplevel = r
	return func() { runGitToplevel = prev }
}

func invokerWorktree() string {
	v := os.Getenv(invokerWorkspaceEnv)
	i := strings.LastIndex(v, "::")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(v[i+2:])
}

func samePath(a, b string) bool {
	norm := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return filepath.Clean(p)
	}
	return norm(a) == norm(b)
}

// foreignBindingRefusal is nil unless the cwd's worktree holds a worker
// binding and the invoking terminal belongs to a different worktree.
func foreignBindingRefusal(ctx context.Context) error {
	if os.Getenv(ForeignBindingOptInEnv) == "1" {
		return nil
	}
	invoker := invokerWorktree()
	if invoker == "" {
		return nil
	}
	top, err := runGitToplevel(ctx)
	if err != nil || top == "" || samePath(invoker, top) {
		return nil // no worktree here (cli#1855) or it is the invoker's own
	}
	b, _, err := readBindingFile(ctx)
	if err != nil || b == nil || b.SessionID == "" {
		return nil // no binding: nothing foreign would be attached
	}
	who := b.WorkerName
	if who == "" {
		who = "another worker"
	}
	return exitcode.Newf(exitcode.Usage,
		"this worktree (%s) is bound to %s, but your terminal belongs to %s; the write would act as %s, so nothing was sent. Run it from your own worktree, or set %s=1 to act through this binding on purpose",
		top, who, invoker, who, ForeignBindingOptInEnv)
}

// ForeignBindingGuard is the per-request check behind
// Factory.GraphQLRequestGuard: it refuses an authored write that would carry a
// foreign worktree's session.
func ForeignBindingGuard() func(context.Context, *graphql.Request) error {
	return func(ctx context.Context, req *graphql.Request) error {
		if !authoredWriteRequest(req) || asPerson(ctx) {
			return nil
		}
		return foreignBindingRefusal(ctx)
	}
}
