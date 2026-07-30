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
	ID   string `json:"id"`
	URN  string `json:"urn"`
	Name string `json:"name"`
	// ListedOnMarketplace is the marketplace-catalogue flag (cor:acl:080:04) that
	// replaced the server's removed Organization.isVisible; the org's separate
	// discoverability now lives behind publicOrganization (#270).
	ListedOnMarketplace bool `json:"listedOnMarketplace"`
	// IsVisible is a DEPRECATED compatibility alias mirroring ListedOnMarketplace.
	// The server dropped Organization.isVisible, but --json is a stable public
	// contract (agents select this key), so it's retained rather than removed —
	// it now carries the listedOnMarketplace value. Slated for removal at a major
	// version bump.
	IsVisible *bool  `json:"isVisible"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// userDTO is the stable --json shape for a user (search results, member.user).
// The identity fields mirror the user package's DTO: a roster of one org's
// members is where two rows for the same human are most visible, and telling
// that apart from two humans needs the provider identity, not just the profile.
type userDTO struct {
	ID               string   `json:"id"`
	Name             *string  `json:"name"`
	Email            *string  `json:"email"`
	Handle           *string  `json:"handle"`
	GithubUsername   *string  `json:"githubUsername"`
	Roles            []string `json:"roles"`
	IdentityProvider *string  `json:"identityProvider"`
	GithubID         *int     `json:"githubId"`
	ExternalID       *string  `json:"externalId"`
	ExternalAppID    *string  `json:"externalAppId"`
	LinkedAt         *string  `json:"linkedAt"`
}

// memberDTO is the stable --json shape for an org membership. CanInvite is only
// populated by `member list` (the mutations don't project it).
type memberDTO struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	CanInvite *bool   `json:"canInvite"`
	User      userDTO `json:"user"`
}

func orgDTOFromFields(o gen.OrgFields) orgDTO {
	listed := o.ListedOnMarketplace
	return orgDTO{
		ID:                  o.Id,
		URN:                 o.Urn,
		Name:                o.Name,
		ListedOnMarketplace: listed,
		IsVisible:           &listed, // deprecated alias — mirrors listedOnMarketplace
		CreatedAt:           o.CreatedAt,
		UpdatedAt:           o.UpdatedAt,
	}
}

func userDTOFromFields(u gen.UserFields) userDTO {
	roles := make([]string, 0, len(u.Roles))
	for _, r := range u.Roles {
		roles = append(roles, string(r))
	}
	return userDTO{
		ID:               u.Id,
		Name:             u.Name,
		Email:            u.Email,
		Handle:           u.Handle,
		GithubUsername:   u.GithubUsername,
		Roles:            roles,
		IdentityProvider: u.IdentityProvider,
		GithubID:         u.GithubId,
		ExternalID:       u.ExternalId,
		ExternalAppID:    u.ExternalAppId,
		LinkedAt:         u.LinkedAt,
	}
}

// invitationDTO is the stable --json shape for an org invitation. `slug` is the
// acceptance token the invitee redeems with `org invite accept <slug>`.
type invitationDTO struct {
	ID              string  `json:"id"`
	Slug            string  `json:"slug"`
	Email           *string `json:"email"`
	Name            *string `json:"name"`
	GithubUsername  *string `json:"githubUsername"`
	MemberRole      string  `json:"memberRole"`
	OrganizationID  *string `json:"organizationId"`
	MaxActivations  *int    `json:"maxActivations"`
	ActivationCount int     `json:"activationCount"`
	ExpiresAt       *string `json:"expiresAt"`
	AcceptedAt      *string `json:"acceptedAt"`
	CreatedAt       string  `json:"createdAt"`
}

func invDTOFromFields(i gen.InvitationFields) invitationDTO {
	return invitationDTO{
		ID: i.Id, Slug: i.Slug, Email: i.Email, Name: i.Name,
		GithubUsername: i.GithubUsername, MemberRole: string(i.MemberRole),
		OrganizationID: i.OrganizationId, MaxActivations: i.MaxActivations,
		ActivationCount: i.ActivationCount, ExpiresAt: i.ExpiresAt,
		AcceptedAt: i.AcceptedAt, CreatedAt: i.CreatedAt,
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
