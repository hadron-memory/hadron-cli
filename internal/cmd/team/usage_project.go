package team

// `team usage project` (hadron-cli#1837): a VIEW of the server's token-burn
// projection (hadron-server#3820, query accountUsageProjection). The numbers,
// the range, the confidence and the what-if are the server's and are printed
// unmodified; nothing here extrapolates. `team usage show` is untouched.

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

type burnDTO struct {
	LowPctPerHour  float64 `json:"lowPctPerHour"`
	HighPctPerHour float64 `json:"highPctPerHour"`
}

// projWindowDTO is one window of one account, field for field the server's
// AccountUsageWindowProjection (absent values are null, never 0).
type projWindowDTO struct {
	Window         string   `json:"window"`
	LengthMinutes  int      `json:"lengthMinutes"`
	Status         string   `json:"status"`
	Reason         *string  `json:"reason"`
	Shape          *string  `json:"shape"`
	Confidence     *string  `json:"confidence"`
	UsedPct        *float64 `json:"usedPct"`
	LowPctAtReset  *float64 `json:"lowPctAtReset"`
	HighPctAtReset *float64 `json:"highPctAtReset"`
	CeilingPct     float64  `json:"ceilingPct"`
	ResetsAt       *string  `json:"resetsAt"`
	RunsOutAt      *string  `json:"runsOutAt"`
	ObservedAt     *string  `json:"observedAt"`
	Burn           *burnDTO `json:"burn"`
	PerWorkerBurn  *burnDTO `json:"perWorkerBurn"`
}

type projTargetDTO struct {
	Account  string          `json:"account"`
	Baseline []projWindowDTO `json:"baseline"`
	Adjusted []projWindowDTO `json:"adjusted"`
}

type projWhatIfDTO struct {
	Adjusted []projWindowDTO `json:"adjusted"`
	Target   *projTargetDTO  `json:"target"`
}

type projAccountDTO struct {
	Account           string          `json:"account"`
	Host              *string         `json:"host"`
	Family            *string         `json:"family"`
	AsOf              string          `json:"asOf"`
	NewestSnapshotAt  *string         `json:"newestSnapshotAt"`
	Workers           []string        `json:"workers"`
	WorkersConfigured bool            `json:"workersConfigured"`
	Windows           []projWindowDTO `json:"windows"`
	WhatIf            *projWhatIfDTO  `json:"whatIf"`
}

type projMoveDTO struct {
	Worker    string  `json:"worker"`
	ToAccount string  `json:"toAccount"`
	ToHost    *string `json:"toHost"`
	ToFamily  *string `json:"toFamily"`
}

// projRequestDTO echoes the what-if that was asked (exactly one of the two).
type projRequestDTO struct {
	AddWorkers *int         `json:"addWorkers"`
	MoveWorker *projMoveDTO `json:"moveWorker"`
}

// projectDTO: Complete is the server's accountUsageAccounts.complete, present
// only when the account list came from the server (no --account, cli#1878).
type projectDTO struct {
	App      string           `json:"app"`
	Accounts []projAccountDTO `json:"accounts"`
	Complete *bool            `json:"complete,omitempty"`
	WhatIf   *projRequestDTO  `json:"whatIf"`
}

// projectIncompleteWarning is the stderr line when the server's account list
// is cut: the listed accounts are projected, others may be missing.
const projectIncompleteWarning = "warning: the server's account list is incomplete; some accounts may be missing from this projection."

// projectEmptyAccount: --account given with an empty or blank value, most
// often an unset shell variable, is refused rather than read as "omitted".
const projectEmptyAccount = "--account needs a label (it was given empty — an unset shell variable?): name one account, or omit --account to project every account the server lists"

// projectMemoryRefusal: with --account omitted the server lists the accounts
// from the team App's team-space memory, so a memory choice cannot apply.
const projectMemoryRefusal = "--memory and --config choose a memory to read account labels from; the server now lists the accounts from the team App's team-space memory, so they do not apply: drop the flag, or name one account with --account"

func newUsageProject(f *cmdutil.Factory) *cobra.Command {
	var (
		account, host, family, memory, config string
		addWorkers                            int
		moveWorker, toAccount                 string
		toHost, toFamily                      string
	)
	c := &cobra.Command{
		Use:   "project",
		Short: "Show the server's projected use of each account at its reset",
		Long: `Read the server's token-burn projection (accountUsageProjection) for the team
App's subscription accounts and print it unmodified: per window, the projected
use at reset as a range (lowPctAtReset to highPctAtReset) with a confidence
(LOW, MEDIUM, HIGH), runsOutAt only when the server returns one, and the
account's per-worker burn. A window the server cannot project prints as
INSUFFICIENT_DATA (under 5% used, or fewer than 3 snapshots over an hour) or
UNKNOWN (no history, or the newest snapshot over 60 minutes old), with its
reason. The projection is advisory; it never authorizes or moves anything.

Without --account the server lists the team App's usage accounts (as "usage
accounts" prints them) and each is projected, one call each, in the server's
order; when the server says its list is incomplete, a warning goes to stderr
(and "complete": false in --json). -m/--memory and --config are refused then:
the server reads only the team App's team-space memory. --account names one
account by its label exactly as "usage show" prints it; add --host/--family
when that label has history from more than one host or family.

What-if (needs --account; exactly one kind): --add-workers N adds N workers
(1 to 50) to the account, each burning its per-worker estimate; --move-worker
<worker> --to-account <label> moves one worker (a name in the team App, or a
full hrn:worker: URN, which is why the move is two flags) to another account,
and the target account's windows are printed too (--to-host/--to-family when
the target label is ambiguous).

The team App is --app, the App context, or the worktree binding.`,
		Example: "  hadron team usage project\n  hadron team usage project --account team-rupali --add-workers 1 --json\n  hadron team usage project --account team-rupali --move-worker Ruth --to-account team-holger",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			flags := cmd.Flags()
			wantAdd := flags.Changed("add-workers")
			wantMove := flags.Changed("move-worker") || flags.Changed("to-account")
			switch {
			// First, so an empty --account is the reason given, before any
			// request (cli#1899): given-but-empty is not omitted.
			case flags.Changed("account") && strings.TrimSpace(account) == "":
				return exitcode.Newf(exitcode.Usage, "%s", projectEmptyAccount)
			case wantAdd && (addWorkers < 1 || addWorkers > 50):
				return exitcode.Newf(exitcode.Usage, "--add-workers must be 1 to 50")
			case wantAdd && wantMove:
				return exitcode.Newf(exitcode.Usage, "give one what-if, --add-workers or --move-worker with --to-account, not both")
			case wantMove && (strings.TrimSpace(moveWorker) == "" || strings.TrimSpace(toAccount) == ""):
				return exitcode.Newf(exitcode.Usage, "--move-worker and --to-account go together: give both or neither")
			case !wantMove && (flags.Changed("to-host") || flags.Changed("to-family")):
				return exitcode.Newf(exitcode.Usage, "--to-host and --to-family qualify --to-account; give --move-worker and --to-account")
			case account == "" && (host != "" || family != ""):
				return exitcode.Newf(exitcode.Usage, "--host and --family qualify one account: give --account")
			case account == "" && (flags.Changed("memory") || flags.Changed("config")):
				return exitcode.Newf(exitcode.Usage, "%s", projectMemoryRefusal)
			case (wantAdd || wantMove) && strings.TrimSpace(account) == "":
				return exitcode.Newf(exitcode.Usage, "a what-if applies to one account: give --account")
			}

			var in *gen.AccountUsageWhatIfInput
			var req *projRequestDTO
			switch {
			case wantAdd:
				n := addWorkers
				in = &gen.AccountUsageWhatIfInput{AddWorkers: &n}
				req = &projRequestDTO{AddWorkers: &n}
			case wantMove:
				mv := &gen.AccountUsageMoveWorkerInput{Worker: moveWorker, ToAccount: toAccount, ToHost: optStr(toHost), ToFamily: optStr(toFamily)}
				in = &gen.AccountUsageWhatIfInput{MoveWorker: mv}
				req = &projRequestDTO{MoveWorker: &projMoveDTO{Worker: moveWorker, ToAccount: toAccount, ToHost: mv.ToHost, ToFamily: mv.ToFamily}}
			}

			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			if scope.Ref == "" {
				return exitcode.Newf(exitcode.Usage, "no team App: pass --app, set an App context, or bind a worker session")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			type target struct{ label, host, family string }
			var targets []target
			var complete *bool
			if account != "" {
				targets = []target{{account, host, family}}
			} else {
				resp, lerr := gen.AccountUsageAccounts(ctx, client, scope.Ref)
				if lerr != nil {
					return mapAccountsError(lerr)
				}
				if resp.AccountUsageAccounts == nil {
					return exitcode.Newf(exitcode.Error, "server returned no account list")
				}
				for _, a := range resp.AccountUsageAccounts.Accounts {
					targets = append(targets, target{a.Account, a.Host, a.Family})
				}
				c := resp.AccountUsageAccounts.Complete
				complete = &c
			}

			dto := projectDTO{App: scope.Ref, Accounts: []projAccountDTO{}, Complete: complete, WhatIf: req}
			for _, t := range targets {
				resp, perr := gen.AccountUsageProjection(ctx, client, scope.Ref, t.label, optStr(t.host), optStr(t.family), in)
				if perr != nil {
					return mapProjectionError(perr)
				}
				dto.Accounts = append(dto.Accounts, projAccountFrom(*resp.AccountUsageProjection))
			}
			if !f.JSON && complete != nil && !*complete {
				_, _ = fmt.Fprintln(f.IOStreams.ErrOut, projectIncompleteWarning)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return writeProjection(w, dto) })
		},
	}
	c.Flags().StringVar(&account, "account", "", "one account, by its label exactly as 'usage show' prints it (default: every account the server lists, as 'usage accounts' prints them)")
	c.Flags().StringVar(&host, "host", "", "with --account: the host, when the label has history from more than one")
	c.Flags().StringVar(&family, "family", "", "with --account: the model family, when the label has history from more than one")
	c.Flags().IntVar(&addWorkers, "add-workers", 0, "what-if: add N workers (1 to 50) to --account")
	c.Flags().StringVar(&moveWorker, "move-worker", "", "what-if: move this worker (a name or hrn:worker: URN) off --account; needs --to-account")
	c.Flags().StringVar(&toAccount, "to-account", "", "what-if: the account label the worker moves to; needs --move-worker")
	c.Flags().StringVar(&toHost, "to-host", "", "with --to-account: the target's host, when its label is ambiguous")
	c.Flags().StringVar(&toFamily, "to-family", "", "with --to-account: the target's model family, when its label is ambiguous")
	c.Flags().StringVar(&config, "config", "", "host usage YAML: rejected when --account is omitted; ignored with --account")
	c.Flags().StringVarP(&memory, "memory", "m", "", "memory of the account records: rejected when --account is omitted; ignored with --account")
	return c
}

// mapProjectionError turns a server without the field into a plain "too old"
// message and routes everything else through api.MapError.
func mapProjectionError(err error) error {
	if strings.Contains(err.Error(), "accountUsageProjection") && (strings.Contains(err.Error(), "Cannot query field") || strings.Contains(err.Error(), "Unknown argument")) {
		return exitcode.Newf(exitcode.Error, "this hadron-server is too old to project account usage (no accountUsageProjection field; needs hadron-server #3820) — upgrade the server")
	}
	return api.MapError(err)
}

func burnFrom(lo, hi float64) *burnDTO { return &burnDTO{LowPctPerHour: lo, HighPctPerHour: hi} }

func enumPtr[T ~string](p *T) *string {
	if p == nil {
		return nil
	}
	s := string(*p)
	return &s
}

func projWindowFrom(w gen.ProjectionWindow) projWindowDTO {
	d := projWindowDTO{
		Window: string(w.Window), LengthMinutes: w.LengthMinutes, Status: string(w.Status),
		Reason: enumPtr(w.Reason), Shape: enumPtr(w.Shape), Confidence: enumPtr(w.Confidence),
		UsedPct: w.UsedPct, LowPctAtReset: w.LowPctAtReset, HighPctAtReset: w.HighPctAtReset,
		CeilingPct: w.CeilingPct, ResetsAt: w.ResetsAt, RunsOutAt: w.RunsOutAt, ObservedAt: w.ObservedAt,
	}
	if w.Burn != nil {
		d.Burn = burnFrom(w.Burn.LowPctPerHour, w.Burn.HighPctPerHour)
	}
	if w.PerWorkerBurn != nil {
		d.PerWorkerBurn = burnFrom(w.PerWorkerBurn.LowPctPerHour, w.PerWorkerBurn.HighPctPerHour)
	}
	return d
}

type (
	projResp = gen.AccountUsageProjectionAccountUsageProjection
)

// projAccountFrom copies the server's answer into the command's own DTO (never
// the generated structs), every list `[]` not null.
func projAccountFrom(p projResp) projAccountDTO {
	d := projAccountDTO{
		Account: p.Account, Host: p.Host, Family: p.Family, AsOf: p.AsOf, NewestSnapshotAt: p.NewestSnapshotAt,
		Workers: append([]string{}, p.Workers...), WorkersConfigured: p.WorkersConfigured,
		Windows: []projWindowDTO{},
	}
	for _, w := range p.Windows {
		if w != nil {
			d.Windows = append(d.Windows, projWindowFrom(w.ProjectionWindow))
		}
	}
	if p.WhatIf != nil {
		wi := &projWhatIfDTO{Adjusted: []projWindowDTO{}}
		for _, w := range p.WhatIf.Adjusted {
			if w != nil {
				wi.Adjusted = append(wi.Adjusted, projWindowFrom(w.ProjectionWindow))
			}
		}
		if t := p.WhatIf.Target; t != nil {
			td := &projTargetDTO{Account: t.Account, Baseline: []projWindowDTO{}, Adjusted: []projWindowDTO{}}
			for _, w := range t.Baseline {
				if w != nil {
					td.Baseline = append(td.Baseline, projWindowFrom(w.ProjectionWindow))
				}
			}
			for _, w := range t.Adjusted {
				if w != nil {
					td.Adjusted = append(td.Adjusted, projWindowFrom(w.ProjectionWindow))
				}
			}
			wi.Target = td
		}
		d.WhatIf = wi
	}
	return d
}

func pctRange(w projWindowDTO) string {
	if w.LowPctAtReset == nil || w.HighPctAtReset == nil {
		return "—"
	}
	return pctNum(*w.LowPctAtReset) + "–" + pctNum(*w.HighPctAtReset)
}

// pctNum prints the server's number as sent (no rounding), so the view never
// shows a figure the server did not.
func pctNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) + "%" }

func usedCell(p *float64) string {
	if p == nil {
		return "—"
	}
	return pctNum(*p)
}

func windowLabel(w projWindowDTO) string {
	switch w.Window {
	case "FIVE_HOUR":
		return "5h"
	case "SEVEN_DAY":
		return "7d"
	}
	return fmt.Sprintf("%dm", w.LengthMinutes)
}

func statusCell(w projWindowDTO) string {
	if w.Status != "PROJECTED" && w.Reason != nil {
		return w.Status + " (" + *w.Reason + ")"
	}
	return w.Status
}

func writeProjection(w io.Writer, dto projectDTO) error {
	t := output.NewTable(w, "ACCOUNT", "HOST", "FAMILY", "WINDOW", "STATUS", "USED", "AT RESET", "CONFIDENCE", "RUNS OUT", "VIEW")
	row := func(account string, host, family *string, view string, ws []projWindowDTO) {
		for _, x := range ws {
			conf := "—"
			if x.Confidence != nil {
				conf = *x.Confidence
			}
			t.Row(output.SafeCell(account), output.SafeCell(output.Dash(host)), output.SafeCell(output.Dash(family)), output.SafeCell(windowLabel(x)), output.SafeCell(statusCell(x)), usedCell(x.UsedPct), pctRange(x), output.SafeCell(conf), output.SafeCell(output.Dash(x.RunsOutAt)), view)
		}
	}
	for _, a := range dto.Accounts {
		row(a.Account, a.Host, a.Family, "projected", a.Windows)
		if a.WhatIf != nil {
			row(a.Account, a.Host, a.Family, "what-if", a.WhatIf.Adjusted)
			if a.WhatIf.Target != nil {
				row(a.WhatIf.Target.Account, nil, nil, "target now", a.WhatIf.Target.Baseline)
				row(a.WhatIf.Target.Account, nil, nil, "target what-if", a.WhatIf.Target.Adjusted)
			}
		}
	}
	return t.Flush()
}
