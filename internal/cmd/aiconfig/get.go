package aiconfig

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <ref>",
		Short: "Inspect one masked AI config and its endpoint",
		Long: `Inspect one AI service config by id or by its URN (hrn:aiconf:...). The
endpoint is the stored override; effectiveEndpoint is the server's resolved
destination. A null endpoint uses the provider default. The full key is never
returned.

The server computes the config's URN; it is shown as returned and is null for
a name that cannot be a URN atom (and on a server too old to have the field).
The CLI passes a URN ref through untouched. With a URN ref, a config you
cannot read answers exactly like one that does not exist (not found); with an
id ref the server's answer, including Forbidden, is shown as it is.`,
		Example: `  hadron ai-config get 01a1...
  hadron ai-config get hrn:aiconf:acme.com:org:default --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var dto urnConfigDTO
			resp, err := gen.AiServiceConfigWithUrn(cmd.Context(), client, args[0])
			switch {
			case err == nil:
				if resp.AiServiceConfig == nil {
					return notFoundConfig(args[0])
				}
				dto = urnConfigDTO{endpointConfigDTO: dtoFromEndpointFields(resp.AiServiceConfig.AiServiceConfigEndpointFields), URN: resp.AiServiceConfig.Urn}
			case isMissingURNField(err):
				// Older server: no urn field. Ask without it; a URN ref reaches the
				// id lookup there and comes back null, i.e. not found.
				old, err := gen.AiServiceConfigWithEndpoint(cmd.Context(), client, args[0])
				if err != nil {
					return mapEndpointError(err)
				}
				if old.AiServiceConfig == nil {
					return notFoundConfig(args[0])
				}
				dto = urnConfigDTO{endpointConfigDTO: dtoFromEndpointFields(old.AiServiceConfig.AiServiceConfigEndpointFields)}
			default:
				return mapEndpointError(err)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if err := writeConfigLine(w, "config", dto.aiConfigDTO); err != nil {
					return err
				}
				if err := writeURNLine(w, dto.URN); err != nil {
					return err
				}
				return writeEndpointLines(w, dto.endpointConfigDTO)
			})
		},
	}
}
