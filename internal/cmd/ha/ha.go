// Package ha implements `hadron ha ...` — the Home Assistant instance registry
// (hadrontool-home-assistant; hadron-server#640). Org-owned instance rows whose
// ops headless runs invoke as `ha__<slug>__<op>` in a node's `data.tools`.
//
// This is the READ-ONLY cut (cli#231): list / get / ops. It never contacts a
// Home Assistant host and never touches the write-only access token — the
// registry exposes no token field. Registering an endpoint grants nothing; a
// run must still be allowed `tool.ha__<slug>__<op>` by the policy chain.
// Credential-bearing writes (create/update/delete) are separately gated.
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
		Short: "Read the Home Assistant instance registry",
		Long: `Read registered Home Assistant instances (hadrontool-home-assistant).

Each row exposes its ops to headless runs as ha__<slug>__<op>, which an author
declares in a node's data.tools. These commands only read the registry: they
never contact a Home Assistant host, and the access token is write-only — no
command here can show it. Registration alone grants nothing; a run must still
be allowed 'tool.ha__<slug>__<op>' by the policy chain.`,
	}
	cmd.AddCommand(newCmdList(f))
	cmd.AddCommand(newCmdGet(f))
	cmd.AddCommand(newCmdOps(f))
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
