// Package admin implements `hadron admin ...`: audit reads that organization
// admins and platform admins use. The server decides who may read what
// (cor:api:240:01); nothing here narrows or widens that.
package admin

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// NewCmdAdmin is the `hadron admin` group.
func NewCmdAdmin(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin <command>",
		Short: "Audit reads for organization and platform admins",
		Long: `Audit surfaces for admins.

  impersonation   the audit record of admin-impersonation sessions

Starting or stopping an impersonation is hadron auth impersonate; this group
only reads the record of them. The server decides who may read what: an
organization ADMIN/OWNER for that organization, a platform admin for any.`,
		Example: `  hadron admin impersonation list --org acme.com
  hadron admin impersonation get 01a18cf6400b7fcc8d2f51e7401c55c1`,
	}
	cmd.AddCommand(newCmdImpersonation(f))
	return cmd
}

func newCmdImpersonation(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "impersonation <command>",
		Short: "Read the admin-impersonation audit record",
		Long: `Read the audit record of admin-impersonation sessions: who impersonated whom,
in which organization, why, and when it started, ended or lapsed.

  list   the sessions of one organization (or all, for a platform admin)
  get    one session by id`,
	}
	cmd.AddCommand(newCmdImpersonationList(f))
	cmd.AddCommand(newCmdImpersonationGet(f))
	return cmd
}

type impUserDTO struct {
	ID     string  `json:"id"`
	Handle *string `json:"handle"`
	Name   *string `json:"name"`
}

type impOrgDTO struct {
	ID   string `json:"id"`
	URN  string `json:"urn"`
	Name string `json:"name"`
}

// impersonationDTO is the stable --json shape for one audit record.
type impersonationDTO struct {
	ID           string     `json:"id"`
	AdminUser    impUserDTO `json:"adminUser"`
	TargetUser   impUserDTO `json:"targetUser"`
	Organization impOrgDTO  `json:"organization"`
	Reason       *string    `json:"reason"`
	CreatedAt    string     `json:"createdAt"`
	ExpiresAt    string     `json:"expiresAt"`
	EndedAt      *string    `json:"endedAt"`
	EndedBy      *string    `json:"endedBy"`
	IsActive     bool       `json:"isActive"`
}

func toImpersonationDTO(s gen.ImpersonationSessionFields) impersonationDTO {
	return impersonationDTO{
		ID:           s.Id,
		AdminUser:    impUserDTO{ID: s.AdminUser.Id, Handle: s.AdminUser.Handle, Name: s.AdminUser.Name},
		TargetUser:   impUserDTO{ID: s.TargetUser.Id, Handle: s.TargetUser.Handle, Name: s.TargetUser.Name},
		Organization: impOrgDTO{ID: s.Organization.Id, URN: s.Organization.Urn, Name: s.Organization.Name},
		Reason:       s.Reason,
		CreatedAt:    s.CreatedAt,
		ExpiresAt:    s.ExpiresAt,
		EndedAt:      s.EndedAt,
		EndedBy:      s.EndedBy,
		IsActive:     s.IsActive,
	}
}

func userLabel(u impUserDTO) string {
	switch {
	case u.Handle != nil && *u.Handle != "":
		return "@" + *u.Handle
	case u.Name != nil && *u.Name != "":
		return *u.Name
	}
	return u.ID
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

type sessionNode = gen.ImpersonationSessionsImpersonationSessionsImpersonationSessionListItemsImpersonationSession

func newCmdImpersonationList(f *cmdutil.Factory) *cobra.Command {
	var org string
	var active bool
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list [--org <org>] [--active]",
		Aliases: []string{"ls"},
		Short:   "List impersonation sessions, newest first",
		Long: `List admin-impersonation sessions, newest first.

--org names the organization whose sessions to list; you must be one of its
ADMIN/OWNERs. A platform admin may leave it out to list every organization.
Anyone else who leaves it out, or names an organization they do not administer,
is refused by the server (exit 8, printed as the server sent it).
--active keeps only sessions that are live right now (not ended, not expired).

By default this drains every page, so the listing is the whole slice. Passing
--limit (server default 20, cap 200) or --offset returns exactly that page.
--json is an array; each record has id, adminUser, targetUser, organization,
reason, createdAt, expiresAt, endedAt, endedBy and isActive; empty is [].`,
		Example: `  hadron admin impersonation list --org acme.com
  hadron admin impersonation list --org acme.com --active --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("org") && org == "" {
				return exitcode.Newf(exitcode.Usage, "--org is empty; give an organization id or URN, or omit it")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var orgArg *string
			if org != "" {
				orgArg = &org
			}
			var filter *gen.ImpersonationSessionFilter
			if active {
				filter = &gen.ImpersonationSessionFilter{ActiveOnly: &active}
			}
			rows := []impersonationDTO{}
			errIncomplete := exitcode.Newf(exitcode.Error, "the server returned an incomplete impersonation listing; not reporting it as empty")
			collect := func(items []*sessionNode) error {
				for _, it := range items {
					if it == nil {
						return errIncomplete
					}
					rows = append(rows, toImpersonationDTO(it.ImpersonationSessionFields))
				}
				return nil
			}
			if cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset") {
				var lim, off *int
				if cmd.Flags().Changed("limit") {
					lim = &limit
				}
				if cmd.Flags().Changed("offset") {
					off = &offset
				}
				resp, err := gen.ImpersonationSessions(cmd.Context(), client, orgArg, filter, lim, off)
				if err != nil {
					return api.MapErrorKeepingCode(err)
				}
				if resp == nil || resp.ImpersonationSessions == nil {
					return errIncomplete
				}
				if err := collect(resp.ImpersonationSessions.Items); err != nil {
					return err
				}
			} else {
				items, err := api.CollectAll(func(limit, offset int) ([]*sessionNode, int, error) {
					resp, err := gen.ImpersonationSessions(cmd.Context(), client, orgArg, filter, &limit, &offset)
					if err != nil {
						return nil, 0, api.MapErrorKeepingCode(err)
					}
					if resp == nil || resp.ImpersonationSessions == nil {
						return nil, 0, errIncomplete
					}
					return resp.ImpersonationSessions.Items, resp.ImpersonationSessions.Total, nil
				})
				if err != nil {
					return err
				}
				if err := collect(items); err != nil {
					return err
				}
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "ADMIN", "TARGET", "ORG", "STATE", "STARTED", "REASON")
				for _, r := range rows {
					t.Row(output.SafeCell(r.ID), output.SafeCell(userLabel(r.AdminUser)), output.SafeCell(userLabel(r.TargetUser)),
						output.SafeCell(r.Organization.Name), stateOf(r), output.SafeCell(r.CreatedAt), output.SafeCell(orDash(r.Reason)))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization id or URN (required unless you are a platform admin)")
	cmd.Flags().BoolVar(&active, "active", false, "only sessions that are live right now")
	cmd.Flags().IntVar(&limit, "limit", 0, "max results per page (server default 20, cap 200)")
	cmd.Flags().IntVar(&offset, "offset", 0, "results to skip")
	return cmd
}

func stateOf(r impersonationDTO) string {
	switch {
	case r.IsActive:
		return "active"
	case r.EndedAt != nil:
		return "ended"
	}
	return "expired"
}

func newCmdImpersonationGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one impersonation session",
		Long: `Show one admin-impersonation session by id (from hadron admin impersonation
list). It is readable by the admin who ran it, an ADMIN/OWNER of its
organization, or a platform admin. For anyone else, and for an id that does not
exist, the server answers the same way, so this exits 4 with one message that
does not say which. --json is the record described under list.`,
		Example: `  hadron admin impersonation get 01a18cf6400b7fcc8d2f51e7401c55c1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ImpersonationSession(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.ImpersonationSession == nil {
				return exitcode.Newf(exitcode.NotFound, "no impersonation session %q is readable here", args[0])
			}
			d := toImpersonationDTO(resp.ImpersonationSession.ImpersonationSessionFields)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				t := output.NewTable(w, "FIELD", "VALUE")
				t.Row("id", output.SafeCell(d.ID))
				t.Row("state", stateOf(d))
				t.Row("admin", output.SafeCell(userLabel(d.AdminUser)))
				t.Row("target", output.SafeCell(userLabel(d.TargetUser)))
				t.Row("org", output.SafeCell(fmt.Sprintf("%s (%s)", d.Organization.Name, d.Organization.URN)))
				t.Row("reason", output.SafeCell(orDash(d.Reason)))
				t.Row("started", output.SafeCell(d.CreatedAt))
				t.Row("expires", output.SafeCell(d.ExpiresAt))
				t.Row("ended", output.SafeCell(orDash(d.EndedAt)))
				t.Row("ended by", output.SafeCell(orDash(d.EndedBy)))
				return t.Flush()
			})
		},
	}
}
