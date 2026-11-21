package secret

import (
	"context"
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

var (
	// changePollInterval is how often --wait re-reads the request.
	changePollInterval = 2 * time.Second
	// defaultChangeWait bounds --wait. It is not the request's own one-hour life.
	defaultChangeWait = 10 * time.Minute
)

// changeSchemaNames are the fields a server without signing does not have.
var changeSchemaNames = []string{"secretChangeRequest", "withdrawSecretChangeRequest"}

func newCmdChange(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change <command>",
		Short: "Check or withdraw a staged change to a protected secret",
		Long: `A change to a protected secret is staged until a person signs it with a passkey
on a server page (see hadron secret protect). These commands read and withdraw
your own request: only the person who opened it can, and anyone else's id
answers "not found". They never show a value, and they never complete the
ceremony.`,
	}
	cmd.AddCommand(newCmdChangeGet(f))
	cmd.AddCommand(newCmdChangeWithdraw(f))
	return cmd
}

// changeDTO is the stable --json shape of a change request.
type changeDTO struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Operation string `json:"operation"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt"`
}

func newCmdChangeGet(f *cmdutil.Factory) *cobra.Command {
	var wait bool
	var waitTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "get <request-id> [--wait] [--wait-timeout <duration>]",
		Short: "Show a staged change request, optionally waiting for it to end",
		Long: `Show your own change request: its state (open, completed, declined, expired,
withdrawn or stale), the operation it asked for, and when it was created and
expires. The staged value is never shown.

Without --wait this is one read and exits 0 whatever the state. --wait polls
every 2 seconds until the request is no longer open, up to --wait-timeout (10
minutes; the request's own life is one hour). Then the exit code says how it
ended: 0 completed (a person signed it); 5 declined, expired, withdrawn or stale
(nothing was applied); 13 still open when the wait ran out (the request is left
as it is; nothing is withdrawn); 6 cancelled while waiting.

Exit 4 is a request that does not exist or is not yours (the server answers
both the same). Exit 2 is a server that does not offer protected-secret changes.`,
		Example: `  hadron secret change get 01a18cf643247d0fa261f320ec74d878
  hadron secret change get 01a18cf643247d0fa261f320ec74d878 --wait --wait-timeout 30m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if wait && waitTimeout <= 0 {
				return exitcode.Newf(exitcode.Usage, "--wait-timeout must be positive")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id := args[0]
			var dto *changeDTO
			var outcome error
			if wait {
				dto, outcome = pollChange(cmd.Context(), client, id, waitTimeout)
			} else {
				dto, outcome = readChange(cmd.Context(), client, id)
			}
			if dto == nil {
				return outcome
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeChange(w, dto)
			}); err != nil {
				return err
			}
			return outcome
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "poll until the request is no longer open")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", defaultChangeWait, "max time to wait with --wait")
	return cmd
}

func writeChange(w io.Writer, d *changeDTO) error {
	for _, r := range [][2]string{{"request", d.ID}, {"state", d.State}, {"operation", d.Operation}, {"createdAt", d.CreatedAt}, {"expiresAt", d.ExpiresAt}} {
		if _, err := fmt.Fprintf(w, "%s: %s\n", r[0], output.SafeCell(r[1])); err != nil {
			return err
		}
	}
	return nil
}

// readChange is one read. A nil dto carries the error; a null answer is "not
// found", the same one the server gives for a request that is not the caller's.
func readChange(ctx context.Context, client graphql.Client, id string) (*changeDTO, error) {
	resp, err := gen.SecretChangeRequest(ctx, client, id)
	if err != nil {
		return nil, mapChangeRead(err)
	}
	if resp == nil || resp.SecretChangeRequest == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "change request %s not found (or not yours)", output.SafeCell(id))
	}
	r := resp.SecretChangeRequest
	return &changeDTO{ID: r.Id, State: r.State, Operation: r.Operation, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}, nil
}

func mapChangeRead(err error) error {
	if api.IsFrameworkValidationOnlyFor(err, changeSchemaNames...) {
		return api.WithServerCode(exitcode.Newf(exitcode.Usage, "%s", noProtectedChanges), err)
	}
	return api.MapErrorKeepingCode(err)
}

// pollChange re-reads the request until its state is not "open" or the timeout
// elapses. It returns the last request read and the exit-coded outcome: nil for
// completed, 13 on timeout, 6 when the caller cancelled, 5 for any other end, the
// mapped error for a failed read. A timeout never withdraws the request.
func pollChange(parent context.Context, client graphql.Client, id string, timeout time.Duration) (*changeDTO, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var last *changeDTO
	quoted := cmdutil.ShellQuote(output.SafeCell(id))
	timedOut := func() error {
		if parent.Err() != nil {
			return exitcode.Newf(exitcode.Cancelled,
				"cancelled while waiting for a signature (%v); the change request %s is left as it is (nothing is withdrawn): read it with `hadron secret change get %s`",
				parent.Err(), output.SafeCell(id), quoted)
		}
		return exitcode.Newf(exitcode.WaitTimeout,
			"not signed within %s; the change request %s is left as it is (nothing is withdrawn): read it with `hadron secret change get %s`",
			timeout, output.SafeCell(id), quoted)
	}
	ticker := time.NewTicker(changePollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return last, timedOut()
		}
		d, err := readChange(ctx, client, id)
		if err != nil {
			if ctx.Err() != nil {
				return last, timedOut()
			}
			return last, err
		}
		last = d
		switch strings.ToLower(d.State) {
		case "open":
		case "completed":
			return last, nil
		case "declined", "expired", "withdrawn", "stale":
			return last, exitcode.Newf(exitcode.Conflict, "the change request %s ended %s without being applied; nothing changed", output.SafeCell(id), output.SafeCell(d.State))
		default:
			return last, exitcode.Newf(exitcode.Error, "the change request %s is in a state this CLI does not know (%s); it is not waited on", output.SafeCell(id), output.SafeCell(d.State))
		}
		select {
		case <-ctx.Done():
			return last, timedOut()
		case <-ticker.C:
		}
	}
}

func newCmdChangeWithdraw(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "withdraw <request-id>",
		Short: "Withdraw your own open change request",
		Long: `Withdraw your own change request while it is still open. The change is never
applied and its staged value is cleared. Only the person who opened it can;
anyone else's id answers "not found" (exit 4).

Prints "✓ withdrawn change request <id>"; --json gives {"id","status":"withdrawn"}.
A request that is no longer open (completed, declined, expired, withdrawn or
stale) exits 5; read its state with hadron secret change get. One request is
sent and never retried. Exit 2 is a server that does not offer protected-secret
changes.`,
		Example: `  hadron secret change withdraw 01a18cf643247d0fa261f320ec74d878`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id := args[0]
			resp, err := gen.WithdrawSecretChangeRequest(cmd.Context(), client, id)
			if err != nil {
				return mapChangeRead(err)
			}
			if resp == nil || !resp.WithdrawSecretChangeRequest {
				return exitcode.Newf(exitcode.Conflict, "change request %s is not open, so nothing was withdrawn; read it with `hadron secret change get %s`",
					output.SafeCell(id), cmdutil.ShellQuote(output.SafeCell(id)))
			}
			dto := map[string]string{"id": id, "status": "withdrawn"}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ withdrawn change request %s\n", output.SafeCell(id))
				return err
			})
		},
	}
	return cmd
}
