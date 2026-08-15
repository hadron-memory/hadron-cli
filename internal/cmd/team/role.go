package team

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdRole(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "role <command>",
		Aliases: []string{"roles"},
		Short:   "The team's role definitions — registers, conventions, role agents",
		Long: `A role definition is a roles:<role> node in the Team Agent's system memory:
the ordered NAME REGISTER the register-mode ` + "`worker cast`" + ` allocates from,
plus the naming conventions (an initial-letter range like F-J, and prose).
The prompt TEMPLATE lives on the role AGENT (its persona dressing — see
` + "`agent get`" + `), not on the role node.

The reads (#403) project the one answer a client cannot compute — which
register names are still FREE, judged against the App's full worker roster
(names are unique per App, case-insensitively, forever — cor:agt:020:02).
The writes (#410) ride createTeamRole/updateTeamRole, whose invariants run
server-side in one App-scoped critical section serialized with
register-mode casting.`,
	}
	cmd.AddCommand(newCmdRoleList(f))
	cmd.AddCommand(newCmdRoleGet(f))
	cmd.AddCommand(newCmdRoleCreate(f))
	cmd.AddCommand(newCmdRoleUpdate(f))
	cmd.AddCommand(newCmdRoleNames(f))
	return cmd
}

// roleNameDTO is one register entry in the stable --json shape. HeldBy keeps
// the worker's id alongside the name — the actionable ref, not just a label.
type roleNameDTO struct {
	Name         string  `json:"name"`
	Taken        bool    `json:"taken"`
	HeldByID     *string `json:"heldById"`
	HeldByName   *string `json:"heldByName"`
	heldByHidden bool    // taken but holder unreadable — render honestly
}

// roleDTO is the stable --json shape for a role definition.
type roleDTO struct {
	Role               string        `json:"role"`
	Loc                string        `json:"loc"`
	NodeID             string        `json:"nodeId"`
	Description        *string       `json:"description"`
	Register           []roleNameDTO `json:"register"`
	FreeCount          int           `json:"freeCount"`
	Exhausted          bool          `json:"exhausted"`
	NameRange          *string       `json:"nameRange"`
	NameConvention     *string       `json:"nameConvention"`
	RoleAgentID        *string       `json:"roleAgentId"`
	RoleAgentURN       *string       `json:"roleAgentUrn"`
	RoleAgentName      *string       `json:"roleAgentName"`
	HasNamePlaceholder *bool         `json:"hasNamePlaceholder"`
}

func roleDTOFromFields(r gen.TeamRoleFields) roleDTO {
	register := []roleNameDTO{}
	for _, n := range r.Register {
		if n == nil {
			continue
		}
		entry := roleNameDTO{Name: n.Name, Taken: n.Taken}
		if n.HeldBy != nil {
			entry.HeldByID, entry.HeldByName = &n.HeldBy.Id, &n.HeldBy.Name
		} else if n.Taken {
			entry.heldByHidden = true
		}
		register = append(register, entry)
	}
	dto := roleDTO{
		Role: r.Role, Loc: r.Loc, NodeID: r.NodeId, Description: r.Description,
		Register: register, FreeCount: r.FreeCount, Exhausted: r.Exhausted,
		NameRange: r.NameRange, NameConvention: r.NameConvention,
		HasNamePlaceholder: r.HasNamePlaceholder,
	}
	if r.RoleAgent != nil {
		dto.RoleAgentID, dto.RoleAgentURN, dto.RoleAgentName = &r.RoleAgent.Id, &r.RoleAgent.Urn, &r.RoleAgent.Name
	}
	return dto
}

// registerLine renders an ordered register with taken markers: "Fred, Gwen,
// Hans, Iris✓, Joe" — allocation order is load-bearing, so never sort.
func registerLine(register []roleNameDTO) string {
	parts := make([]string, len(register))
	for i, n := range register {
		s := n.Name
		if n.Taken {
			s += "✓"
		}
		parts[i] = s
	}
	return strings.Join(parts, ", ")
}

const rolePageSize = 200

// scanTeamRoles pages an App's role definitions to exhaustion (the issue-#23
// rule: an unbounded list is one default page).
func scanTeamRoles(ctx context.Context, client graphql.Client, appRef string, teamAgentRef *string) ([]gen.TeamRoleFields, error) {
	roles := []gen.TeamRoleFields{}
	limit := rolePageSize
	for offset := 0; ; {
		off := offset
		resp, err := gen.TeamRoles(ctx, client, appRef, teamAgentRef, &limit, &off)
		if err != nil {
			return nil, api.MapError(err)
		}
		if resp.TeamRoles == nil {
			return roles, nil
		}
		for _, r := range resp.TeamRoles.Items {
			if r == nil {
				continue
			}
			roles = append(roles, r.TeamRoleFields)
		}
		offset += len(resp.TeamRoles.Items)
		if len(resp.TeamRoles.Items) < rolePageSize || offset >= resp.TeamRoles.Total {
			return roles, nil
		}
	}
}

// roleAppScope resolves the App a role command works against, the group
// convention: --app / the configured context / the worktree binding.
func roleAppScope(cmd *cobra.Command, f *cmdutil.Factory) (string, error) {
	ctx := cmd.Context()
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil {
		return "", err
	}
	return resolveTeamApp(ctx, f, b)
}

func newCmdRoleList(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	cmd := &cobra.Command{
		Use:     "list [--team-agent <ref>]",
		Aliases: []string{"ls"},
		Short:   "Roles, registers, and which names are still free",
		Long: `List the App's role definitions with their registers — the question to
answer immediately BEFORE casting, since a name is permanent
(cor:agt:020:02). Taken names are marked ✓ (retired workers hold their
names forever); FREE and the exhausted flag are server truths judged
against the App's full roster. --team-agent disambiguates when more than
one installed agent carries a roles: branch.`,
		Example: `  hadron team role list --app acme.com:eng-team
  hadron team role list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			rows, err := scanTeamRoles(cmd.Context(), client, appRef, optStr(teamAgent))
			if err != nil {
				return err
			}
			roles := []roleDTO{}
			for _, r := range rows {
				roles = append(roles, roleDTOFromFields(r))
			}
			return output.Write(f.IOStreams, f.JSON, roles, func(w io.Writer) error {
				t := output.NewTable(w, "ROLE", "REGISTER (✓ = taken)", "FREE", "RANGE", "AGENT")
				for _, r := range roles {
					free := fmt.Sprintf("%d", r.FreeCount)
					if r.Exhausted {
						free = "0 (exhausted)"
					}
					t.Row(r.Role, registerLine(r.Register), free, dash(r.NameRange), dash(r.RoleAgentName))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				for _, r := range roles {
					if r.HasNamePlaceholder != nil && !*r.HasNamePlaceholder {
						fmt.Fprintf(w, "! role %s: the role-agent's prompt template never binds {{name}} — its workers would be nameless in their own briefing\n", r.Role)
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	return cmd
}

func newCmdRoleGet(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	cmd := &cobra.Command{
		Use:   "get <role> [--team-agent <ref>]",
		Short: "Show one role: register with holders, conventions, role agent",
		Long: `Show one role definition. The register lists every name in allocation
order with its holder (retired workers hold their names forever); the
conventions (nameRange, nameConvention) are what register writes validate
against. The prompt TEMPLATE lives on the role agent — ` + "`agent get <ref>`" + `
shows it.`,
		Example: `  hadron team role get backend-engineer --app acme.com:eng-team`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			rows, err := scanTeamRoles(cmd.Context(), client, appRef, optStr(teamAgent))
			if err != nil {
				return err
			}
			// No single-role query server-side; the page is small and this
			// keeps get/list reading the same projection.
			for _, r := range rows {
				if !strings.EqualFold(r.Role, args[0]) {
					continue
				}
				dto := roleDTOFromFields(r)
				return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
					fmt.Fprintf(w, "%s (%s)\n  description: %s\n", dto.Role, dto.Loc, dash(dto.Description))
					fmt.Fprintf(w, "  register (%d free%s):\n", dto.FreeCount, map[bool]string{true: " — EXHAUSTED", false: ""}[dto.Exhausted])
					for _, n := range dto.Register {
						switch {
						case n.HeldByName != nil:
							fmt.Fprintf(w, "    %s — held by %s (%s)\n", n.Name, *n.HeldByName, *n.HeldByID)
						case n.heldByHidden:
							fmt.Fprintf(w, "    %s — taken (holder not visible to you)\n", n.Name)
						default:
							fmt.Fprintf(w, "    %s — free\n", n.Name)
						}
					}
					if dto.NameRange != nil || dto.NameConvention != nil {
						fmt.Fprintf(w, "  conventions: range %s%s\n", dash(dto.NameRange), suffixIf(dto.NameConvention))
					}
					if dto.RoleAgentName != nil {
						fmt.Fprintf(w, "  role agent: %s (%s) — the prompt template lives there (`agent get`)\n", *dto.RoleAgentName, *dto.RoleAgentURN)
					} else {
						fmt.Fprintf(w, "  role agent: — (no single installed agent carries this persona role)\n")
					}
					if dto.HasNamePlaceholder != nil && !*dto.HasNamePlaceholder {
						fmt.Fprintf(w, "  ! the role-agent's prompt template never binds {{name}} — its workers would be nameless in their own briefing\n")
					}
					return nil
				})
			}
			return exitcode.Newf(exitcode.NotFound,
				"no role %q in this App — `hadron team role list` shows the definitions", args[0])
		},
	}
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	return cmd
}

func suffixIf(s *string) string {
	if s == nil || *s == "" {
		return ""
	}
	return " — " + *s
}

// resolveRole reads the App's role definitions fresh and returns the one the
// argument names (case-insensitively, like the server's own matching). Every
// write starts here: the sugar verbs compose from current state, and the
// server's diff validation is the safety net when that state goes stale
// between read and write.
func resolveRole(ctx context.Context, client graphql.Client, appRef string, teamAgentRef *string, arg string) (gen.TeamRoleFields, error) {
	var zero gen.TeamRoleFields
	rows, err := scanTeamRoles(ctx, client, appRef, teamAgentRef)
	if err != nil {
		return zero, err
	}
	for _, r := range rows {
		if strings.EqualFold(r.Role, arg) {
			return r, nil
		}
	}
	return zero, exitcode.Newf(exitcode.NotFound,
		"no role %q in this App — `hadron team role list` shows the definitions", arg)
}

// emitRoleReceipt prints the write's receipt: the resulting register in
// allocation order with taken markers — visible outcome, no follow-up read.
func emitRoleReceipt(f *cmdutil.Factory, r gen.TeamRoleFields, verb string) error {
	dto := roleDTOFromFields(r)
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		free := fmt.Sprintf("%d free", dto.FreeCount)
		if dto.Exhausted {
			free = "EXHAUSTED"
		}
		_, err := fmt.Fprintf(w, "✓ %s role %s — register: %s (%s)\n", verb, dto.Role, registerLine(dto.Register), free)
		return err
	})
}

func newCmdRoleCreate(f *cmdutil.Factory) *cobra.Command {
	var names, nameRange, nameConvention, description, teamAgent string
	var allowOutOfRange bool
	cmd := &cobra.Command{
		Use:   "create <role> --names <a,b,c> [--name-range F-J] [--name-convention <text>] [--description <d>]",
		Short: "Mint a role definition (register, conventions) — server-validated",
		Long: `Create a roles:<role> definition in the Team Agent's system memory: ONE
platform call (createTeamRole, hadron-server#960) that owns the spec
knowledge hand-authoring required — the loc shape, the register in
data.names (ordered: position determines who is cast next), and the
register invariants. An added name may not appear in another of this
App's registers (TEAM_ROLE_NAME_DUPLICATE) and must fall inside
--name-range when one is set (TEAM_ROLE_NAME_OUT_OF_RANGE;
--allow-out-of-range overrides deliberately). An existing role refuses
(TEAM_ROLE_EXISTS) — ` + "`role update`" + ` and ` + "`role names`" + ` are the edit paths.

The prompt TEMPLATE is not here: it is the role AGENT's persona dressing
(` + "`agent create/update --persona-prompt`" + `). Authorization is the Team
Agent's definition-edit gate — whoever may write its system memory.`,
		Example: `  hadron team role create backend-engineer --names Fred,Gwen,Hans,Iris,Joe \
      --name-range F-J --app acme.com:eng-team`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			list := splitNames(names)
			if len(list) == 0 {
				return exitcode.Newf(exitcode.Usage, "--names is required — the ordered register the register-mode cast allocates from")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var allow *bool
			if allowOutOfRange {
				allow = &allowOutOfRange
			}
			resp, err := gen.CreateTeamRole(cmd.Context(), client, appRef, optStr(teamAgent), args[0], list,
				optStr(nameRange), optStr(nameConvention), optStr(description), allow)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateTeamRole == nil {
				return exitcode.Newf(exitcode.Error, "server returned no role")
			}
			return emitRoleReceipt(f, resp.CreateTeamRole.TeamRoleFields, "created")
		},
	}
	cmd.Flags().StringVar(&names, "names", "", "the ordered name register, comma-separated (position = allocation order)")
	cmd.Flags().StringVar(&nameRange, "name-range", "", "initial-letter range the register's names must fall in, e.g. F-J")
	cmd.Flags().StringVar(&nameConvention, "name-convention", "", "prose describing the naming convention")
	cmd.Flags().StringVar(&description, "description", "", "role description")
	cmd.Flags().BoolVar(&allowOutOfRange, "allow-out-of-range", false, "deliberately admit a name outside --name-range")
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	_ = cmd.MarkFlagRequired("names")
	return cmd
}

func newCmdRoleUpdate(f *cmdutil.Factory) *cobra.Command {
	var nameRange, nameConvention, description, teamAgent string
	var clearNameRange, clearNameConvention bool
	cmd := &cobra.Command{
		Use:   "update <role> [--name-range <r> | --clear-name-range] [--name-convention <t> | --clear-name-convention] [--description <d>]",
		Short: "Edit a role's conventions and description (names have their own verbs)",
		Long: `Edit a role definition's conventions. Only the fields you pass change; the
register itself is edited with ` + "`role names set|add|rm|mv`" + `, never here.

An unset shell variable must not silently clear a convention, so clearing
is EXPLICIT: --clear-name-range / --clear-name-convention send the null
the server reads as "remove this key"; the paired value flags set it.
Sibling data keys always survive (hadron-server#960 — the --data clobber
hazard is unreachable through this surface).`,
		Example: `  hadron team role update backend-engineer --name-range F-K
  hadron team role update backend-engineer --clear-name-convention`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name-range") && !changed("name-convention") && !changed("description") &&
				!clearNameRange && !clearNameConvention {
				return exitcode.Newf(exitcode.Usage,
					"nothing to update — pass --name-range/--name-convention/--description or a --clear-* flag (register edits: `role names`)")
			}
			if (changed("name-range") && clearNameRange) || (changed("name-convention") && clearNameConvention) {
				return exitcode.Newf(exitcode.Usage, "pass a value or its --clear-* flag, not both")
			}
			if changed("name-range") && strings.TrimSpace(nameRange) == "" {
				return exitcode.Newf(exitcode.Usage, "empty --name-range — to remove the range, use --clear-name-range")
			}
			if changed("name-convention") && strings.TrimSpace(nameConvention) == "" {
				return exitcode.Newf(exitcode.Usage, "empty --name-convention — to remove it, use --clear-name-convention")
			}
			appRef, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Read-modify-write: the operation always sends both convention
			// fields (a value sets, null clears), so an untouched one resends
			// its current value. The fresh read also turns an unknown role
			// into an honest NotFound before any write.
			current, err := resolveRole(cmd.Context(), client, appRef, optStr(teamAgent), args[0])
			if err != nil {
				return err
			}
			rangeArg := current.NameRange
			if changed("name-range") {
				rangeArg = &nameRange
			} else if clearNameRange {
				rangeArg = nil // marshals as the explicit null the server reads as "clear"
			}
			convArg := current.NameConvention
			if changed("name-convention") {
				convArg = &nameConvention
			} else if clearNameConvention {
				convArg = nil
			}
			resp, err := gen.UpdateTeamRoleMeta(cmd.Context(), client, appRef, optStr(teamAgent), current.Role,
				rangeArg, convArg, changedStr(cmd, "description", description))
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateTeamRole == nil {
				return exitcode.Newf(exitcode.Error, "server returned no role")
			}
			return emitRoleReceipt(f, resp.UpdateTeamRole.TeamRoleFields, "updated")
		},
	}
	cmd.Flags().StringVar(&nameRange, "name-range", "", "initial-letter range, e.g. F-J")
	cmd.Flags().BoolVar(&clearNameRange, "clear-name-range", false, "remove the name range")
	cmd.Flags().StringVar(&nameConvention, "name-convention", "", "naming-convention prose")
	cmd.Flags().BoolVar(&clearNameConvention, "clear-name-convention", false, "remove the naming-convention prose")
	cmd.Flags().StringVar(&description, "description", "", "role description")
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	return cmd
}

func newCmdRoleNames(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "names <command>",
		Short: "Maintain a role's name register (ordered — position is allocation order)",
		Long: `Edit a role's name register. The register is an ORDERED list: the
register-mode ` + "`worker cast`" + ` walks it first-to-last, so position determines
who is cast next — which is why mv exists.

The sugar verbs (add/rm/mv) are CAS-safe (#436, hadron-server#987): each
submits with the expectedNames precondition, and on a concurrent edit the
server refuses TEAM_ROLE_STALE with the current register, onto which the
edit is rebased and retried — two admins racing lose nothing. ` + "`set`" + ` is
the explicit whole-list replacement and deliberately carries NO
precondition: it asserts the final state, not an edit of the observed one.
Either way the server validates the DIFF, so nothing can smuggle a
violation: a name minted in this App may never be removed
(TEAM_ROLE_NAME_MINTED — the entry records the allocation forever), an
added name may not appear in another register (TEAM_ROLE_NAME_DUPLICATE),
and added names validate against the range (TEAM_ROLE_NAME_OUT_OF_RANGE;
--allow-out-of-range overrides). Conventions and sibling data are
untouched by construction. Every write prints the resulting register.`,
	}
	cmd.AddCommand(newCmdRoleNamesSet(f))
	cmd.AddCommand(newCmdRoleNamesAdd(f))
	cmd.AddCommand(newCmdRoleNamesRm(f))
	cmd.AddCommand(newCmdRoleNamesMv(f))
	return cmd
}

// splitNames parses a comma-separated register into trimmed, non-empty names.
func splitNames(s string) []string {
	out := []string{}
	for _, n := range strings.Split(s, ",") {
		if t := strings.TrimSpace(n); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// notYetInRegister returns the additions not already present in stored,
// compared case-insensitively (the register's own dedup rule), preserving
// the caller's order and dropping duplicates within the additions.
func notYetInRegister(stored, additions []string) []string {
	seen := map[string]bool{}
	for _, n := range stored {
		seen[strings.ToLower(n)] = true
	}
	fresh := []string{}
	for _, n := range additions {
		k := strings.ToLower(n)
		if seen[k] {
			continue
		}
		seen[k] = true
		fresh = append(fresh, n)
	}
	return fresh
}

// equalNames reports whether two registers are identical — same order, same
// exact spellings (the server compares exactly, so a case difference IS a
// change).
func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// emitRoleUnchanged is the honest receipt for a write that would change
// nothing: no mutation is sent, and the output never says "updated".
func emitRoleUnchanged(f *cmdutil.Factory, r gen.TeamRoleFields, reason string) error {
	dto := roleDTOFromFields(r)
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "· %s unchanged — %s: %s\n", dto.Role, reason, registerLine(dto.Register))
		return err
	})
}

func pluralNames(names []string) string {
	if len(names) == 1 {
		return "that name is"
	}
	return "those names are"
}

// writeRoleNames submits a register and prints the receipt. expectedNames
// nil means unconditional (the `set` path — an explicit wholesale
// replacement asserts final state); non-nil turns the write into the #987
// compare-and-swap the sugar verbs ride. A POINTER so a present-but-empty
// precondition (a sugar edit from an empty register) survives the wire —
// omitempty on a plain slice would drop it (PR #440 review).
func writeRoleNames(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, appRef string, teamAgentRef *string, role string, names []string, expectedNames *[]string, allowOutOfRange bool) error {
	var allow *bool
	if allowOutOfRange {
		allow = &allowOutOfRange
	}
	resp, err := gen.UpdateTeamRoleNames(cmd.Context(), client, appRef, teamAgentRef, role, names, allow, expectedNames)
	if err != nil {
		return err // callers map (the CAS loop branches on TEAM_ROLE_STALE first)
	}
	if resp.UpdateTeamRole == nil {
		return exitcode.Newf(exitcode.Error, "server returned no role")
	}
	return emitRoleReceipt(f, resp.UpdateTeamRole.TeamRoleFields, "updated")
}

// casRoleNames runs a sugar verb's edit as compare-and-swap (#436 /
// hadron-server#987): recompose derives the submission from the CURRENT
// stored register, the write carries it as expectedNames, and a
// TEAM_ROLE_STALE refusal rebases onto the refusal's storedNames and
// retries — so two admins racing edits lose nothing; each retry re-derives
// from fresher state, and a target that vanished meanwhile fails honestly
// inside recompose. Bounded: a register that will not hold still long
// enough is surfaced as the conflict it is.
func casRoleNames(cmd *cobra.Command, f *cmdutil.Factory, client graphql.Client, appRef string, teamAgentRef *string, role string, stored []string, recompose func(stored []string) ([]string, error), allowOutOfRange bool) error {
	const casAttempts = 4
	for attempt := 1; ; attempt++ {
		names, err := recompose(stored)
		if err != nil {
			return err
		}
		// Nothing to write: the register already reflects the edit. Reached
		// on a REBASE when a concurrent admin landed the same addition (PR
		// #444 review), and on the first attempt for an mv to the position a
		// name already holds. Writing anyway would print "✓ updated" for a
		// no-op — the false-update behavior #442 exists to remove.
		if equalNames(names, stored) {
			row, rerr := resolveRole(cmd.Context(), client, appRef, teamAgentRef, role)
			if rerr != nil {
				return rerr
			}
			return emitRoleUnchanged(f, row, "the register already matches the requested edit")
		}
		err = writeRoleNames(cmd, f, client, appRef, teamAgentRef, role, names, &stored, allowOutOfRange)
		if err == nil {
			return nil
		}
		current, stale := api.TeamRoleStaleNames(err)
		if !stale || attempt >= casAttempts {
			return api.MapError(err)
		}
		if current == nil {
			// A stale refusal without the payload: re-read instead.
			row, rerr := resolveRole(cmd.Context(), client, appRef, teamAgentRef, role)
			if rerr != nil {
				return rerr
			}
			current = registerNames(row)
		}
		fmt.Fprintf(f.IOStreams.ErrOut,
			"note: the register changed concurrently — rebasing the edit onto the current one and retrying\n")
		stored = current
	}
}

// registerNames extracts the current register's names, in order.
func registerNames(r gen.TeamRoleFields) []string {
	names := []string{}
	for _, n := range r.Register {
		if n != nil {
			names = append(names, n.Name)
		}
	}
	return names
}

// namesCommandSetup resolves the App scope and the current role row (an
// unknown role becomes an honest NotFound before any write).
func namesCommandSetup(cmd *cobra.Command, f *cmdutil.Factory, teamAgent, roleArg string) (appRef string, client graphql.Client, current gen.TeamRoleFields, err error) {
	appRef, err = roleAppScope(cmd, f)
	if err != nil {
		return "", nil, current, err
	}
	client, err = f.GraphQLClient()
	if err != nil {
		return "", nil, current, err
	}
	current, err = resolveRole(cmd.Context(), client, appRef, optStr(teamAgent), roleArg)
	return appRef, client, current, err
}

// registerNames extracts the current register's names, in order.
func newCmdRoleNamesSet(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	var allowOutOfRange bool
	cmd := &cobra.Command{
		Use:   "set <role> <n1,n2,...>",
		Short: "Replace the register wholesale (explicit)",
		Long: `Replace the role's register with exactly this ordered list. Unlike the
add/rm/mv sugar, set carries NO compare-and-swap precondition: it asserts
the FINAL state, not an edit of the observed one. The server still
validates the DIFF against the stored register, so a minted name missing
from the new list refuses (TEAM_ROLE_NAME_MINTED) rather than silently
un-recording an allocation; out-of-range and cross-register-duplicate
additions refuse typed too.`,
		Example: `  hadron team role names set backend-engineer Fred,Gwen,Hans,Iris,Joe`,
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			names := splitNames(args[1])
			if len(names) == 0 {
				return exitcode.Newf(exitcode.Usage, "empty register — pass an ordered comma-separated list of names")
			}
			appRef, client, current, err := namesCommandSetup(cmd, f, teamAgent, args[0])
			if err != nil {
				return err
			}
			if err := writeRoleNames(cmd, f, client, appRef, optStr(teamAgent), current.Role, names, nil, allowOutOfRange); err != nil {
				return api.MapError(err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	cmd.Flags().BoolVar(&allowOutOfRange, "allow-out-of-range", false, "deliberately admit a name outside the role's range")
	return cmd
}

func newCmdRoleNamesAdd(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	var allowOutOfRange bool
	cmd := &cobra.Command{
		Use:   "add <role> <name>...",
		Short: "Append names to the register, in order (CAS-safe)",
		Long: `Append names to the role's register, in order. Names may be given as
separate arguments, comma-separated, or both — the comma form matches
` + "`role create --names`" + ` and ` + "`role names set`" + `, so reaching for it here does
what you mean instead of appending one corrupt composite entry (#442).

A name already in the register is skipped rather than re-appended, and if
every name given is already there the command says so instead of claiming
an update.`,
		Example: `  hadron team role names add backend-engineer Gwen Hans
  hadron team role names add backend-engineer Gwen,Hans`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// #442: split every argument on commas. `create --names` and
			// `names set` both take the comma form, so a caller reaching for
			// it here used to append the whole string as ONE name — a corrupt
			// entry the server's range check cannot catch (it only inspects
			// the initial), sitting in line for the next register-mode cast,
			// where the name would become permanent.
			additions := []string{}
			for _, a := range args[1:] {
				additions = append(additions, splitNames(a)...)
			}
			if len(additions) == 0 {
				return exitcode.Newf(exitcode.Usage, "no names to add")
			}
			appRef, client, current, err := namesCommandSetup(cmd, f, teamAgent, args[0])
			if err != nil {
				return err
			}
			// An append of a name already present is a no-op server-side (the
			// register dedups case-insensitively), which used to print a
			// "✓ updated" receipt for a write that changed nothing.
			if fresh := notYetInRegister(registerNames(current), additions); len(fresh) == 0 {
				return emitRoleUnchanged(f, current, pluralNames(additions)+" already in the register")
			}
			return casRoleNames(cmd, f, client, appRef, optStr(teamAgent), current.Role, registerNames(current),
				func(stored []string) ([]string, error) {
					// Re-filter on every attempt: a rebase may have brought in
					// a name this command was about to add.
					return append(append([]string{}, stored...), notYetInRegister(stored, additions)...), nil
				}, allowOutOfRange)
		},
	}
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	cmd.Flags().BoolVar(&allowOutOfRange, "allow-out-of-range", false, "deliberately admit a name outside the role's range")
	return cmd
}

func newCmdRoleNamesRm(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	cmd := &cobra.Command{
		Use:   "rm <role> <name>...",
		Short: "Remove free names from the register (minted names refuse; CAS-safe)",
		Long: `Remove names from the register. A name minted in this App can never be
removed (TEAM_ROLE_NAME_MINTED, server-enforced): keeping Iris in the
register is what records that the name — and its initial — belongs to this
role forever. A name not in the register refuses rather than silently
no-opping.

Each argument is matched LITERALLY — deliberately, unlike ` + "`names add`" + `:
quoting a comma-bearing entry is how a register corrupted by an older CLI
(#442) is repaired, e.g. ` + "`names rm <role> \"Linn,Mia\"`" + `.`,
		Example: `  hadron team role names rm backend-engineer Gwen`,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appRef, client, current, err := namesCommandSetup(cmd, f, teamAgent, args[0])
			if err != nil {
				return err
			}
			role := current.Role
			return casRoleNames(cmd, f, client, appRef, optStr(teamAgent), role, registerNames(current),
				func(stored []string) ([]string, error) {
					drop := map[string]bool{}
					for _, n := range args[1:] {
						drop[strings.ToLower(strings.TrimSpace(n))] = false
					}
					names := []string{}
					for _, n := range stored {
						if _, listed := drop[strings.ToLower(n)]; listed {
							drop[strings.ToLower(n)] = true
							continue
						}
						names = append(names, n)
					}
					for arg, found := range drop {
						if !found {
							// A typo'd rm must not silently no-op — and on a CAS
							// retry this also catches a name a concurrent edit
							// removed first.
							return nil, exitcode.Newf(exitcode.Usage, "%q is not in %s's register — `hadron team role get %s` shows it", arg, role, role)
						}
					}
					return names, nil
				}, false)
		},
	}
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	return cmd
}

func newCmdRoleNamesMv(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	cmd := &cobra.Command{
		Use:   "mv <role> <name> <position>",
		Short: "Reorder a register name (1-based; position is allocation order; CAS-safe)",
		Long: `Move a name to a new 1-based position. Order is load-bearing: the
register-mode cast walks the list first-to-last, so position determines
who is cast next.`,
		Example: `  hadron team role names mv backend-engineer Joe 1`,
		Args:    cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			pos, perr := strconv.Atoi(args[2])
			if perr != nil || pos < 1 {
				return exitcode.Newf(exitcode.Usage, "position is 1-based, got %q", args[2])
			}
			appRef, client, current, err := namesCommandSetup(cmd, f, teamAgent, args[0])
			if err != nil {
				return err
			}
			role := current.Role
			return casRoleNames(cmd, f, client, appRef, optStr(teamAgent), role, registerNames(current),
				func(stored []string) ([]string, error) {
					from := -1
					for i, n := range stored {
						if strings.EqualFold(n, args[1]) {
							from = i
							break
						}
					}
					if from < 0 {
						return nil, exitcode.Newf(exitcode.Usage, "%q is not in %s's register — `hadron team role get %s` shows it", args[1], role, role)
					}
					if pos > len(stored) {
						return nil, exitcode.Newf(exitcode.Usage, "position %d is past the end — the register has %d names", pos, len(stored))
					}
					names := append([]string{}, stored...)
					moved := names[from]
					names = append(names[:from], names[from+1:]...)
					rest := append([]string{}, names[pos-1:]...)
					names = append(append(names[:pos-1], moved), rest...)
					return names, nil
				}, false)
		},
	}
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	return cmd
}
