package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	urn "github.com/hadron-memory/urn-lib-go"
	"github.com/spf13/cobra"
)

var appActionCode = regexp.MustCompile(`^app\.[a-z0-9-]+(\.[a-z0-9][a-z0-9-]*)+$`)

type actionRecordDTO struct {
	EventID string                  `json:"eventId"`
	Action  cmdutil.ActionAnswerDTO `json:"action"`
}

func newCmdAction(f *cmdutil.Factory) *cobra.Command {
	c := &cobra.Command{Use: "action", Short: "Record an App-defined action"}
	var task, payload, file string
	record := &cobra.Command{
		Use: "record <code>", Short: "Record one App-defined action and print its register answer",
		Long: `Record an App-defined action in the current App (--app or the active App). An
App-defined action is a named event your App reports (for example
"app.eng.design.approved"); the server stores it on the App's event log, where
the App's participants can read it, and answers with what the App's action
register makes of it.

The code is app.<current-slug>.<name>: lowercase letters, digits and hyphens in
dot-separated parts, starting with "app." and the App's current slug, ending in a
letter or digit, at most 64 bytes in all. Anything else is refused before any
request (exit 2): "app.eng.Design" fails on the capital letter. The server
checks the current slug, so after an App rename codes with the old slug are
refused.

--task attaches the action to a task, in the same id, qualified handle or bare
handle forms as "team task get". The server resolves the task and judges the
caller; a bare handle may need qualification when you belong to several Apps.

--payload or --payload-file supplies one JSON object of at most 4 KiB.
Payloads are readable by every participant of the App and by its key: put
nothing secret in them. Omitted payload is not sent. There is one mutation and
no retry: after an unknown transport outcome inspect "hadron team events"
before recording again, because each successful call adds a new event.

The register answer is what the server returns, never an authorization check:
  MAPPED      the App's action register has an entry for this code and a
              runnable task was found for it (its URN is returned).
  UNRESOLVED  there is an entry, but no runnable task was found where it points.
  UNMAPPED    no register entry matches this code; only the code comes back.
  WITHHELD    you have no Agent to ask as, so the server returns the code only,
              with no task and no register lookup.`,
		Example: "  hadron app action record app.eng.design.approved --app acme.com:eng\n  hadron app action record app.eng.design.approved --task AT0.7 --payload '{\"review\":\"done\"}' --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code := args[0]
			if !appActionCode.MatchString(code) || len(code) > urn.MaxAtomLen || urn.ValidateAtomShape("code", code) != nil {
				return exitcode.Newf(exitcode.Usage, "code must match app.<slug>.<name>, start and end with a letter or digit, and be at most %d bytes", urn.MaxAtomLen)
			}
			if cmd.Flags().Changed("payload") && cmd.Flags().Changed("payload-file") {
				return exitcode.Newf(exitcode.Usage, "choose --payload or --payload-file")
			}
			var raw *json.RawMessage
			if cmd.Flags().Changed("payload") || cmd.Flags().Changed("payload-file") {
				data := []byte(payload)
				if cmd.Flags().Changed("payload-file") {
					if strings.TrimSpace(file) == "" {
						return exitcode.Newf(exitcode.Usage, "--payload-file needs a path")
					}
					fh, err := os.Open(file)
					if err != nil {
						return exitcode.Newf(exitcode.Usage, "read --payload-file: %v", err)
					}
					data, err = io.ReadAll(io.LimitReader(fh, 4097))
					closeErr := fh.Close()
					if err != nil {
						return exitcode.Newf(exitcode.Usage, "read --payload-file: %v", err)
					}
					if closeErr != nil {
						return exitcode.Newf(exitcode.Usage, "close --payload-file: %v", closeErr)
					}
				}
				var object map[string]json.RawMessage
				if len(data) > 4096 || json.Unmarshal(data, &object) != nil || object == nil {
					return exitcode.Newf(exitcode.Usage, "payload must be one JSON object of at most 4 KiB")
				}
				v := json.RawMessage(data)
				raw = &v
			}
			var taskArg *string
			if cmd.Flags().Changed("task") {
				if strings.TrimSpace(task) == "" {
					return exitcode.Newf(exitcode.Usage, "--task needs a task reference")
				}
				trimmed := strings.TrimSpace(task)
				taskArg = &trimmed
			}
			ref, err := cmdutil.ResolveAppRef(f, "")
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RecordAppAction(cmd.Context(), client, ref, code, taskArg, raw)
			if err != nil {
				if api.ServerCodeOf(err) == "APP_ACTION_CODE_INVALID" {
					message := "APP_ACTION_CODE_INVALID: the action code must name this App and satisfy the App action grammar"
					if appURN, ok := api.ExtensionString(err, "appUrn"); ok {
						message += " (App " + output.SafeCell(appURN) + ")"
					}
					return api.WithServerCode(exitcode.Newf(exitcode.Usage, "%s", message), err)
				}
				return api.MapErrorKeepingCode(err)
			}
			if resp == nil || resp.RecordAppAction == nil || resp.RecordAppAction.Action == nil || resp.RecordAppAction.EventId == "" {
				return exitcode.Newf(exitcode.Unavailable, "server returned no action receipt; inspect team events before recording again")
			}
			a := resp.RecordAppAction.Action
			dto := actionRecordDTO{EventID: resp.RecordAppAction.EventId, Action: cmdutil.ActionAnswerDTO{Code: a.Code, State: string(a.State), TaskLoc: a.TaskLoc, TaskURN: a.TaskUrn}}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				source := "from the App context"
				if cmd.Flags().Changed("app") {
					source = "from --app"
				}
				if _, err := fmt.Fprintf(w, "app: %s (%s)\n", ref, source); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "event %s\naction %s  %s\ntask loc %s  task URN %s\n", output.SafeCell(dto.EventID), output.SafeCell(dto.Action.Code), output.SafeCell(dto.Action.State), actionCell(dto.Action.TaskLoc), actionCell(dto.Action.TaskURN))
				return err
			})
		},
	}
	record.Flags().StringVar(&task, "task", "", "task id or handle (same forms as team task get)")
	record.Flags().StringVar(&payload, "payload", "", "JSON object, at most 4 KiB; never secrets")
	record.Flags().StringVar(&file, "payload-file", "", "file holding a JSON object, at most 4 KiB")
	c.AddCommand(record)
	return c
}
func actionCell(s *string) string {
	if s == nil {
		return "—"
	}
	return output.SafeCell(*s)
}
