package team

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/filterreport"
	"github.com/hadron-memory/hadron-cli/internal/orcaterm"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Linker-overridable only for compiled fake-/proc QA; no runtime input changes
// the production /proc source. The diagnostic has no process-control interface.
var findFilteredProcRoot = "/proc"

const findFilteredHostRefusal = "Orca-hosted workers only: an Orca CLI and reachable server are required; nothing was queued or changed"

type filterSources struct {
	runner   func() (orcaterm.Runner, bool)
	targets  func(context.Context, string, []string) ([]filterreport.Target, error)
	procRoot string
}

func newCmdWorkerFindFiltered(f *cmdutil.Factory) *cobra.Command {
	return newCmdWorkerFindFilteredWithSources(f, filterSources{
		runner: func() (orcaterm.Runner, bool) {
			bin, err := exec.LookPath("orca")
			if err != nil {
				return nil, false
			}
			return orcaterm.Exec(bin, 10*time.Second), true
		},
		targets: func(ctx context.Context, app string, args []string) ([]filterreport.Target, error) {
			gql, err := f.GraphQLClient()
			if err != nil {
				return nil, err
			}
			workers, err := scanWorkerRoster(ctx, gql, app)
			if err != nil {
				return nil, err
			}
			targets := []filterreport.Target{}
			for _, w := range workers {
				if w.RetiredAt != nil {
					continue
				}
				if len(args) > 0 && !strings.EqualFold(args[0], w.Name) && args[0] != w.Id && (w.Urn == nil || args[0] != *w.Urn) {
					continue
				}
				targets = append(targets, filterreport.Target{Name: w.Name, ID: w.Id})
			}
			return targets, nil
		}, procRoot: findFilteredProcRoot,
	})
}

func newCmdWorkerFindFilteredWithSources(f *cmdutil.Factory, source filterSources) *cobra.Command {
	return &cobra.Command{
		Use: "find-filtered [worker]", Short: "Report provider filter notices on local worker terminals",
		Long: `Read-only, operator-run, report-only inspection of this team's Orca terminals.
A full idle provider notice is BLOCKED; incomplete evidence or a running turn
is AMBIGUOUS. Other screens are NOT_BLOCKED; unreadable screens are UNREADABLE.
No screen or draft text is printed. The agent_tree column is diagnostic context,
never authority to act. Process checks require /proc and the invoking uid.

Only Orca terminal list/read are used. Nothing is signalled, written, delivered,
restarted or changed. The team comes from --app, the App context, or this
worktree's local binding. A named worker must belong to that team. Unbound or
unreadable local worktrees cannot be attributed and are omitted; multiple
terminals for one worker are AMBIGUOUS. Exit 0 means the scan ran, including
when BLOCKED rows exist; a non-Orca host refuses with exit 2, a failed scan 1.
--json returns an array of worker/worktree/terminal/classification/agent_tree/note
objects, including an empty array when there are no local workers.`,
		Example: "  hadron team worker find-filtered --app acme.com:eng-team\n  hadron team worker find-filtered Iris --json",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// #953 ruling 6975717606: a local context check, then terminal list;
			// never reuse onboard's status preflight or git-based binding lookup.
			runner, available := source.runner()
			if !available {
				return exitcode.Newf(exitcode.Usage, findFilteredHostRefusal)
			}
			client := orcaterm.NewReadOnlyClient(runner)
			terms, err := client.List(cmd.Context())
			if err != nil {
				return exitcode.Newf(exitcode.Usage, findFilteredHostRefusal)
			}
			app, err := f.App()
			if err != nil {
				return exitcode.Newf(exitcode.Error, "app_context_unreadable")
			}
			if app == "" {
				cwd, e := os.Getwd()
				if e != nil {
					return exitcode.Newf(exitcode.Error, "worktree_unreadable")
				}
				b, e := filterreport.ReadBinding(cwd)
				if e != nil || b.AppID == "" {
					return exitcode.Newf(exitcode.Usage, "no team App; pass --app or run from a bound worktree")
				}
				app = b.AppID
			}
			targets, err := source.targets(cmd.Context(), app, args)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "team_roster_unreadable")
			}
			if len(args) > 0 && len(targets) == 0 {
				return exitcode.Newf(exitcode.Error, "worker_not_in_team")
			}
			server, err := f.Server()
			if err != nil {
				return exitcode.Newf(exitcode.Error, "server_context_unreadable")
			}
			rows, err := filterreport.Scan(cmd.Context(), client, terms, targets, server, source.procRoot, os.Getuid())
			if err != nil {
				return exitcode.Newf(exitcode.Error, "filter_scan_failed")
			}
			if len(args) > 0 && len(rows) == 0 {
				rows = append(rows, filterreport.Row{Worker: output.SafeLine(targets[0].Name, 256), Classification: "UNREADABLE", AgentTree: "no_wrapper", Note: "no_terminal"})
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				table := output.NewTable(w, "WORKER", "WORKTREE", "TERMINAL", "CLASSIFICATION", "AGENT TREE", "NOTE")
				blocked := false
				for _, r := range rows {
					table.Row(r.Worker, r.Worktree, r.Terminal, r.Classification, r.AgentTree, r.Note)
					blocked = blocked || r.Classification == "BLOCKED"
				}
				if err := table.Flush(); err != nil {
					return err
				}
				if blocked {
					_, err := fmt.Fprintln(w, "The block is the provider's. Daybreak access or Claude capacity is the human's route.")
					return err
				}
				return nil
			})
		},
	}
}
