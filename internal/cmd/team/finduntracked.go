package team

// `hadron team task find-untracked` (hadron-cli#1961, cor:agt:120:04): lists
// the outside items the team's intake adapter reports that no task of the App
// holds, and with --track claims some. The adapter is a local executable that
// knows the tracker; Hadron never calls the tracker. The server-side task
// integration path (hadron-cli#1524) is retired with the server's (cli#1964):
// its flags are gone, and naming one is refused with the way out.

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

func newCmdTaskFindUntracked(f *cmdutil.Factory) *cobra.Command {
	var limit int
	var adapterPath, forWorker, assignee, owner string
	var track bool
	cmd := &cobra.Command{
		Use:   "find-untracked --adapter <exe> [flags]",
		Short: "List outside items (issues) no task claims, or claim some",
		Long: `Find outside items (for example GitHub issues) that no task of the App
holds, and optionally claim some as tasks. An outside item is a record in your
team's tracker; "tracked" means a task of this App, finished or cancelled
included, holds it as its external id (` + "`task create --external-id`" + `).
` + "`task release`" + ` gives back only the releasing task's hold: the item stays tracked
while any other task of this App still holds it.

The source is the team's intake adapter: a small executable your team owns that
talks to the tracker, so Hadron never does. Ask whoever runs your intake for its
path, or see "App properties, task externalId and find-untracked" in
` + "`hadron agentic-usage`" + ` and docs/plans/1961-intake-adapter-runner.md in the CLI
repository for the contract it must speak. Name it with --adapter <executable>,
or set $HADRON_INTAKE_ADAPTER (it counts only when no --adapter is given).
Without an adapter the command cannot run. The server's task integrations are
retired, and so are --integration and the flags that belonged to it (--team,
--no-team, --ready, --repo, --skip, --assign-to-me): naming one is refused by name
(exit 2, nothing sent), with what the adapter offers in its place.

INTAKE ADAPTER (--adapter <executable> [--for <worker>] [--track] [--assignee
<party>] [--owner <party>] [--limit N]): the team's own local
executable that knows its tracker and holds the tracker credential; Hadron
never calls the tracker. The CLI runs it as ` + "`<executable> <call>`" + ` (describe,
list-untracked, mark-tracked, untrack, report), a JSON request on stdin and one JSON answer on
stdout. Every HADRON_* environment variable is removed before invocation;
configure the tracker credential under its own name. Its output is untrusted:
capped, parsed strictly, printed terminal-safe.
An adapter that speaks another contract version exits 15 before it is asked
for anything, and before the App or Hadron is read. Its describe answer must
list the calls it answers (list-untracked, and mark-tracked for --track), else
exit 2 with nothing claimed. The adapter pages its own list by an opaque cursor, so --skip does
not exist on this path. Each listed item is checked against the App's tasks
(Hadron decides what is tracked); the rest are listed, up to --limit (1-100,
default 20). --for <worker> asks for that worker's items.

--track claims them: --limit (default 1) is how many tasks this call HOLDS. The
create is the claim: if another caller got the item first (TASK_EXTERNAL_ID_TAKEN)
you lost it, it does not count, and the next item is tried. A lost race exits 0.
The winner is told to the adapter (mark-tracked) with the task, the server's
portal link and the worker. The claim goes to --assignee, else --for, else your
bound worker. The server starts a caller-own claim atomically in the create;
the CLI reports the returned state and sends no separate start. Another
assignee's claim, or an older server's PENDING answer, stays PENDING for its
assignee to start. A refused create stops the walk after printing what was
claimed; a failed mark leaves the claim standing and exits 1.

On the adapter path, JSON complete means the list ran out without an error.
Reaching --limit is a successful stop (exit 0), with complete:false and
stoppedReason:"limit_reached"; no extra page is fetched. Both list and track
report stoppedReason: null on exhaustion, "limit_reached" on that successful
limit stop, otherwise the stage that failed — check_failed, walk_failed and
reconcile_failed on both list and track; claim_failed and mark_failed only
under --track.

RECONCILE (every adapter run, before the list). The CLI reads, from the server,
what changed on the App's tasks that carry an outside item since its cursor, and
tells the adapter what its tracker should hear: a reassignment calls mark-tracked
again for the new assignee, a release (` + "`task release`" + `) calls untrack with the
released item, a cancel calls report only if the adapter's describe lists it, and
a finish, hand-back or first assignment call nothing. The cursor is the server's
own opaque value, kept per server, App and adapter in a file beside the CLI's
config (intake-cursors.json); one run reconciles a feed at a time (a second
waits up to 2 minutes, then exits 5); the first run reads from the oldest change, a later
run only what is new, and nothing is told twice. A change the adapter refuses
holds the cursor there so the next run retries it: the list and claim still run,
the run exits 1, and the output's reconcile object names the refusal. An
adapter whose describe does not list a call is simply not told. The summary is a
note on stderr, and the reconcile object in --json.`,
		Example: `  # with your team's intake adapter: list one worker's items, then claim three
  hadron team task find-untracked --adapter ~/bin/hadron-task-adapter-github --for Marco
  hadron team task find-untracked --adapter ~/bin/hadron-task-adapter-github --track --limit 3`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u := adapterFlagUse{track: track, forWorker: forWorker, assignee: assignee, owner: owner, limit: limit}
			path, err := selectAdapter(cmd, adapterPath, u)
			if err != nil {
				return err
			}
			return runAdapterCommand(cmd, f, path, u)
		},
	}
	cmd.Flags().StringVar(&adapterPath, "adapter", "", "the team's intake adapter executable (else $"+adapterEnv+")")
	cmd.Flags().BoolVar(&track, "track", false, "with an adapter: claim items as tasks (the create is the claim); --limit is how many to hold")
	cmd.Flags().StringVar(&forWorker, "for", "", "with an adapter: only `worker` X's items, and X holds the claims unless --assignee names another")
	cmd.Flags().IntVar(&limit, "limit", 0, "items to list (1-100, default 20), or with --track how many claims to hold (default 1)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "with --track: the claim's assignee, worker:<ref> or user:<ref>, sent as written (an App key names both parties)")
	cmd.Flags().StringVar(&owner, "owner", "", "with --track: the claim's owner, worker:<ref> or user:<ref>, sent as written (an App key names both parties)")
	// The flags of the retired server integration path are gone, so cobra
	// rejects them while parsing, before RunE. A stale call (a script or a
	// prompt still naming --integration or --ready) gets the retirement and
	// the way out instead of a bare "unknown flag" (cli#1964).
	//
	// Every other flag error goes to the parent's handler (the root's, which
	// makes it a usage error, exit 2): setting a handler here would otherwise
	// shadow it, and a malformed value such as --limit abc would exit 1.
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		var unknown *pflag.NotExistError
		if errors.As(err, &unknown) && unknown.GetSpecifiedShortnames() == "" {
			if _, retired := retiredFindUntrackedFlags[unknown.GetSpecifiedName()]; retired {
				return errFlagRetired(unknown.GetSpecifiedName())
			}
		}
		if p := c.Parent(); p != nil {
			return p.FlagErrorFunc()(c, err)
		}
		return err
	})
	return cmd
}
