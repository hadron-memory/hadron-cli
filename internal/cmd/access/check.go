package access

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

// checkUserDTO is the resolved subject of an access check.
type checkUserDTO struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Email  *string `json:"email"`
	Handle *string `json:"handle"`
}

// checkResourceDTO names the resource the access was evaluated against.
type checkResourceDTO struct {
	URN  string `json:"urn"`
	Kind string `json:"kind"`
}

// grantDTO is one reason access is (or would be) granted.
type grantDTO struct {
	Source string  `json:"source"`
	Role   string  `json:"role"`
	Via    *string `json:"via"`
}

// checkDTO is the stable --json shape for `access check`. Field names mirror
// the server's effectiveAccess so the contract reads the same on both sides.
type checkDTO struct {
	User      checkUserDTO     `json:"user"`
	Resource  checkResourceDTO `json:"resource"`
	CanRead   bool             `json:"canRead"`
	CanWrite  bool             `json:"canWrite"`
	CanManage bool             `json:"canManage"`
	CanDelete bool             `json:"canDelete"`
	Role      *string          `json:"role"`
	Grants    []grantDTO       `json:"grants"`
}

func newCmdCheck(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <user> <resource>",
		Short: "Show a user's effective access to a resource",
		Long: `Show the effective access a user has to a resource, with the grants
that confer it.

The user is identified by id, email, or handle. The resource is a
fully-qualified URN — hrn:memory:…, hrn:node:…, hrn:app:…, or
hrn:agent:… — or a bare AiServiceConfig id.

Reading this requires permission to audit the resource: a platform
admin, an ADMIN/OWNER of the resource's owning org, or (for a strict-
owner memory) the memory's principal.`,
		Example: `  hadron access check alice@acme.com hrn:memory:acme.com::kb
  hadron access check @alice hrn:node:acme.com::kb::start-here
  hadron access check usr_123 hrn:app:acme.com::support --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Validate the resource ref locally before the user lookup so a
			// malformed resource fails fast without a network round-trip.
			resource, err := normalizeResourceRef(args[1])
			if err != nil {
				return err
			}
			userID, err := resolveUserID(cmd, client, args[0])
			if err != nil {
				return err
			}

			resp, err := gen.EffectiveAccess(cmd.Context(), client, userID, resource)
			if err != nil {
				return api.MapError(err)
			}
			ea := resp.EffectiveAccess
			if ea == nil || ea.User == nil {
				return exitcode.Newf(exitcode.Error, "server returned no access result")
			}

			dto := checkDTO{
				User: checkUserDTO{
					ID:     ea.User.Id,
					Name:   ea.User.Name,
					Email:  ea.User.Email,
					Handle: ea.User.Handle,
				},
				Resource:  checkResourceDTO{URN: ea.ResourceUrn, Kind: ea.ResourceKind},
				CanRead:   ea.CanRead,
				CanWrite:  ea.CanWrite,
				CanManage: ea.CanManage,
				CanDelete: ea.CanDelete,
				Role:      ea.Role,
				Grants:    []grantDTO{},
			}
			for _, g := range ea.Grants {
				if g == nil {
					continue
				}
				dto.Grants = append(dto.Grants, grantDTO{
					Source: string(g.Source),
					Role:   g.Role,
					Via:    g.Via,
				})
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderCheck(w, dto)
			})
		},
	}
	return cmd
}

func renderCheck(w io.Writer, dto checkDTO) error {
	fmt.Fprintf(w, "User:     %s (%s)\n", userLabel(dto.User), dto.User.ID)
	fmt.Fprintf(w, "Resource: %s (%s)\n", dto.Resource.URN, dto.Resource.Kind)
	fmt.Fprintf(w, "Role:     %s\n\n", strDash(dto.Role))

	caps := output.NewTable(w, "READ", "WRITE", "MANAGE", "DELETE")
	caps.Row(checkMark(dto.CanRead), checkMark(dto.CanWrite), checkMark(dto.CanManage), checkMark(dto.CanDelete))
	if err := caps.Flush(); err != nil {
		return err
	}

	if len(dto.Grants) == 0 {
		_, err := fmt.Fprintf(w, "\nNo access — %s has no grants on this resource.\n", userLabel(dto.User))
		return err
	}

	fmt.Fprintln(w, "\nGrants:")
	t := output.NewTable(w, "SOURCE", "ROLE", "VIA")
	for _, g := range dto.Grants {
		t.Row(g.Source, g.Role, strDash(g.Via))
	}
	return t.Flush()
}

func userLabel(u checkUserDTO) string {
	switch {
	case u.Email != nil && *u.Email != "":
		return *u.Email
	case u.Handle != nil && *u.Handle != "":
		return *u.Handle
	case u.Name != nil && *u.Name != "":
		return *u.Name
	default:
		return u.ID
	}
}

func checkMark(b bool) string {
	if b {
		return "✓"
	}
	return "✗"
}

func strDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}
