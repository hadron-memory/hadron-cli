package team

import (
	"context"
	"fmt"
	"sync"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// ApprovalAsPersonNote is the hook behind Factory.NoteApprovingAsPerson
// (cli#1843). An approval acts as the signed-in person unless --as-worker says
// otherwise (cli#1872, ruling R10), and nothing on screen said so when the
// worktree is bound to a worker session. From a bound worktree it prints ONE
// stderr line per run saying whose approval this is. Nothing sent changes.
//
// The binding is only read, never verified: a note is not an attribution, so
// an unreadable, missing or unbound state prints nothing (an unbound cwd is
// the #1855 note's case, and --as-worker has its own refusals).
//
// asWorkerFlag says whether the command has --as-worker to point at; `memory
// approve-all` does not, so its note stops at who approves. The grant clause
// names a boot prompt only: an ordinary node's approval follows cor:dmo:120:02
// and needs no grant (cor:agt:040:18).
func ApprovalAsPersonNote(f *cmdutil.Factory) func(context.Context, bool) {
	var once sync.Once
	return func(ctx context.Context, asWorkerFlag bool) {
		once.Do(func() {
			b, _, err := readBindingFile(ctx)
			if err != nil || b == nil || b.SessionID == "" {
				return
			}
			who := "the bound worker"
			if b.WorkerName != "" {
				who = "the bound worker " + b.WorkerName
			}
			line := fmt.Sprintf("note: approving as you (the signed-in person), not as %s.", who)
			if asWorkerFlag {
				line += " --as-worker sends the worker session instead; a boot prompt then needs a batch grant a person signed (cor:agt:040:18)."
			}
			fmt.Fprintln(f.IOStreams.ErrOut, line)
		})
	}
}
