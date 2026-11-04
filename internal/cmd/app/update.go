package app

import (
	"encoding/json"
	"fmt"
	urnlib "github.com/hadron-memory/urn-lib-go"
	"io"
	"slices"
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
	var settings settingsFlags
	var newURN, appType string
	var (
		name, description     string
		clearDescription, yes bool
		timeout               int
	)
	cmd := &cobra.Command{
		Use:   "update <app-ref>",
		Short: "Update an App's settings",
		Long: `Change an App's settings. Surfaces are descriptive strings, not authorization
boundaries; the portal shows its chat tab when the list contains chat. Updating
surfaces or Agent tools replaces the whole list; their clear flags send [].
Changing type does not change surfaces. Admission policy updates and legacy AI
keys are not exposed here (use ai-config for AI configuration).

Only the flags you pass are sent; every other field keeps its stored value.
description can also be CLEARED, with a separate explicit
flag (--clear-description); name and the timeout have
no clear. Passing no flag changes nothing and is refused. The server stays
the only authority: its validation and authorization refusals are shown as
returned, and the CLI invents no timeout default and clamps nothing.

The target App (as resolved) and the NAMES of the fields to change are shown
for confirmation; a rename also shows the old and new full URNs. Without a terminal, pass
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

			prepared, err := settings.prepare(cmd)
			if err != nil {
				return err
			}
			var urnPtr *string
			var typePtr *gen.AppType
			if fl.Changed("urn") {
				if err := cmdutil.ValidateURNSlug("--urn", newURN); err != nil {
					return err
				}
				urnPtr = &newURN
				prepared.changed = append(prepared.changed, "urn")
			}
			if fl.Changed("type") {
				v := gen.AppType(appType)
				if !slices.Contains(gen.AllAppType, v) {
					return exitcode.Newf(exitcode.Usage, "invalid --type")
				}
				typePtr = &v
				prepared.changed = append(prepared.changed, "appType")
			}
			var (
				namePtr    *string
				descJSON   json.RawMessage
				timeoutPtr *int
				changed    = prepared.changed
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
				return exitcode.Newf(exitcode.Usage, "nothing to update: pass a setting flag or a true --clear-* flag")
			}

			appRef, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			prompt := fmt.Sprintf("Update App %s: %s?", appRef, strings.Join(changed, ", "))
			if urnPtr != nil && !yes && f.IOStreams.IsInputTerminal() {
				metadata, err := gen.GetAppRenameURN(cmd.Context(), client, appRef)
				if err != nil {
					return api.MapError(err)
				}
				if metadata.App == nil {
					return exitcode.Newf(exitcode.NotFound, "App not found")
				}
				oldURN := metadata.App.Urn
				parsed, err := urnlib.ParseUrn(oldURN)
				if err != nil || len(parsed.PathSegments) < 2 {
					return exitcode.Newf(exitcode.Error, "server returned an invalid App URN")
				}
				renamed, err := urnlib.ComposeUrnV2("app", parsed.PathSegments[0], newURN)
				if err != nil {
					return exitcode.Newf(exitcode.Usage, "invalid App rename")
				}
				prompt = fmt.Sprintf("Update App %s -> %s: %s?", oldURN, renamed, strings.Join(changed, ", "))
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			var dto updateAppDTO
			dto.App = appRef
			dto.Status = "updated"
			dto.Changed = changed
			if len(prepared.changed) > 0 {
				resp, callErr := gen.UpdateAppSettings(cmd.Context(), client, appRef, namePtr, descJSON, timeoutPtr, urnPtr, typePtr, prepared.expiresJSON, prepared.dormancy, prepared.expire, prepared.role, prepared.permission, prepared.identify, prepared.ttl, prepared.training, prepared.tools, prepared.surfaces)
				err = callErr
				if err == nil {
					u := resp.UpdateApp
					dto.ID = u.Id
					dto.URN = u.Urn
					dto.Name = u.Name
					dto.SessionTimeoutSeconds = u.SessionTimeoutSeconds
				}
			} else {
				resp, callErr := gen.UpdateApp(cmd.Context(), client, appRef, namePtr, descJSON, timeoutPtr)
				err = callErr
				if err == nil {
					u := resp.UpdateApp
					dto.ID = u.Id
					dto.URN = u.Urn
					dto.Name = u.Name
					dto.SessionTimeoutSeconds = u.SessionTimeoutSeconds
				}
			}
			if err != nil {
				mapped := api.MapError(err)
				if exitcode.FromError(mapped) == exitcode.Unavailable {
					return exitcode.New(exitcode.Unavailable, fmt.Errorf("app update outcome unknown (sent once, not retried; read the App back before retrying): %w", mapped))
				}
				return mapped
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Updated App %s (%s)\n", appRef, strings.Join(changed, ", "))
				return err
			})
		},
	}
	settings.flags(cmd, false)
	fl := cmd.Flags()
	fl.StringVar(&newURN, "urn", "", "new App slug; confirmation names the old and new full URNs")
	fl.StringVar(&appType, "type", "", "App type: AGENT|AUTOMATION|CHATBOT|CLOUD|IOT|WORKSTATION; leaves surfaces unchanged")
	fl.StringVar(&name, "name", "", "the App's new name")
	fl.StringVar(&description, "description", "", "the App's new description")
	fl.BoolVar(&clearDescription, "clear-description", false, "clear the description (sends null)")
	fl.IntVar(&timeout, "session-timeout-seconds", 0, "the session timeout in seconds (the server validates the value)")
	fl.BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	cmdutil.RetireFlags(cmd, "an installed Agent's boot prompt is now a node: write the source in the App install memory (role boot-prompt, hadron node update), compile it with `hadron app agent compile`, then have the App's manager approve the compiled node with `hadron node approve <compiled-node-ref>`",
		cmdutil.RetiredFlag{Name: "system-prompt"}, cmdutil.RetiredFlag{Name: "system-prompt-file"}, cmdutil.RetiredFlag{Name: "clear-system-prompt", Bool: true})
	return cmd
}

func mustJSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // a string always marshals
	return b
}
