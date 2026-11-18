package webpoll

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

// webPollPageDTO is the --json shape of `web-poll list`: one page of watches
// with the continuation facts the server returned (cli#2032). It replaces the
// bare array this command used to print; the items are the unchanged webPollDTO.
type webPollPageDTO struct {
	Items      []webPollDTO `json:"items"`
	HasMore    bool         `json:"hasMore"`
	NextCursor *string      `json:"nextCursor"`
}

func newCmdList(f *cmdutil.Factory) *cobra.Command {
	var org, after string
	var limit int
	cmd := &cobra.Command{
		Use:     "list --org <ref> [--limit <n>] [--after <cursor>]",
		Aliases: []string{"ls"},
		Short:   "List one page of an organization's page-monitor watches (org ADMIN)",
		Long: `List one page of the page-monitor watches the server returns for an
organization (org ADMIN/OWNER). One call shows one page: 20 watches by default,
at most 100 (a larger --limit is clamped by the server, not refused here).

When there are more, the output ends with "has more: true" and a "next cursor"
line; pass that value to --after to read the next page, exactly as printed
(--json carries the same value in nextCursor). Each page is a snapshot at the
time of the call; an empty page means no rows on that page.

--json prints {items, hasMore, nextCursor}: the watches under items, and
nextCursor null when there is no further page.`,
		Example: `  hadron web-poll list --org acme.com
  hadron web-poll list --org acme.com --limit 50 --json
  hadron web-poll list --org acme.com --after <next cursor from the previous page>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			first, err := cmdutil.PageLimit(cmd, limit)
			if err != nil {
				return err
			}
			afterPtr, err := cmdutil.PageAfter(cmd, after)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.WebPollsPage(cmd.Context(), client, org, first, afterPtr)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.WebPollsPage == nil {
				return exitcode.Newf(exitcode.Error, "server returned no web-poll page")
			}
			page := resp.WebPollsPage
			if err := cmdutil.CheckPage(page.HasMore, page.NextCursor, afterPtr); err != nil {
				return err
			}
			dto := webPollPageDTO{Items: []webPollDTO{}, HasMore: page.HasMore, NextCursor: page.NextCursor}
			for _, p := range page.Items {
				if p != nil {
					dto.Items = append(dto.Items, dtoFromWebPoll(&p.WebPollFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 {
					if _, err := fmt.Fprintln(w, "no watches on this page"); err != nil {
						return err
					}
				} else {
					table := output.NewTable(w, "JOB", "STATUS", "URL", "LAST CHECKED", "NEXT RUN", "TRIGGERS", "FAILURES", "EXPIRES")
					for _, p := range dto.Items {
						table.Row(output.SafeCell(p.JobID), output.SafeCell(p.Status), output.SafeCell(p.URL),
							output.SafeCell(orDash(p.LastCheckedAt)), output.SafeCell(orDash(p.NextRunAt)),
							output.SafeCell(itoa(p.TriggerCount)), output.SafeCell(itoa(p.ConsecutiveFailures)), output.SafeCell(p.ExpiresAt))
					}
					if err := table.Flush(); err != nil {
						return err
					}
				}
				if _, err := fmt.Fprintf(w, "has more: %t\n", dto.HasMore); err != nil {
					return err
				}
				if dto.NextCursor != nil {
					_, err := fmt.Fprintf(w, "next cursor: %s\n", output.SafeCell(*dto.NextCursor))
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization whose watches to list (ID or URN; required)")
	cmd.Flags().IntVar(&limit, "limit", 0, "watches per page (default 20; the server clamps to 100)")
	cmd.Flags().StringVar(&after, "after", "", "continue after this cursor, exactly as a previous page printed it")
	_ = cmd.MarkFlagRequired("org")
	return cmd
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}
