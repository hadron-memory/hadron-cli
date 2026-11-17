package team

// `team utilization` — advisory current utilization of the team's workers by
// model family, tier and role (cli#1836 slice 1 over hadron-server#3828). A
// read-only view: the server computes every number, the CLI never matches,
// launches or moves anything from them.

import (
	"fmt"
	"io"
	"sort"
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

type utilizationComboDTO struct {
	Family          string  `json:"family"`
	Tier            *string `json:"tier"`
	Role            *string `json:"role"`
	EligibleWorkers int     `json:"eligibleWorkers"`
	BusyWorkers     int     `json:"busyWorkers"`
	Utilization     float64 `json:"utilization"`
	Saturated       bool    `json:"saturated"`
}

var utilizationTiers = map[string]bool{"high": true, "mid": true, "low": true}

func newCmdTeamUtilization(f *cmdutil.Factory) *cobra.Command {
	var family, tier, role, window string
	var all, failOnSaturated bool
	cmd := &cobra.Command{
		Use:   "utilization (--all | --family <f> [--tier <t>] [--role <r>]) [--window <hours>h] [--fail-on-saturated]",
		Short: "Current worker utilization by model family, tier and role",
		Long: `Show how busy the team's eligible workers are, per model family, tier and role
(advisory; the server computes it and no matching or authority depends on it).

Each combination has eligible workers, busy workers (holding a task) and their
utilization, plus saturated. In this slice saturated is true ONLY for zero
eligible workers (utilization then reads 100%); failure-only saturation arrives
with the server's match-outcome integration. An unknown tier is its own row
(tier null in --json, "-" in the table).

--all lists every combination, highest utilization first (a tie keeps the
server's order): the first row is where to expand. Without --all the filters
must narrow to ONE combination, else the command refuses and says to add
--tier/--role or --all.

--window is a whole number of hours as a duration (e.g. 24h, 168h). When omitted
the CLI sends none and prints the window the server used.

--fail-on-saturated makes the command exit 5 when any combination shown is
saturated, applied after the output is written; otherwise a successful read
exits 0 whatever the numbers. It is a script exit code, not an alarm.`,
		Example: `  hadron team utilization --all
  hadron team utilization --family claude --tier high --role qa --json
  hadron team utilization --all --window 168h --fail-on-saturated`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if !all && family == "" {
				return exitcode.Newf(exitcode.Usage, "name what to show: --all, or --family <f> (with --tier/--role) for one combination")
			}
			var fam, rol, tr *string
			var hours *int
			if family != "" {
				fam = &family
			}
			if role != "" {
				rol = &role
			}
			if tier != "" {
				t := strings.ToLower(tier)
				if !utilizationTiers[t] {
					return exitcode.Newf(exitcode.Usage, "--tier takes high, mid or low, got %q", tier)
				}
				tr = &t
			}
			if window != "" {
				d, err := time.ParseDuration(window)
				if err != nil || d < time.Hour || d%time.Hour != 0 {
					return exitcode.Newf(exitcode.Usage, "--window takes a whole number of hours as a duration, e.g. 24h or 168h, got %q", window)
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
			resp, err := gen.TeamUtilization(ctx, client, scope.Ref, fam, rol, tr, hours)
			if err != nil {
				return api.MapError(err)
			}
			u := resp.TeamUtilization
			dto := utilizationDTO{AppID: u.AppId, WindowHours: u.WindowHours, ComputedAt: u.ComputedAt, Combinations: []utilizationComboDTO{}}
			for _, c := range u.Combinations {
				if c == nil {
					continue
				}
				dto.Combinations = append(dto.Combinations, utilizationComboDTO{
					Family: c.Family, Tier: c.Tier, Role: c.Role,
					EligibleWorkers: c.EligibleWorkers, BusyWorkers: c.BusyWorkers,
					Utilization: c.Utilization, Saturated: c.Saturated,
				})
			}
			sort.SliceStable(dto.Combinations, func(i, j int) bool {
				return dto.Combinations[i].Utilization > dto.Combinations[j].Utilization
			})
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
	cmd.Flags().BoolVar(&all, "all", false, "list every combination, highest utilization first")
	cmd.Flags().StringVar(&family, "family", "", "model family token, e.g. claude or codex")
	cmd.Flags().StringVar(&tier, "tier", "", "model tier: high, mid or low")
	cmd.Flags().StringVar(&role, "role", "", "worker role")
	cmd.Flags().StringVar(&window, "window", "", "look-back window as whole hours, e.g. 24h (default: the server's)")
	cmd.Flags().BoolVar(&failOnSaturated, "fail-on-saturated", false, "exit 5 when any combination shown is saturated (zero eligible workers in this slice)")
	return cmd
}

func renderUtilization(w io.Writer, dto utilizationDTO) error {
	if _, err := fmt.Fprintf(w, "window: %dh   computed: %s\n", dto.WindowHours, dto.ComputedAt); err != nil {
		return err
	}
	t := output.NewTable(w, "FAMILY", "TIER", "ROLE", "ELIGIBLE", "BUSY", "UTILIZATION", "SATURATED")
	for _, c := range dto.Combinations {
		t.Row(output.SafeCell(c.Family), output.SafeCell(strOr(c.Tier)), output.SafeCell(strOr(c.Role)),
			fmt.Sprint(c.EligibleWorkers), fmt.Sprint(c.BusyWorkers),
			fmt.Sprintf("%.0f%%", c.Utilization*100), yesNo(c.Saturated))
	}
	return t.Flush()
}
