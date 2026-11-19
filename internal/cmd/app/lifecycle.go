package app

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// The App lifecycle (cli#1917, hadron-server#3774, cor:dmo:050:21; Holger's
// option A, R4): each command calls exactly one mutation and reports the
// RESULTING state in the spec's words. uninstallApp/reinstallApp answer true
// for a real move and for a repeat alike, so no output claims a transition;
// a repeat deleteApp is refused (APP_DELETED by id, hadron-server#3975).
const (
	appStateDeleted     = "deleted"
	appStateUninstalled = "uninstalled"
	appStateInstalled   = "installed"
)

// appStateDTO is the --json shape of all three: {app, status}.
type appStateDTO struct {
	App    string `json:"app"`
	Status string `json:"status"`
}

// lifecycleOptions keeps each command's mutation, confirmation and receipt together.
type lifecycleOptions struct {
	confirm func(ref string) error
	call    func(ctx context.Context, client graphql.Client, ref string) error
	state   string
	text    func(ref string) string
}

// runAppLifecycle canonicalizes the reference without a lookup or preflight.
// IDs and URNs retain their identity; org:slug becomes a v2 App URN.
// The server's extensions.code is kept as the JSON envelope's serverCode.
func runAppLifecycle(cmd *cobra.Command, f *cmdutil.Factory, raw string, opts lifecycleOptions) error {
	appRef, err := cmdutil.CanonicalAppRef("<app-ref>", raw)
	if err != nil {
		return err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	if opts.confirm != nil {
		if err := opts.confirm(appRef); err != nil {
			return err
		}
	}
	if err := opts.call(cmd.Context(), client, appRef); err != nil {
		return api.MapErrorKeepingCode(err)
	}
	return output.Write(f.IOStreams, f.JSON, appStateDTO{App: appRef, Status: opts.state}, func(w io.Writer) error {
		_, err := fmt.Fprintln(w, opts.text(appRef))
		return err
	})
}

// reinstallHint names the App by id when the caller gave one: once its handle
// is taken, a URN names the App holding it, not this one.
func reinstallHint(ref string) string {
	if cmdutil.IsAppID(ref) {
		return "hadron app reinstall " + ref
	}
	return "hadron app reinstall <its id>"
}

func newCmdDelete(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <app-ref>",
		Aliases: []string{"rm"},
		Short:   "Delete an App: it stops serving, with no undelete",
		Long: `Delete an App. It stops serving requests and is kept, with its install
memories, as history. There is no undelete. Its handle (the App's name as a URN,
hrn:app:<org>:<slug>) can later be taken by a new App. To stop an App being
written to and keep the way back, use "hadron app uninstall" instead.

Name the App by ID, by URN, or as org:slug. A URN names only the installed App
that holds it at that moment, so to delete an App that is already uninstalled,
name it by its id: its old URN may by now name a different App, and a delete by
that URN would delete that one. So delete by id from the start (find it with
"hadron app get"), never by URN. The server decides who
may delete. A missing App and a caller without the right get the same refusal
(exit 8); a repeat delete by ID is refused APP_DELETED (exit 5).

Asks for confirmation on a terminal; a script passes --yes. Prints "✓ App
<ref> is deleted: ..."; --json is {"app": <ref>, "status": "deleted"}.`,
		Example: `  hadron app delete capp100000000000000000000 --yes
  hadron app rm hrn:app:acme.com:support --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppLifecycle(cmd, f, args[0], lifecycleOptions{
				confirm: func(ref string) error {
					what := "App " + ref
					if !cmdutil.IsAppID(ref) {
						what += " (the installed App currently holding this URN)"
					}
					return cmdutil.ConfirmDeletion(f.IOStreams, yes, what)
				},
				call: func(ctx context.Context, c graphql.Client, ref string) error {
					_, err := gen.DeleteApp(ctx, c, ref)
					return err
				},
				state: appStateDeleted,
				text: func(ref string) string {
					return fmt.Sprintf("✓ App %s is deleted: no longer serving, kept as history, no undelete.", ref)
				},
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdUninstall(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall <app-ref>",
		Short: "Uninstall an App: kept read-only, handle freed, reversible",
		Long: `Take an App out of service without deleting it. The App is kept but read-only:
every write through it is refused (APP_UNINSTALLED). Its handle (the App's name
as a URN, hrn:app:<org>:<slug>) is freed, so another App may take it, and its
workers' open dark-worker alarms (raised when a worker leaves a wake-up call
unacknowledged too long) end. Undo this with "hadron app reinstall". To remove
the App for good, use "hadron app delete". Uninstall no longer deletes (it did
in older releases): deleting is "hadron app delete", alias "rm".

Name the App by ID, by URN, or as org:slug. A URN names the installed App that
holds it, else the one uninstalled App that has it. Whoever may delete the App
may uninstall it; anyone else, and an App that does not exist, get the same
refusal. A deleted App is refused (APP_DELETED).

Asks for confirmation on a terminal; a script passes --yes. Prints "✓ App
<ref> is uninstalled: ..."; --json is {"app": <ref>, "status":
"uninstalled"}. A repeat changes nothing and prints the same.`,
		Example: `  hadron app uninstall capp100000000000000000000 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppLifecycle(cmd, f, args[0], lifecycleOptions{
				confirm: func(ref string) error {
					return cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
						"Uninstall App %s? It stays, read-only (writes refused), and its handle is freed; undo with hadron app reinstall.", ref))
				},
				call: func(ctx context.Context, c graphql.Client, ref string) error {
					_, err := gen.UninstallApp(ctx, c, ref)
					return err
				},
				state: appStateUninstalled,
				text: func(ref string) string {
					return fmt.Sprintf("✓ App %s is uninstalled: kept, read-only (writes are refused), and its handle is free for another App. Reverse with: %s",
						ref, reinstallHint(ref))
				},
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdReinstall(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "reinstall <app-ref>",
		Short: "Reinstall an uninstalled App: the same App, nothing new",
		Long: `Put an uninstalled App back in service: the same App, with its id, keys,
members and memories, is installed again. Nothing new is created.

It is refused while another installed App holds the App's handle
(APP_URN_IN_USE, exit 5): ask whoever manages that App to uninstall or delete
it first. A deleted App is refused (APP_DELETED, exit 5); there is no undelete.
The same people may reinstall as may uninstall.

Name the App by ID, by URN, or as org:slug. A URN names the installed App that
holds it, else the one uninstalled App that has it; if two or more uninstalled
Apps have it, name one by ID.

Prints "✓ App <ref> is installed"; --json is {"app": <ref>, "status":
"installed"}. A repeat on an installed App changes nothing and prints the same.`,
		Example: `  hadron app reinstall capp100000000000000000000`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppLifecycle(cmd, f, args[0], lifecycleOptions{
				call: func(ctx context.Context, c graphql.Client, ref string) error {
					_, err := gen.ReinstallApp(ctx, c, ref)
					return err
				},
				state: appStateInstalled,
				text: func(ref string) string {
					return fmt.Sprintf("✓ App %s is installed: the same App, with its id, keys, members and memories.", ref)
				},
			})
		},
	}
}
