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
		Long: `See an organization's AI credit: what the models cost ("prices"), how much
credit is left ("balance") and every grant and charge ("ledger").

Money is shown in micro-cents, never converted to dollars:
1 USD = 100,000,000 micro-cents, so 100000000 is 1 USD, 5000000 is 5 cents and
1000000 is 1 cent. Divide by 100000000 to get dollars.

These commands only read. Setting a rate or granting credit is a platform-admin
action and has no command here.

Details:
  Amounts are base-10 integers exactly as the server keeps them, signed on the
  ledger, never rounded; the credit metering records are described in spec 046.`,
		Example: `  hadron billing prices --provider anthropic
  hadron billing prices get <id>
  hadron billing balance hrn:org:acme.com
  hadron billing ledger hrn:org:acme.com --limit 20`,
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
		Long: `List what each AI model costs: one row per provider, model and tier, newest
effective date first. Any signed-in caller can read them. Narrow with
--provider, --model or --tier (the tier is the number in each row's tier
column; it separates rate rows for the same model).

Rates are micro-cents per million tokens, for input and for output
separately: a rate of 300000000 micro-cents per million tokens is 3 USD per
million tokens (divide by 100000000 to get dollars). markupBasisPoints is a
markup added on top of the rate, in hundredths of a percent (100 = 1%); when it
is null this row sets no markup of its own and the server's default markup
applies (the default's value is not shown here).

The first column is each row's id; "hadron billing prices get <id>" reads
that one row.`,
		Example: `  hadron billing prices
  hadron billing prices --provider anthropic --json`,
		Args: cobra.NoArgs,
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
				prices = append(prices, priceFrom(p))
			}
			return output.Write(f.IOStreams, f.JSON, prices, func(w io.Writer) error {
				t := output.NewTable(w, "ID", "PROVIDER", "MODEL", "TIER", "INPUT MICROCENTS/MTOK", "OUTPUT MICROCENTS/MTOK", "MARKUP BP", "EFFECTIVE")
				for _, p := range prices {
					t.Row(output.SafeCell(p.ID), output.SafeCell(p.Provider), output.SafeCell(p.Model), fmt.Sprint(p.Tier), output.SafeCell(p.InputMicroCentsPerMTok), output.SafeCell(p.OutputMicroCentsPerMTok), output.SafeCell(p.markup()), output.SafeCell(p.EffectiveAt))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "only this provider's rows")
	cmd.Flags().StringVar(&model, "model", "", "only this model's rows")
	cmd.Flags().IntVar(&tier, "tier", 0, "only this pricing tier's rows")
	page.register(cmd)
	cmd.AddCommand(newCmdPricesGet(f))
	return cmd
}

// priceFrom copies a rate row into its stable DTO; money stays the server's
// string.
func priceFrom(p *gen.AiModelPriceFields) priceDTO {
	return priceDTO{
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
	}
}

// markup renders the row's own markup, or "default" when it sets none.
func (p priceDTO) markup() string {
	if p.MarkupBasisPoints == nil {
		return "default"
	}
	return fmt.Sprint(*p.MarkupBasisPoints)
}

func newCmdPricesGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <id>",
		Short: "Show one provider rate row",
		Long: `Show one rate row by its id, the ID column of "hadron billing prices" (the
"id" field with --json).
Any signed-in caller can read rates.

It shows the columns of the "hadron billing prices" table, plus who created
the row and when. Rates are micro-cents per million tokens (divide by
100000000 to get dollars); a markup of "default" means the row sets none of its
own and the server's default applies.

If the server returns no row, the command exits 4 with "not found or not
visible": the server answers the same way for an id that does not exist and
one you cannot see, so the CLI cannot tell them apart. Check the id against
"hadron billing prices".`,
		Example: `  hadron billing prices get <id>
  hadron billing prices get <id> --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "" {
				return exitcode.Newf(exitcode.Usage, "the rate row id is empty")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AiModelPrice(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.AiModelPrice == nil {
				return exitcode.Newf(exitcode.NotFound, "rate row %s not found or not visible", output.SafeCell(args[0]))
			}
			dto := priceFrom(&resp.AiModelPrice.AiModelPriceFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w, "FIELD", "VALUE")
				t.Row("id", output.SafeCell(dto.ID))
				t.Row("provider", output.SafeCell(dto.Provider))
				t.Row("model", output.SafeCell(dto.Model))
				t.Row("tier", fmt.Sprint(dto.Tier))
				t.Row("input microcents/mtok", output.SafeCell(dto.InputMicroCentsPerMTok))
				t.Row("output microcents/mtok", output.SafeCell(dto.OutputMicroCentsPerMTok))
				t.Row("markup bp", output.SafeCell(dto.markup()))
				t.Row("effective", output.SafeCell(dto.EffectiveAt))
				t.Row("created", output.SafeCell(dto.CreatedAt))
				t.Row("created by", output.SafeCell(output.Dash(dto.CreatedBy)))
				return t.Flush()
			})
		},
	}
}

func newCmdBalance(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "balance <org>",
		Short: "Show an organization's credit balance",
		Long: `Show how much AI credit an organization has left: grants minus charges, as one
number in micro-cents (1 USD = 100,000,000 micro-cents). Requires org ADMIN.

<org> is an organization ID or its hrn:org:<slug> URN. The output is one line,
for example "250000000 micro-cents (organization <id>)", which is 2.50 USD. To
see what made up the number, run "hadron billing ledger".`,
		Example: `  hadron billing balance hrn:org:acme.com
  hadron billing balance hrn:org:acme.com --json`,
		Args: cobra.ExactArgs(1),
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
		Long: `List every credit grant and charge for an organization, newest first, so you can
see what produced the balance. Grants are positive and charges negative, in
micro-cents (1 USD = 100,000,000 micro-cents; a charge of -1500000 is 1.5
cents). Requires org ADMIN. Without --limit the whole ledger is listed; --limit
and --offset ask for one page.

<org> is an organization ID or its hrn:org:<slug> URN.`,
		Example: `  hadron billing ledger hrn:org:acme.com --limit 20
  hadron billing ledger hrn:org:acme.com --limit 20 --offset 20`,
		Args: cobra.ExactArgs(1),
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
