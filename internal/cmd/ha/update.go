package ha

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name, url, tokenFile string
		allowOps             []string
		clearAllowOps        bool
		enabled, disabled    bool
	)
	cmd := &cobra.Command{
		Use:   "update <id> [flags]",
		Short: "Change a registered Home Assistant instance (org admin)",
		Long: `Change a registered Home Assistant instance. You must be an admin or owner of
its organization. <id> is the ID column of "hadron ha list".

Only the fields you pass change; everything else keeps its stored value. The
slug can never be changed: runs name the instance's ops ha__<slug>__<op>.

--token-file replaces the access token, read as in "ha create" (- for stdin,
@path or a path for a file); the old token is never read. Without it the
stored token stays, even when stdin is piped.

--allow-op <op> (repeatable) replaces the list of allowed ops.
--clear-allow-ops empties that list, which allows EVERY op in the catalog
(not none). To allow nothing, use --disabled instead.

--enabled and --disabled switch the instance on and off.

Changing the registry contacts nothing: the Home Assistant host is not called.

If you get "not found" (exit 4), no instance has that ID or it belongs to an
organization you are not a member of; the server does not say which. A member
who is not an admin is refused (exit 8).

Prints "✓ updated Home Assistant instance <slug> (id <id>, enabled)"; --json
prints the instance as "ha get --json" does, without the token.`,
		Example: `  hadron ha update <instance-id> --name "Main house"
  printf '%s' "$NEW_HA_TOKEN" | hadron ha update <instance-id> --token-file -
  hadron ha update <instance-id> --allow-op get_state --allow-op list_states
  hadron ha update <instance-id> --clear-allow-ops --enabled`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Gate on flag presence (Changed), not the parsed value, so an
			// explicit --enabled=false still counts as a change.
			changed := cmd.Flags().Changed
			// --clear-allow-ops=false has no meaning, and acting on its presence
			// would widen the allowlist to every op: refuse it.
			if changed("clear-allow-ops") && !clearAllowOps {
				return exitcode.Newf(exitcode.Usage, "--clear-allow-ops takes no value: pass it to allow every op, or omit it to keep the allowlist")
			}
			if !changed("name") && !changed("url") && !changed("token-file") &&
				!changed("allow-op") && !changed("clear-allow-ops") &&
				!changed("enabled") && !changed("disabled") {
				return exitcode.Newf(exitcode.Usage, "nothing to update: pass at least one of --name, --url, --token-file, --allow-op, --clear-allow-ops, --enabled, --disabled")
			}
			var nameArg, urlArg, tokenArg *string
			if changed("name") {
				nameArg = &name
			}
			if changed("url") {
				urlArg = &url
			}
			if changed("token-file") {
				token, err := readToken(f.IOStreams, tokenFile, true)
				if err != nil {
					return err
				}
				tokenArg = &token
			}
			// --clear-allow-ops sends an explicit [], which the server reads as
			// every catalog op.
			var allowArg *[]string
			switch {
			case changed("allow-op"):
				allowArg = &allowOps
			case changed("clear-allow-ops"):
				empty := []string{}
				allowArg = &empty
			}
			var enabledArg *bool
			switch {
			case changed("enabled"):
				enabledArg = &enabled
			case changed("disabled"):
				v := !disabled
				enabledArg = &v
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id := args[0]
			resp, err := gen.UpdateHomeAssistantInstance(cmd.Context(), client, id, nameArg, urlArg, tokenArg, allowArg, enabledArg)
			if err != nil {
				return writeError(err, "hadron ha get "+cmdutil.ShellQuote(id))
			}
			if resp == nil || resp.UpdateHomeAssistantInstance == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no instance; run `hadron ha get %s` to see whether the change applied", cmdutil.ShellQuote(id))
			}
			dto := dtoFromFields(resp.UpdateHomeAssistantInstance.HomeAssistantInstanceFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ updated Home Assistant instance %s (id %s, %s)\n", output.SafeCell(dto.Slug), output.SafeCell(dto.ID), dto.statusLabel())
				return err
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new display name")
	cmd.Flags().StringVar(&url, "url", "", "new address")
	cmd.Flags().StringVar(&tokenFile, "token-file", "", `replace the access token: "-" stdin, "@file", or a file path`)
	cmd.Flags().StringArrayVar(&allowOps, "allow-op", nil, "replace the allowed ops with these (repeatable)")
	cmd.Flags().BoolVar(&clearAllowOps, "clear-allow-ops", false, "empty the allowed-op list, which allows every op")
	cmd.Flags().BoolVar(&enabled, "enabled", false, "switch the instance on")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "switch the instance off")
	cmd.MarkFlagsMutuallyExclusive("allow-op", "clear-allow-ops")
	cmd.MarkFlagsMutuallyExclusive("enabled", "disabled")
	return cmd
}
