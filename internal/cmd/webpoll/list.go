package webpoll

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var org string
	cmd := &cobra.Command{
		Use:     "list --org <ref>",
		Aliases: []string{"ls"},
		Short:   "List an organization's page-monitor watches (org ADMIN)",
		Long: `List the page-monitor watches the server returns for an organization (org
ADMIN/OWNER).

The server returns this list unpaged, so the output is exactly the rows it sent:
the command states no total and does not claim to show every watch.`,
		Example: `  hadron web-poll list --org acme.com
  hadron web-poll list --org acme.com --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.WebPolls(cmd.Context(), client, org)
			if err != nil {
				return api.MapError(err)
			}
			polls := []webPollDTO{}
			if resp != nil {
				for _, p := range resp.WebPolls {
					if p != nil {
						polls = append(polls, dtoFromWebPoll(p))
					}
				}
			}
			return output.Write(f.IOStreams, f.JSON, polls, func(w io.Writer) error {
				table := output.NewTable(w, "JOB", "STATUS", "URL", "LAST CHECKED", "NEXT RUN", "TRIGGERS", "FAILURES", "EXPIRES")
				for _, p := range polls {
					table.Row(output.SafeCell(p.JobID), output.SafeCell(p.Status), output.SafeCell(p.URL),
						output.SafeCell(orDash(p.LastCheckedAt)), output.SafeCell(orDash(p.NextRunAt)),
						output.SafeCell(itoa(p.TriggerCount)), output.SafeCell(itoa(p.ConsecutiveFailures)), output.SafeCell(p.ExpiresAt))
				}
				return table.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization whose watches to list (ID or URN; required)")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}
