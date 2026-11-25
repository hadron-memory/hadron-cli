package billing

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// spendDTO is the stable --json shape of `billing spend` (cli#3071 ruling 2).
// Field names are the server's; groupBy is the CLI spelling the caller gave;
// money and token counts stay the server's strings.
type spendDTO struct {
	From        string        `json:"from"`
	To          string        `json:"to"`
	GroupBy     string        `json:"groupBy"`
	Truncated   bool          `json:"truncated"`
	TotalGroups int           `json:"totalGroups"`
	Rows        []spendRowDTO `json:"rows"`
}

type spendRowDTO struct {
	Key               string `json:"key"`
	Events            int    `json:"events"`
	TokensIn          string `json:"tokensIn"`
	TokensOut         string `json:"tokensOut"`
	CostMicroCents    string `json:"costMicroCents"`
	UnpricedEvents    int    `json:"unpricedEvents"`
	ChargedMicroCents string `json:"chargedMicroCents"`
}

// cost renders the COST cell. The server's sum covers priced units only, so a
// row with no priced unit sums to "0"; showing that would present unpriced
// spend as a cost of zero (cor:dmo:170:04).
func (r spendRowDTO) cost() string {
	if r.Events > 0 && r.UnpricedEvents >= r.Events {
		return "unpriced"
	}
	return r.CostMicroCents
}

// spendGroup maps --group-by to the server's enum. Closed: there is no
// grouping by person (cor:dmo:170:04's ruling of 2026-11-24).
func spendGroup(v string) (gen.AiSpendGroup, error) {
	switch v {
	case "day":
		return gen.AiSpendGroupDay, nil
	case "app":
		return gen.AiSpendGroupApp, nil
	case "model":
		return gen.AiSpendGroupModel, nil
	case "key-owner":
		return gen.AiSpendGroupKeyOwner, nil
	}
	return "", exitcode.Newf(exitcode.Usage, "--group-by takes day, app, model or key-owner")
}

// spendTime reads --from/--to: exactly YYYY-MM-DD is midnight UTC (cli#3071
// ruling 1), anything else follows the shared time-flag rule.
func spendTime(name, v string) (string, error) {
	if _, err := time.Parse(time.DateOnly, v); err == nil {
		return v + "T00:00:00Z", nil
	}
	t, err := cmdutil.ParseTimeFlag(name, v)
	if err != nil {
		return "", exitcode.Newf(exitcode.Usage, "--%s takes a date (2026-11-01, midnight UTC) or an ISO date-time with Z or an explicit offset (the value given is not shown)", name)
	}
	return *t, nil
}

func newCmdSpend(f *cmdutil.Factory) *cobra.Command {
	var from, to, groupBy string
	cmd := &cobra.Command{
		Use:   "spend <org> --from <time> --to <time> --group-by <day|app|model|key-owner>",
		Short: "Show an organization's AI spend over a date range",
		Long: `Show what an organization's AI use cost and what it was charged, grouped one
way. Requires org ADMIN.

<org> is an organization ID or its URN, hrn:org:<root> (for example
hrn:org:acme.com). --from is inclusive and --to exclusive; each is a date
(2026-11-01, read as midnight UTC) or an ISO date-time with Z or an explicit
offset. The server limits how long a range may be and says so if it refuses.

--group-by:
  day        the UTC date
  app        the App's id
  model      provider/model
  key-owner  who held the AI key that served the call

Cost and charged are two separate figures, both in micro-cents (100000000
micro-cents = 1 USD). Cost is what the use cost; charged is what was debited
from the credit. Use on the organization's own key has a cost and no charge.
Cost covers only units that have a price: UNPRICED counts the rest, and a row
with no priced unit shows "unpriced" instead of a number. Never read it as 0.

When the server has more groups than it returns, a line on stderr says so.
A row with no value for the grouping has the key "(none)", an empty string
with --json.`,
		Example: `  hadron billing spend hrn:org:acme.com --from 2026-11-01 --to 2026-12-01 --group-by day
  hadron billing spend hrn:org:acme.com --from 2026-11-01 --to 2026-12-01 --group-by key-owner --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "" {
				return exitcode.Newf(exitcode.Usage, "the organization is empty")
			}
			for _, r := range []struct{ name, val string }{{"from", from}, {"to", to}, {"group-by", groupBy}} {
				if err := requireFlag(cmd, r.name, r.val); err != nil {
					return err
				}
			}
			group, err := spendGroup(groupBy)
			if err != nil {
				return err
			}
			fromAt, err := spendTime("from", from)
			if err != nil {
				return err
			}
			toAt, err := spendTime("to", to)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AiSpend(cmd.Context(), client, args[0], fromAt, toAt, group)
			if err != nil {
				return api.MapError(err)
			}
			if resp.AiSpend == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no spend for the organization (the org given is not shown)")
			}
			s := resp.AiSpend
			dto := spendDTO{From: s.From, To: s.To, GroupBy: groupBy, Truncated: s.Truncated, TotalGroups: s.TotalGroups, Rows: []spendRowDTO{}}
			for _, r := range s.Rows {
				if r == nil {
					continue
				}
				dto.Rows = append(dto.Rows, spendRowDTO{
					Key: r.Key, Events: r.Events, TokensIn: r.TokensIn, TokensOut: r.TokensOut,
					CostMicroCents: r.CostMicroCents, UnpricedEvents: r.UnpricedEvents, ChargedMicroCents: r.ChargedMicroCents,
				})
			}
			if dto.Truncated {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "warning: the list is partial: the server returned only the groups with the largest charge (%d of %d)\n", len(dto.Rows), dto.TotalGroups)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "AI spend from %s to %s, cost and charged in micro-cents\n", output.SafeCell(dto.From), output.SafeCell(dto.To)); err != nil {
					return err
				}
				t := output.NewTable(w, "KEY", "EVENTS", "TOKENS IN", "TOKENS OUT", "COST", "UNPRICED", "CHARGED")
				for _, r := range dto.Rows {
					key := r.Key
					if key == "" {
						key = "(none)"
					}
					t.Row(output.SafeCell(key), fmt.Sprint(r.Events), output.SafeCell(r.TokensIn), output.SafeCell(r.TokensOut),
						output.SafeCell(r.cost()), fmt.Sprint(r.UnpricedEvents), output.SafeCell(r.ChargedMicroCents))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "start of the range, inclusive: a date (midnight UTC) or an ISO date-time (required)")
	cmd.Flags().StringVar(&to, "to", "", "end of the range, exclusive: a date (midnight UTC) or an ISO date-time (required)")
	cmd.Flags().StringVar(&groupBy, "group-by", "", "day, app, model or key-owner (required)")
	return cmd
}
