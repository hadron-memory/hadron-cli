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

// The writes: setAiModelPrice (platform admin, cli#215), grantCredits and
// adjustCredits (platform admin or the billing role, cli#3071). A money write is never retried by the CLI, and is confirmed
// (--yes when not on a terminal) before anything is sent.
//
// Money flags are opaque strings: the CLI neither parses nor range-checks them
// (no float, no int64, no positivity rule of its own — cor:api:240:01, the
// server owns the domain, cor:dmo:170:03). What the CLI does check is request
// shape: a required flag is present and non-empty.

// promptValue renders an operative value inside a confirmation prompt: control
// and format characters are neutralised (no forged line, no terminal tricks) but
// the text is NEVER truncated. SafeCell's table-cell cut would let the operator
// approve a shortened amount or org that differs from what is sent (cli#1560).
func promptValue(s string) string { return output.SanitizeLine(s) }

// requireFlag refuses a required string flag that is absent or empty.
func requireFlag(cmd *cobra.Command, name, val string) error {
	if !cmd.Flags().Changed(name) || val == "" {
		return exitcode.Newf(exitcode.Usage, "--%s is required and must not be empty", name)
	}
	return nil
}

// optionalFlag returns a pointer to the flag's value when it was given, nil
// when it was not (so the field is omitted and the server default applies). An
// explicitly empty value is refused rather than sent as "".
func optionalFlag(cmd *cobra.Command, name string, val *string) (*string, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	if *val == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--%s is empty; omit it to leave it unset", name)
	}
	return val, nil
}

func newCmdPricesSet(f *cmdutil.Factory) *cobra.Command {
	var provider, model, input, output_, effectiveAt string
	var tier, markupBP int
	var yes bool
	cmd := &cobra.Command{
		Use:   "set --provider <p> --model <m> --input <n> --output <n> [--tier <1-3>] [--markup-bp <n>] [--effective-at <time>]",
		Short: "Add a provider rate row (platform admin)",
		Long: `Add one rate row: what a model costs per million input and output tokens.
Platform admin only. A row is never edited: setting a rate appends a new
effective-dated row, and the newest row in effect is the one that applies.
"hadron billing prices" shows them.

--input and --output are micro-cents per million tokens, whole numbers sent to
the server exactly as typed (300000000 is 3 USD per million tokens;
100000000 micro-cents = 1 USD). The CLI does not check them: the server decides what
is valid and says so if it refuses.

Leave --tier, --markup-bp and --effective-at off to take the server's defaults:
tier 1, the server's default markup, effective now. --markup-bp is the debit
markup for this tier in hundredths of a percent (2500 = 25%); 0 is a real
markup of none, different from leaving it off.

--tier says which pricing tier the row belongs to (1 to 3). Every charge
resolves tier 1 today, so leave --tier off. Rows at tier 2 or 3 are stored but
unused: nothing reads them yet, so they are never applied to a charge. A tier
with no row is unpriced, never billed at another tier's rate.

It asks before sending (or pass --yes; without a terminal --yes is required).`,
		Example: `  # 15 USD in and 75 USD out per million tokens
  hadron billing prices set --provider anthropic --model claude-opus-5-5 \
    --input 1500000000 --output 7500000000 --yes
  hadron billing prices set --provider anthropic --model claude-opus-5-5 \
    --input 1500000000 --output 7500000000 --markup-bp 2500 --yes --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, r := range []struct{ name, val string }{{"provider", provider}, {"model", model}, {"input", input}, {"output", output_}} {
				if err := requireFlag(cmd, r.name, r.val); err != nil {
					return err
				}
			}
			eff, err := optionalFlag(cmd, "effective-at", &effectiveAt)
			if err != nil {
				return err
			}
			in := gen.SetAiModelPriceInput{
				Provider:                provider,
				Model:                   model,
				InputMicroCentsPerMTok:  input,
				OutputMicroCentsPerMTok: output_,
				EffectiveAt:             eff,
			}
			if cmd.Flags().Changed("tier") {
				in.Tier = &tier
			}
			if cmd.Flags().Changed("markup-bp") {
				in.MarkupBasisPoints = &markupBP
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Add a rate row for %s / %s (input %s, output %s micro-cents per million tokens)?",
				promptValue(provider), promptValue(model), promptValue(input), promptValue(output_))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.SetAiModelPrice(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			if resp.SetAiModelPrice == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no rate row")
			}
			dto := priceFrom(&resp.SetAiModelPrice.AiModelPriceFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Rate row %s: %s / %s tier %d, input %s, output %s micro-cents per million tokens, markup %s, effective %s\n",
					output.SafeCell(dto.ID), output.SafeCell(dto.Provider), output.SafeCell(dto.Model), dto.Tier,
					output.SafeCell(dto.InputMicroCentsPerMTok), output.SafeCell(dto.OutputMicroCentsPerMTok),
					output.SafeCell(dto.markup()), output.SafeCell(dto.EffectiveAt))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&provider, "provider", "", "the provider (required)")
	cmd.Flags().StringVar(&model, "model", "", "the model (required)")
	cmd.Flags().StringVar(&input, "input", "", "input micro-cents per million tokens, a whole number (required)")
	cmd.Flags().StringVar(&output_, "output", "", "output micro-cents per million tokens, a whole number (required)")
	cmd.Flags().IntVar(&tier, "tier", 0, "the pricing tier this row belongs to (1-3); leave it off for the server default (1): tiers 2 and 3 are stored but not read yet")
	cmd.Flags().IntVar(&markupBP, "markup-bp", 0, "debit markup in basis points (2500 = 25%); unset takes the server default")
	cmd.Flags().StringVar(&effectiveAt, "effective-at", "", "when the row takes effect (ISO-8601); unset means now")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newCmdGrant(f *cmdutil.Factory) *cobra.Command {
	var amount, sourceType, sourceID, expiresAt, note string
	var yes bool
	cmd := &cobra.Command{
		Use:   "grant <org> --amount <micro-cents> --source-type <t> --source-id <i> [--expires-at <time>] [--note <text>]",
		Short: "Grant credit to an organization (platform admin or billing role)",
		Long: `Add credit to an organization's ledger. For platform admins and the billing
role. It really credits the organization: use it deliberately.

<org> is an organization ID or its URN, hrn:org:<root> (for example
hrn:org:acme.com). --amount is micro-cents (100000000 micro-cents = 1 USD), a whole number sent to the server exactly as typed. The
CLI does not check it: the server requires a positive whole number and says so
if it refuses.

--source-type and --source-id are both required: every ledger entry names its
source. The pair is also an idempotency key: granting the same pair, amount and
organization again returns the entry that already exists instead of crediting
twice, and any other entry on that pair is refused (exit 5). The server refuses
the source types it writes itself. The CLI never retries by itself.

The server's answer does not say whether the entry is new or was already
there, so the command prints the returned ledger entry the same way either
time. On success it prints "Ledger entry <id>: <amount> micro-cents to
organization <org id>" (--json: the ledger entry). "hadron billing ledger <org>"
shows the ledger.

--expires-at must be later than now. From that instant what is left of the
grant stops counting, and the server removes it with a settling ledger entry.
Granting the same pair again does not change a stored expiry.

--note is kept for good with the entry and must not hold personal data: use
codes and ids.

It asks before sending (or pass --yes; without a terminal --yes is required).`,
		Example: `  hadron billing grant hrn:org:acme.com --amount 500000000 --source-type trial --source-id t-1 --expires-at 2027-06-01T00:00:00Z --yes
  hadron billing grant hrn:org:acme.com --amount 500000000 \
    --source-type stripe-checkout --source-id cs_123 --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "" {
				return exitcode.Newf(exitcode.Usage, "the organization is empty")
			}
			if err := requireFlag(cmd, "amount", amount); err != nil {
				return err
			}
			// The SDL keeps the pair nullable; the server requires it at run
			// time (hadron-server#5007), so the CLI does too.
			for _, r := range []struct{ name, val string }{{"source-type", sourceType}, {"source-id", sourceID}} {
				if err := requireFlag(cmd, r.name, r.val); err != nil {
					return err
				}
			}
			exp, err := optionalFlag(cmd, "expires-at", &expiresAt)
			if err != nil {
				return err
			}
			nt, err := optionalFlag(cmd, "note", &note)
			if err != nil {
				return err
			}
			in := gen.GrantCreditsInput{
				OrgRef:           &args[0],
				AmountMicroCents: amount,
				SourceType:       &sourceType,
				SourceId:         &sourceID,
				ExpiresAt:        exp,
				Note:             nt,
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Grant %s micro-cents of credit to %s?", promptValue(amount), promptValue(args[0]))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GrantCredits(cmd.Context(), client, &in)
			if err != nil {
				return api.MapError(err)
			}
			if resp.GrantCredits == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no ledger entry")
			}
			e := resp.GrantCredits.CreditLedgerEntryFields
			dto := ledgerEntryFrom(&e)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Ledger entry %s: %s micro-cents to organization %s\n",
					output.SafeCell(dto.ID), output.SafeCell(dto.AmountMicroCents), output.SafeCell(dto.OrganizationID))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&amount, "amount", "", "micro-cents to grant, a whole number (required)")
	cmd.Flags().StringVar(&sourceType, "source-type", "", "what kind of source the credit comes from (required)")
	cmd.Flags().StringVar(&sourceID, "source-id", "", "the source's id (required); repeating the same grant returns the existing entry rather than crediting twice")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "when what is left of the grant stops counting (ISO-8601, later than now); unset never expires")
	cmd.Flags().StringVar(&note, "note", "", "a note on the entry; kept for good, must not hold personal data")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newCmdAdjust(f *cmdutil.Factory) *cobra.Command {
	var amount, sourceType, sourceID, note string
	var yes bool
	cmd := &cobra.Command{
		Use:   "adjust <org> --amount <signed micro-cents> --source-type <t> --source-id <i> --note <text>",
		Short: "Correct an organization's credit, up or down (platform admin or billing role)",
		Long: `Write a signed correction, such as a refund, on an organization's ledger. For
platform admins and the billing role. It really changes the balance. An entry is
never edited: a correction is a new entry.

<org> is an organization ID or its URN, hrn:org:<root> (for example
hrn:org:acme.com). --amount is micro-cents (100000000 micro-cents = 1 USD), a
whole number sent to the server exactly as typed; a leading minus lowers the
balance. The CLI does not check it: the server refuses zero and anything that is
not a whole number.

--source-type and --source-id name what caused the correction. The pair is also
an idempotency key: the same pair, amount and organization again returns the
entry that already exists, and any other entry on that pair, a grant included,
is refused (exit 5). The server refuses the source types it writes itself. The
CLI never retries by itself.

--note says why. It is kept for good with the entry and must not hold personal
data: use codes and ids.

The server's answer does not say whether the entry is new or was already there.
On success it prints "Ledger entry <id>: <amount> micro-cents on organization
<org id>" (--json: the ledger entry, as in "hadron billing ledger").

It asks before sending (or pass --yes; without a terminal --yes is required).`,
		Example: `  hadron billing adjust hrn:org:acme.com --amount -300 \
    --source-type refund --source-id r-1 --note "refund r-1" --yes
  hadron billing adjust hrn:org:acme.com --amount 5000000 \
    --source-type correction --source-id c-7 --note "ticket c-7" --yes --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "" {
				return exitcode.Newf(exitcode.Usage, "the organization is empty")
			}
			for _, r := range []struct{ name, val string }{{"amount", amount}, {"source-type", sourceType}, {"source-id", sourceID}, {"note", note}} {
				if err := requireFlag(cmd, r.name, r.val); err != nil {
					return err
				}
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Adjust the credit of %s by %s micro-cents (source %s / %s, note %s; the note is kept for good)?",
				promptValue(args[0]), promptValue(amount), promptValue(sourceType), promptValue(sourceID), promptValue(note))); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AdjustCredits(cmd.Context(), client, &gen.AdjustCreditsInput{
				OrgRef:           &args[0],
				AmountMicroCents: amount,
				SourceType:       sourceType,
				SourceId:         sourceID,
				Note:             note,
			})
			if err != nil {
				return api.MapError(err)
			}
			if resp.AdjustCredits == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no ledger entry")
			}
			dto := ledgerEntryFrom(&resp.AdjustCredits.CreditLedgerEntryFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Ledger entry %s: %s micro-cents on organization %s\n",
					output.SafeCell(dto.ID), output.SafeCell(dto.AmountMicroCents), output.SafeCell(dto.OrganizationID))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&amount, "amount", "", "signed micro-cents, a whole number; a leading minus lowers the balance (required)")
	cmd.Flags().StringVar(&sourceType, "source-type", "", "what kind of source caused the correction (required)")
	cmd.Flags().StringVar(&sourceID, "source-id", "", "the source's id (required); repeating the same adjustment returns the existing entry")
	cmd.Flags().StringVar(&note, "note", "", "why (required); kept for good, must not hold personal data")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ledgerEntryFrom copies a ledger entry into its stable DTO; money stays the
// server's string.
func ledgerEntryFrom(e *gen.CreditLedgerEntryFields) ledgerEntryDTO {
	return ledgerEntryDTO{
		ID:               e.Id,
		OrganizationID:   stringOrEmpty(e.OrganizationId),
		Kind:             e.Kind,
		AmountMicroCents: e.AmountMicroCents,
		ExpiresAt:        e.ExpiresAt,
		SourceType:       e.SourceType,
		SourceID:         e.SourceId,
		Note:             e.Note,
		CreatedAt:        e.CreatedAt,
		CreatedBy:        e.CreatedBy,
	}
}
