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
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// appJoinDTO is the stable --json shape of `app join`: the member row the
// server returned. It carries no "already a member" or "created" field,
// because JoinAppPayload says neither, and nothing is inferred from createdAt
// (cli#1625).
type appJoinDTO struct {
	App           string           `json:"app"`
	AppID         string           `json:"appId"`
	AppURN        *string          `json:"appUrn"`
	AppName       *string          `json:"appName"`
	User          appMemberUserDTO `json:"user"`
	Role          string           `json:"role"`
	CreatedAt     string           `json:"createdAt"`
	IsOrgExternal bool             `json:"isOrgExternal"`
}

// appLeaveDTO is the stable --json shape of `app leave`. The server answers
// the same whether or not a row existed, so Status is "absent" — the state now
// — never "left".
type appLeaveDTO struct {
	App    string `json:"app"`
	AppID  string `json:"appId"`
	UserID string `json:"userId"`
	Status string `json:"status"`
}

func newCmdJoin(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "join <app-ref>",
		Short: "Join an App as yourself",
		Long: `Join an App yourself, as the signed-in user, without anyone adding you. Name
the App by ID, by URN or as org:slug.

Joining yourself is closed unless the App's primary Agent opts in by naming the
role self-joiners get. Then you get that role, never owner. To add someone with
another role, an App member manager uses "hadron app member add". The server
refuses, and the CLI prints its message unchanged with a nonzero exit, when
self-join is closed (or the App has no installed Agent), the App has reached
its member limit, the App is not found or is uninstalled, the Agent's policy
does not accept the role, or you are not signed in.

If you are already a member you get your existing membership back and nothing
changes. The server does not say whether this call created it, so the command
prints your membership and never claims "already a member": joining twice
prints the same and exits 0. Prints "✓ You are a member of <App> with role
<role>" and the date you became a member; --json is the membership (app, appId,
appUrn, appName, user, role, createdAt, isOrgExternal).`,
		Example: `  hadron app join hrn:app:acme.com:eng-team
  hadron app join hrn:app:acme.com:eng-team --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.JoinApp(cmd.Context(), client, appRef)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.JoinApp == nil || resp.JoinApp.AppMember == nil || resp.JoinApp.AppMember.User == nil {
				return exitcode.Newf(exitcode.Unavailable,
					"outcome unknown: the server returned no member row. Run "+memberListHint+" to check")
			}
			m := resp.JoinApp.AppMember
			dto := appJoinDTO{
				App:           appRef,
				AppID:         m.AppId,
				User:          memberUserDTO(m.User.Id, m.User.Handle, m.User.Urn),
				Role:          m.Role,
				CreatedAt:     m.CreatedAt,
				IsOrgExternal: m.IsOrgExternal,
			}
			if m.App != nil {
				name := m.App.Name
				dto.AppURN, dto.AppName = &m.App.Urn, &name
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				name := appRef
				if dto.AppURN != nil {
					name = *dto.AppURN
				}
				if _, err := fmt.Fprintf(w, "✓ You are a member of %s with role %s\n", memberText(name), memberText(dto.Role)); err != nil {
					return err
				}
				if dto.AppName != nil {
					if _, err := fmt.Fprintf(w, "  app: %s\n", memberCell(dto.AppName)); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "  member since: %s\n", output.SafeCell(dto.CreatedAt))
				return err
			})
		},
	}
}

const leaveKeptNote = "Your personal memory in the App is kept and re-attaches if you can join again. Rejoining is subject to self-join policy and capacity."

func newCmdLeave(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "leave <app-ref> [--yes] [--instruction <kind>:<ref>]",
		Short: "Leave an App you are a member of",
		Long: `Leave an App yourself, as the signed-in user. Name the App by ID, by URN or
as org:slug.

Your personal memory in the App is kept and re-attaches if you join again.
Rejoining is subject to the App's self-join policy and its capacity.

Leaving an App you are not a member of changes nothing and exits 0, so success
means "not a member now", not "was removed". It asks for confirmation on a
terminal; a script passes --yes, and without it a non-interactive run is
refused before anything is sent. Prints "✓ You are not a member of <App> now"
and a note that your personal memory is kept; --json is {"app", "appId",
"userId", "status": "absent"}.

The server decides who may leave. A worker needs an instruction or its own
app.members grant (below); a session acting as someone else, an App's own
automated run, or a session the server cannot tie to one person is refused. Each
refusal is printed unchanged with a nonzero exit.`,
		Example: `  hadron app leave hrn:app:acme.com:eng-team --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			claim, instructed, err := memberInstruction(cmd, f)
			if err != nil {
				return err
			}
			appRef, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Leave app %s? %s", appRef, leaveKeptNote)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ctx, err := memberWriteContext(cmd, appRef, claim, instructed)
			if err != nil {
				return err
			}
			resp, err := leaveApp(ctx, client, appRef, claim)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.LeaveApp == nil {
				return exitcode.Newf(exitcode.Unavailable,
					"outcome unknown: the server did not confirm the leave. Run "+memberListHint+" to check")
			}
			dto := appLeaveDTO{App: appRef, AppID: resp.LeaveApp.AppId, UserID: resp.LeaveApp.UserId, Status: "absent"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ You are not a member of %s now. %s\n", memberText(appRef), leaveKeptNote)
				return err
			})
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	addMemberInstruction(cmd)
	return cmd
}

func leaveApp(ctx context.Context, c graphql.Client, appRef string, claim *gen.ActInstructionInput) (*gen.LeaveAppResponse, error) {
	if claim == nil {
		return gen.LeaveApp(ctx, c, appRef)
	}
	r, err := gen.LeaveAppInstructed(ctx, c, appRef, claim)
	if r == nil || r.LeaveApp == nil {
		return nil, err
	}
	return &gen.LeaveAppResponse{LeaveApp: &gen.LeaveAppLeaveAppLeaveAppPayload{AppId: r.LeaveApp.AppId, UserId: r.LeaveApp.UserId}}, err
}
