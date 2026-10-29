package app

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// updateAppDTO is the stable --json shape of `app update` (cli#1058). It names
// WHICH fields changed, never their values.
type updateAppDTO struct {
	App                   string   `json:"app"`
	ID                    string   `json:"id"`
	URN                   string   `json:"urn"`
	Name                  string   `json:"name"`
	SessionTimeoutSeconds int      `json:"sessionTimeoutSeconds"`
	Status                string   `json:"status"`
	Changed               []string `json:"changed"`
}

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name, description     string
		clearDescription, yes bool
		timeout               int
	)
	cmd := &cobra.Command{
		Use:   "update <app-ref>",
		Short: "Update an App's name, description or session timeout",
		Long: `Change an App's basic settings: name, description and
sessionTimeoutSeconds — nothing else (URN, type, surfaces, roles, identity and
admission policy and AI keys stay in the portal).

Only the flags you pass are sent; every other field keeps its stored value.
description can also be CLEARED, with a separate explicit
flag (--clear-description); name and the timeout have
no clear. Passing no flag changes nothing and is refused. The server stays
the only authority: its validation and authorization refusals are shown as
returned, and the CLI invents no timeout default and clamps nothing.

The target App (as resolved) and the NAMES of the fields to change are shown
for confirmation — never field values. Without a terminal, pass
--yes. If the connection drops after the request is sent, the outcome is
unknown: nothing is retried and no success is claimed — read the App back
before trying again.`,
		Example: `  hadron app update hrn:app:acme.com:support --name "Support Desk" --yes
  hadron app update acme.com:support --session-timeout-seconds 3600
  hadron app update acme.com:support --clear-description --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if fl.Changed("description") && clearDescription {
				return exitcode.Newf(exitcode.Usage, "--description and --clear-description are mutually exclusive")
			}

			var (
				namePtr    *string
				descJSON   json.RawMessage
				timeoutPtr *int
				changed    = []string{}
			)
			if fl.Changed("name") {
				namePtr = &name
				changed = append(changed, "name")
			}
			switch {
			case fl.Changed("description"):
				descJSON = mustJSONString(description)
				changed = append(changed, "description")
			case clearDescription:
				descJSON = json.RawMessage("null")
				changed = append(changed, "description (cleared)")
			}
			if fl.Changed("session-timeout-seconds") {
				timeoutPtr = &timeout
				changed = append(changed, "sessionTimeoutSeconds")
			}
			if len(changed) == 0 {
				return exitcode.Newf(exitcode.Usage, "nothing to update: pass at least one of --name, --description/--clear-description, --session-timeout-seconds")
			}

			appRef, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf("Update App %s: %s?", appRef, strings.Join(changed, ", "))); err != nil {
				return err
			}
			resp, err := gen.UpdateApp(cmd.Context(), client, appRef, namePtr, descJSON, timeoutPtr)
			if err != nil {
				mapped := api.MapError(err)
				if exitcode.FromError(mapped) == exitcode.Unavailable {
					return exitcode.New(exitcode.Unavailable, fmt.Errorf("app update outcome unknown (sent once, not retried; read the App back before retrying): %w", mapped))
				}
				return mapped
			}
			u := resp.UpdateApp
			dto := updateAppDTO{
				App:                   appRef,
				ID:                    u.Id,
				URN:                   u.Urn,
				Name:                  u.Name,
				SessionTimeoutSeconds: u.SessionTimeoutSeconds,
				Status:                "updated",
				Changed:               changed,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Updated App %s (%s)\n", appRef, strings.Join(changed, ", "))
				return err
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&name, "name", "", "the App's new name")
	fl.StringVar(&description, "description", "", "the App's new description")
	fl.BoolVar(&clearDescription, "clear-description", false, "clear the description (sends null)")
	fl.IntVar(&timeout, "session-timeout-seconds", 0, "the session timeout in seconds (the server validates the value)")
	fl.BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func mustJSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // a string always marshals
	return b
}
