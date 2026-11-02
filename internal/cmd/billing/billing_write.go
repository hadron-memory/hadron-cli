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

// The write cut (cli#215): setAiModelPrice and grantCredits, both
// PLATFORM-ADMIN. A money write is never retried by the CLI, and is confirmed
// (--yes when not on a terminal) before anything is sent.
//
// Money flags are opaque strings: the CLI neither parses nor range-checks them
// (no float, no int64, no positivity rule of its own — cor:api:240:01, the
// server owns the domain, cor:dmo:100:04). What the CLI does check is request
// shape: a required flag is present and non-empty, and the idempotency pair is
// given whole.

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
the server exactly as typed (300000000 is 3 USD per million tokens; divide by
100000000 to get dollars). The CLI does not check them: the server decides what
is valid and says so if it refuses.

Leave --tier, --markup-bp and --effective-at off to take the server's defaults:
tier 1, the server's default markup, effective now. --markup-bp is the debit
markup for this tier in hundredths of a percent (2500 = 25%); 0 is a real
markup of none, different from leaving it off.

It asks before sending (or pass --yes; without a terminal --yes is required).`,
		Example: `  hadron billing prices set --provider anthropic --model claude-opus-5-5 \
    --input 1500000000 --output 7500000000 --yes
  hadron billing prices set --provider anthropic --model claude-opus-5-5 \
    --input 1500000000 --output 7500000000 --tier 2 --markup-bp 2500 --yes --json`,
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
				output.SafeCell(provider), output.SafeCell(model), output.SafeCell(input), output.SafeCell(output_))); err != nil {
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
	cmd.Flags().IntVar(&tier, "tier", 0, "pricing tier 1-3; unset takes the server default (1)")
	cmd.Flags().IntVar(&markupBP, "markup-bp", 0, "debit markup in basis points (2500 = 25%); unset takes the server default")
	cmd.Flags().StringVar(&effectiveAt, "effective-at", "", "when the row takes effect (ISO-8601); unset means now")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newCmdGrant(f *cmdutil.Factory) *cobra.Command {
	var amount, sourceType, sourceID, expiresAt, note string
	var yes bool
	cmd := &cobra.Command{
		Use:   "grant <org> --amount <micro-cents> [--source-type <t> --source-id <i>] [--expires-at <time>] [--note <text>]",
		Short: "Grant credit to an organization (platform admin)",
		Long: `Add credit to an organization's ledger. Platform admin only. It really credits
the organization: use it deliberately.

<org> is an organization ID or its hrn:org:<slug> URN. --amount is micro-cents
(1 USD = 100,000,000), a whole number sent to the server exactly as typed. The
CLI does not check it: the server requires a positive whole number and says so
if it refuses.

--source-type and --source-id go together or not at all (the command refuses
one without the other). When both are given, the server treats the pair as an
idempotency key: granting the same pair again returns the entry that already
exists instead of crediting twice. Without the pair there is no such
protection, and running the command again credits again, so a retry after a
timeout can double-credit. The CLI never retries by itself.

The server's answer does not say whether the entry is new or was already
there, so the command prints the returned ledger entry the same way either
time. "hadron billing ledger <org>" shows the ledger.

--expires-at is recorded on the entry; it is not enforced.

It asks before sending (or pass --yes; without a terminal --yes is required).`,
		Example: `  hadron billing grant hrn:org:acme.com --amount 500000000 --note "trial" --yes
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
			st, err := optionalFlag(cmd, "source-type", &sourceType)
			if err != nil {
				return err
			}
			si, err := optionalFlag(cmd, "source-id", &sourceID)
			if err != nil {
				return err
			}
			if (st == nil) != (si == nil) {
				return exitcode.Newf(exitcode.Usage, "--source-type and --source-id go together: give both or neither")
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
				OrgRef:           args[0],
				AmountMicroCents: amount,
				SourceType:       st,
				SourceId:         si,
				ExpiresAt:        exp,
				Note:             nt,
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Grant %s micro-cents of credit to %s?", output.SafeCell(amount), output.SafeCell(args[0]))); err != nil {
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
	cmd.Flags().StringVar(&sourceType, "source-type", "", "idempotency pair, with --source-id: what kind of source")
	cmd.Flags().StringVar(&sourceID, "source-id", "", "idempotency pair, with --source-type: the source's id; a repeated pair returns the existing entry rather than crediting twice")
	cmd.Flags().StringVar(&expiresAt, "expires-at", "", "recorded on the entry; not enforced (ISO-8601)")
	cmd.Flags().StringVar(&note, "note", "", "a note on the entry")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

// ledgerEntryFrom copies a ledger entry into its stable DTO; money stays the
// server's string.
func ledgerEntryFrom(e *gen.CreditLedgerEntryFields) ledgerEntryDTO {
	return ledgerEntryDTO{
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
	}
}
