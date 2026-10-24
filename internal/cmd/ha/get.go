package ha

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "get <id>",
		Aliases: []string{"show"},
		Short:   "Show one registered Home Assistant instance",
		Long: `Show one registered Home Assistant instance by registry id.

A null answer means the row is absent OR you are not a member of its org; the
server deliberately does not distinguish the two, so neither does this command.`,
		Example: `  hadron ha get 01h... --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.HomeAssistantInstance(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.HomeAssistantInstance == nil {
				return exitcode.Newf(exitcode.NotFound, "Home Assistant instance %q not found or not visible to you", args[0])
			}
			dto := dtoFromFields(resp.HomeAssistantInstance.HomeAssistantInstanceFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  id: %s\n  slug: %s\n  url: %s\n", dto.Name, dto.ID, dto.Slug, dto.URL)
				fmt.Fprintf(w, "  org: %s\n  status: %s\n", dto.OrganizationID, dto.statusLabel())
				fmt.Fprintf(w, "  allowlist: %s\n", dto.allowlistLabel())
				fmt.Fprintf(w, "  created: %s by %s\n", dto.CreatedAt, output.Dash(dto.CreatedBy))
				fmt.Fprintf(w, "  updated: %s by %s\n", dto.UpdatedAt, output.Dash(dto.UpdatedBy))
				return nil
			})
		},
	}
}
