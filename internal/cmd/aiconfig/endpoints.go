package aiconfig

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

type providerEndpointDTO struct {
	URL       string  `json:"url"`
	Label     string  `json:"label"`
	IsDefault bool    `json:"isDefault"`
	Note      *string `json:"note"`
}

func newCmdEndpoints(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "endpoints <provider>",
		Short: "Show the server's endpoint choices and billing notes",
		Long: `Show endpoint suggestions from the server for one provider. The
default choice is the destination selected when a config has no stored
endpoint. Notes may explain billing or provider terms; an empty list means
the server has no suggestions. This command never changes a config.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AiProviderEndpoints(cmd.Context(), client, args[0])
			if err != nil {
				return mapEndpointError(err)
			}
			items := make([]providerEndpointDTO, 0, len(resp.AiProviderEndpoints))
			for _, e := range resp.AiProviderEndpoints {
				if e != nil {
					items = append(items, providerEndpointDTO{URL: e.Url, Label: e.Label, IsDefault: e.IsDefault, Note: e.Note})
				}
			}
			return output.Write(f.IOStreams, f.JSON, items, func(w io.Writer) error {
				t := output.NewTable(w, "URL", "LABEL", "DEFAULT", "NOTE")
				for _, e := range items {
					flag := ""
					if e.IsDefault {
						flag = "yes"
					}
					t.Row(e.URL, e.Label, flag, endpointDisplay(e.Note, ""))
				}
				return t.Flush()
			})
		},
	}
}
