package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// These DTOs are the personal read's public contract, independent of codegen.
type personalGrantSignature struct {
	State             *string `json:"state"`
	IntegrityProblems int     `json:"integrityProblems"`
}
type personalGrant struct {
	ID              string                 `json:"id"`
	AppID           string                 `json:"appId"`
	Action          string                 `json:"action"`
	Values          *json.RawMessage       `json:"values"`
	HolderKind      string                 `json:"holderKind"`
	HolderID        string                 `json:"holderId"`
	StartsAt        *string                `json:"startsAt"`
	ExpiresAt       *string                `json:"expiresAt"`
	MaxUses         *int                   `json:"maxUses"`
	UseCount        int                    `json:"useCount"`
	Reason          string                 `json:"reason"`
	CreatedAt       string                 `json:"createdAt"`
	ActivatedAt     *string                `json:"activatedAt"`
	RevokedAt       *string                `json:"revokedAt"`
	Allows          bool                   `json:"allows"`
	SignatureStatus personalGrantSignature `json:"signatureStatus"`
}
type personalGrantPage struct {
	HasMore    bool            `json:"hasMore"`
	NextOffset *int            `json:"nextOffset"`
	Items      []personalGrant `json:"items"`
	Total      int             `json:"total"`
}

func newCmdGrants(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "grants",
		Short: "Read personal grants across Apps",
		Long: `Read one page of grants naming the signed-in person directly as holder,
across Apps and organizations, in every state, newest first. Grants you only
issued or requested for someone else are not included.

Uses the current credential, never this worktree's worker-session binding.
The read is not App-scoped. An App key, run or impersonated credential can
receive an empty page; empty output never proves you have no grants.
Sign in with hadron auth login; use hadron auth whoami to inspect the
current credential. Missing credentials exit 3. Servers without myGrants
report an API compatibility error (exit 2); no fallback claims an empty list.

Prints the server's timestamps, allows (a fact about now), and signature
status as returned. It does not invent a lifecycle label from those fields.
No signature records, signer identities or manager-only end details are read.
--json returns {items:[],total:N,hasMore,nextOffset}; --limit 0 returns {total:N} only.
The default page size is 20. The server caps pages at 200 rows, rather than
refusing a larger request. --offset selects the start. Use nextOffset as
the next --offset when hasMore is true. No page is fetched automatically.

This command only reads. Signing a grant requires the person's passkey on
the server's browser ceremony page; the CLI does not activate grants.
Manager revoke and withdrawal have separate authority and confirmation
flows; this personal listing offers neither action.`,
		Example: "  hadron auth grants\n  hadron auth grants --limit 20 --offset 20 --json\n  hadron auth grants --limit 0 --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 || int64(limit) > 2147483647 || offset < 0 || int64(offset) > 2147483647 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset take 0..2147483647; the server caps each page at 200 rows")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// No binding lookup or WithSession: myGrants admits the person audience,
			// whereas a person-driven worker session deliberately receives empty.
			resp, err := gen.MyGrants(cmd.Context(), client, limit, offset)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.MyGrants == nil {
				return exitcode.Newf(exitcode.Error, "server returned no personal grants page")
			}
			if limit == 0 {
				return output.Write(f.IOStreams, f.JSON, struct {
					Total int `json:"total"`
				}{resp.MyGrants.Total}, func(w io.Writer) error {
					_, err := fmt.Fprintf(w, "total returned: %d\n", resp.MyGrants.Total)
					return err
				})
			}
			dto := personalGrantPage{Items: []personalGrant{}, Total: resp.MyGrants.Total}
			for _, g := range resp.MyGrants.Items {
				if g == nil || g.SignatureStatus == nil {
					return exitcode.Newf(exitcode.Error, "server returned an incomplete personal grant")
				}
				sig := personalGrantSignature{IntegrityProblems: g.SignatureStatus.IntegrityProblems}
				if g.SignatureStatus.State != nil {
					state := string(*g.SignatureStatus.State)
					sig.State = &state
				}
				dto.Items = append(dto.Items, personalGrant{
					ID: g.Id, AppID: g.AppId, Action: g.Action, Values: g.Values,
					HolderKind: string(g.HolderKind), HolderID: g.HolderId,
					StartsAt: g.StartsAt, ExpiresAt: g.ExpiresAt, MaxUses: g.MaxUses, UseCount: g.UseCount,
					Reason: g.Reason, CreatedAt: g.CreatedAt, ActivatedAt: g.ActivatedAt, RevokedAt: g.RevokedAt,
					Allows: g.Allows, SignatureStatus: sig,
				})
			}
			next := int64(offset) + int64(len(dto.Items))
			dto.HasMore = next < int64(dto.Total)
			if dto.HasMore {
				if len(dto.Items) == 0 {
					return exitcode.Newf(exitcode.Error, "server returned an empty personal-grants page before its total")
				}
				if next > 2147483647 {
					return exitcode.Newf(exitcode.Error, "next page offset exceeds the API integer range")
				}
				n := int(next)
				dto.NextOffset = &n
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 {
					if _, err := fmt.Fprintln(w, "No rows returned on this page; empty responses also cover credentials outside the person audience."); err != nil {
						return err
					}
				} else {
					table := output.NewTable(w, "ID", "APP ID", "ACTION", "VALUES", "HOLDER KIND", "HOLDER ID", "STARTS", "EXPIRES", "MAX USES", "USE COUNT", "REASON", "CREATED", "ACTIVATED", "REVOKED", "ALLOWS", "SIGNATURE STATE", "INTEGRITY PROBLEMS")
					for _, g := range dto.Items {
						values := "null"
						if g.Values != nil {
							values = string(*g.Values)
						}
						max := "—"
						if g.MaxUses != nil {
							max = strconv.Itoa(*g.MaxUses)
						}
						cells := []string{g.ID, g.AppID, g.Action, values, g.HolderKind, g.HolderID, personalTime(g.StartsAt), personalTime(g.ExpiresAt), max, strconv.Itoa(g.UseCount), g.Reason, g.CreatedAt, personalTime(g.ActivatedAt), personalTime(g.RevokedAt), strconv.FormatBool(g.Allows), personalTime(g.SignatureStatus.State), strconv.Itoa(g.SignatureStatus.IntegrityProblems)}
						for i := range cells {
							cells[i] = output.SafeCell(cells[i])
						}
						table.Row(cells...)
					}
					if err := table.Flush(); err != nil {
						return err
					}
				}
				if dto.NextOffset != nil {
					if _, err := fmt.Fprintf(w, "next offset: %d\n", *dto.NextOffset); err != nil {
						return err
					}
				}
				_, err := fmt.Fprintf(w, "total returned: %d; offset: %d; requested limit: %d; has more: %t\n", dto.Total, offset, limit, dto.HasMore)
				return err
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "requested page size (server caps at 200); 0 returns only the total count")
	cmd.Flags().IntVar(&offset, "offset", 0, "page start, 0..2147483647")
	return cmd
}

func personalTime(s *string) string {
	if s == nil {
		return "—"
	}
	return *s
}
