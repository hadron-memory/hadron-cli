package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// setDTO is the stable --json shape for policy set --mine: the user and the
// link now stored (null = no link).
type setDTO struct {
	Layer  string           `json:"layer"`
	UserID string           `json:"userId"`
	Handle *string          `json:"handle"`
	Policy *json.RawMessage `json:"policy"`
}

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	var mine, clear bool
	cmd := &cobra.Command{
		Use:   "set --mine (<json> | @<file> | --clear)",
		Short: "Set your own user-layer action policy",
		Long: `Set your own user layer of the action-policy chain: it restricts every run
made on your behalf, in every org. --mine is required; it names the layer
(the org layer is set with hadron org update --policy).

Pass the link as JSON, inline or as @file:
  '{"allow":["run.execute","comm.outbound"]}'   allow only these actions
  '{"allow":[]}'                                 allow nothing
--clear removes your link, so your layer no longer restricts anything.

The server checks the shape ({"allow": [strings]}); anything else exits 2.
Any signed-in user may set their own; nobody can set another user's.

Prints "✓ set user policy for <you>: <link>"; --json gives layer, userId,
handle and policy (null after --clear).`,
		Example: `  hadron policy set --mine '{"allow":["run.execute"]}'
  hadron policy set --mine @my-policy.json
  hadron policy set --mine --clear`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !mine {
				return exitcode.Newf(exitcode.Usage, "pass --mine: policy set writes your own user layer (the org layer is hadron org update --policy)")
			}
			if clear == (len(args) == 1) {
				return exitcode.Newf(exitcode.Usage, "pass the policy JSON (or @file), or --clear, not both")
			}
			var policy *json.RawMessage
			if !clear {
				raw := args[0]
				if strings.HasPrefix(raw, "@") {
					data, err := os.ReadFile(strings.TrimPrefix(raw, "@"))
					if err != nil {
						return exitcode.Newf(exitcode.Usage, "reading %s: %v", raw, err)
					}
					raw = string(data)
				}
				raw = strings.TrimSpace(raw)
				if raw == "" {
					return exitcode.Newf(exitcode.Usage, "the policy is empty; pass JSON such as '{\"allow\":[]}', or --clear")
				}
				var probe any
				if err := json.Unmarshal([]byte(raw), &probe); err != nil {
					return exitcode.Newf(exitcode.Usage, "the policy must be valid JSON: %v", err)
				}
				msg := json.RawMessage(raw)
				policy = &msg
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateMyPolicy(cmd.Context(), client, policy)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateMyPolicy == nil {
				return exitcode.Newf(exitcode.Error, "server returned no user")
			}
			u := resp.UpdateMyPolicy
			dto := setDTO{Layer: "user", UserID: u.Id, Handle: u.Handle, Policy: u.Policy}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				who := dto.UserID
				if dto.Handle != nil && *dto.Handle != "" {
					who = *dto.Handle
				}
				link := "none (no restriction)"
				if dto.Policy != nil && string(*dto.Policy) != "null" {
					link = string(*dto.Policy)
				}
				_, err := fmt.Fprintf(w, "✓ set user policy for %s: %s\n", output.SafeCell(who), output.SafeCell(link))
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&mine, "mine", false, "write your own user layer (required)")
	cmd.Flags().BoolVar(&clear, "clear", false, "remove your user-layer link (no restriction)")
	return cmd
}
