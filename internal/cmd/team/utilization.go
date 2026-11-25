package team

// `team utilization` — advisory current utilization of the team's workers by
// model family, tier and role (cli#1836 slice 1 over hadron-server#3828). A
// read-only view: the server computes every number, the CLI never matches,
// launches or moves anything from them.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// utilizationDTO is the stable --json shape. Field names mirror the server's;
// tier and role are null for the server's "unknown tier" / roleless rows.
type utilizationDTO struct {
	AppID        string                `json:"appId"`
	WindowHours  int                   `json:"windowHours"`
	ComputedAt   string                `json:"computedAt"`
	Combinations []utilizationComboDTO `json:"combinations"`
}

// utilizationServerFloor is the server floor: the UTC day hadron-server's failed-match saturation
// (its lastMatchFailure field) merged to main, the house convention for a floor; merged is not
// necessarily served.
const utilizationServerFloor = "2026-11-21"

type utilizationComboDTO struct {
	// Family is null (an open family) when the failed task left it open: no family named, or
	// some excluded (cli#2896); never "".
	Family          *string `json:"family"`
	Tier            *string `json:"tier"`
	Role            *string `json:"role"`
	EligibleWorkers int     `json:"eligibleWorkers"`
	BusyWorkers     int     `json:"busyWorkers"`
	Utilization     float64 `json:"utilization"`
	Saturated       bool    `json:"saturated"`
	// LastMatchFailure is the latest no-worker match outcome inside the window, as the server
	// serves it, or null (a later match clears it).
	LastMatchFailure *utilizationMatchFailureDTO `json:"lastMatchFailure"`
}

// utilizationMatchFailureDTO is the server's MatchFailure: when the failed match was decided and
// the coarse reason (null on a row recorded without one), both exactly as served.
type utilizationMatchFailureDTO struct {
	At     string  `json:"at"`
	Reason *string `json:"reason"`
}

var utilizationTiers = map[string]bool{"high": true, "mid": true, "low": true}

func newCmdTeamUtilization(f *cmdutil.Factory) *cobra.Command {
	var family, tier, role, window string
	var all, failOnSaturated bool
	cmd := &cobra.Command{
		Use:   "utilization (--all | --family <f> [--tier <t>] [--role <r>]) [--window <hours>h] [--fail-on-saturated]",
		Short: "Current worker utilization by model family, tier and role",
		Long: `Show how busy the team's eligible workers are, per model family, tier and
role (advisory; the server computes it and no matching or authority depends
on it).

Each combination has eligible workers, busy workers (holding a task) and
their utilization, plus saturated: true when the combination's most recent
match inside the window answered no worker (a later match clears it), or when
it has workers and none eligible; utilization then reads 100%. An unknown
tier is its own row (tier null in --json, "—" in the table). A combination
whose family shows "—" (null in --json) has an open family: no family named,
or some excluded (the task that failed to match left it open). --family
cannot select an open-family row, so those rows are shown only with --all.

LAST MATCH FAILURE is the time and the coarse reason of the latest no-worker
match outcome inside the window, exactly as the server serves them; a null
reason prints the time alone, an empty or blank reason prints the time and
"(empty)", and "—" (null in --json) means there is none (a later match
clears it).

--all lists every combination in the server's order, which ranks by
utilization, then the most recent no-worker match, then fewest eligible
workers, then names: the first row is where to expand. The CLI does not
re-sort. Without --all, several matching combinations are refused (add
--tier/--role to narrow, or use --all); none matching prints an empty list and
exits 0.

--all takes no --family/--tier/--role (refused, exit 2).

--window is a whole number of hours from 1h to 168h as a duration (e.g. 24h,
168h). When omitted the CLI sends none and prints the window the server used.

--fail-on-saturated makes the command exit 5 when any combination shown is
saturated, applied after the output is written; otherwise a successful read
exits 0 whatever the numbers. It is a script exit code, not an alarm.

Needs server ` + utilizationServerFloor + ` or later: the query asks for the last match failure,
which an older server rejects as an unknown field.`,
		Example: `  hadron team utilization --all
  hadron team utilization --family claude --tier high --role qa --json
  hadron team utilization --all --window 168h --fail-on-saturated`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if !all && family == "" {
				return exitcode.Newf(exitcode.Usage, "name what to show: --all, or --family <f> (with --tier/--role) for one combination")
			}
			if all && (cmd.Flags().Changed("family") || cmd.Flags().Changed("tier") || cmd.Flags().Changed("role")) {
				return exitcode.Newf(exitcode.Usage, "--all lists every combination and takes no --family/--tier/--role; drop --all to filter")
			}
			var familyFilter, roleFilter, tierFilter *string
			var hours *int
			if family != "" {
				familyFilter = &family
			}
			if role != "" {
				roleFilter = &role
			}
			if tier != "" {
				t := strings.ToLower(tier)
				if !utilizationTiers[t] {
					return exitcode.Newf(exitcode.Usage, "--tier takes high, mid or low, got %q", tier)
				}
				tierFilter = &t
			}
			if window != "" {
				d, err := time.ParseDuration(window)
				if err != nil || d < time.Hour || d > 168*time.Hour || d%time.Hour != 0 {
					return exitcode.Newf(exitcode.Usage, "--window takes a whole number of hours from 1h to 168h, e.g. 24h or 168h, got %q", window)
				}
				h := int(d / time.Hour)
				hours = &h
			}
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamUtilization(ctx, client, scope.Ref, familyFilter, roleFilter, tierFilter, hours)
			if err != nil {
				return api.MapError(err)
			}
			u := resp.TeamUtilization
			if u == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no utilization result (teamUtilization was null)")
			}
			dto := utilizationDTO{AppID: u.AppId, WindowHours: u.WindowHours, ComputedAt: u.ComputedAt, Combinations: []utilizationComboDTO{}}
			for _, c := range u.Combinations {
				if c == nil {
					continue
				}
				var lastFailure *utilizationMatchFailureDTO
				if c.LastMatchFailure != nil {
					lastFailure = &utilizationMatchFailureDTO{At: c.LastMatchFailure.At, Reason: c.LastMatchFailure.Reason}
				}
				dto.Combinations = append(dto.Combinations, utilizationComboDTO{
					Family: c.Family, Tier: c.Tier, Role: c.Role,
					EligibleWorkers: c.EligibleWorkers, BusyWorkers: c.BusyWorkers,
					Utilization: c.Utilization, Saturated: c.Saturated, LastMatchFailure: lastFailure,
				})
			}
			// The rows are the server's, in its order: utilization, then the most recent failed
			// match, then fewest eligible workers, then family, tier and role, a total order. The
			// CLI never re-sorts (cli#2896).
			if !all && len(dto.Combinations) > 1 {
				return exitcode.Newf(exitcode.Usage, "%d combinations match; add --tier/--role to narrow to one, or use --all", len(dto.Combinations))
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderUtilization(w, dto)
			}); err != nil {
				return err
			}
			if failOnSaturated {
				for _, c := range dto.Combinations {
					if c.Saturated {
						return exitcode.Silent(exitcode.Conflict)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every combination, in the server's order")
	cmd.Flags().StringVar(&family, "family", "", "model family token, e.g. claude or codex")
	cmd.Flags().StringVar(&tier, "tier", "", "model tier: high, mid or low")
	cmd.Flags().StringVar(&role, "role", "", "worker role")
	cmd.Flags().StringVar(&window, "window", "", "look-back window as whole hours, 1h to 168h, e.g. 24h (default: the server's)")
	cmd.Flags().BoolVar(&failOnSaturated, "fail-on-saturated", false, "exit 5 when any combination shown is saturated")
	return cmd
}

func renderUtilization(w io.Writer, dto utilizationDTO) error {
	if _, err := fmt.Fprintf(w, "window: %dh   computed: %s\n", dto.WindowHours, output.SafeCell(dto.ComputedAt)); err != nil {
		return err
	}
	t := output.NewTable(w, "FAMILY", "TIER", "ROLE", "ELIGIBLE", "BUSY", "UTILIZATION", "SATURATED", "LAST MATCH FAILURE")
	for _, c := range dto.Combinations {
		t.Row(output.SafeCell(strOr(c.Family)), output.SafeCell(strOr(c.Tier)), output.SafeCell(strOr(c.Role)),
			fmt.Sprint(c.EligibleWorkers), fmt.Sprint(c.BusyWorkers),
			fmt.Sprintf("%.0f%%", c.Utilization*100), yesNo(c.Saturated), matchFailureCell(c.LastMatchFailure))
	}
	return t.Flush()
}

// emptyReasonCell is the word an empty reason prints after the time (cli#2986). It is not the null
// word: "—" already means "no failure at all", and a reason the server served as "" is neither that
// nor a null reason.
const emptyReasonCell = "(empty)"

// matchFailureCell is the failure's time and coarse reason exactly as served (a null reason prints
// the time alone, an empty or blank one the time and emptyReasonCell so it does not read as "nothing
// recorded"; the CLI never turns a reason into a promise), or the null word when there is none.
func matchFailureCell(m *utilizationMatchFailureDTO) string {
	if m == nil {
		return nullCell
	}
	if m.Reason == nil {
		return output.SafeCell(m.At)
	}
	if strings.TrimSpace(*m.Reason) == "" {
		return output.SafeCell(m.At) + " " + emptyReasonCell
	}
	return output.SafeCell(m.At) + " " + output.SafeCell(*m.Reason)
}
