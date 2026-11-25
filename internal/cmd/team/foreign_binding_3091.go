package team

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/vektah/gqlparser/v2/ast"
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
	return foreignBindingRefusalIf(ctx, nil)
}

// foreignBindingRefusalIf refuses only when uses (if given) says the request
// involves the foreign binding's session.
func foreignBindingRefusalIf(ctx context.Context, uses func(b *binding) bool) error {
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
	if uses != nil && !uses(b) {
		return nil
	}
	who := b.WorkerName
	if who == "" {
		who = "another worker"
	}
	return exitcode.Newf(exitcode.Usage,
		"this worktree (%s) is bound to %s, but your terminal belongs to %s; the write would act as %s, so nothing was sent. Run it from your own worktree, or set %s=1 to act through this binding on purpose",
		top, who, invoker, who, ForeignBindingOptInEnv)
}

// mutationRequest reports whether the request is a GraphQL mutation.
func mutationRequest(req *graphql.Request) bool {
	doc, err := parseQuery(&ast.Source{Input: req.Query})
	if err != nil {
		return false
	}
	op := doc.Operations.ForName(req.OpName)
	if op == nil && req.OpName == "" && len(doc.Operations) == 1 {
		op = doc.Operations[0]
	}
	return op != nil && op.Operation == ast.Mutation
}

// carriesSession reports whether a mutation names the binding's session in
// its variables (chat post's sessionRef, session end's id, work records...):
// those act as the bound worker without riding the X-Hadron-Session header.
func carriesSession(req *graphql.Request, b *binding) bool {
	if b.SessionID == "" || req.Variables == nil {
		return false
	}
	raw, err := json.Marshal(req.Variables)
	return err == nil && strings.Contains(string(raw), `"`+b.SessionID+`"`)
}

// Ordering invariant: this guard runs BEFORE AuthoredWriteContext decorates the
// call, and a command that deliberately acts as the person (withAsPerson,
// `chat post --as-me`) either marks its ctx or names no session, so it passes.
// A guard error fails open only where the signal cannot be established (no
// launcher env, git or binding unreadable): those cases never refuse.
//
// ForeignBindingGuard is the per-request check behind
// Factory.GraphQLRequestGuard: it refuses a mutation that would act through a
// foreign worktree's worker session, as an authored write (the header) or by
// naming that session in its variables.
func ForeignBindingGuard() func(context.Context, *graphql.Request) error {
	return func(ctx context.Context, req *graphql.Request) error {
		if asPerson(ctx) {
			return nil
		}
		if authoredWriteRequest(req) {
			return foreignBindingRefusal(ctx)
		}
		if !mutationRequest(req) {
			return nil
		}
		return foreignBindingRefusalIf(ctx, func(b *binding) bool { return carriesSession(req, b) })
	}
}
