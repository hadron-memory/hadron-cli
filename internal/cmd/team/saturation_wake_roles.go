package team

// `team saturation-wake-roles` — the roles an App's saturation alarms wake
// (cli#2894, companion to hadron-server#4777). `get` is any participant's
// read; `set <role>... | --clear` replaces the whole list and is for whoever
// manages the App. The instructed manager write follows `team stall-schedule
// set` (its own document, the same claim path).

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// saturationWakeRolesFloor is the server floor: the UTC day hadron-server's wake-roles
// list (the read, the manager write and its instruction argument) merged to main.
const saturationWakeRolesFloor = "2026-11-22"

// saturationWakeRolesInspectHint is the hint for a set that answered null.
const saturationWakeRolesInspectHint = "check `team saturation-wake-roles get`"

// saturationWakeRolesMeaning is what the list is, said once for every page: it names ROLES,
// who is woken, that a worker who is never due is not woken now, and that a change reaches
// later alarms only. The never-due clause describes the rule (cor:agt:080:11, "Who is never
// due") and does not enumerate it: the rule names more cases than the three given as examples
// (a short-lived Agent's worker with no open session is due nothing too), the server's own scan
// leaves it to its caller ("Who is never due (cor:agt:080:11) is the caller's", taskWakes.ts), and
// for that case the rule says nothing is dropped. So the help says
// "such as", never a closed list, and "not woken now", never that the wake is lost. One verb
// ("woken") throughout, the one the command's own output uses.
const saturationWakeRolesMeaning = `The list names roles. Each worker of the App in a listed role is woken once
when a saturation alarm is raised, and not again for that alarm; a worker who is
never due, such as one that is retired, paused or dormant, is not woken now. A change
to the list applies only to alarms raised afterwards. The list is empty by default, so
nobody is woken until an App manager lists a role.`

type saturationWakeRolesDTO struct {
	AppID string   `json:"appId"`
	Roles []string `json:"roles"`
}

// saturationWakeRolesDTOFrom copies the stored list; a nil list is an empty one ([] in
// --json, never null).
func saturationWakeRolesDTOFrom(f *gen.SaturationWakeRolesFields) saturationWakeRolesDTO {
	roles := make([]string, 0, len(f.Roles))
	roles = append(roles, f.Roles...)
	return saturationWakeRolesDTO{AppID: f.AppId, Roles: roles}
}

func renderSaturationWakeRoles(w io.Writer, dto saturationWakeRolesDTO) error {
	if len(dto.Roles) == 0 {
		_, err := fmt.Fprintln(w, "wake roles: none (nobody is woken)")
		return err
	}
	cells := make([]string, len(dto.Roles))
	for i, r := range dto.Roles {
		cells[i] = output.SafeCell(r)
	}
	_, err := fmt.Fprintf(w, "wake roles: %s\n", strings.Join(cells, ", "))
	return err
}

func newCmdTeamSaturationWakeRoles(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "saturation-wake-roles <command>",
		Short: "The roles an App's saturation alarms wake",
		Long: saturationWakeRolesMeaning + `

` + "`get`" + ` shows the list to any participant of the App; ` + "`set`" + ` replaces it and
is for whoever manages the App.`,
	}
	cmd.AddCommand(newCmdTeamSaturationWakeRolesGet(f))
	cmd.AddCommand(newCmdTeamSaturationWakeRolesSet(f))
	return cmd
}

func newCmdTeamSaturationWakeRolesGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the App's saturation wake roles",
		Long: `Show the roles the App's saturation alarms wake, as the server stored them. An
empty list reads "none (nobody is woken)". --json is {appId, roles}, with
roles [] when the list is empty. Any participant of the App may read it; only
whoever manages the App may change it (team saturation-wake-roles set).

` + saturationWakeRolesMeaning + `

Needs server ` + saturationWakeRolesFloor + ` or later.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamSaturationWakeRoles(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			// A success answer can still carry a null selected read (the class of
			// cli#2477): a command error, never a panic.
			if resp == nil || resp.SaturationAlarmWakeRoles == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no wake-roles list and no error; the read cannot be trusted")
			}
			dto := saturationWakeRolesDTOFrom(&resp.SaturationAlarmWakeRoles.SaturationWakeRolesFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				return renderSaturationWakeRoles(w, dto)
			})
		},
	}
}

func newCmdTeamSaturationWakeRolesSet(f *cmdutil.Factory) *cobra.Command {
	var clearList bool
	var rawInstruction string
	cmd := &cobra.Command{
		Use:   "set <role>... | --clear",
		Short: "Replace the App's saturation wake roles",
		Long: `Replace the whole list with the roles you name, or empty it with --clear. The
result printed is what the server stored, not what you sent.

` + saturationWakeRolesMeaning + `

The server trims each name and lower-cases it, so a worker's role matches
whatever its case, collapses duplicates and keeps the list sorted; a role no
worker holds is accepted, and a retired worker is skipped. It allows at most 32
names, each 1 to 64 characters with no colon or whitespace, and refuses
anything else (exit 2). Setting the list it already holds changes nothing.
Only whoever manages the App may set it (APP_MANAGER_REQUIRED otherwise).
Name a role or pass --clear: a bare set, a blank role and a role together with
--clear are refused (exit 2) before anything is sent.

Without --instruction the write is the signed-in person's, including from a
bound worktree. --instruction opens the App-manager's held-worker path; the
server decides admission. Only set accepts this flag; get stays a read.
Needs server ` + saturationWakeRolesFloor + ` or later.

` + instructionHelpFor(saturationWakeRolesFloor),
		Example: `  hadron team saturation-wake-roles set backend-engineer qa-engineer
  hadron team saturation-wake-roles set --clear`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if clearList && len(args) > 0 {
				return exitcode.Newf(exitcode.Usage, "name roles or pass --clear, not both: --clear empties the list")
			}
			if !clearList && len(args) == 0 {
				return exitcode.Newf(exitcode.Usage, "name at least one role, or pass --clear to empty the list")
			}
			for _, r := range args {
				if strings.TrimSpace(r) == "" {
					return exitcode.Newf(exitcode.Usage, "a role is empty (an unset variable?): name each role, or pass --clear to empty the list")
				}
			}
			// The wire list is the roles as typed; an empty list is [] (it clears), never nil.
			roles := append([]string{}, args...)
			claim, err := taskClaim(cmd, rawInstruction)
			if err != nil {
				return err
			}
			var finishInstruction InstructedWrite
			if claim != nil {
				finishInstruction = BeginInstructedWrite(f)(ctx)
				ctx = withAsPerson(ctx)
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var fields gen.SaturationWakeRolesFields
			if claim == nil {
				resp, callErr := gen.TeamSaturationWakeRolesSet(ctx, client, scope.Ref, roles)
				if callErr != nil {
					return api.MapError(callErr)
				}
				if resp == nil || resp.SetSaturationAlarmWakeRoles == nil {
					return nullMutationResult("setSaturationAlarmWakeRoles", "wake-roles list", saturationWakeRolesInspectHint)
				}
				fields = resp.SetSaturationAlarmWakeRoles.SaturationWakeRolesFields
			} else {
				// InstructedWrite's contract (instruction.go): targetAppID is "" because this
				// command holds the App as a ref, and the write resolves it; the discarded bool
				// is whether the worker session was attached, unused because admission is the
				// server's either way (the same as stall-schedule set).
				ctx, _, err = finishInstruction(ctx, "", scope.Ref)
				if err != nil {
					return err
				}
				resp, callErr := gen.TeamSaturationWakeRolesSetInstructed(ctx, client, scope.Ref, roles, claim)
				if callErr != nil {
					return api.MapErrorKeepingCode(callErr)
				}
				if resp == nil || resp.SetSaturationAlarmWakeRoles == nil {
					return nullMutationResult("setSaturationAlarmWakeRoles", "wake-roles list", saturationWakeRolesInspectHint)
				}
				fields = resp.SetSaturationAlarmWakeRoles.SaturationWakeRolesFields
			}
			dto := saturationWakeRolesDTOFrom(&fields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ saturation wake roles set for %s\n", appLabel()); err != nil {
					return err
				}
				return renderSaturationWakeRoles(w, dto)
			})
		},
	}
	cmd.Flags().BoolVar(&clearList, "clear", false, "empty the list (nobody is woken)")
	cmd.Flags().StringVar(&rawInstruction, "instruction", "", instruction.FlagUsageForServer(saturationWakeRolesFloor))
	return cmd
}
