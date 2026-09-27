package aiconfig

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Inspect one masked AI config and its endpoint",
		Long: `Inspect one AI service config by id. The endpoint is the stored
override; effectiveEndpoint is the server's resolved destination. A null
endpoint uses the provider default. The full key is never returned.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AiServiceConfigWithEndpoint(cmd.Context(), client, args[0])
			if err != nil {
				return mapEndpointError(err)
			}
			if resp.AiServiceConfig == nil {
				return exitcode.Newf(exitcode.NotFound, "AI service config %q was not found", args[0])
			}
			dto := dtoFromEndpointFields(resp.AiServiceConfig.AiServiceConfigEndpointFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if err := writeConfigLine(w, "config", dto.aiConfigDTO); err != nil {
					return err
				}
				return writeEndpointLines(w, dto)
			})
		},
	}
}
