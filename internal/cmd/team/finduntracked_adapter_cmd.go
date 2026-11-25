package team

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/intake"
)

// adapterFlagUse is the flag values the adapter selection judges.
type adapterFlagUse struct {
	forWorker, assignee, owner string
	limit                      int
	track                      bool
}

// retiredFindUntrackedFlags are the flags of the server's task integration
// path, retired with it (cli#1964), each with what the intake adapter offers
// in its place. Naming one is refused by name (the first one named in argv).
var retiredFindUntrackedFlags = map[string]string{
	"integration":  "",
	"team":         "with the adapter, --for <worker> names one worker's items",
	"no-team":      "the adapter filters by its own configuration",
	"ready":        "the adapter filters by its own configuration",
	"repo":         "the adapter knows its own repositories",
	"skip":         "the adapter pages by its own cursor; --limit bounds the list",
	"assign-to-me": "with the adapter, --track claims items",
}

// errFlagRetired is find-untracked's answer to a retired flag: the server's
// task integrations are retired, and the team's intake adapter replaced them.
// It is a parse error, so nothing is sent.
func errFlagRetired(name string) error {
	if name == "integration" {
		return exitcode.Newf(exitcode.Usage, "--integration is retired: the server no longer serves task integrations, so nothing was sent. Use the team's intake adapter instead: --adapter <executable> (or set $%s); see the INTAKE ADAPTER section of --help", adapterEnv)
	}
	return exitcode.Newf(exitcode.Usage, "--%s is retired with --integration: the server no longer serves task integrations, so nothing was sent; %s. The source is the team's intake adapter: --adapter <executable> (or set $%s); see the INTAKE ADAPTER section of --help", name, retiredFindUntrackedFlags[name], adapterEnv)
}

// selectAdapter names the intake adapter a find-untracked call runs and
// refuses an incomplete call, before any request or binding read.
//
// The adapter is named by --adapter, else $HADRON_INTAKE_ADAPTER. Nothing is
// chosen silently: a call with neither is a usage error.
func selectAdapter(cmd *cobra.Command, adapterFlag string, u adapterFlagUse) (string, error) {
	fl := cmd.Flags()
	adapterGiven := fl.Changed("adapter")
	if adapterGiven && strings.TrimSpace(adapterFlag) == "" {
		return "", exitcode.Newf(exitcode.Usage, "--adapter needs the adapter executable's path")
	}
	path := strings.TrimSpace(adapterFlag)
	if !adapterGiven {
		path = strings.TrimSpace(os.Getenv(adapterEnv))
	}
	if path == "" {
		if fl.Changed("track") || fl.Changed("for") {
			return "", exitcode.Newf(exitcode.Usage, "--track and --for need an intake adapter: pass --adapter <executable> or set $%s; nothing was sent", adapterEnv)
		}
		return "", exitcode.Newf(exitcode.Usage, "name where the items come from: --adapter <executable> (or $%s), the team's intake adapter; nothing was sent", adapterEnv)
	}
	if !u.track && (fl.Changed("assignee") || fl.Changed("owner")) {
		return "", exitcode.Newf(exitcode.Usage, "--assignee and --owner name a claim's parties: they need --track (a list takes neither); nothing was sent")
	}
	if fl.Changed("for") && strings.TrimSpace(u.forWorker) == "" {
		return "", exitcode.Newf(exitcode.Usage, "--for needs a worker name")
	}
	if fl.Changed("limit") && (u.limit < 1 || u.limit > adapterMaxLimit) {
		return "", exitcode.Newf(exitcode.Usage, "--limit takes 1 to %d with an intake adapter", adapterMaxLimit)
	}
	return path, nil
}

// runAdapterCommand resolves the App and the caller's binding, parses the
// parties, and runs the adapter flow.
func runAdapterCommand(cmd *cobra.Command, f *cmdutil.Factory, path string, u adapterFlagUse) error {
	root := cmd.Context()
	if root == nil {
		root = context.Background()
	}
	// Nothing at the CLI root turns an interrupt into a context cancel, so
	// without this Ctrl-C kills the process by signal and never reaches the
	// documented Cancelled exit (QA on cli#1971; same as chat wait).
	ctx, stopSignals := signal.NotifyContext(root, os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	// Restore the default disposition after cancellation so a second signal
	// can force exit while the adapter drains its pipes.
	go func() {
		<-ctx.Done()
		stopSignals()
	}()
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
