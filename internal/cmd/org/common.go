// Package org implements `hadron org ...` — organization and membership
// management.
package org

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// orgDTO is the stable --json shape for an organization.
type orgDTO struct {
	ID        string `json:"id"`
	URN       string `json:"urn"`
	Name      string `json:"name"`
	IsVisible *bool  `json:"isVisible"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// userDTO is the stable --json shape for a user (search results, member.user).
type userDTO struct {
	ID             string   `json:"id"`
	Name           *string  `json:"name"`
	Email          *string  `json:"email"`
	Handle         *string  `json:"handle"`
	GithubUsername *string  `json:"githubUsername"`
	Roles          []string `json:"roles"`
}

// memberDTO is the stable --json shape for an org membership. CanInvite is only
// populated by `member ls` (the mutations don't project it).
type memberDTO struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	CanInvite *bool   `json:"canInvite"`
	User      userDTO `json:"user"`
}

func orgDTOFromFields(o gen.OrgFields) orgDTO {
	return orgDTO{
		ID:        o.Id,
		URN:       o.Urn,
		Name:      o.Name,
		IsVisible: o.IsVisible,
		CreatedAt: o.CreatedAt,
		UpdatedAt: o.UpdatedAt,
	}
}

func userDTOFromFields(u gen.UserFields) userDTO {
	roles := make([]string, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, string(r))
	}
	return userDTO{
		ID:             u.Id,
		Name:           u.Name,
		Email:          u.Email,
		Handle:         u.Handle,
		GithubUsername: u.GithubUsername,
		Roles:          roles,
	}
}

// parseRole maps a --role flag (case-insensitive) to the Role enum.
func parseRole(s string) (gen.Role, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "OWNER":
		return gen.RoleOwner, nil
	case "ADMIN":
		return gen.RoleAdmin, nil
	case "CONTRIBUTOR":
		return gen.RoleContributor, nil
	case "READER":
		return gen.RoleReader, nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "invalid --role %q (want OWNER, ADMIN, CONTRIBUTOR, or READER)", s)
	}
}

// orDash renders an optional string for tables.
func orDash(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	return *s
}
