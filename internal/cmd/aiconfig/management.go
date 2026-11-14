package aiconfig

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type styleConfigDTO struct {
	endpointConfigDTO
	APIStyle          *string `json:"apiStyle"`
	EffectiveAPIStyle *string `json:"effectiveApiStyle"`
}

func styleString(v *gen.AiApiStyle) *string {
	if v == nil {
		return nil
	}
	s := string(*v)
	return &s
}

func dtoFromStyleFields(v gen.AiServiceConfigStyleFields) styleConfigDTO {
	return styleConfigDTO{endpointConfigDTO: dtoFromEndpointFields(v.AiServiceConfigEndpointFields),
		APIStyle: styleString(v.ApiStyle), EffectiveAPIStyle: styleString(v.EffectiveApiStyle)}
}

func parseAPIStyle(v string) (gen.AiApiStyle, error) {
	switch gen.AiApiStyle(strings.ToUpper(v)) {
	case gen.AiApiStyleChatCompletions:
		return gen.AiApiStyleChatCompletions, nil
	case gen.AiApiStyleResponses:
		return gen.AiApiStyleResponses, nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "--api-style must be CHAT_COMPLETIONS or RESPONSES; use --clear-api-style on update for the provider default")
	}
}

func writeStyleConfig(f *cmdutil.Factory, verb string, v gen.AiServiceConfigStyleFields) error {
	dto := dtoFromStyleFields(v)
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		if err := writeConfigLine(w, verb, dto.aiConfigDTO); err != nil {
			return err
		}
		if err := writeEndpointLines(w, dto.endpointConfigDTO); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "  API style: %s; effective: %s\n", output.SafeCell(endpointDisplay(dto.APIStyle, "provider default")), output.SafeCell(endpointDisplay(dto.EffectiveAPIStyle, "single-protocol provider")))
		return err
	})
}

type maskedListFlags struct {
	ownerType, ownerID string
	allOwners          bool
	limit, offset      int
}

func (o *maskedListFlags) requested(cmd *cobra.Command) bool {
	for _, name := range []string{"owner-type", "owner-id", "all-owners", "limit", "offset"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func (o *maskedListFlags) run(cmd *cobra.Command, f *cmdutil.Factory) error {
	changed := cmd.Flags().Changed
	if changed("agent") {
		return exitcode.Newf(exitcode.Usage, "--agent selects the resolved list; use --owner-type AGENT --owner-id for the masked owner list")
	}
	if o.allOwners && (changed("owner-type") || changed("owner-id")) {
		return exitcode.Newf(exitcode.Usage, "--all-owners and --owner-type/--owner-id are mutually exclusive")
	}
	var filter *gen.AiServiceConfigFilter
	if !o.allOwners {
		if !changed("owner-type") || !changed("owner-id") || strings.TrimSpace(o.ownerID) == "" {
			return exitcode.Newf(exitcode.Usage, "pass both --owner-type and --owner-id, or --all-owners, for the masked owner list")
		}
		t := gen.AiConfigOwnerType(strings.ToUpper(o.ownerType))
		switch t {
		case gen.AiConfigOwnerTypeApp, gen.AiConfigOwnerTypeAgent, gen.AiConfigOwnerTypeOrganization, gen.AiConfigOwnerTypeHadronServer:
		default:
			return exitcode.Newf(exitcode.Usage, "--owner-type must be APP, AGENT, ORGANIZATION or HADRON_SERVER")
		}
		filter = &gen.AiServiceConfigFilter{OwnerType: t, OwnerId: o.ownerID}
	}
	var limit, offset *int
	if changed("limit") {
		limit = &o.limit
	}
	if changed("offset") {
		offset = &o.offset
	}
	client, err := f.GraphQLClient()
	if err != nil {
		return err
	}
	dto := struct {
		Items []urnStyleConfigDTO `json:"items"`
		Total int                 `json:"total"`
	}{Items: []urnStyleConfigDTO{}}
	r, err := gen.AiServiceConfigsMaskedWithUrn(cmd.Context(), client, filter, limit, offset)
	switch {
	case err == nil:
		if r.AiServiceConfigs == nil {
			return exitcode.Newf(exitcode.Error, "server returned no AI config page")
		}
		dto.Total = r.AiServiceConfigs.Total
		for _, item := range r.AiServiceConfigs.Items {
			if item != nil {
				dto.Items = append(dto.Items, urnStyleConfigDTO{styleConfigDTO: dtoFromStyleFields(item.AiServiceConfigStyleFields), URN: item.Urn})
			}
		}
	case isMissingURNField(err):
		// Older server: no urn field. Ask without it.
		old, oldErr := gen.AiServiceConfigsMasked(cmd.Context(), client, filter, limit, offset)
		if oldErr != nil {
			return api.MapError(oldErr)
		}
		if old.AiServiceConfigs == nil {
			return exitcode.Newf(exitcode.Error, "server returned no AI config page")
		}
		dto.Total = old.AiServiceConfigs.Total
		for _, item := range old.AiServiceConfigs.Items {
			if item != nil {
				dto.Items = append(dto.Items, urnStyleConfigDTO{styleConfigDTO: dtoFromStyleFields(item.AiServiceConfigStyleFields)})
			}
		}
	default:
		return api.MapError(err)
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		showURN := false
		for _, c := range dto.Items {
			showURN = showURN || hasURN(c.URN)
		}
		header := []string{"ID", "NAME", "OWNER", "OWNER ID", "PROVIDER", "MODEL", "ENABLED", "KEY", "API STYLE"}
		if showURN {
			header = append(header, "URN")
		}
		t := output.NewTable(w, header...)
		for _, c := range dto.Items {
			row := []string{output.SafeCell(c.ID), output.SafeCell(c.Name), output.SafeCell(c.OwnerType), output.SafeCell(c.OwnerID), output.SafeCell(c.Provider), output.SafeCell(c.Model), fmt.Sprint(c.Enabled), output.SafeCell(endpointDisplay(c.APIKeyPreview, "—")), output.SafeCell(endpointDisplay(c.APIStyle, "provider default"))}
			if showURN {
				row = append(row, output.SafeCell(urnCell(c.URN)))
			}
			t.Row(row...)
		}
		if err := t.Flush(); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "total %d\n", dto.Total)
		return err
	})
}
