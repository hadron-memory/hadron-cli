// Package webhook implements `hadron webhook ...` — inbound webhook triggers
// for headless runs (spec-040, D-2026-05-02). A POST to the webhook's URL fires
// an entry node under an App's identity. The URL path and platform token are
// shown ONCE at create/rotate and are never queryable again.
package webhook

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
)

// webhookDTO is the stable --json shape for an AgentWebhook. The secret is never
// part of it — only the create/rotate credentials carry the path and token.
type webhookDTO struct {
	ID             string           `json:"id"`
	OrganizationID string           `json:"organizationId"`
	AppID          string           `json:"appId"`
	AgentID        *string          `json:"agentId"`
	Name           string           `json:"name"`
	Enabled        bool             `json:"enabled"`
	EntryNodeURN   string           `json:"entryNodeUrn"`
	AIConfigName   *string          `json:"aiConfigName"`
	UserID         *string          `json:"userId"`
	CreatedBy      *string          `json:"createdBy"`
	ArgsSchema     *json.RawMessage `json:"argsSchema"`
	EventData      *json.RawMessage `json:"eventData"`
	Policy         *json.RawMessage `json:"policy"`
	LastCalledAt   *string          `json:"lastCalledAt"`
	CreatedAt      string           `json:"createdAt"`
}

// credentialsDTO is the stable --json shape for a create/rotate result. It
// carries the shown-once path and token alongside the webhook record.
type credentialsDTO struct {
	Path    string     `json:"path"`
	Token   string     `json:"token"`
	Webhook webhookDTO `json:"webhook"`
}

func dtoFromFields(f gen.AgentWebhookFields) webhookDTO {
	return webhookDTO{
		ID:             f.Id,
		OrganizationID: f.OrganizationId,
		AppID:          f.AppId,
		AgentID:        f.AgentId,
		Name:           f.Name,
		Enabled:        f.Enabled,
		EntryNodeURN:   f.EntryNodeUrn,
		AIConfigName:   f.AiConfigName,
		UserID:         f.UserId,
		CreatedBy:      f.CreatedBy,
		ArgsSchema:     f.ArgsSchema,
		EventData:      f.EventData,
		Policy:         f.Policy,
		LastCalledAt:   f.LastCalledAt,
		CreatedAt:      f.CreatedAt,
	}
}

func credsFromFields(f gen.AgentWebhookCredentialFields) credentialsDTO {
	dto := credentialsDTO{Path: f.Path, Token: f.Token}
	if f.Webhook != nil {
		dto.Webhook = dtoFromFields(f.Webhook.AgentWebhookFields)
	}
	return dto
}

// writeCredentials renders the human view of a create/rotate result. The path
// and token are the secret — shown once, never queryable again — so they are
// framed with an explicit warning.
func writeCredentials(w io.Writer, verb string, c credentialsDTO) error {
	if _, err := fmt.Fprintf(w, "✓ %s webhook %s\n", verb, c.Webhook.Name); err != nil {
		return err
	}
	fmt.Fprintf(w, "  id %s  app %s  entry %s\n", c.Webhook.ID, c.Webhook.AppID, c.Webhook.EntryNodeURN)
	fmt.Fprintln(w, "\n  ⚠ Shown once — store these now; the secret is never queryable again.")
	fmt.Fprintf(w, "  URL path:       %s\n", c.Path)
	fmt.Fprintf(w, "  platform token: %s\n", c.Token)
	fmt.Fprintf(w, "  Call it as:     POST %s?hpt=%s\n", c.Path, c.Token)
	return nil
}
