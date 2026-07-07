package memory

import (
	"fmt"
	"io"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// accessUserDTO is the user shape carried by member/share output.
type accessUserDTO struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Email  *string `json:"email"`
	Handle *string `json:"handle"`
}

// memberDTO is the stable --json shape for a memory membership row.
type memberDTO struct {
	Role string        `json:"role"`
	User accessUserDTO `json:"user"`
}

// shareDTO is the stable --json shape for a memory share grant.
type shareDTO struct {
	Role    string        `json:"role"`
	Grantee accessUserDTO `json:"grantee"`
}

// subscriptionDTO is the stable --json shape for a memory subscription — an
// ORGANIZATION granted a role on the memory (member/share are per-user).
// `activated` reflects the server's activation state.
type subscriptionDTO struct {
	Role         string `json:"role"`
	Activated    bool   `json:"activated"`
	Organization orgRef `json:"organization"`
}

type orgRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URN  string `json:"urn"`
}

func userFromMemFields(u gen.MemUserFields) accessUserDTO {
	return accessUserDTO{ID: u.Id, Name: u.Name, Email: u.Email, Handle: u.Handle}
}

func parseMemberRole(s string) (gen.MemoryMemberRole, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "owner":
		return gen.MemoryMemberRoleOwner, nil
	case "writer":
		return gen.MemoryMemberRoleWriter, nil
	case "reader":
		return gen.MemoryMemberRoleReader, nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "invalid --role %q (want owner, writer, or reader)", s)
	}
}

func parseShareRole(s string) (gen.MemoryShareRole, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "writer":
		return gen.MemoryShareRoleWriter, nil
	case "reader":
		return gen.MemoryShareRoleReader, nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "invalid --role %q (want writer or reader)", s)
	}
}

// parseSubscriptionRole parses the general Role enum (subscriptions carry the
// full ADMIN/CONTRIBUTOR/OWNER/READER set, not the reduced member/share roles).
func parseSubscriptionRole(s string) (gen.Role, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ADMIN":
		return gen.RoleAdmin, nil
	case "CONTRIBUTOR":
		return gen.RoleContributor, nil
	case "OWNER":
		return gen.RoleOwner, nil
	case "READER":
		return gen.RoleReader, nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "invalid --role %q (want admin, contributor, owner, or reader)", s)
	}
}

func accessDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}

// accessLabel picks the most human label for a user.
func accessLabel(u accessUserDTO) string {
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

func emitMember(f *cmdutil.Factory, verb string, dto memberDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "%s %s as %s\n", verb, accessLabel(dto.User), dto.Role)
		return err
	})
}

func emitShare(f *cmdutil.Factory, verb string, dto shareDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		_, err := fmt.Fprintf(w, "%s %s as %s\n", verb, accessLabel(dto.Grantee), dto.Role)
		return err
	})
}
