package ticket

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

func newCmdMint(f *cmdutil.Factory) *cobra.Command {
	var (
		org, app, action, note, expires string
		count                           int
	)
	cmd := &cobra.Command{
		Use:   "mint --org <ref> --action comm.outbound --count <n>",
		Short: "Mint action tickets into the org ledger (org ADMIN)",
		Long: `Mint consumable action tickets into the org ledger (org ADMIN; cor:acl:050:04).

v1 supports the comm.outbound action only. --app scopes the tickets to one App
(omit for org-wide). --note records why they exist (ledger legibility).
--expires sets an ISO-8601 expiry.`,
		Example: `  hadron ticket mint --org acme.com --action comm.outbound --count 100 \
    --note 'nightly digest send budget'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if count <= 0 {
				return exitcode.Newf(exitcode.Usage, "--count must be a positive integer")
			}
			if !isSupportedMintAction(action) {
				return exitcode.Newf(exitcode.Usage, "unsupported --action %q — v1 supports %s", action, strings.Join(supportedMintActions, ", "))
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			input := &gen.MintActionTicketsInput{
				OrgId:  org,
				Action: action,
				Count:  count,
			}
			if app != "" {
				input.AppId = &app
			}
			if note != "" {
				input.Note = &note
			}
			if expires != "" {
				input.ExpiresAt = &expires
			}

			resp, err := gen.MintActionTickets(cmd.Context(), client, input)
			if err != nil {
				return api.MapError(err)
			}
			dto := mintResultDTO{Minted: resp.MintActionTickets, Action: action, OrgID: org}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ minted %d %s ticket(s) for org %s\n", dto.Minted, dto.Action, dto.OrgID)
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization to mint into (ID or URN; required)")
	cmd.Flags().StringVar(&app, "app", "", "scope the tickets to one App (ID; omit for org-wide)")
	cmd.Flags().StringVar(&action, "action", "comm.outbound", "the action these tickets grant (v1: comm.outbound)")
	cmd.Flags().IntVar(&count, "count", 0, "how many tickets to mint (required)")
	cmd.Flags().StringVar(&note, "note", "", "why these tickets exist (ledger legibility)")
	cmd.Flags().StringVar(&expires, "expires", "", "ISO-8601 expiry timestamp")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("count")
	return cmd
}
