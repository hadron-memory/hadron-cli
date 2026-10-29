package team

// `team health check` (cli#1181) — an independent, mechanical check that the
// team's key workers react to their tickers, meant to run off-host on a
// schedule with a read-only key. It shares no code path with wake delivery:
// it reads the roster and the tickers and judges client-side, so a held
// deliverer or a silent server alarm (#2746) cannot hide a dark team from it.
// The alarm is the exit code; nothing is written.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// healthDefaultRoles are the roster role slugs watched when --role is not
// given (the issue's coordinator, program manager and project manager).
var healthDefaultRoles = []string{"coordinator", "program-manager", "project-manager"}

const healthDefaultSlack = 5 * time.Minute

// The verdicts, one per key worker.
const (
	healthOK      = "OK"
	healthDark    = "DARK"
	healthSkipped = "SKIPPED"
)

// healthSkipReasons are the server's PENDING reasons that mean "not ticked
// now" (cor:agt:100:02). NO_TASK is not one: a coordinator with no task is
// still ticked and must still react.
var healthSkipReasons = map[string]string{
	"TEAM_PAUSED": "team paused",
	"STOPPED":     "stop signal",
	"PAUSED":      "paused",
	"ISOLATED":    "isolated",
	"STANDBY":     "standby",
}

type healthDTO struct {
	AppID        string            `json:"appId"`
	AsOf         string            `json:"asOf"`
	SlackSeconds int               `json:"slackSeconds"`
	Roles        []string          `json:"roles"`
	Healthy      bool              `json:"healthy"`
	Workers      []healthWorkerDTO `json:"workers"`
}

type healthWorkerDTO struct {
	Worker       taskPartyDTO `json:"worker"`
	Role         string       `json:"role"`
	Verdict      string       `json:"verdict"`
	Reason       string       `json:"reason"`
	LastActiveAt *string      `json:"lastActiveAt"`
	// The latest tick, outside quiet hours and older than the slack, that the
	// worker was expected to react to; null when none is due yet.
	DueTickAt *string `json:"dueTickAt"`
	// The ticker that tick belongs to (its slug); null with dueTickAt.
	DueTicker *string  `json:"dueTicker"`
	Tickers   []string `json:"tickers"`
}

// healthTicker is one counted ticker's schedule plus its live subscribers.
type healthTicker struct {
	Slug    string
	Anchor  time.Time
	Cadence time.Duration
	// Grace is added to the slack: 0 for an every-tick ticker, one cadence
	// for a live-check one, which skips a tick when the worker checked within
	// its cadence, so the worker must react within two of its ticks.
	Grace       time.Duration
	Quiet       *quietHours
	Subscribers map[string]time.Time // worker id -> subscribed at
}

// healthWorker is the roster row the rule reads.
type healthWorker struct {
	Party        taskPartyDTO
	Role         string
	LastActiveAt *time.Time
	PausedUntil  *time.Time
	PendingWhy   string // the server's PENDING reason, "" when not PENDING
}

func newCmdTeamHealth(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check that the team's key workers react to their tickers",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newCmdTeamHealthCheck(f))
	return cmd
}

func newCmdTeamHealthCheck(f *cmdutil.Factory) *cobra.Command {
	var roles []string
	var slack time.Duration
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Exit 9 when a key worker did not react to its latest tick",
		Long: `Check that the team's key workers react to their tickers. The check is
read-only and mechanical. It is meant to run on a schedule from somewhere that
shares nothing with wake delivery, using a read-only key; a non-zero exit is
the alarm.

The key workers are the App's roster workers whose role is one of --role
(default: coordinator, program-manager, project-manager). A ticker counts for a
worker when it is enabled, the worker has a live subscription to it, and it
is sure to wake the worker: an every-tick ticker counts as is, and a
live-check ticker counts with one extra cadence of grace (it skips a tick when
the worker checked within its cadence). A ticker that fires only on unread
(read-team-channel alone) does not count: its silence proves nothing.

For each counted ticker, the check finds its latest tick that fell more than
--slack (plus any grace) ago and outside the ticker's quiet hours. Ticks from before the
worker subscribed, or before its own pause ended, do not count. A worker is
DARK when its last activity (the roster's lastActiveAt) is earlier than the
latest such tick: it did not react to it. A worker in quiet hours overnight is
not DARK for the silence. A worker that has never been driven (no
lastActiveAt) and has a counted ticker is DARK.

A worker is SKIPPED, with the reason, when the team is paused, when its own
pause is in force, or when the server reports it PENDING because of a stop
signal, isolation or standby. It is also SKIPPED, shown as "no counted
ticker", when no ticker counts for it.
The server sees a stop signal only for a worker with a live session, so a
stopped worker with no session reads DARK. Retired workers are not on the
roster and are not listed.

lastActiveAt can under-report on a session the server reaped, and a turn that
makes no Hadron call does not move it. Both read as older activity, which
errs toward DARK, the safe direction. If a ticker's time zone cannot be
loaded on this machine, its quiet hours are ignored, which errs the same way.

Exit codes: 0 when every key worker is OK or SKIPPED; 9 when a key worker is
DARK, or when no key worker is found (nobody to watch is a dark team);
3 when credentials are missing or rejected; 7 when the server is unreachable;
8 when the roster read is refused. The check never exits 0 without having
read the roster.`,
		Example: `  # The default key workers, 5 minutes of slack
  hadron team health check --app hrn:app:acme.com:eng-team

  # Watch only coordinators, with 10 minutes of slack, as JSON
  hadron team health check --role coordinator --slack 10m --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if slack < 0 {
				return exitcode.Newf(exitcode.Usage, "--slack must not be negative")
			}
			want := map[string]bool{}
			for _, r := range roles {
				if r = strings.ToLower(strings.TrimSpace(r)); r != "" {
					want[r] = true
				}
			}
			if len(want) == 0 {
				return exitcode.Newf(exitcode.Usage, "--role must name at least one role")
			}
			ctx := cmd.Context()
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
			dto, err := runHealthCheck(ctx, client, scope.Ref, want, slack)
			if err != nil {
				return err
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderHealth(w, dto)
			}); err != nil {
				return err
			}
			if !dto.Healthy {
				return exitcode.Silent(exitcode.HealthDark)
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&roles, "role", healthDefaultRoles, "Roster role of a key worker (repeatable)")
	cmd.Flags().DurationVar(&slack, "slack", healthDefaultSlack, "How long after a tick a key worker may take to react")
	return cmd
}

func runHealthCheck(ctx context.Context, client graphql.Client, appRef string, roles map[string]bool, slack time.Duration) (healthDTO, error) {
	rr, err := gen.TeamHealthRoster(ctx, client, appRef)
	if err != nil {
		return healthDTO{}, api.MapError(err)
	}
	if rr == nil || rr.TeamRoster == nil {
		return healthDTO{}, exitcode.Newf(exitcode.Unavailable, "the server returned no roster; the team was not checked — try again")
	}
	asOf, err := time.Parse(time.RFC3339Nano, rr.TeamRoster.AsOf)
	if err != nil {
		return healthDTO{}, exitcode.Newf(exitcode.Error, "the roster's asOf %q is not a time: %v", rr.TeamRoster.AsOf, err)
	}
	ar, err := gen.TeamHealthApp(ctx, client, appRef)
	if err != nil {
		return healthDTO{}, api.MapError(err)
	}
	if ar == nil || ar.App == nil {
		return healthDTO{}, exitcode.Newf(exitcode.NotFound, "app %q not found", appRef)
	}
	var workers []healthWorker
	for _, e := range rr.TeamRoster.Workers {
		if e == nil || e.Role == nil || !roles[strings.ToLower(*e.Role)] {
			continue
		}
		w := healthWorker{Role: *e.Role, LastActiveAt: parseTimePtr(e.LastActiveAt), PausedUntil: parseTimePtr(e.PausedUntil)}
		if p := partyDTO(e.Worker); p != nil {
			w.Party = *p
		}
		if e.CheckStatus != nil && e.CheckStatus.Status == "PENDING" && e.CheckStatus.PendingReason != nil {
			w.PendingWhy = string(*e.CheckStatus.PendingReason)
		}
		workers = append(workers, w)
	}
	var tickers []healthTicker
	if len(workers) > 0 {
		if tickers, err = loadHealthTickers(ctx, client, appRef); err != nil {
			return healthDTO{}, err
		}
	}
	dto := evaluateHealth(asOf, slack, ar.App.TeamPausedAt != nil, workers, tickers)
	dto.AppID = rr.TeamRoster.AppId
	dto.AsOf = rr.TeamRoster.AsOf
	dto.Roles = sortedKeys(roles)
	return dto, nil
}

// loadHealthTickers lists the App's counted tickers (enabled, and every-tick
// or live-check) and pages every one's live subscribers to exhaustion:
// Ticker.subscribers is the only read that names a worker's subscriptions.
func loadHealthTickers(ctx context.Context, client graphql.Client, appRef string) ([]healthTicker, error) {
	lr, err := gen.ListTickers(ctx, client, appRef)
	if err != nil {
		return nil, api.MapError(err)
	}
	if lr == nil {
		return nil, exitcode.Newf(exitcode.Unavailable, "the server returned no ticker list; the team was not checked — try again")
	}
	var out []healthTicker
	for _, t := range lr.Tickers {
		if t == nil || !t.Enabled {
			continue
		}
		anchor, err := time.Parse(time.RFC3339Nano, t.AnchorAt)
		if err != nil || t.CadenceSeconds <= 0 {
			return nil, exitcode.Newf(exitcode.Error, "ticker %s has an unreadable schedule", t.Slug)
		}
		grace, counted := healthTickerGrace(t.Reasons, time.Duration(t.CadenceSeconds)*time.Second)
		if !counted {
			continue
		}
		ht := healthTicker{Slug: t.Slug, Anchor: anchor, Cadence: time.Duration(t.CadenceSeconds) * time.Second, Grace: grace, Quiet: parseQuietHours(t.QuietHours), Subscribers: map[string]time.Time{}}
		var after *string
		seen := map[string]bool{}
		for {
			r, err := gen.TickerSubscribers(ctx, client, t.Id, 200, after)
			if err != nil {
				return nil, api.MapError(err)
			}
			if r == nil || r.Ticker == nil || r.Ticker.Subscribers == nil {
				return nil, exitcode.Newf(exitcode.Unavailable, "the server returned no subscribers for ticker %s; the team was not checked — try again", t.Slug)
			}
			p := r.Ticker.Subscribers
			for _, s := range p.Items {
				if s == nil || s.Worker == nil || s.EndedAt != nil {
					continue
				}
				at, _ := time.Parse(time.RFC3339Nano, s.CreatedAt)
				ht.Subscribers[s.Worker.Id] = at
			}
			if !p.HasNextPage {
				break
			}
			if p.EndCursor == nil || *p.EndCursor == "" || seen[*p.EndCursor] {
				return nil, exitcode.Newf(exitcode.Unavailable, "the server returned a page already read for ticker %s; the list is not advancing", t.Slug)
			}
			seen[*p.EndCursor] = true
			after = p.EndCursor
		}
		out = append(out, ht)
	}
	return out, nil
}

// healthTickerGrace is whether a ticker with these reasons counts, and its
// grace (Marco's ruling on cli#1181, #44783): only a tick that is sure to
// wake the worker can show it did not react.
func healthTickerGrace(reasons []gen.TickerReason, cadence time.Duration) (time.Duration, bool) {
	live := false
	for _, r := range reasons {
		switch r {
		case gen.TickerReasonEveryTick:
			return 0, true
		case gen.TickerReasonLiveCheck:
			live = true
		}
	}
	return cadence, live
}

// evaluateHealth is the rule, pure: no reads, the server's asOf as now.
func evaluateHealth(now time.Time, slack time.Duration, teamPaused bool, workers []healthWorker, tickers []healthTicker) healthDTO {
	dto := healthDTO{SlackSeconds: int(slack / time.Second), Healthy: len(workers) > 0, Workers: []healthWorkerDTO{}}
	cutoff := now.Add(-slack)
	for _, w := range workers {
		row := healthWorkerDTO{Worker: w.Party, Role: w.Role, LastActiveAt: formatTimePtr(w.LastActiveAt), Tickers: []string{}}
		// A tick only counts once the worker could receive it: after it
		// subscribed and after its own pause ended.
		var due time.Time
		for _, t := range tickers {
			subAt, ok := t.Subscribers[w.Party.ID]
			if !ok {
				continue
			}
			row.Tickers = append(row.Tickers, t.Slug)
			tick, ok := t.latestTickBefore(cutoff.Add(-t.Grace))
			if !ok || tick.Before(subAt) || (w.PausedUntil != nil && tick.Before(*w.PausedUntil)) {
				continue
			}
			if tick.After(due) {
				due = tick
				slug := t.Slug
				row.DueTicker = &slug
			}
		}
		if !due.IsZero() {
			row.DueTickAt = formatTimePtr(&due)
		}
		switch {
		case teamPaused:
			row.Verdict, row.Reason = healthSkipped, "team paused"
		case healthSkipReasons[w.PendingWhy] != "":
			row.Verdict, row.Reason = healthSkipped, healthSkipReasons[w.PendingWhy]
		case w.PausedUntil != nil && w.PausedUntil.After(now):
			row.Verdict, row.Reason = healthSkipped, "paused"
		case len(row.Tickers) == 0:
			row.Verdict, row.Reason = healthSkipped, "no counted ticker"
		case w.LastActiveAt == nil:
			row.Verdict, row.Reason = healthDark, "never driven"
		case !due.IsZero() && w.LastActiveAt.Before(due):
			row.Verdict, row.Reason = healthDark, "no activity since the tick at "+due.UTC().Format(time.RFC3339)
		default:
			row.Verdict, row.Reason = healthOK, "active since its latest due tick"
			if due.IsZero() {
				row.Reason = "no tick due yet"
			}
		}
		if row.Verdict == healthDark {
			dto.Healthy = false
		}
		dto.Workers = append(dto.Workers, row)
	}
	return dto
}

// latestTickBefore is the latest tick strictly before cutoff that is outside
// quiet hours — the server's currentTick (ticks.ts) evaluated at cutoff, with
// a tick exactly at cutoff not yet due. Tick k falls at anchor + k·cadence for
// k >= 1; the anchor itself is not a tick.
func (t healthTicker) latestTickBefore(cutoff time.Time) (time.Time, bool) {
	const maxSkips = 7*86400/60 + 1
	k := int64(cutoff.Sub(t.Anchor) / t.Cadence)
	if !t.Anchor.Add(time.Duration(k) * t.Cadence).Before(cutoff) {
		k--
	}
	for skipped := 0; k >= 1 && skipped <= maxSkips; k, skipped = k-1, skipped+1 {
		at := t.Anchor.Add(time.Duration(k) * t.Cadence)
		if !t.Quiet.contains(at) {
			return at, true
		}
	}
	return time.Time{}, false
}

// quietHours mirrors the server's ticker quiet hours (quietHours.ts): weekly
// windows in a time zone, start included, end excluded, from > to running
// overnight. Days are ISO, 1 = Monday.
type quietHours struct {
	loc     *time.Location
	windows []quietWindow
}

type quietWindow struct {
	Days []int  `json:"days"`
	From string `json:"from"`
	To   string `json:"to"`
}

// parseQuietHours reads the ticker's quietHours JSON. Nil (no quiet hours)
// when absent, unreadable or in a zone this machine cannot load: more ticks
// then count, which errs toward DARK.
func parseQuietHours(raw *json.RawMessage) *quietHours {
	if raw == nil || len(*raw) == 0 || string(*raw) == "null" {
		return nil
	}
	var v struct {
		Timezone string        `json:"timezone"`
		Windows  []quietWindow `json:"windows"`
	}
	if json.Unmarshal(*raw, &v) != nil || len(v.Windows) == 0 {
		return nil
	}
	loc, err := time.LoadLocation(v.Timezone)
	if err != nil {
		return nil
	}
	return &quietHours{loc: loc, windows: v.Windows}
}

func (q *quietHours) contains(at time.Time) bool {
	if q == nil {
		return false
	}
	const week = 7 * 1440
	l := at.In(q.loc)
	day := int(l.Weekday()+6) % 7 // Monday = 0
	minute := day*1440 + l.Hour()*60 + l.Minute()
	for _, w := range q.windows {
		f, okF := hhmm(w.From)
		to, okT := hhmm(w.To)
		if !okF || !okT {
			continue
		}
		for _, d := range w.Days {
			base := (d - 1) * 1440
			if to > f {
				if minute >= base+f && minute < base+to {
					return true
				}
				continue
			}
			end := base + 1440
			if minute >= base+f && minute < min(end, week) {
				return true
			}
			if next := end % week; to > 0 && minute >= next && minute < next+to {
				return true
			}
		}
	}
	return false
}

func hhmm(s string) (int, bool) {
	var h, m int
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func parseTimePtr(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return nil
	}
	return &t
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339Nano)
	return &s
}

func renderHealth(w io.Writer, dto healthDTO) error {
	if len(dto.Workers) == 0 {
		_, err := fmt.Fprintf(w, "DARK: no key worker found with role %s\n", strings.Join(dto.Roles, ", "))
		return err
	}
	t := output.NewTable(w, "WORKER", "ROLE", "VERDICT", "LAST ACTIVE", "REASON")
	for _, r := range dto.Workers {
		t.Row(partyLabel(r.Worker), output.SafeCell(r.Role), r.Verdict, output.SafeCell(strOr(r.LastActiveAt)), output.SafeCell(r.Reason))
	}
	if err := t.Flush(); err != nil {
		return err
	}
	verdict := "healthy"
	if !dto.Healthy {
		verdict = "DARK"
	}
	_, err := fmt.Fprintf(w, "\n%s as of %s (slack %s)\n", verdict, output.SafeCell(dto.AsOf), time.Duration(dto.SlackSeconds)*time.Second)
	return err
}
