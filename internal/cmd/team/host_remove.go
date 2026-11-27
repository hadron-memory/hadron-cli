package team

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/hostcred"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// forceEffects is what the server's force does (resolvers.agentTeamHost.ts,
// endRemovedHost), and what it does NOT do. The listing, the confirmation and the help
// all say exactly this: a person who forces a removal must know the open sessions stay
// open (hadron-cli#3282, Holger's ruling of 2026-11-27).
const forceEffects = "Removing a host ends its registration, fails its pending operations,\n  revokes its keys and removes its assignments. It does NOT end any open\n  session: the sessions stay open."

type hostRemoveDTO struct {
	Server          string   `json:"server"`
	AppID           string   `json:"appId"`
	HostID          string   `json:"hostId"`
	Forced          bool     `json:"forced"`
	OpenSessions    []string `json:"openSessions"`
	RemovedOnServer bool     `json:"removedOnServer"`
	RemovedLocally  bool     `json:"removedLocally"`
}

func newCmdTeamHostRemove(f *cmdutil.Factory) *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "remove <host> [--force] [--app <ref>] [--yes]",
		Short: "Remove a team App host, even past its open sessions (a host that is not this machine's)",
		Long: `Remove one of the App's hosts by its host id (see "team host hosts"): the
server revokes its keys, ends its registration, removes its assignments and
settles its pending operations as failed. Meant for a host that is not this
machine's, such as a lost machine; for this machine's own host use "team host
leave", which also deletes the stored key and has no force.

It lists the host's workers with an open session first, then states what a
removal does and asks before it sends (or pass --yes; without a terminal --yes is
required). Without --force a host with an open session is refused (exit 5, the
workers named), exactly as "leave" does. --force removes it anyway:

  ` + forceEffects + `

End those sessions yourself if they should stop. If the listing cannot be read,
--force removes nothing. The stored key is deleted only when this machine holds
the removed host's key, and only after the server confirmed the removal.

WHO: whoever manages the App, signed in as themselves; the request never carries
a worker session or a stored key. --json prints {server, appId, hostId, forced,
openSessions, removedOnServer, removedLocally}. Exit codes: 2 no --yes off a
terminal, 3 not signed in, 4 no such host, 5 open sessions (without --force), 7
server unreachable or answer lost, 8 not permitted.`,
		Example: `  hadron team host remove old-laptop --app acme.com:eng-team --force --yes
  hadron team host remove old-laptop --app acme.com:eng-team`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			hostID := args[0]
			if hostID == "" {
				return exitcode.Newf(exitcode.Usage, "the host is empty")
			}
			scope, client, server, appID, err := hostApp(cmd, f)
			if err != nil {
				return err
			}
			ctx := personCtx(cmd.Context())
			// The listing runs before the confirmation and before any mutation. With
			// --force it must succeed: a person never forces blind.
			sessions, lerr := openSessionList(ctx, client, scope.Ref, hostID)
			if lerr != nil {
				if force {
					code := exitcode.FromError(api.MapError(lerr))
					return exitcode.Newf(code, "could not list the host's open sessions (%s), so nothing was removed (the host given is not shown)", failureClassHost(code))
				}
				sessions = []string{}
			}
			w := f.IOStreams.ErrOut
			if len(sessions) > 0 {
				fmt.Fprintf(w, "host %s has %d worker(s) with an open session: %s\n", output.SafeCell(hostID), len(sessions), strings.Join(sessions, ", "))
			} else if lerr == nil {
				fmt.Fprintf(w, "host %s has no worker with an open session\n", output.SafeCell(hostID))
			}
			fmt.Fprintln(w, strings.Join(strings.Fields(forceEffects), " "))
			what := fmt.Sprintf("host %s of this App (registration ended, operations failed, keys revoked, assignments removed; open sessions STAY OPEN)", output.SafeCell(hostID))
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, what); err != nil {
				return err
			}
			// force is sent only when asked for: omitted, never false, so without the
			// flag the request is the one `leave` sends.
			var forceArg *bool
			if force {
				t := true
				forceArg = &t
			}
			d := hostRemoveDTO{Server: server, AppID: appID, HostID: hostID, Forced: force, OpenSessions: sessions}
			resp, err := gen.RemoveAgentTeamHost(ctx, client, scope.Ref, hostID, forceArg)
			switch {
			case err != nil && api.HasErrorCode(err, "HOST_NOT_FOUND"):
				return exitcode.Newf(exitcode.NotFound, "the server has no such host for this App; nothing was removed (the host given is not shown)")
			case err != nil && api.HasErrorCode(err, "AGENT_TEAM_HOST_HAS_OPEN_SESSIONS"):
				names := ""
				if len(sessions) > 0 {
					names = " (" + strings.Join(sessions, ", ") + ")"
				}
				return exitcode.Newf(exitcode.Conflict, "the host has a worker with an open session%s; end it first, or pass --force to remove the host anyway (the open sessions stay open; the host given is not shown)", names)
			case err != nil:
				if api.IsFrameworkValidationOnlyFor(err, "removeAgentTeamHost") {
					return exitcode.Newf(exitcode.Error, "%s", agentTeamHostsMissing)
				}
				code := exitcode.FromError(api.MapError(err))
				if lostAnswer(code) {
					return exitcode.Newf(code, "removeAgentTeamHost was sent and its answer was lost, so the host may or may not be removed. Run `hadron team host remove` again. Cause: %s", failureClassHost(code))
				}
				return exitcode.Newf(code, "removeAgentTeamHost refused: %s", failureClassHost(code))
			case resp == nil || !resp.RemoveAgentTeamHost:
				return exitcode.Newf(exitcode.Error, "the server did not confirm removing the host (the host given is not shown)")
			}
			d.RemovedOnServer = true
			// Only after the server confirmed: the stored key goes only if THIS machine
			// holds the removed host's key. A store that cannot be read does not undo
			// the removal; it is reported.
			if store, serr := hostcred.Lock(); serr != nil {
				fmt.Fprintf(w, "note: host %s is removed on the server, but this machine's key store could not be checked (%s); if this machine held its key, run `hadron team host leave`\n", output.SafeCell(hostID), storeErr(serr).Error())
			} else {
				if entry, ok := store.Find(server, appID); ok && entry.HostID == hostID {
					if _, rerr := store.Remove(server, appID); rerr != nil {
						store.Unlock()
						return exitcode.Newf(exitcode.Error, "the host is removed on the server but its stored key, now revoked, could not be deleted (%s); run `hadron team host leave`", storeErr(rerr).Error())
					}
					d.RemovedLocally = true
				}
				store.Unlock()
			}
			appLabel := lazyAppLabel(cmd.Context(), f, scope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				line := fmt.Sprintf("✓ removed host %s from the App on %s", output.SafeCell(d.HostID), output.SafeCell(server))
				if d.RemovedLocally {
					line += " and deleted its stored key"
				}
				if len(d.OpenSessions) > 0 {
					line += fmt.Sprintf("; its open sessions stay open (%s)", strings.Join(d.OpenSessions, ", "))
				}
				_, err := fmt.Fprintln(w, line)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "remove the host even while a worker of it has an open session (the sessions stay open)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (required when not on a terminal)")
	return cmd
}
