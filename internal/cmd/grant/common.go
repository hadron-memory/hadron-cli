package grant

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// grantDTO is the stable --json shape for a PrincipalGrant.
type grantDTO struct {
	ID              string   `json:"id"`
	PrincipalType   string   `json:"principalType"`
	PrincipalID     string   `json:"principalId"`
	PrincipalHandle *string  `json:"principalHandle"`
	OrganizationID  string   `json:"organizationId"`
	OrganizationURN *string  `json:"organizationUrn"`
	Actions         []string `json:"actions"`
	ExpiresAt       *string  `json:"expiresAt"`
	CreatedAt       string   `json:"createdAt"`
}

func dtoFromFields(g gen.PrincipalGrantFields) grantDTO {
	dto := grantDTO{
		ID:              g.Id,
		PrincipalType:   g.PrincipalType,
		PrincipalID:     g.PrincipalId,
		PrincipalHandle: g.PrincipalHandle,
		OrganizationID:  g.OrganizationId,
		OrganizationURN: g.OrganizationUrn,
		Actions:         []string{},
		ExpiresAt:       g.ExpiresAt,
		CreatedAt:       g.CreatedAt,
	}
	dto.Actions = append(dto.Actions, g.Actions...)
	return dto
}

// grantee renders the most legible principal label for the table.
func (g grantDTO) grantee() string {
	if g.PrincipalHandle != nil && *g.PrincipalHandle != "" {
		return *g.PrincipalHandle
	}
	return g.PrincipalID
}

// expiry collapses the lifecycle into one legible word for the table. Expiry
// is a wall-clock comparison the server owns; surface the deadline rather
// than guess expired vs. live.
func (g grantDTO) expiry() string {
	if g.ExpiresAt != nil && *g.ExpiresAt != "" {
		return "expires " + *g.ExpiresAt
	}
	return "perpetual"
}

func (g grantDTO) actionList() string {
	return strings.Join(g.Actions, ",")
}
