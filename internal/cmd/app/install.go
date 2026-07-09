package app

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdInstall(f *cmdutil.Factory) *cobra.Command {
	var (
		org         string
		agent       string
		name        string
		urn         string
		appType     string
		description string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install an Agent into an organization as an App",
		Example: `  hadron app install --org acme.com --agent agent_123 --name "Support Bot"
  hadron app install --org acme.com --agent agent_123 --name "Support Bot" --type CHATBOT`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// --urn is an optional slug; validate it only when supplied.
			if urn != "" {
				if err := cmdutil.ValidateURNSlug("--urn", urn); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			optional := func(s string) *string {
				if s == "" {
					return nil
				}
				return &s
			}
			var typeArg *gen.AppType
			if appType != "" {
				t := gen.AppType(appType)
				typeArg = &t
			}

			resp, err := gen.CreateApp(cmd.Context(), client, org, agent, name, optional(urn), typeArg, optional(description))
			if err != nil {
				return api.MapError(err)
			}

			a := resp.CreateApp
			dto := appDTO{
				ID:          a.Id,
				URN:         a.Urn,
				Name:        a.Name,
				AppType:     string(a.AppType),
				AgentID:     a.AgentId,
				MemberCount: a.MemberCount,
				CreatedAt:   a.CreatedAt,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ installed", dto.Name, dto.URN)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization ID or URN (required)")
	cmd.Flags().StringVar(&agent, "agent", "", "Agent ID or URN to install (required)")
	cmd.Flags().StringVar(&name, "name", "", "App name (required)")
	cmd.Flags().StringVar(&urn, "urn", "", "App URN slug")
	cmd.Flags().StringVar(&appType, "type", "", "App type: AGENT|AUTOMATION|CHATBOT|CLOUD|IOT|WORKSTATION")
	cmd.Flags().StringVar(&description, "description", "", "App description")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("agent")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}
