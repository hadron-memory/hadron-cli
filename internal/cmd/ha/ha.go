// Package ha implements `hadron ha ...` — the Home Assistant instance registry
// (hadrontool-home-assistant; hadron-server#640). Org-owned instance rows whose
// ops headless runs invoke as `ha__<slug>__<op>` in a node's `data.tools`.
//
// list / get / ops read the registry; create / update / rm write it (cli#231).
// Nothing here contacts a Home Assistant host. The access token is write-only:
// it is read from stdin or a file (never argv), sent, and never printed — the
// registry exposes no token field. Registering an endpoint grants nothing; a
// run must still be allowed `tool.ha__<slug>__<op>` by the policy chain.
package ha

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
)

// NewCmdHa builds the `ha` command group.
func NewCmdHa(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ha <command>",
		Short: "Manage the Home Assistant instance registry",
		Long: `See which Home Assistant instances (smart-home hubs) are registered for your
organizations, and which actions ("ops") an AI agent may take on each one.
Start with "ha list", then "ha ops <id>" for one instance's actions.

An organization admin registers an instance with "ha create", changes it with
"ha update" and deletes it with "ha rm". None of these commands contacts the
Home Assistant host. Its access token is write-only: you give it from stdin or
a file, and no command ever shows it.

Details:
  Each registered instance offers its ops to headless (unattended) runs under
  the name ha__<slug>__<op>, which an author lists in a node's data.tools.
  Registering an instance grants nothing by itself: a run must still be allowed
  tool.ha__<slug>__<op> by the policy chain (the rules that decide what a run
  may use). The registry is the hadrontool-home-assistant tool's.`,
		Example: `  hadron ha list --org hrn:org:acme.com
  hadron ha ops <instance-id>
  printf '%s' "$HA_TOKEN" | hadron ha create --org hrn:org:acme.com --slug home \
    --name Home --url https://ha.example.com --token-file -`,
	}
	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdOps(f))
	cmd.AddCommand(newCmdCreate(f))
	cmd.AddCommand(newCmdUpdate(f))
	cmd.AddCommand(newCmdRm(f))
	return cmd
}

// instanceDTO is the stable --json shape for a HomeAssistantInstance. There is
// no token field: the registry never returns one. CreatedBy/UpdatedBy keep
// their nulls.
type instanceDTO struct {
	ID             string   `json:"id"`
	OrganizationID string   `json:"organizationId"`
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	URL            string   `json:"url"`
	Enabled        bool     `json:"enabled"`
	OpAllowlist    []string `json:"opAllowlist"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
	CreatedBy      *string  `json:"createdBy"`
	UpdatedBy      *string  `json:"updatedBy"`
}

func dtoFromFields(s gen.HomeAssistantInstanceFields) instanceDTO {
	dto := instanceDTO{
		ID:             s.Id,
		OrganizationID: s.OrganizationId,
		Slug:           s.Slug,
		Name:           s.Name,
		URL:            s.Url,
		Enabled:        s.Enabled,
		OpAllowlist:    []string{},
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
		CreatedBy:      s.CreatedBy,
		UpdatedBy:      s.UpdatedBy,
	}
	dto.OpAllowlist = append(dto.OpAllowlist, s.OpAllowlist...)
	return dto
}

func (s instanceDTO) statusLabel() string {
	if s.Enabled {
		return "enabled"
	}
	return "disabled"
}

// allowlistLabel renders the allowlist for a table. An EMPTY allowlist means
// every op in the catalog — not deny-all — so it reads "all", never blank.
func (s instanceDTO) allowlistLabel() string {
	if len(s.OpAllowlist) == 0 {
		return "all"
	}
	return strings.Join(s.OpAllowlist, ",")
}

// opDTO is the stable --json shape for one op. RunToolName is the server's own
// string — never composed client-side.
type opDTO struct {
	Op          string           `json:"op"`
	RunToolName string           `json:"runToolName"`
	Description string           `json:"description"`
	InputSchema *json.RawMessage `json:"inputSchema"`
}
