package team

// `team isolation set|clear` — isolate a worker and clear it (cli#911 slice
// B2, MCP cut C2 over hadron-server#2937; cor:agt:080:29). Both return the
// worker's wake settings, rendered as `team wake-settings` renders them; the
// read is `team wake-settings get` (ruling R2 (b): no `team isolation get`).
// Ruling R1 (Holger, 2026-10-31): ship against the inert wire. Until S7b
// (server#2130) the server refuses every set WORKER_ISOLATION_UNAVAILABLE and
// projects a stored isolation as none; clearing is not gated.

import (
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const isolationHelp = `Who may (cor:agt:080:29): whoever manages the App or the worker's holder, as
a person acting as themselves. Anyone in that set may change or clear an
isolation another one set. No worker isolates, even one a manager or the
holder drives: a bound worker session is never attached to these writes, and
the server refuses one WORKER_ISOLATION_FORBIDDEN (exit 8), as it does another
participant; an App key or an impersonation is refused too.

While isolation is unavailable (until S7b), set is refused
WORKER_ISOLATION_UNAVAILABLE (exit 5) and clear works. The server reports no
isolation meanwhile, so a returned snapshot never shows the worker isolated,
and a successful clear does not mean one was in force. "you may manage
isolation" says whether you are in the set above, not whether isolation is
available now. Read a worker's isolation with ` + "`hadron team wake-settings get`" + `.`

// isolationRefusalCodes are the refusals these two calls document, kept as
// the message prefix; the exit code is the mapped one, unchanged.
var isolationRefusalCodes = []string{"WORKER_ISOLATION_UNAVAILABLE", "WORKER_ISOLATION_FORBIDDEN", "FORBIDDEN", "WORKER_NOT_FOUND", "BAD_USER_INPUT"}

func newCmdTeamIsolation(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "isolation",
		Short: "Isolate a worker or clear its isolation",
		Long: `Isolate a worker, or clear its isolation (cor:agt:080:29). Isolation holds
what is addressed to the worker until it ends or is cleared. Both commands
take the worker's id or URN and print the worker's wake settings as the
server returns them.

` + isolationHelp,
	}
	cmd.AddCommand(newCmdTeamIsolationSet(f), newCmdTeamIsolationClear(f))
	return cmd
}

func newCmdTeamIsolationSet(f *cmdutil.Factory) *cobra.Command {
	var worker, until, forDur string
	cmd := &cobra.Command{
		Use:   "set --worker <id-or-URN> [--until <RFC3339> | --for <duration>]",
		Short: "Isolate a worker, or change when its isolation ends",
		Long: `Isolate one worker until a time, and print the wake settings the server
returns. --until is an RFC 3339 time with an offset and whole seconds, later
than now; --for is a positive whole-second duration from now (90m, 2h). Both
are sent as UTC and they cannot be combined. With neither, the server applies
its default of 2 hours. The server's current limit is 7 days ahead; the CLI
does not check it. Setting again with a different end extends or shortens the
isolation.

` + isolationHelp,
		Example: `  hadron team isolation set --worker hrn:worker:acme.com:ops:iris
  hadron team isolation set --worker <worker-id> --for 90m
  hadron team isolation set --worker <worker-id> --until 2026-11-01T18:00:00+01:00 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := wakeWorkerRef(worker)
			if err != nil {
				return err
			}
			end, err := isolationEnd(cmd, until, forDur, time.Now())
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.IsolateWorker(cmd.Context(), client, ref, end)
			if err != nil {
				return prefixedRefusal(err, isolationRefusalCodes)
			}
			if resp.IsolateWorker == nil {
				return exitcode.Newf(exitcode.Error, "server returned no wake settings; read them with `team wake-settings get` before retrying")
			}
			dto := wakeSettingsDTOFrom(&resp.IsolateWorker.WorkerWakeSettingsFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderWakeSettings(w, dto) })
		},
	}
	cmd.Flags().StringVar(&worker, "worker", "", "Worker ID or fully-qualified worker URN (required)")
	cmd.Flags().StringVar(&until, "until", "", "end of the isolation: RFC 3339 with an offset, later than now (not with --for)")
	cmd.Flags().StringVar(&forDur, "for", "", "length of the isolation from now, e.g. 90m or 2h (not with --until)")
	_ = cmd.MarkFlagRequired("worker")
	return cmd
}

// isolationEnd turns --until or --for into the UTC end to send, read against
// one clock, or nil when neither is given (the server's default applies).
func isolationEnd(cmd *cobra.Command, until, forDur string, now time.Time) (*string, error) {
	hasUntil, hasFor := cmd.Flags().Changed("until"), cmd.Flags().Changed("for")
	var end time.Time
	switch {
	case hasUntil && hasFor:
		return nil, exitcode.Newf(exitcode.Usage, "--until and --for both name the end: give one")
	case hasUntil:
		t, err := time.Parse(time.RFC3339, until)
		if err != nil || strings.Contains(until, ".") {
			return nil, exitcode.Newf(exitcode.Usage, "--until takes an RFC 3339 time with an offset and whole seconds, e.g. 2026-11-01T18:00:00Z, got %q", until)
		}
		if !t.After(now) {
			return nil, exitcode.Newf(exitcode.Usage, "--until %s is not later than now", until)
		}
		end = t
	case hasFor:
		d, err := time.ParseDuration(strings.TrimSpace(forDur))
		if err != nil || d <= 0 || d%time.Second != 0 {
			return nil, exitcode.Newf(exitcode.Usage, "--for takes a positive whole-second duration, e.g. 90m or 2h, got %q", forDur)
		}
		end = now.Add(d)
	default:
		return nil, nil
	}
	s := end.UTC().Truncate(time.Second).Format(time.RFC3339)
	return &s, nil
}

func newCmdTeamIsolationClear(f *cmdutil.Factory) *cobra.Command {
	var worker string
	cmd := &cobra.Command{
		Use:   "clear --worker <id-or-URN>",
		Short: "Clear a worker's isolation",
		Long: `Clear one worker's isolation and print the wake settings the server
returns. Clearing a worker with no isolation succeeds and changes nothing.

` + isolationHelp,
		Example: `  hadron team isolation clear --worker hrn:worker:acme.com:ops:iris
  hadron team isolation clear --worker <worker-id> --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := wakeWorkerRef(worker)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ClearWorkerIsolation(cmd.Context(), client, ref)
			if err != nil {
				return prefixedRefusal(err, isolationRefusalCodes)
			}
			if resp.ClearWorkerIsolation == nil {
				return exitcode.Newf(exitcode.Error, "server returned no wake settings; read them with `team wake-settings get` before retrying")
			}
			dto := wakeSettingsDTOFrom(&resp.ClearWorkerIsolation.WorkerWakeSettingsFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error { return renderWakeSettings(w, dto) })
		},
	}
	cmd.Flags().StringVar(&worker, "worker", "", "Worker ID or fully-qualified worker URN (required)")
	_ = cmd.MarkFlagRequired("worker")
	return cmd
}
