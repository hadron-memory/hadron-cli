package team

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

// #911 slice B1: stop signals over server #2022/#2166 (cor:agt:090). The CLI
// stores and reads signals; it computes no coverage, expands no team, sends no
// notice and interrupts nothing. The server owns all of it.

// Explicit DTO: schema regeneration must not change this JSON contract. Ids and
// times only, nulls preserved, no nested principal.
type stopSignalDTO struct {
	ID             string  `json:"id"`
	Level          string  `json:"level"`
	Scope          string  `json:"scope"`
	TargetWorkerID *string `json:"targetWorkerId"`
	TargetHostID   *string `json:"targetHostId"`
	SetAt          string  `json:"setAt"`
	Until          *string `json:"until"`
	SetByUserID    *string `json:"setByUserId"`
	SetByHostKey   bool    `json:"setByHostKey"`
	Live           bool    `json:"live"`
	EndedAt        *string `json:"endedAt"`
	EndKind        *string `json:"endKind"`
	EndedByUserID  *string `json:"endedByUserId"`
	EndedByHostKey bool    `json:"endedByHostKey"`
}

type stopLiftDTO struct {
	Changed bool          `json:"changed"`
	Signal  stopSignalDTO `json:"signal"`
}

func stopSignalDTOFrom(s gen.StopSignalFields) stopSignalDTO {
	d := stopSignalDTO{
		ID: s.Id, Level: string(s.Level), Scope: string(s.Scope),
		TargetWorkerID: s.TargetWorkerId, TargetHostID: s.TargetHostId,
		SetAt: s.SetAt, Until: s.Until, SetByUserID: s.SetByUserId, SetByHostKey: s.SetByHostKey,
		Live: s.Live, EndedAt: s.EndedAt, EndedByUserID: s.EndedByUserId, EndedByHostKey: s.EndedByHostKey,
	}
	if s.EndKind != nil {
		k := string(*s.EndKind)
		d.EndKind = &k
	}
	return d
}

const stopHelp = `A stop signal stops covered workers at one level: pause-dispatch (no new
assignment), pause (also no ticks or stall time) or halt (also an interrupt to
save work and hand off). Its scope is one worker, a team (a head worker and the
whole reporting tree under it) or a host. Signals are independent: setting one
never replaces another, and lifting one never ends another.

Stop notices are recorded but not delivered: the notice lane is dark, so this
command tells no worker anything and interrupts nothing. Results show the
server's stored signal, not proof that any worker stopped or resumed.

Team rule: nobody sets or lifts a live stop signal before the stop-notice host
consumer (hadron-server#2169) is live. The server currently refuses every set
with STOP_SIGNAL_UNAVAILABLE; lifting and listing are unaffected.`

func newCmdTeamStop(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "stop <command>", Short: "List, set or lift the App's stop signals", Long: stopHelp}
	c.AddCommand(newCmdTeamStopList(f), newCmdTeamStopSet(f), newCmdTeamStopLift(f))
	return c
}

var stopLevels = map[string]gen.StopSignalLevel{
	"pause-dispatch": gen.StopSignalLevelPauseDispatch, "pause_dispatch": gen.StopSignalLevelPauseDispatch,
	"pause": gen.StopSignalLevelPause, "halt": gen.StopSignalLevelHalt,
}

var stopScopes = map[string]gen.StopSignalScope{
	"worker": gen.StopSignalScopeWorker, "team": gen.StopSignalScopeTeam, "host": gen.StopSignalScopeHost,
}

func newCmdTeamStopSet(f *cmdutil.Factory) *cobra.Command {
	var scope, level, worker, host, until string
	c := &cobra.Command{
		Use:   "set --scope <worker|team|host> --level <pause-dispatch|pause|halt> [--worker <ref> | --host <id>] [--until <RFC3339>]",
		Short: "Set a stop signal",
		Long: `Set one stop signal. --scope worker and --scope team need --worker (a team is
named by its head worker); --scope host takes --host (a host key may omit it,
and the server takes its own host). --until is an RFC 3339 time with an offset,
later than now, sent as UTC; without it the signal lasts until lifted.

Whoever manages the App may set any level and scope; a host key, a HALT on its
own host. This is the person's write: a bound worktree's worker session is
never attached. The server decides who may set what and refuses the rest.

` + stopHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			lv, ok := stopLevels[strings.ToLower(level)]
			if !ok {
				return exitcode.Newf(exitcode.Usage, "--level takes pause-dispatch, pause or halt, got %q", level)
			}
			sc, ok := stopScopes[strings.ToLower(scope)]
			if !ok {
				return exitcode.Newf(exitcode.Usage, "--scope takes worker, team or host, got %q", scope)
			}
			input := gen.SetTeamStopSignalInput{Level: lv, Scope: sc}
			if sc == gen.StopSignalScopeHost {
				if cmd.Flags().Changed("worker") {
					return exitcode.Newf(exitcode.Usage, "--scope host names a host with --host; --worker belongs to worker and team scopes")
				}
				if cmd.Flags().Changed("host") {
					if strings.TrimSpace(host) == "" {
						return exitcode.Newf(exitcode.Usage, "--host is empty (an unset variable?); it takes the host's id, and only a host key acting on its own host omits it")
					}
					input.Host = &host
				}
			} else {
				if cmd.Flags().Changed("host") {
					return exitcode.Newf(exitcode.Usage, "--host belongs to --scope host; --scope %s names a worker with --worker", strings.ToLower(scope))
				}
				if strings.TrimSpace(worker) == "" {
					return exitcode.Newf(exitcode.Usage, "--scope %s needs a non-empty --worker <id, URN or name> (a team is named by its head; an unset variable?)", strings.ToLower(scope))
				}
				input.Worker = &worker
			}
			if cmd.Flags().Changed("until") {
				t, err := time.Parse(time.RFC3339Nano, until)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "--until takes an RFC 3339 time with an offset, e.g. 2026-12-01T10:00:00Z, got %q", until)
				}
				u := t.UTC().Format(time.RFC3339Nano)
				input.Until = &u
			}
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			appScope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			input.AppRef = appScope.Ref
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamStopSignalSet(ctx, client, &input)
			if err != nil {
				return stopFailure(err, "set")
			}
			if resp == nil || resp.SetTeamStopSignal == nil {
				return exitcode.Newf(exitcode.Error, "server returned no stop signal; check with hadron team stop list")
			}
			d := stopSignalDTOFrom(resp.SetTeamStopSignal.StopSignalFields)
			label := lazyAppLabel(ctx, f, appScope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\nStop signal stored. Its notice is recorded but not delivered.\n", label()); err != nil {
					return err
				}
				return writeStopSignal(w, d)
			})
		},
	}
	c.Flags().StringVar(&scope, "scope", "", "worker, team or host (required)")
	c.Flags().StringVar(&level, "level", "", "pause-dispatch, pause or halt (required)")
	c.Flags().StringVar(&worker, "worker", "", "worker and team scopes: the worker's id, URN or name (a team by its head)")
	c.Flags().StringVar(&host, "host", "", "host scope: the host's id (a host key may omit it for its own host)")
	c.Flags().StringVar(&until, "until", "", "RFC 3339 time with an offset when the signal ends on its own; omitted, until lifted")
	return c
}

func newCmdTeamStopLift(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "lift <signal-id>",
		Short: "Lift one stop signal by its id",
		Long: `Lift exactly one stop signal. Other stop signals may still cover the same
workers: lifting one never ends another, and nothing restarts by itself. A
signal that had already ended is reported as unchanged, not as lifted.
The receipt displays the signal id without controls, capped at 256 runes;
--json preserves the original id.

Whoever manages the App may lift any signal; a host key, a HALT on its own host.
This is the person's write: a bound worktree's worker session is never attached.

` + stopHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "the signal id is empty (an unset variable?); lift takes the id hadron team stop list prints")
			}
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			appScope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamStopSignalLift(ctx, client, appScope.Ref, id)
			if err != nil {
				return stopFailure(err, "lift")
			}
			if resp == nil || resp.LiftTeamStopSignal == nil {
				return exitcode.Newf(exitcode.Error, "server returned no lift result; check with hadron team stop list --include-ended")
			}
			d := stopLiftDTO{Changed: resp.LiftTeamStopSignal.Changed, Signal: stopSignalDTOFrom(resp.LiftTeamStopSignal.Signal.StopSignalFields)}
			label := lazyAppLabel(ctx, f, appScope)
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				headline := "Lifted stop signal " + output.SafeCell(d.Signal.ID) + "."
				if !d.Changed {
					headline = "Stop signal " + output.SafeCell(d.Signal.ID) + " had already ended; nothing changed."
				}
				if _, err := fmt.Fprintf(w, "app: %s\n%s Other stop signals may still cover the same workers.\n", label(), headline); err != nil {
					return err
				}
				return writeStopSignal(w, d.Signal)
			})
		},
	}
}

// stopListPageSize is the server's maximum page.
const stopListPageSize = 200

func newCmdTeamStopList(f *cmdutil.Factory) *cobra.Command {
	var worker string
	var includeEnded bool
	c := &cobra.Command{
		Use:     "list [--worker <ref>] [--include-ended]",
		Aliases: []string{"ls"},
		Short:   "List the App's stop signals, oldest set first",
		Long: `List every stop signal the server shows this caller, page by page to the end.

--worker narrows to the signals covering that worker now: a team signal on a
head also covers each worker in the reporting tree under it. --include-ended
adds lifted and expired signals.

A person in the App sees every signal. A worker's session sees the live
signals covering it (--include-ended is not for that view), and a host key
sees its own host's signals. This command sends the person's credential; it
attaches no worker session.

` + stopHelp,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			appScope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var workerIn *string
			if cmd.Flags().Changed("worker") {
				if strings.TrimSpace(worker) == "" {
					return exitcode.Newf(exitcode.Usage, "--worker is empty (an unset variable?); it takes the worker's id, URN or name")
				}
				workerIn = &worker
			}
			var includeIn *bool
			if cmd.Flags().Changed("include-ended") {
				includeIn = &includeEnded
			}
			first := stopListPageSize
			signals := []stopSignalDTO{}
			var after *string
			// Every cursor already sent, not just the last one: a → b → a
			// cycles as surely as a → a, and would reprint the cycle's rows
			// (cli#1026 BLIND, Jane P2).
			seen := map[string]bool{}
			for {
				resp, err := gen.TeamStopSignals(ctx, client, appScope.Ref, workerIn, includeIn, &first, after)
				if err != nil {
					return stopFailure(err, "list")
				}
				if resp == nil {
					return exitcode.Newf(exitcode.Error, "server returned no stop-signal page")
				}
				page := resp.TeamStopSignals
				for _, s := range page.Signals {
					signals = append(signals, stopSignalDTOFrom(s.StopSignalFields))
				}
				if !page.PageInfo.HasNextPage {
					break
				}
				next := page.PageInfo.EndCursor
				if next == nil || *next == "" || seen[*next] {
					return exitcode.Newf(exitcode.Error, "the server's stop-signal pages did not advance; the list is incomplete, so none is shown")
				}
				seen[*next] = true
				after = next
			}
			label := lazyAppLabel(ctx, f, appScope)
			return output.Write(f.IOStreams, f.JSON, signals, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "app: %s\n", label()); err != nil {
					return err
				}
				if len(signals) == 0 {
					msg := "No live stop signals"
					if includeIn != nil && *includeIn {
						msg = "No stop signals, live or ended"
					}
					if workerIn != nil {
						msg += " covering " + *workerIn + " now"
					}
					_, err := fmt.Fprintln(w, msg+" visible to this caller.")
					return err
				}
				t := output.NewTable(w, "ID", "LEVEL", "SCOPE", "TARGET", "SET AT", "UNTIL", "STATE")
				for _, s := range signals {
					t.Row(output.SafeCell(s.ID), output.SafeCell(s.Level), output.SafeCell(s.Scope), output.SafeCell(stopTarget(s)), output.SafeCell(s.SetAt), output.SafeCell(stopUntil(s)), output.SafeCell(stopState(s)))
				}
				return t.Flush()
			})
		},
	}
	c.Flags().StringVar(&worker, "worker", "", "only signals covering that worker now (id, URN or name)")
	c.Flags().BoolVar(&includeEnded, "include-ended", false, "also list lifted and expired signals")
	return c
}

func writeStopSignal(w io.Writer, s stopSignalDTO) error {
	t := output.NewTable(w, "FIELD", "VALUE")
	t.Row("id", output.SafeCell(s.ID))
	t.Row("level", output.SafeCell(s.Level))
	t.Row("scope", output.SafeCell(s.Scope))
	t.Row("target", output.SafeCell(stopTarget(s)))
	t.Row("set at", output.SafeCell(s.SetAt))
	setBy := stopValue(s.SetByUserID)
	if s.SetByHostKey {
		setBy = "host key"
	}
	t.Row("set by", output.SafeCell(setBy))
	t.Row("until", output.SafeCell(stopUntil(s)))
	t.Row("state", output.SafeCell(stopState(s)))
	if s.EndedAt != nil {
		t.Row("ended at", output.SafeCell(*s.EndedAt))
		endedBy := stopValue(s.EndedByUserID)
		if s.EndedByHostKey {
			endedBy = "host key"
		}
		t.Row("ended by", output.SafeCell(endedBy))
	}
	return t.Flush()
}

func stopTarget(s stopSignalDTO) string {
	switch {
	case s.TargetWorkerID != nil && s.Scope == string(gen.StopSignalScopeTeam):
		return "team headed by " + *s.TargetWorkerID
	case s.TargetWorkerID != nil:
		return "worker " + *s.TargetWorkerID
	case s.TargetHostID != nil:
		return "host " + *s.TargetHostID
	}
	return "-"
}

func stopState(s stopSignalDTO) string {
	if s.Live {
		return "live"
	}
	if s.EndKind != nil {
		return "ended (" + strings.ToLower(*s.EndKind) + ")"
	}
	return "ended"
}

func stopUntil(s stopSignalDTO) string {
	if s.Until == nil {
		return "until lifted"
	}
	return *s.Until
}

func stopValue(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

// Fixed public sentences, one per known code: the server's own free text is
// never printed (the gate's safe-sentence rule), and an unknown code prints a
// generic line. The exit class is MapError's, so it matches every other path.
var stopRefusals = map[string]string{
	"STOP_SIGNAL_UNAVAILABLE":     "setting a stop signal is refused until the stop-notice lane is on (a worker would be stopped without being told); nothing was set",
	"APP_MANAGER_REQUIRED":        "only whoever manages the App may set or lift a stop signal (a host key: a HALT on its own host); nothing changed",
	"HOST_SCOPE_REQUIRED":         "a host key may set or lift only a HALT on its own host; nothing changed",
	"STOP_SIGNAL_SCOPE_FORBIDDEN": "this credential may not act on that scope or target; nothing changed",
	"APP_NOT_FOUND":               "the App was not found for this caller",
	"FORBIDDEN":                   "this caller is not permitted to do that in this App",
	"UNAUTHENTICATED":             "not authenticated; run hadron auth login or set HADRON_TOKEN",
	"GRAPHQL_VALIDATION_FAILED":   "this server does not accept the stop-signal operation; check the CLI and server versions",
}

func stopRefusalText(op, code string) (string, bool) {
	switch code {
	case "STOP_SIGNAL_NOT_FOUND":
		if op == "set" {
			return "the target worker or host was not found or is not visible to you; nothing was set", true
		}
		return "no stop signal with that id is visible in this App; nothing changed", true
	case "BAD_USER_INPUT":
		switch op {
		case "set":
			return "the server refused the input; check --scope, --level, --worker, --host and --until (later than now)", true
		case "lift":
			return "the server refused the input; check the signal id", true
		}
		return "the server refused the input; check --worker", true
	}
	text, ok := stopRefusals[code]
	return text, ok
}

var stopRefusalCodes = []string{"STOP_SIGNAL_UNAVAILABLE", "APP_MANAGER_REQUIRED", "HOST_SCOPE_REQUIRED", "STOP_SIGNAL_SCOPE_FORBIDDEN", "STOP_SIGNAL_NOT_FOUND", "BAD_USER_INPUT", "APP_NOT_FOUND", "FORBIDDEN", "UNAUTHENTICATED", "GRAPHQL_VALIDATION_FAILED"}

func stopFailure(err error, op string) error {
	mapped := api.MapError(err)
	exit := exitcode.FromError(mapped)
	// A scope refusal arrives as FORBIDDEN but maps to 3: the remedy is a
	// different credential, not someone's permission, so FORBIDDEN's fixed
	// sentence would name the wrong fix (cli#1026 SECURITY P3). Checked
	// before the code table; the remedy is the CLI's own text.
	if kind := api.OAuthScopeRefusal(err); kind != "" {
		return exitcode.Newf(exitcode.AuthRequired, "this credential's OAuth scope cannot use the stop-signal API. %s", api.ScopeRefusalRemedy(kind))
	}
	for _, code := range stopRefusalCodes {
		if api.HasErrorCode(err, code) {
			text, _ := stopRefusalText(op, code)
			return exitcode.Newf(exit, "%s: %s", code, text)
		}
	}
	switch exit {
	case exitcode.Unavailable:
		// The CLI's own transport sentence carries no server text. A write's
		// outcome is unknown, and it is never retried here.
		if op == "list" {
			return mapped
		}
		return exitcode.Newf(exit, "%s. Check with hadron team stop list --include-ended before retrying", mapped.Error())
	case exitcode.AuthRequired:
		return exitcode.Newf(exit, "%s", stopRefusals["UNAUTHENTICATED"])
	}
	if op == "list" {
		return exitcode.Newf(exit, "the stop-signal list failed with an error this CLI does not recognise")
	}
	// Not a known refusal, so nothing says the write did not happen.
	return exitcode.Newf(exit, "the stop-signal %s failed with an error this CLI does not recognise; its outcome is unknown. Check with hadron team stop list --include-ended before retrying", op)
}
