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
		Use:   "mint --org <ref> --count <n> [--action comm.outbound]",
		Short: "Mint action tickets into the org ledger (org ADMIN)",
		Long: `Add tickets to an org's ledger so its agent runs can send. Needs ADMIN or
OWNER in the org (else exit 8).

--count is how many to mint, at least 1; the server caps a single mint at
1000. --action defaults to comm.outbound, the only action today (anything else
exits 2). --app <App id or URN> limits the tickets to that App's runs; without
it they are org-wide; an App that is not in the org exits 4. This --app is not taken from your config, worktree
binding or the global --app flag; only an explicit --app scopes tickets.
--expires is an ISO-8601 time; without it tickets never expire. --note says
why they exist, for whoever reads the ledger.

Prints "✓ minted <n> comm.outbound ticket(s) for org <org>"; --json gives
minted (the number actually minted), action and orgId. Check the result with
hadron ticket list.`,
		Example: `  hadron ticket mint --org acme.com --count 100 --note 'nightly digest send budget'
  hadron ticket mint --org acme.com --app hrn:app:acme.com:support --count 20 \
    --expires 2027-01-01T00:00:00Z
  hadron ticket list --org acme.com`,
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
				OrgRef: org,
				Action: action,
				Count:  count,
			}
			if app != "" {
				appRef, err := cmdutil.CanonicalAppRef("--app", app)
				if err != nil {
					return err
				}
				input.AppRef = &appRef
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
				_, err := fmt.Fprintf(w, "✓ minted %d %s ticket(s) for org %s\n", dto.Minted, output.SafeCell(dto.Action), output.SafeCell(dto.OrgID))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&org, "org", "", "organization to mint into (ID or URN; required)")
	cmd.Flags().StringVar(&app, "app", "", "scope the tickets to one App (ID or URN; omit for org-wide)")
	cmd.Flags().StringVar(&action, "action", "comm.outbound", "the action these tickets grant (v1: comm.outbound)")
	cmd.Flags().IntVar(&count, "count", 0, "how many tickets to mint, 1 to 1000 (required)")
	cmd.Flags().StringVar(&note, "note", "", "why these tickets exist (ledger legibility)")
	cmd.Flags().StringVar(&expires, "expires", "", "ISO-8601 expiry timestamp (omit: never expires)")
	_ = cmd.MarkFlagRequired("org")
	_ = cmd.MarkFlagRequired("count")
	return cmd
}
