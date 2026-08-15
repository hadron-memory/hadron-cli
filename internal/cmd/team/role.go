package team

import (
	"context"
	"fmt"
	"io"
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

These are reads (#403, hadron-server#960): the server projects the one
answer a client cannot compute — which register names are still FREE,
judged against the App's full worker roster (names are unique per App,
case-insensitively, forever — cor:agt:020:02). Register writes are #410.`,
	}
	cmd.AddCommand(newCmdRoleList(f))
	cmd.AddCommand(newCmdRoleGet(f))
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
		Use:   "get <role>",
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
