// Package billing is the spec-046 metering read surface (cli#215): provider
// rate rows and an org's credit balance and ledger.
//
// Every money field is a base-10 integer string of micro-cents (1e-8 USD) on
// the wire, and this package never parses one: a ledger amount is signed and
// unbounded, so a float64 loses digits past 2^53 and an int64 can overflow.
// The strings go into the DTOs and out to --json and the table byte for byte.
package billing

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

// NewCmdBilling builds the `billing` command group.
func NewCmdBilling(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "billing <command>",
		Short: "Read AI-credit rates, balances and ledgers",
		Long: `Read the AI-credit metering records (spec 046, cor:dmo:100).

Money is shown exactly as the server keeps it: a base-10 integer of
micro-cents (1e-8 USD), signed on the ledger, never rounded or converted.

These are reads only. Setting a rate and granting credits are platform-admin
writes and have no command here yet.`,
	}
	cmd.AddCommand(newCmdPrices(f))
	cmd.AddCommand(newCmdBalance(f))
	cmd.AddCommand(newCmdLedger(f))
	return cmd
}

type priceDTO struct {
	ID                      string  `json:"id"`
	Provider                string  `json:"provider"`
	Model                   string  `json:"model"`
	Tier                    int     `json:"tier"`
	InputMicroCentsPerMTok  string  `json:"inputMicroCentsPerMTok"`
	OutputMicroCentsPerMTok string  `json:"outputMicroCentsPerMTok"`
	MarkupBasisPoints       *int    `json:"markupBasisPoints"`
	EffectiveAt             string  `json:"effectiveAt"`
	CreatedAt               string  `json:"createdAt"`
	CreatedBy               *string `json:"createdBy"`
}

type balanceDTO struct {
	OrganizationID    string `json:"organizationId"`
	BalanceMicroCents string `json:"balanceMicroCents"`
}

type ledgerEntryDTO struct {
	ID               string  `json:"id"`
	OrganizationID   string  `json:"organizationId"`
	Kind             string  `json:"kind"`
	AmountMicroCents string  `json:"amountMicroCents"`
	ExpiresAt        *string `json:"expiresAt"`
	SourceType       *string `json:"sourceType"`
	SourceID         *string `json:"sourceId"`
	Note             *string `json:"note"`
	CreatedAt        string  `json:"createdAt"`
	CreatedBy        *string `json:"createdBy"`
}

// pageFlags is --limit/--offset: unset lists everything, an explicit flag is
// one deliberate page (cli#967).
type pageFlags struct{ limit, offset int }

func (p *pageFlags) register(cmd *cobra.Command) {
	cmd.Flags().IntVar(&p.limit, "limit", 0, "max results (one page of that size); unset lists everything")
	cmd.Flags().IntVar(&p.offset, "offset", 0, "results to skip")
}

// pointers validates the flags and returns them as request variables, nil
// when unset.
func (p *pageFlags) pointers(cmd *cobra.Command) (lim, off *int, err error) {
	if p.limit < 0 || p.offset < 0 {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--limit and --offset must be non-negative")
	}
	if cmd.Flags().Changed("limit") {
		lim = &p.limit
	}
	if cmd.Flags().Changed("offset") {
		off = &p.offset
	}
	return lim, off, nil
}

// collect runs one page when --limit/--offset was given, else drains the list.
func collect[T any](lim, off *int, fetch func(limit, offset *int) ([]T, int, error)) ([]T, error) {
	if lim != nil || off != nil {
		rows, _, err := fetch(lim, off)
		return rows, err
	}
	return api.CollectAll(func(l, o int) ([]T, int, error) { return fetch(&l, &o) })
}

func newCmdPrices(f *cmdutil.Factory) *cobra.Command {
	var provider, model string
	var tier int
	var page pageFlags
	cmd := &cobra.Command{
		Use:   "prices [--provider <p>] [--model <m>] [--tier <n>]",
		Short: "List provider rate rows",
		Long: `List provider rate rows, ordered by provider, model and tier, newest
effective first. Any signed-in caller can read them.

Rates are micro-cents per million tokens. A null markupBasisPoints means the
row sets no markup of its own and the server's default applies.`,
		Example: `  hadron billing prices --provider anthropic --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			lim, off, err := page.pointers(cmd)
			if err != nil {
				return err
			}
			// The server reads "" as no filter, so an empty value would widen
			// the list instead of narrowing it
			// (review:an-empty-flag-is-not-an-absent-flag).
			var providerPtr, modelPtr *string
			for _, fl := range []struct {
				name string
				val  *string
				dst  **string
			}{{"provider", &provider, &providerPtr}, {"model", &model, &modelPtr}} {
				if !cmd.Flags().Changed(fl.name) {
					continue
				}
				if *fl.val == "" {
					return exitcode.Newf(exitcode.Usage, "--%s is empty; omit it to list every %s", fl.name, fl.name)
				}
				*fl.dst = fl.val
			}
			var tierPtr *int
			if cmd.Flags().Changed("tier") {
				tierPtr = &tier
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			items, err := collect(lim, off, func(l, o *int) ([]*gen.AiModelPriceFields, int, error) {
				resp, err := gen.AiModelPrices(cmd.Context(), client, providerPtr, modelPtr, tierPtr, l, o)
				if err != nil || resp.AiModelPrices == nil {
					return nil, 0, err
				}
				out := make([]*gen.AiModelPriceFields, 0, len(resp.AiModelPrices.Items))
				for _, p := range resp.AiModelPrices.Items {
					if p != nil {
						out = append(out, &p.AiModelPriceFields)
					}
				}
				return out, resp.AiModelPrices.Total, nil
			})
			if err != nil {
				return api.MapError(err)
			}
			prices := make([]priceDTO, 0, len(items))
			for _, p := range items {
				prices = append(prices, priceDTO{
					ID:                      p.Id,
					Provider:                p.Provider,
					Model:                   p.Model,
					Tier:                    p.Tier,
					InputMicroCentsPerMTok:  p.InputMicroCentsPerMTok,
					OutputMicroCentsPerMTok: p.OutputMicroCentsPerMTok,
					MarkupBasisPoints:       p.MarkupBasisPoints,
					EffectiveAt:             p.EffectiveAt,
					CreatedAt:               p.CreatedAt,
					CreatedBy:               p.CreatedBy,
				})
			}
			return output.Write(f.IOStreams, f.JSON, prices, func(w io.Writer) error {
				t := output.NewTable(w, "PROVIDER", "MODEL", "TIER", "INPUT MICROCENTS/MTOK", "OUTPUT MICROCENTS/MTOK", "MARKUP BP", "EFFECTIVE")
				for _, p := range prices {
					markup := "default"
					if p.MarkupBasisPoints != nil {
						markup = fmt.Sprint(*p.MarkupBasisPoints)
					}
					t.Row(output.SafeCell(p.Provider), output.SafeCell(p.Model), fmt.Sprint(p.Tier), output.SafeCell(p.InputMicroCentsPerMTok), output.SafeCell(p.OutputMicroCentsPerMTok), output.SafeCell(markup), output.SafeCell(p.EffectiveAt))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "only this provider's rows")
	cmd.Flags().StringVar(&model, "model", "", "only this model's rows")
	cmd.Flags().IntVar(&tier, "tier", 0, "only this pricing tier's rows")
	page.register(cmd)
	return cmd
}

func newCmdBalance(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "balance <org>",
		Short: "Show an organization's credit balance",
		Long: `Show an organization's credit balance: the sum of its append-only ledger,
in micro-cents. Requires org ADMIN.

<org> is an organization ID or its hrn:org:<slug> URN.`,
		Example: `  hadron billing balance hrn:org:acme.com --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.CreditBalance(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreditBalance == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no balance for %s", args[0])
			}
			dto := balanceDTO{
				OrganizationID:    resp.CreditBalance.OrganizationId,
				BalanceMicroCents: resp.CreditBalance.BalanceMicroCents,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s micro-cents (organization %s)\n", output.SafeCell(dto.BalanceMicroCents), output.SafeCell(dto.OrganizationID))
				return err
			})
		},
	}
}

func newCmdLedger(f *cmdutil.Factory) *cobra.Command {
	var page pageFlags
	cmd := &cobra.Command{
		Use:   "ledger <org>",
		Short: "List an organization's credit ledger",
		Long: `List an organization's credit ledger, newest first. Grants are positive
and debits negative, in micro-cents. Requires org ADMIN.

<org> is an organization ID or its hrn:org:<slug> URN.`,
		Example: `  hadron billing ledger hrn:org:acme.com --limit 20`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			lim, off, err := page.pointers(cmd)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			items, err := collect(lim, off, func(l, o *int) ([]*gen.CreditLedgerEntryFields, int, error) {
				resp, err := gen.CreditLedgerEntries(cmd.Context(), client, args[0], l, o)
				if err != nil || resp.CreditLedgerEntries == nil {
					return nil, 0, err
				}
				out := make([]*gen.CreditLedgerEntryFields, 0, len(resp.CreditLedgerEntries.Items))
				for _, e := range resp.CreditLedgerEntries.Items {
					if e != nil {
						out = append(out, &e.CreditLedgerEntryFields)
					}
				}
				return out, resp.CreditLedgerEntries.Total, nil
			})
			if err != nil {
				return api.MapError(err)
			}
			entries := make([]ledgerEntryDTO, 0, len(items))
			for _, e := range items {
				entries = append(entries, ledgerEntryDTO{
					ID:               e.Id,
					OrganizationID:   e.OrganizationId,
					Kind:             e.Kind,
					AmountMicroCents: e.AmountMicroCents,
					ExpiresAt:        e.ExpiresAt,
					SourceType:       e.SourceType,
					SourceID:         e.SourceId,
					Note:             e.Note,
					CreatedAt:        e.CreatedAt,
					CreatedBy:        e.CreatedBy,
				})
			}
			return output.Write(f.IOStreams, f.JSON, entries, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "KIND", "AMOUNT MICROCENTS", "CREATED", "EXPIRES", "NOTE")
				for _, e := range entries {
					t.Row(output.SafeCell(e.ID), output.SafeCell(e.Kind), output.SafeCell(e.AmountMicroCents), output.SafeCell(e.CreatedAt), output.SafeCell(output.Dash(e.ExpiresAt)), output.SafeCell(output.Dash(e.Note)))
				}
				return t.Flush()
			})
		},
	}
	page.register(cmd)
	return cmd
}
