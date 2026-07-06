// Package ticket implements `hadron ticket ...` — the action-ticket ledger
// (spec-040, cor:acl:050:04 tier 2). Tickets are consumable grants (v1:
// comm.outbound) an org ADMIN mints into the ledger; a headless run consumes one
// per gated action. This group mints tickets and reads the ledger.
package ticket

import (
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// mintResultDTO is the stable --json shape for a `ticket mint` result — the
// count minted plus the scope it was minted for.
type mintResultDTO struct {
	Minted int    `json:"minted"`
	Action string `json:"action"`
	OrgID  string `json:"orgId"`
}

// ticketDTO is the stable --json shape for an ActionTicket.
type ticketDTO struct {
	ID              string  `json:"id"`
	OrganizationID  string  `json:"organizationId"`
	AppID           *string `json:"appId"`
	Action          string  `json:"action"`
	MintedBy        string  `json:"mintedBy"`
	Note            *string `json:"note"`
	ConsumedByRunID *string `json:"consumedByRunId"`
	ConsumedAt      *string `json:"consumedAt"`
	ExpiresAt       *string `json:"expiresAt"`
	CreatedAt       string  `json:"createdAt"`
}

func dtoFromFields(f gen.ActionTicketFields) ticketDTO {
	return ticketDTO{
		ID:              f.Id,
		OrganizationID:  f.OrganizationId,
		AppID:           f.AppId,
		Action:          f.Action,
		MintedBy:        f.MintedBy,
		Note:            f.Note,
		ConsumedByRunID: f.ConsumedByRunId,
		ConsumedAt:      f.ConsumedAt,
		ExpiresAt:       f.ExpiresAt,
		CreatedAt:       f.CreatedAt,
	}
}

// status collapses the ledger lifecycle into one legible word for the table.
func (t ticketDTO) status() string {
	if t.ConsumedAt != nil && *t.ConsumedAt != "" {
		return "consumed"
	}
	if t.ExpiresAt != nil && *t.ExpiresAt != "" {
		// Expiry is a wall-clock comparison the server owns; without a clock
		// here, surface the deadline rather than guess expired vs. live.
		return "expires " + *t.ExpiresAt
	}
	return "available"
}
