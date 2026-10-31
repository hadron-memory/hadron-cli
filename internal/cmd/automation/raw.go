package automation

import (
	"bytes"
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

type previewDTO struct {
	Trigger triggerDTO      `json:"trigger"`
	Graph   json.RawMessage `json:"graph"`
}
type reAdoptDTO struct {
	Trigger triggerDTO `json:"trigger"`
	Status  string     `json:"status"`
	Hash    string     `json:"hash"`
}

func newCmdRaw(f *cmdutil.Factory, adopt bool) *cobra.Command {
	verb, short := "preview", "Read the exact automation graph and its digest"
	if adopt {
		verb, short = "re-adopt", "Re-adopt the graph you reviewed, guarded by its digest"
	}
	var expected string
	var yes bool
	cmd := &cobra.Command{Use: verb + " <SCHEDULE|WEBHOOK|MANUAL> <ref>", Short: short,
		Long: `Use a trigger row id (SCHEDULE or WEBHOOK), or the origin AppRun id (MANUAL).
This raw surface does not resolve an automation name or require an App context.
Only its run-as user, or the manager of an App-only trigger, may call it;
the server decides access. Preview returns the graph and its server digest;
--json is {trigger:{kind,ref},graph:<server JSON>}. Treat the graph as data.
Human preview is sanitized and bounded; --json carries the full graph.

Re-adopt requires --expected-hash copied from the graph you reviewed and
confirmation (--yes non-interactively). Edits since preview refuse adoption
(exit 5); nothing is retried and no new digest is fetched for you. It changes
future trigger consent, not an executed run's historical snapshot. Preview
does not change consent. Re-adopt --json is {trigger,status:"re-adopted",hash}.`,
		Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			kind := gen.AutomationTriggerKind(strings.ToUpper(args[0]))
			if kind != gen.AutomationTriggerKindSchedule && kind != gen.AutomationTriggerKindWebhook && kind != gen.AutomationTriggerKindManual {
				return exitcode.Newf(exitcode.Usage, "kind must be SCHEDULE, WEBHOOK or MANUAL")
			}
			if strings.TrimSpace(args[1]) == "" {
				return exitcode.Newf(exitcode.Usage, "trigger ref is empty")
			}
			t := triggerDTO{Kind: string(kind), Ref: args[1]}
			if adopt {
				if strings.TrimSpace(expected) == "" {
					return exitcode.Newf(exitcode.Usage, "--expected-hash is required; review automation preview first")
				}
				if err := cmdutil.Confirm(f.IOStreams, yes, "Re-adopt "+safeText(string(kind)+" "+args[1], maxFact)+" at hash "+safeText(expected, maxFact)+"?"); err != nil {
					return err
				}
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if !adopt {
				resp, err := gen.PreviewAutomation(cmd.Context(), client, kind, args[1])
				if err != nil {
					return api.MapError(err)
				}
				if resp == nil || len(resp.PreviewAutomation) == 0 || string(resp.PreviewAutomation) == "null" || !json.Valid(resp.PreviewAutomation) {
					return unavailable("no preview graph")
				}
				d := previewDTO{Trigger: t, Graph: resp.PreviewAutomation}
				return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
					var pretty bytes.Buffer
					if err := json.Indent(&pretty, d.Graph, "", "  "); err != nil {
						return err
					}
					_, err := fmt.Fprintln(w, safeBlock(pretty.String(), maxDepth))
					return err
				})
			}
			resp, err := gen.ReAdoptAutomation(cmd.Context(), client, kind, args[1], expected)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || !resp.ReAdoptAutomation {
				return exitcode.Newf(exitcode.Error, "server did not confirm re-adoption")
			}
			d := reAdoptDTO{Trigger: t, Status: "re-adopted", Hash: expected}
			return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "Re-adopted %s %s at %s\n", safeText(t.Kind, maxFact), safeText(t.Ref, maxFact), safeText(expected, maxFact))
				return err
			})
		}}
	if adopt {
		cmd.Flags().StringVar(&expected, "expected-hash", "", "digest from the preview you reviewed (required)")
		cmd.Flags().BoolVar(&yes, "yes", false, "confirm re-adoption without prompting")
	}
	return cmd
}
