package team

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
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

// filteredWorkerDTO is the command-owned stable JSON contract, independent of
// generated API types and the scanner's internal observations.
type filteredWorkerDTO struct {
	Worker         string `json:"worker"`
	Worktree       string `json:"worktree"`
	Terminal       string `json:"terminal"`
	Classification string `json:"classification"`
	AgentTree      string `json:"agent_tree"`
	Note           string `json:"note"`
}

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
			return findFilteredTargets(ctx, f, app, args)
		}, procRoot: findFilteredProcRoot,
	})
}

func newCmdWorkerFindFilteredWithSources(f *cmdutil.Factory, source filterSources) *cobra.Command {
	return &cobra.Command{
		Use: "find-filtered [worker]", Short: "Report provider filter notices on local worker terminals",
		Long: `Read-only, operator-run, report-only inspection of this team's Orca terminals.
A full idle provider notice is BLOCKED; incomplete evidence or a running turn
is AMBIGUOUS. Other screens are NOT_BLOCKED; unreadable screens are UNREADABLE.
Sign in with a credential that can read the selected team App first.
No screen or draft text is printed. The agent_tree column is diagnostic context,
never authority to act. Process checks require /proc and the invoking uid.

Only Orca terminal list/read are used. Nothing is signalled, written, delivered,
restarted or changed. The team comes from --app, the App context, or this
worktree's local binding. A named worker must belong to that team. A binding identifies a worker, not a live
session; local terminals of retired workers remain diagnostic rows. Unbound or
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
				b, e := filterreport.ReadNearestBinding(cwd)
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
			dto := []filteredWorkerDTO{}
			for _, r := range rows {
				dto = append(dto, filteredWorkerDTO{r.Worker, r.Worktree, r.Terminal, r.Classification, r.AgentTree, r.Note})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
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

// PublicGraphQLClient preserves credentials and transport security without the
// authored-write snapshot hook (that hook reads the binding through git even
// for a query). Require authentication here: this is a private team read, not
// anonymous access. No write operation is called by this reader.
func findFilteredTargets(ctx context.Context, f *cmdutil.Factory, app string, args []string) ([]filterreport.Target, error) {
	gql, authenticated, err := f.PublicGraphQLClient()
	if err != nil {
		return nil, err
	}
	if !authenticated {
		return nil, exitcode.Newf(exitcode.Error, "team_roster_unreadable")
	}
	targets := []filterreport.Target{}
	if len(args) > 0 {
		response, err := gen.FilteredWorkerTarget953(ctx, gql, app, args[0])
		if err != nil {
			return nil, err
		}
		w := response.Worker
		if w == nil {
			return targets, nil
		}
		// A name is scoped by appRef. Explicit ids/URNs need the returned App
		// identity too, since the server gives explicit references precedence.
		if w.Id == "" || w.Name == "" || w.AppId == "" {
			return nil, exitcode.Newf(exitcode.Error, "team_roster_unreadable")
		}
		if directWorkerRef(args[0]) && w.AppId != app {
			if w.AppUrn == nil || *w.AppUrn == "" {
				return nil, exitcode.Newf(exitcode.Error, "worker_app_unavailable")
			}
			returnedApp, e := cmdutil.CanonicalAppRef("returned App", *w.AppUrn)
			if e != nil {
				return nil, exitcode.Newf(exitcode.Error, "worker_app_unavailable")
			}
			if returnedApp != app {
				return targets, nil
			}
		}
		return []filterreport.Target{{Name: w.Name, ID: w.Id}}, nil
	}
	seen := map[string]bool{}
	for offset := 0; ; {
		response, err := gen.FilteredWorkerRoster953(ctx, gql, app, workerPageSize, offset)
		if err != nil {
			return nil, err
		}
		page := response.Workers
		if page == nil || page.Items == nil || page.Total < 0 {
			return nil, exitcode.Newf(exitcode.Error, "team_roster_unreadable")
		}
		for _, w := range page.Items {
			if w == nil || w.Id == "" || w.Name == "" {
				return nil, exitcode.Newf(exitcode.Error, "team_roster_unreadable")
			}
			if !seen[w.Id] {
				targets = append(targets, filterreport.Target{Name: w.Name, ID: w.Id})
				seen[w.Id] = true
			}
		}
		offset += len(page.Items)
		if offset >= page.Total {
			return targets, nil
		}
		if len(page.Items) == 0 {
			return nil, exitcode.Newf(exitcode.Error, "team_roster_unreadable")
		}
	}
}
