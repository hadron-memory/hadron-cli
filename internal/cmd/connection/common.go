package connection

import (
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// validScopeList is the closed set of connection-grant scopes (spec-042);
// validScopes indexes it for the create-time check. Kept together so the flag
// help and the validation never drift.
var validScopeList = []string{"mail.read", "mail.send", "calendar.freebusy", "calendar.read"}

var validScopes = func() map[string]bool {
	m := make(map[string]bool, len(validScopeList))
	for _, s := range validScopeList {
		m[s] = true
	}
	return m
}()

// connectionGrantDTO is the stable --json shape for a ConnectionGrant.
type connectionGrantDTO struct {
	ID             string   `json:"id"`
	ConnectionID   string   `json:"connectionId"`
	GranteeAppID   string   `json:"granteeAppId"`
	GranteeAppName *string  `json:"granteeAppName"`
	GranteeAppURN  *string  `json:"granteeAppUrn"`
	Scopes         []string `json:"scopes"`
	ExpiresAt      *string  `json:"expiresAt"`
	CreatedAt      string   `json:"createdAt"`
}

func dtoFromFields(g gen.ConnectionGrantFields) connectionGrantDTO {
	dto := connectionGrantDTO{
		ID:             g.Id,
		ConnectionID:   g.ConnectionId,
		GranteeAppID:   g.GranteeAppId,
		GranteeAppName: g.GranteeAppName,
		GranteeAppURN:  g.GranteeAppUrn,
		Scopes:         []string{},
		ExpiresAt:      g.ExpiresAt,
		CreatedAt:      g.CreatedAt,
	}
	dto.Scopes = append(dto.Scopes, g.Scopes...)
	return dto
}

// grantee renders the most legible App label for the table: URN, then name,
// then the raw id.
func (g connectionGrantDTO) grantee() string {
	if g.GranteeAppURN != nil && *g.GranteeAppURN != "" {
		return *g.GranteeAppURN
	}
	if g.GranteeAppName != nil && *g.GranteeAppName != "" {
		return *g.GranteeAppName
	}
	return g.GranteeAppID
}

// expiry collapses the lifecycle into one legible word for the table. Expiry
// is a wall-clock comparison the server owns; surface the deadline rather than
// guess expired vs. live.
func (g connectionGrantDTO) expiry() string {
	if g.ExpiresAt != nil && *g.ExpiresAt != "" {
		return "expires " + *g.ExpiresAt
	}
	return "perpetual"
}

func (g connectionGrantDTO) scopeList() string {
	return strings.Join(g.Scopes, ",")
}
