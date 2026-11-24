package team

// `team role-lists` — the App's three role lists (cli#3030, companion to hadron-server#4921 slice A): who may
// link a defect to a pull request (defect-link), the register pick's role route (register-pick) and who may
// withdraw a negative review verdict besides the reviewer who recorded it (review-withdraw). The App names
// the roles; the platform names none. `get` is any participant's read; `set <purpose> <role>... | --clear`
// replaces ONE list whole and is for whoever manages the App; the instructed manager write follows
// `team saturation-wake-roles set` (its own document, the same claim path).

import (
	"fmt"
	"io"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/instruction"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// roleListsFloor is the server floor: the UTC day hadron-server's role lists (the read and the manager
// write, #4921 slice A) merged to main.
const roleListsFloor = "2026-11-22"

// roleListsInspectHint is the hint for a set that answered null.
const roleListsInspectHint = "check `team role-lists get`"

// roleListsMeaning is what the lists are, said once for every page. It names no team role: the App says which
// of its roles holds a purpose.
const roleListsMeaning = `An App keeps three role lists, one per purpose: defect-link (who may link a defect to
a pull request), register-pick (the role route of the register pick) and
review-withdraw (who may withdraw a negative review verdict besides the reviewer who
recorded it). The App names the roles; the platform names none. A purpose whose
list is empty uses the platform's default: an empty list means the default for that
purpose. Today the server only stores the lists: no door reads them yet, so setting
one changes nothing until the doors read it (its answer says what it stored).`

// roleListPurposes maps the command's spelling of a purpose to the server's enum, in the order they print.
var roleListPurposes = []struct {
	name string
	enum gen.AppRolePurpose
}{
	{"defect-link", gen.AppRolePurposeDefectLink},
	{"register-pick", gen.AppRolePurposeRegisterPick},
	{"review-withdraw", gen.AppRolePurposeReviewWithdraw},
}

// roleListPurposeName is the command's spelling of a purpose the server answered with: the table's name, or for a
// purpose this CLI does not know yet the enum lower-cased with its underscores as dashes, so an answer is never
// printed as a purpose the operator did not name.
func roleListPurposeName(enum gen.AppRolePurpose) string {
	for _, p := range roleListPurposes {
		if p.enum == enum {
			return p.name
		}
	}
	return strings.ToLower(strings.ReplaceAll(string(enum), "_", "-"))
}

func roleListPurposeNames() string {
	names := make([]string, len(roleListPurposes))
	for i, p := range roleListPurposes {
		names[i] = p.name
	}
	return strings.Join(names, ", ")
}

// roleListLimits is the server's limit on a list, stated in every local refusal.
const roleListLimits = "a list holds at most 32 names of 1 to 64 characters each, none containing ':' or whitespace (the server trims, lower-cases and collapses duplicates first)"

// isJSSpace is the server's whitespace: JavaScript's String.trim and /\s/ (hadron-server normalizeWakeRoles
// runs on JS strings). It is Go's unicode.IsSpace except U+0085 (JS does not count it) and with U+FEFF (JS does).
func isJSSpace(r rune) bool { return (unicode.IsSpace(r) && r != 0x85) || r == 0xFEFF }

// checkRoleList mirrors the server's own refusals (hadron-server src/lib/agentTasks/saturation.ts
// normalizeWakeRoles, the one role-list format): a name empty after trimming, over 64 UTF-16 units or holding a
// ':' or whitespace, and more than 32 names after collapsing. It refuses ONLY what the server refuses, judged
// with the server's whitespace set; the one residual difference is Go's lower-casing, which can differ from
// JavaScript's toLowerCase for a few special-case letters, so a name at the 64-unit edge may be judged by the
// server instead. The list is still sent as typed and the server's stored form is what is printed.
func checkRoleList(names []string) error {
	distinct := map[string]bool{}
	for _, raw := range names {
		r := strings.ToLower(strings.TrimFunc(raw, isJSSpace))
		switch {
		case r == "":
			return exitcode.Newf(exitcode.Usage, "a role is empty (an unset variable?): name each role, or pass --clear to empty the list; %s", roleListLimits)
		case len(utf16.Encode([]rune(r))) > 64:
			return exitcode.Newf(exitcode.Usage, "role %q is over 64 characters: %s", output.SafeCell(raw), roleListLimits)
		case strings.ContainsRune(r, ':') || strings.IndexFunc(r, isJSSpace) >= 0:
			return exitcode.Newf(exitcode.Usage, "role %q holds a ':' or whitespace: %s", output.SafeCell(raw), roleListLimits)
		}
		distinct[r] = true
	}
	if len(distinct) > 32 {
		return exitcode.Newf(exitcode.Usage, "%d distinct roles named: %s", len(distinct), roleListLimits)
	}
	return nil
}

type roleListsDTO struct {
	AppID          string   `json:"appId"`
	DefectLink     []string `json:"defectLink"`
	RegisterPick   []string `json:"registerPick"`
	ReviewWithdraw []string `json:"reviewWithdraw"`
}

type roleListDTO struct {
	AppID   string   `json:"appId"`
	Purpose string   `json:"purpose"`
	Roles   []string `json:"roles"`
}

func copyRoles(in []string) []string {
	out := make([]string, 0, len(in))
	return append(out, in...)
}

// roleListLine is one list as the text output shows it; an empty one says the default applies.
func roleListLine(name string, roles []string) string {
	if len(roles) == 0 {
		return name + ": none (the default applies)"
	}
	cells := make([]string, len(roles))
	for i, r := range roles {
		cells[i] = output.SafeCell(r)
	}
	return name + ": " + strings.Join(cells, ", ")
}

func newCmdTeamRoleLists(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "role-lists <command>",
		Short: "The App's role lists: defect link, register pick, review withdrawal",
		Long: roleListsMeaning + `

` + "`get`" + ` shows the three lists to any participant of the App; ` + "`set`" + ` replaces one and is
for whoever manages the App.`,
	}
	cmd.AddCommand(newCmdTeamRoleListsGet(f))
	cmd.AddCommand(newCmdTeamRoleListsSet(f))
	return cmd
}

func newCmdTeamRoleListsGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get",
		Short: "Show the App's three role lists",
		Long: `Show the App's three role lists as the server stored them: one line each for
defect-link, register-pick and review-withdraw. An empty list reads "none (the
default applies)". --json is {appId, defectLink, registerPick, reviewWithdraw}, each
an array, [] when empty. Any participant of the App may read them; only whoever
manages the App may change one (team role-lists set).

` + roleListsMeaning + `

Needs server ` + roleListsFloor + ` or later.`,
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
			resp, err := gen.TeamRoleLists(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			// A success answer can still carry a null selected read (the class of cli#2477): a command
			// error, never a panic and never an empty list.
			if resp == nil || resp.AppRoleLists == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no role lists and no error; the read cannot be trusted")
			}
			l := resp.AppRoleLists.AppRoleListsFields
			dto := roleListsDTO{AppID: l.AppId, DefectLink: copyRoles(l.DefectLink), RegisterPick: copyRoles(l.RegisterPick), ReviewWithdraw: copyRoles(l.ReviewWithdraw)}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				for _, line := range []string{
					roleListLine("defect-link", dto.DefectLink),
					roleListLine("register-pick", dto.RegisterPick),
					roleListLine("review-withdraw", dto.ReviewWithdraw),
				} {
					if _, err := fmt.Fprintln(w, line); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

func newCmdTeamRoleListsSet(f *cmdutil.Factory) *cobra.Command {
	var clearList bool
	var rawInstruction string
	cmd := &cobra.Command{
		Use:   "set <purpose> <role>... | <purpose> --clear",
		Short: "Replace one of the App's role lists",
		Long: `Replace ONE role list whole with the roles you name, or empty it with --clear (the
purpose then uses the platform's default). <purpose> is one of ` + roleListPurposeNames() + ` (any
case). The result printed is what the server stored, not what you sent; --json
carries it as {appId, purpose, roles} with purpose in this spelling.

` + roleListsMeaning + `

The server trims each name and lower-cases it, collapses duplicates and keeps the
list sorted; a role no worker holds is accepted. ` + roleListLimits + `. The
command refuses a name the server would refuse (exit 2) before anything is sent,
and sends the rest as typed. Only whoever manages the App may set a list
(APP_MANAGER_REQUIRED otherwise). Name a purpose and a role, or a purpose and
--clear: a bare set, a blank role and a role together with --clear are refused
(exit 2) before anything is sent.

Without --instruction the write is the signed-in person's, including from a bound
worktree. --instruction opens the App-manager's held-worker path; the server
decides admission. Only set accepts this flag; get stays a read.
Needs server ` + roleListsFloor + ` or later.

` + instructionHelpFor(roleListsFloor),
		Example: `  hadron team role-lists set defect-link role-a role-b
  hadron team role-lists set review-withdraw --clear`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if len(args) == 0 {
				return exitcode.Newf(exitcode.Usage, "name the purpose (%s): set <purpose> <role>... | <purpose> --clear", roleListPurposeNames())
			}
			// The purpose is matched without regard to case or surrounding space (cli#3046), and what is SENT is the
			// server's enum from the table, never what was typed, so a local match is never looser than the server.
			var purpose gen.AppRolePurpose
			var purposeName string
			typed := strings.ToLower(strings.TrimSpace(args[0]))
			for _, p := range roleListPurposes {
				if typed == p.name {
					purpose, purposeName = p.enum, p.name
				}
			}
			if purposeName == "" {
				return exitcode.Newf(exitcode.Usage, "unknown purpose (the value given is not shown): it is one of %s", roleListPurposeNames())
			}
			names := args[1:]
			if clearList && len(names) > 0 {
				return exitcode.Newf(exitcode.Usage, "name roles or pass --clear, not both: --clear empties the list")
			}
			if !clearList && len(names) == 0 {
				return exitcode.Newf(exitcode.Usage, "name at least one role, or pass --clear to empty the list")
			}
			if err := checkRoleList(names); err != nil {
				return err
			}
			// The wire list is the roles as typed; an empty list is [] (it clears), never nil.
			roles := append([]string{}, names...)
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
			var fields gen.AppRoleListFields
			if claim == nil {
				resp, callErr := gen.TeamRoleListSet(ctx, client, scope.Ref, purpose, roles)
				if callErr != nil {
					return api.MapError(callErr)
				}
				if resp == nil || resp.SetAppRoleList == nil {
					return nullMutationResult("setAppRoleList", "role list", roleListsInspectHint)
				}
				fields = resp.SetAppRoleList.AppRoleListFields
			} else {
				// InstructedWrite's contract (instruction.go): targetAppID is "" because this command holds
				// the App as a ref, and the write resolves it; the discarded bool is whether the worker
				// session was attached, unused because admission is the server's either way.
				ctx, _, err = finishInstruction(ctx, "", scope.Ref)
				if err != nil {
					return err
				}
				resp, callErr := gen.TeamRoleListSetInstructed(ctx, client, scope.Ref, purpose, roles, claim)
				if callErr != nil {
					return api.MapErrorKeepingCode(callErr)
				}
				if resp == nil || resp.SetAppRoleList == nil {
					return nullMutationResult("setAppRoleList", "role list", roleListsInspectHint)
				}
				fields = resp.SetAppRoleList.AppRoleListFields
			}
			// purpose is the command's own spelling (defect-link), what the operator typed and get's keys are named
			// for, not the server's enum (cli#3046).
			dto := roleListDTO{AppID: fields.AppId, Purpose: roleListPurposeName(fields.Purpose), Roles: copyRoles(fields.Roles)}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "✓ %s role list set for %s\n", purposeName, appLabel()); err != nil {
					return err
				}
				_, err := fmt.Fprintln(w, roleListLine(purposeName, dto.Roles))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&clearList, "clear", false, "empty the list (the purpose uses the platform's default)")
	cmd.Flags().StringVar(&rawInstruction, "instruction", "", instruction.FlagUsageForServer(roleListsFloor))
	return cmd
}
