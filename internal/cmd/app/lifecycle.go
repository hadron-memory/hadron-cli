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

// runAppLifecycle sends the ref to the server unchanged (an id, or a URN the
// server resolves): no client lookup or preflight read, so a missing App and
// a refused caller get whatever the server answers: the CLI adds no
// existence oracle of its own. The server's extensions.code is kept as the
// --json envelope's serverCode.
func runAppLifecycle(cmd *cobra.Command, f *cmdutil.Factory, raw string, confirm func(ref string) error,
	call func(ctx context.Context, client graphql.Client, ref string) error, state string, text func(ref string) string) error {
	appRef, err := cmdutil.CanonicalAppRef("<app-ref>", raw)
	if err != nil {
		return err
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	if confirm != nil {
		if err := confirm(appRef); err != nil {
			return err
		}
	}
	if err := call(cmd.Context(), client, appRef); err != nil {
		return api.MapErrorKeepingCode(err)
	}
	return output.Write(f.IOStreams, f.JSON, appStateDTO{App: appRef, Status: state}, func(w io.Writer) error {
		_, err := fmt.Fprintln(w, text(appRef))
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
		Long: `Delete an App (cor:dmo:050:21). It stops serving and is kept, with its
install memories, as history; there is no undelete. A new App may later
reclaim its handle. To keep an App read-only and reversible instead, use
hadron app uninstall.

<app-ref> is an App id or URN, sent to the server unchanged. A URN names
only the installed App holding it, so name an uninstalled App by its id:
its former URN may now name a different, installed App.

Prints the resulting state; --json is {"app": <ref>, "status": "deleted"}.
A missing App and a caller without the right get one identical refusal
(exit 8). A repeat by id is refused APP_DELETED (exit 5). A URN is
resolved afresh on every call: while no installed App holds it, a repeat
gets that same refusal (exit 8), but once another App reclaims the handle
the URN names that App, and deleting it deletes that App. Delete and retry
by id to keep the target fixed. Asks for confirmation on a terminal; a
non-interactive caller must pass --yes. Before cli#1917 this was
"app uninstall"; rm still means delete.`,
		Example: `  hadron app delete capp1a2b3c4d5e6f7g8h9i0j --yes
  hadron app rm hrn:app:acme.com:support --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppLifecycle(cmd, f, args[0],
				func(ref string) error { return cmdutil.ConfirmDeletion(f.IOStreams, yes, "App "+ref) },
				func(ctx context.Context, c graphql.Client, ref string) error {
					_, err := gen.DeleteApp(ctx, c, ref)
					return err
				},
				appStateDeleted,
				func(ref string) string {
					return fmt.Sprintf("✓ App %s is deleted: no longer serving, kept as history, no undelete.", ref)
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
		Long: `Uninstall an App (cor:dmo:050:21). The App is kept but read-only: every
write through it is refused (APP_UNINSTALLED). Its handle is freed, so
another App may take it, and its workers' open dark-worker episodes end.
Reverse it with hadron app reinstall.

BREAKING (cli#1917): app uninstall no longer deletes. Deleting is now
hadron app delete (alias rm, which moved there with it).

<app-ref> is an App id or URN, sent to the server unchanged: a URN names
the installed App holding it, else the one uninstalled App with it. Whoever
may delete the App may uninstall it; anyone else, and an App that does not
exist, get one identical refusal. A deleted App is refused (APP_DELETED).
Prints the resulting state; --json is {"app": <ref>, "status":
"uninstalled"}, the same for a repeat, which changes nothing. Asks for
confirmation on a terminal; a non-interactive caller must pass --yes.`,
		Example: `  hadron app uninstall capp1a2b3c4d5e6f7g8h9i0j --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppLifecycle(cmd, f, args[0],
				func(ref string) error {
					return cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
						"Uninstall App %s? It stays, read-only (writes refused), and its handle is freed; undo with hadron app reinstall.", ref))
				},
				func(ctx context.Context, c graphql.Client, ref string) error {
					_, err := gen.UninstallApp(ctx, c, ref)
					return err
				},
				appStateUninstalled,
				func(ref string) string {
					return fmt.Sprintf("✓ App %s is uninstalled: kept, read-only (writes are refused), and its handle is free for another App. Reverse with: %s",
						ref, reinstallHint(ref))
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
		Long: `Reinstall an uninstalled App (cor:dmo:050:21): the same App, with its id,
keys, members and memories, is installed again. Nothing new is created.

Refused APP_URN_IN_USE (exit 5; the --json error's serverCode) while
another installed App holds the handle. The handle must be free
first: the App holding it is uninstalled or deleted by whoever manages it.
A deleted App is refused APP_DELETED (exit 5; there is no undelete).

Prints the resulting state; --json is {"app": <ref>, "status":
"installed"}. A repeat on an installed App changes nothing and prints the
same.

<app-ref> is an App id or URN, sent to the server unchanged: a URN names
the installed App holding it, else the one uninstalled App with it (two or
more: name one by id). Same gate as app uninstall.`,
		Example: `  hadron app reinstall capp1a2b3c4d5e6f7g8h9i0j`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAppLifecycle(cmd, f, args[0], nil,
				func(ctx context.Context, c graphql.Client, ref string) error {
					_, err := gen.ReinstallApp(ctx, c, ref)
					return err
				},
				appStateInstalled,
				func(ref string) string {
					return fmt.Sprintf("✓ App %s is installed: the same App, with its id, keys, members and memories.", ref)
				})
		},
	}
}
