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
		Long: `Show one registered Home Assistant instance: its name, slug, address, owning
organization, whether it is enabled, which ops are allowed ("allowlist": "all"
means every op the server offers, not none) and who created and last changed it.

<id> is the instance ID: the ID column of "hadron ha list". If you get "not
found or not visible", either no instance has that ID or it belongs to an
organization you are not a member of; the server does not say which. Check the
ID with "hadron ha list" and ask an organization admin if it should be yours.`,
		Example: `  hadron ha get <instance-id>
  hadron ha get <instance-id> --json`,
		Args: cobra.ExactArgs(1),
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
				fmt.Fprintf(w, "%s\n  id: %s\n  slug: %s\n  url: %s\n", output.SafeCell(dto.Name), output.SafeCell(dto.ID), output.SafeCell(dto.Slug), output.SafeCell(dto.URL))
				fmt.Fprintf(w, "  org: %s\n  status: %s\n", output.SafeCell(dto.OrganizationID), output.SafeCell(dto.statusLabel()))
				fmt.Fprintf(w, "  allowlist: %s\n", output.SafeCell(dto.allowlistLabel()))
				fmt.Fprintf(w, "  created: %s by %s\n", output.SafeCell(dto.CreatedAt), output.SafeCell(output.Dash(dto.CreatedBy)))
				fmt.Fprintf(w, "  updated: %s by %s\n", output.SafeCell(dto.UpdatedAt), output.SafeCell(output.Dash(dto.UpdatedBy)))
				return nil
			})
		},
	}
}
