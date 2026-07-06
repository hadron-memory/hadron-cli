package run

import (
	"context"
	"io"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// pollInterval is how often `--wait` re-reads the run's status. A package var so
// tests can shorten it.
var pollInterval = 2 * time.Second

func newCmdTrigger(f *cmdutil.Factory) *cobra.Command {
	var (
		app, entry, aiConfig string
		args                 []string
		asSelf, wait         bool
		waitTimeout          time.Duration
	)
	cmd := &cobra.Command{
		Use:   "trigger --app <ref> --entry <node-urn> [--arg k=v ...]",
		Short: "Trigger a headless App run now (MANUAL)",
		Long: `Trigger a headless App run now — a MANUAL run of an entry node under the App's
identity (cor:agt:010:02). Prints the run id.

--as-self runs on behalf of YOU (required to reach your personal memories, and
only usable by an authenticated user — an App-key caller gets UNAUTHENTICATED;
cor:agt:010:01). --arg sets template args (repeatable; value parsed as JSON or
string). --ai-config selects a named AI config for the run (spec-036 walk
override). --wait polls the run to a terminal status before returning, then
exits non-zero if it ended in a non-COMPLETED status (FAILED/TIMED_OUT/CANCELLED),
so a script can branch on the run's outcome.`,
		Example: `  hadron run trigger --app acme.com:ops --entry acme.com::ops::tasks:nightly-digest
  hadron run trigger --app acme.com:ops --entry acme.com::ops::tasks:brief \
    --arg topic=security --as-self --wait --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, cmdArgs []string) error {
			appRef, err := cmdutil.ResolveAppRef(f, app)
			if err != nil {
				return err
			}
			entryURN, err := cmdutil.CanonicalNodeURN(entry)
			if err != nil {
				return err
			}
			eventData, err := cmdutil.KeyValsToJSON(args, "arg")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			input := &gen.TriggerAppRunInput{
				AppId:        appRef,
				EntryNodeUrn: entryURN,
				EventData:    eventData,
			}
			if asSelf {
				input.RunAsSelf = &asSelf
			}
			if aiConfig != "" {
				input.AiConfigName = &aiConfig
			}

			resp, err := gen.TriggerAppRun(cmd.Context(), client, input)
			if err != nil {
				return api.MapError(err)
			}
			if resp.TriggerAppRun == nil {
				return exitcode.Newf(exitcode.Error, "server returned no run")
			}
			dto := dtoFromFields(resp.TriggerAppRun.AppRunFields)

			if wait && !isTerminal(dto.Status) {
				dto, err = pollToTerminal(cmd, client, dto.ID, waitTimeout)
				if err != nil {
					return err
				}
			}

			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeRunDetail(w, dto)
			}); err != nil {
				return err
			}
			// With --wait we know the outcome, so reflect it in the exit code: a
			// run that ended in any non-COMPLETED terminal status is a failure a
			// script can branch on (the run — including its failure payload — is
			// already printed, so the error is silent).
			if wait && isTerminal(dto.Status) && dto.Status != "COMPLETED" {
				return exitcode.Silent(exitcode.Error)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "App to run (ID or URN; defaults to the App context)")
	cmd.Flags().StringVar(&entry, "entry", "", "fully-qualified entry node URN to run (required)")
	cmd.Flags().BoolVar(&asSelf, "as-self", false, "run on behalf of you (reaches your personal memories; authenticated user only)")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "template arg key=value (repeatable; value parsed as JSON or string)")
	cmd.Flags().StringVar(&aiConfig, "ai-config", "", "named AI config for the run (spec-036 walk override)")
	cmd.Flags().BoolVar(&wait, "wait", false, "poll the run to a terminal status before returning")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 5*time.Minute, "max time to wait with --wait")
	_ = cmd.MarkFlagRequired("entry")
	return cmd
}

// pollToTerminal re-reads a run until it reaches a terminal status or the
// timeout elapses. The timeout bounds the whole wait — including a single hung
// poll request — via a deadline context, so `--wait-timeout` is honored even if
// the server stalls. A timeout returns the last-seen run plus a Cancelled-coded
// error so callers/scripts can tell "still running" from "finished".
func pollToTerminal(cmd *cobra.Command, client graphql.Client, runID string, timeout time.Duration) (appRunDTO, error) {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	var last appRunDTO
	timedOut := func() error {
		return exitcode.Newf(exitcode.Cancelled, "run %s did not finish within %s (last status %s)", runID, timeout, statusOr(last))
	}
	for {
		select {
		case <-ctx.Done():
			// A zero/elapsed --wait-timeout lands here before the first poll.
			return last, timedOut()
		case <-time.After(pollInterval):
		}
		resp, err := gen.AppRun(ctx, client, runID)
		if err != nil {
			if ctx.Err() != nil { // the deadline cancelled an in-flight poll
				return last, timedOut()
			}
			return last, api.MapError(err)
		}
		if resp.AppRun == nil {
			return last, exitcode.Newf(exitcode.NotFound, "run %q not found", runID)
		}
		last = dtoFromFields(resp.AppRun.AppRunFields)
		if isTerminal(last.Status) {
			return last, nil
		}
	}
}
