package connection

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// flowDTO is the stable --json shape of `connection flow`: every field is
// always present, null when it does not apply.
type flowDTO struct {
	FlowID       string  `json:"flowId"`
	Status       string  `json:"status"`
	ConnectionID *string `json:"connectionId"`
	FailureCode  *string `json:"failureCode"`
}

func newCmdFlow(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "flow <flow-id>",
		Short: "Show where a connect flow is: pending, connected or failed",
		Long: `Read the state of a connection flow started by "hadron connection connect",
once. <flow-id> is the "flowId" in that command's result (and its start notice).

The answer is one of: PENDING (consent has not finished), CONNECTED (with the
connection id; on a reconnect, the id of the connection that already existed)
or FAILED (with the flow's stable failure code, for example access_denied).
Every one of them is an answer and exits 0, so check the status field, not the
exit code. Reading writes nothing and is safe to repeat.

Only the user who started the flow can read it, for one hour from the start. An
unknown flow id, an expired one and another user's all get the same refusal
(exit 8), so the command cannot tell you which it was.`,
		Example: `  hadron connection flow 7f3c1a90-0000-4000-8000-000000000000
  hadron connection flow <flow-id> --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(args[0])
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "give a nonempty <flow-id>")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			fl, err := readFlow(cmd.Context(), client, id)
			if err != nil {
				var ce *exitcode.CodedError
				if errors.As(err, &ce) {
					return err
				}
				return api.WithServerCode(api.MapError(err), err)
			}
			dto := flowDTO{FlowID: id, Status: string(fl.Status), ConnectionID: fl.ConnectionId, FailureCode: fl.FailureCode}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				line := fmt.Sprintf("flow %s: %s", output.SafeCell(id), output.SafeCell(dto.Status))
				if dto.ConnectionID != nil && *dto.ConnectionID != "" {
					line += " (connection " + output.SafeCell(*dto.ConnectionID) + ")"
				}
				if dto.FailureCode != nil && *dto.FailureCode != "" {
					line += " (" + output.SafeCell(*dto.FailureCode) + ")"
				}
				_, err := fmt.Fprintln(w, line)
				return err
			})
		},
	}
}
