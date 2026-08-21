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
		Short:   "The team's role definitions",
		Long: `A role definition is a roles:<role> node in the Team Agent's system memory:
a role name and a description. The prompt TEMPLATE lives on the role AGENT
(its persona dressing — see ` + "`agent get`" + `), not on the role node.

THE NAME REGISTER IS GONE (hadron-server#1050, hadron-cli#496). A role used
to carry an ordered cast-list that ` + "`worker cast`" + ` allocated the next free
name from, plus an initial-letter range and convention prose. The server no
longer has any of it: cor:agt:020:07 is WITHDRAWN rather than superseded, so
` + "`role names set|add|rm|mv`" + `, --name-range, --name-convention and
--transfer-register-to are gone from this group too, and a cast now takes an
explicit ` + "`--name`" + `.

Retiring a role still does NOT free a name for re-casting, and neither did
removing the register: a name is permanent per App (cor:agt:020:02) because
the ROSTER says so, not because a register recorded the allocation. That the
register could be deleted without amending a durable clause is the evidence
it was bookkeeping rather than identity.

WHICH APP — every command here says so (#468). The team App resolves
ambiently (--app, the configured App context, then the worktree binding),
so each read opens with the App it landed on AND where that scope came
from, each write names it in the receipt, and ` + "`role rm`" + ` names it in the
confirmation.`,
	}
	cmd.AddCommand(newCmdRoleList(f))
	cmd.AddCommand(newCmdRoleGet(f))
	cmd.AddCommand(newCmdRoleCreate(f))
	cmd.AddCommand(newCmdRoleUpdate(f))
	cmd.AddCommand(newCmdRoleRm(f))
	return cmd
}

// The retirement payload's generated name is deeply nested; alias it.
type deletedRolePayload = gen.DeleteTeamRoleDeleteTeamRoleDeleteTeamRolePayload

// roleDeletedDTO is the stable --json shape for `role rm`.
//
// `transferredNames` and `transferredTo` are GONE with the register
// (hadron-cli#496): they described a hand-off of allocation state that no
// longer exists. Dropping keys from a --json contract is a breaking change,
// and the alternative — keeping them as a permanent `[]`/`null` — would have
// promised a hand-off the server cannot perform.
type roleDeletedDTO struct {
	Role         string `json:"role"`
	NodesDeleted int    `json:"nodesDeleted"`
}

// roleDTO is the stable --json shape for a role definition.
type roleDTO struct {
	Role               string  `json:"role"`
	Loc                string  `json:"loc"`
	NodeID             string  `json:"nodeId"`
	Description        *string `json:"description"`
	RoleAgentID        *string `json:"roleAgentId"`
	RoleAgentURN       *string `json:"roleAgentUrn"`
	RoleAgentName      *string `json:"roleAgentName"`
	HasNamePlaceholder *bool   `json:"hasNamePlaceholder"`
}

func roleDTOFromFields(r gen.TeamRoleFields) roleDTO {
	dto := roleDTO{
		Role: r.Role, Loc: r.Loc, NodeID: r.NodeId, Description: r.Description,
		HasNamePlaceholder: r.HasNamePlaceholder,
	}
	if r.RoleAgent != nil {
		dto.RoleAgentID, dto.RoleAgentURN, dto.RoleAgentName = &r.RoleAgent.Id, &r.RoleAgent.Urn, &r.RoleAgent.Name
	}
	return dto
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
// convention: --app / the configured context / the worktree binding. It
// returns the SCOPE rather than the bare ref (#468) — this group writes, and
// an edit landing in another team is invisible without it.
func roleAppScope(cmd *cobra.Command, f *cmdutil.Factory) (appScope, error) {
	ctx := cmd.Context()
	b, err := readBindingOrNilWithApp(ctx, f)
	if err != nil {
		return appScope{}, err
	}
	return resolveTeamAppScope(ctx, f, b)
}

func newCmdRoleList(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	cmd := &cobra.Command{
		Use:     "list [--team-agent <ref>]",
		Aliases: []string{"ls"},
		Short:   "The App's role definitions and their role agents",
		Long: `List the App's role definitions. --team-agent disambiguates when more than
one installed agent carries a roles: branch.

This used to answer "which names are still free", the question to ask
immediately before casting. There is no register to answer it from
(hadron-server#1050) — a cast now takes an explicit --name, and the roster
(` + "`worker list`" + `) is what says whether one is already taken.`,
		Example: `  hadron team role list --app acme.com:eng-team
  hadron team role list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(cmd.Context(), f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			rows, err := scanTeamRoles(cmd.Context(), client, scope.Ref, optStr(teamAgent))
			if err != nil {
				return err
			}
			roles := []roleDTO{}
			for _, r := range rows {
				roles = append(roles, roleDTOFromFields(r))
			}
			return output.Write(f.IOStreams, f.JSON, roles, func(w io.Writer) error {
				// Why this App — before the table, and on an empty App too,
				// since that is where "no roles defined" and "wrong team" look
				// identical.
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				t := output.NewTable(w, "ROLE", "DESCRIPTION", "AGENT")
				for _, r := range roles {
					t.Row(r.Role, dash(r.Description), dash(r.RoleAgentName))
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
		Short: "Show one role: its description and role agent",
		Long: `Show one role definition. The prompt TEMPLATE lives on the role agent —
` + "`agent get <ref>`" + ` shows it.`,
		Example: `  hadron team role get backend-engineer --app acme.com:eng-team`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(cmd.Context(), f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			rows, err := scanTeamRoles(cmd.Context(), client, scope.Ref, optStr(teamAgent))
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
					if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
						return err
					}
					fmt.Fprintf(w, "%s (%s)\n  description: %s\n", dto.Role, dto.Loc, dash(dto.Description))
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

// resolveRole reads the App's role definitions fresh and returns the one the
// argument names (case-insensitively, like the server's own matching). The
// fresh read turns an unknown role into an honest NotFound before any write.
func resolveRole(ctx context.Context, client graphql.Client, appRef string, teamAgentRef *string, arg string) (gen.TeamRoleFields, error) {
	rows, err := scanTeamRoles(ctx, client, appRef, teamAgentRef)
	if err != nil {
		var zero gen.TeamRoleFields
		return zero, err
	}
	return pickRole(rows, arg)
}

// emitRoleReceipt prints the write's receipt — the resulting definition, and
// WHERE it landed (#468). An edit is only re-editable if you notice it went to
// the wrong team, and the receipt is the one thing the operator is guaranteed
// to read.
func emitRoleReceipt(f *cmdutil.Factory, r gen.TeamRoleFields, verb string, appLabel func() string) error {
	dto := roleDTOFromFields(r)
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "✓ %s role %s — %s\n", verb, dto.Role, dash(dto.Description)); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "  app: %s\n", appLabel())
		return err
	})
}

func newCmdRoleCreate(f *cmdutil.Factory) *cobra.Command {
	var description, teamAgent string
	cmd := &cobra.Command{
		Use:   "create <role> [--description <d>]",
		Short: "Mint a role definition",
		Long: `Create a roles:<role> definition in the Team Agent's system memory: ONE
platform call (createTeamRole, hadron-server#960) that owns the spec
knowledge hand-authoring required — the loc shape and the node type. An
existing role refuses (TEAM_ROLE_EXISTS); ` + "`role update`" + ` is the edit path.

--names, --name-range, --name-convention and --allow-out-of-range are gone
(hadron-server#1050): there is no name register for them to describe.

The prompt TEMPLATE is not here either: it is the role AGENT's persona
dressing (` + "`agent create/update --persona-prompt`" + `). Authorization is the
Team Agent's definition-edit gate — whoever may write its system memory.`,
		Example: `  hadron team role create backend-engineer --description "Go services" --app acme.com:eng-team`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			appRef, appLabel := scope.Ref, lazyAppLabel(cmd.Context(), f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreateTeamRole(cmd.Context(), client, appRef, optStr(teamAgent), args[0], optStr(description))
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateTeamRole == nil {
				return exitcode.Newf(exitcode.Error, "server returned no role")
			}
			return emitRoleReceipt(f, resp.CreateTeamRole.TeamRoleFields, "created", appLabel)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "role description")
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	return cmd
}

func newCmdRoleUpdate(f *cmdutil.Factory) *cobra.Command {
	var description, teamAgent string
	cmd := &cobra.Command{
		Use:   "update <role> --description <d>",
		Short: "Edit a role's description",
		Long: `Edit a role definition's description — the only field a client sets now
that the name register is gone (hadron-server#1050).

Omitted is "preserve" on this server, so an update that names nothing is
refused rather than sent: a no-op write that reports success is worse than
a usage error. Sibling data keys always survive (hadron-server#960).`,
		Example: `  hadron team role update backend-engineer --description "Go services and the GraphQL API"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("description") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass --description")
			}
			scope, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			appRef, appLabel := scope.Ref, lazyAppLabel(cmd.Context(), f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// The fresh read turns an unknown role into an honest NotFound
			// before any write, and lets the mutation send the role as the
			// server spells it rather than as it was typed.
			current, err := resolveRole(cmd.Context(), client, appRef, optStr(teamAgent), args[0])
			if err != nil {
				return err
			}
			resp, err := gen.UpdateTeamRoleMeta(cmd.Context(), client, appRef, optStr(teamAgent), current.Role, &description)
			if err != nil {
				return api.MapError(err)
			}
			if resp.UpdateTeamRole == nil {
				return exitcode.Newf(exitcode.Error, "server returned no role")
			}
			return emitRoleReceipt(f, resp.UpdateTeamRole.TeamRoleFields, "updated", appLabel)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "role description")
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	return cmd
}

func newCmdRoleRm(f *cmdutil.Factory) *cobra.Command {
	var teamAgent string
	var yes bool
	cmd := &cobra.Command{
		Use:     "rm <role>",
		Aliases: []string{"delete"},
		Short:   "Retire a role definition",
		Long: `Retire a roles:<role> definition (deleteTeamRole, hadron-server#1002).
The delete is SOFT — the role node and anything under it (a
roles:<role>:notes is content OF the role) are tombstoned, so the subtree
is recoverable.

UNCONDITIONAL since hadron-server#1050. There used to be a minted-name
gate: a role holding allocated names refused TEAM_ROLE_IN_USE unless
--transfer-register-to handed its register to a successor. Both are gone
with the register — there is no allocation ledger left to preserve, and
nothing for a bare retirement to refuse over.

Retiring does NOT free a name for re-casting: a worker's name is permanent
per App (cor:agt:020:02), enforced against the whole roster. That was true
while the register existed and is true without it, which is the evidence
the register was bookkeeping rather than identity.

The confirmation names the APP before the role (#468) — an ambient scope
is the mistake this prompt is best placed to catch, and the role name only
confirms what you just typed. --yes skips the prompt, so a non-interactive
caller gets the App in the receipt instead.

The role AGENT is a separate object and is NOT touched: retire it with
` + "`app agent remove <app> <agent> --yes`" + ` and ` + "`agent rm <agent> --yes`" + ` once
nothing else needs it.`,
		Example: `  hadron team role rm typo-role --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := roleAppScope(cmd, f)
			if err != nil {
				return err
			}
			appRef, appLabel := scope.Ref, lazyAppLabel(cmd.Context(), f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// An unknown role becomes an honest NotFound before anything is
			// deleted, and the mutation gets the server's own spelling.
			current, err := resolveRole(cmd.Context(), client, appRef, optStr(teamAgent), args[0])
			if err != nil {
				return err
			}
			// Confirm, not ConfirmDeletion: the latter appends "This cannot be
			// undone", which is false here — the delete is SOFT and the
			// subtree stays recoverable (same reasoning as `asset rm`).
			// Build the prompt only when one can be shown. describeRetirement
			// renders the App, which costs a read, and the prompt is an
			// ARGUMENT — so composing it unconditionally would make
			// `role rm --yes --json` pay for a string Confirm discards, which
			// is exactly what lazyAppLabel exists to prevent.
			prompt := ""
			if !yes {
				prompt = describeRetirement(current, appLabel)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			resp, err := gen.DeleteTeamRole(cmd.Context(), client, appRef, optStr(teamAgent), current.Role)
			if err != nil {
				return api.MapError(err)
			}
			if resp.DeleteTeamRole == nil {
				return exitcode.Newf(exitcode.Error, "server returned no result")
			}
			return emitRoleRetired(f, resp.DeleteTeamRole, appLabel)
		},
	}
	cmd.Flags().StringVar(&teamAgent, "team-agent", "", "Team Agent holding the roles branch, when the App installs more than one")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// pickRole finds a role in an already-scanned page, matching case-insensitively
// like the server does.
func pickRole(rows []gen.TeamRoleFields, arg string) (gen.TeamRoleFields, error) {
	for _, r := range rows {
		if strings.EqualFold(r.Role, arg) {
			return r, nil
		}
	}
	var zero gen.TeamRoleFields
	return zero, exitcode.Newf(exitcode.NotFound,
		"no role %q in this App — `hadron team role list` shows the definitions", arg)
}

// describeRetirement is the confirmation QUESTION (Confirm takes the whole
// prompt). The App leads the sentence (#468): the prompt is the ONE moment a
// user is reading carefully before something destructive, and the App is the
// fact most likely to reveal that the ambient scope picked the wrong team —
// naming the role alone confirms only what they already typed. Only rendered
// when a prompt is actually shown, so `--yes` pays nothing for it.
func describeRetirement(r gen.TeamRoleFields, appLabel func() string) string {
	return fmt.Sprintf("In %s: retire role %s? The definition is soft-deleted and stays recoverable.",
		appLabel(), r.Role)
}

func emitRoleRetired(f *cmdutil.Factory, p *deletedRolePayload, appLabel func() string) error {
	dto := roleDeletedDTO{Role: p.Role, NodesDeleted: p.NodesDeleted}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if _, err := fmt.Fprintf(w, "✓ retired role %s — %s tombstoned (soft, recoverable)\n",
			dto.Role, pluralNodes(dto.NodesDeleted)); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "  app: %s\n", appLabel())
		return err
	})
}

func pluralNodes(n int) string {
	if n == 1 {
		return "1 node"
	}
	return fmt.Sprintf("%d nodes", n)
}
