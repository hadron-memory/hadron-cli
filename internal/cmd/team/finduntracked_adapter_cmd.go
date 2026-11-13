package team

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/intake"
)

// adapterFlagUse is the flag values the adapter selection judges.
type adapterFlagUse struct {
	team, ready, repo, forWorker, assignee, owner string
	skip, limit                                   int
	assignToMe, noTeam, track                     bool
}

// selectAdapter decides which find-untracked path a call takes and refuses the
// flags that belong to the other, before any request or binding read.
//
// The adapter is named by --adapter, else $HADRON_INTAKE_ADAPTER; --integration
// names the server-side (compat-window) path and wins over the environment,
// never over an explicit --adapter. Nothing is chosen silently: a call with
// neither is a usage error.
func selectAdapter(cmd *cobra.Command, adapterFlag, integration string, u adapterFlagUse) (string, bool, error) {
	fl := cmd.Flags()
	adapterGiven := fl.Changed("adapter")
	integrationGiven := fl.Changed("integration") && strings.TrimSpace(integration) != ""
	if adapterGiven && strings.TrimSpace(adapterFlag) == "" {
		return "", false, exitcode.Newf(exitcode.Usage, "--adapter needs the adapter executable's path")
	}
	if adapterGiven && integrationGiven {
		return "", false, exitcode.Newf(exitcode.Usage, "--adapter and --integration cannot be combined: --adapter asks the team's intake adapter, --integration the server's task integration; nothing was sent")
	}
	path := strings.TrimSpace(adapterFlag)
	if !adapterGiven && !integrationGiven {
		path = strings.TrimSpace(os.Getenv(adapterEnv))
	}
	if path == "" {
		if fl.Changed("track") || fl.Changed("for") {
			return "", false, exitcode.Newf(exitcode.Usage, "--track and --for need an intake adapter: pass --adapter <executable> or set $%s; nothing was sent", adapterEnv)
		}
		if !integrationGiven {
			return "", false, exitcode.Newf(exitcode.Usage, "name where the items come from: --adapter <executable> (or $%s) for the team's intake adapter, or --integration <name> for the server's task integration", adapterEnv)
		}
		return "", false, nil
	}
	// The adapter path. What belongs to the server's integration is refused
	// loudly rather than ignored.
	for _, bad := range []struct {
		set  bool
		flag string
		why  string
	}{
		{fl.Changed("team"), "--team", "a lead resolves to their team in the adapter's own config, so no tracker label reaches Hadron; use --for"},
		{fl.Changed("no-team"), "--no-team", "it filters the server integration's team labels"},
		{fl.Changed("ready"), "--ready", "it filters the server integration's ready label"},
		{fl.Changed("repo"), "--repo", "it names a repository connected to the server integration"},
		{fl.Changed("skip"), "--skip", "an adapter is paged by its opaque cursor, never an offset (cor:agt:120:04)"},
		{fl.Changed("assign-to-me"), "--assign-to-me", "an adapter claims with --track"},
	} {
		if bad.set {
			return "", false, exitcode.Newf(exitcode.Usage, "%s does not combine with an intake adapter: %s; nothing was sent", bad.flag, bad.why)
		}
	}
	if !u.track && (fl.Changed("assignee") || fl.Changed("owner")) {
		return "", false, exitcode.Newf(exitcode.Usage, "--assignee and --owner name a claim's parties: they need --track (a list takes neither); nothing was sent")
	}
	if fl.Changed("for") && strings.TrimSpace(u.forWorker) == "" {
		return "", false, exitcode.Newf(exitcode.Usage, "--for needs a worker name")
	}
	if fl.Changed("limit") && (u.limit < 1 || u.limit > adapterMaxLimit) {
		return "", false, exitcode.Newf(exitcode.Usage, "--limit takes 1 to %d with an intake adapter, got %d", adapterMaxLimit, u.limit)
	}
	return path, true, nil
}

// runAdapterCommand resolves the App and the caller's binding, parses the
// parties, and runs the adapter flow.
func runAdapterCommand(cmd *cobra.Command, f *cmdutil.Factory, path string, u adapterFlagUse) error {
	ctx := cmd.Context()
	o := adapterOpts{path: path, forWorker: strings.TrimSpace(u.forWorker), track: u.track}
	if cmd.Flags().Changed("limit") {
		o.limit = u.limit
	}
	var err error
	if cmd.Flags().Changed("assignee") {
		if o.assignee, err = parseTaskPartyFlag("--assignee", u.assignee); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("owner") {
		if o.owner, err = parseTaskPartyFlag("--owner", u.owner); err != nil {
			return err
		}
	}
	// The adapter is described FIRST: a skewed or incapable adapter is refused
	// before the binding, the App or Hadron is read (cor:agt:120:04).
	runner := &intake.Runner{Path: path}
	desc, err := runner.Describe(ctx)
	if err != nil {
		return adapterErr(err)
	}
	if err := requireCalls(desc, o.track); err != nil {
		return err
	}
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil && !errors.Is(err, errNoWorktree) {
		return err
	}
	scope, err := resolveTeamAppScope(ctx, f, b)
	if err != nil {
		return err
	}
	return runFindUntrackedAdapter(ctx, f, b, scope.Ref, runner, desc, o)
}
