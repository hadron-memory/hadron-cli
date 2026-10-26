package automation

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// resultDTO is the stable --json shape of approve and decline.
type resultDTO struct {
	App     string     `json:"app"`
	Trigger triggerDTO `json:"trigger"`
	Status  string     `json:"status"`
	Hash    string     `json:"hash,omitempty"`
}

// inApp is the " in <app>" tail of a header or prompt. A MANUAL origin is found
// by its run id alone — the server's projection and mutations take no App for it
// — so the CLI must not claim the run belongs to the App it was run against.
func inApp(sel *selection, app string) string {
	if sel.Kind == gen.AutomationTriggerKindManual {
		return " (found by run id only; which App it belongs to is not checked here)"
	}
	return " in " + app
}

// terminalSafe reports whether s survives sanitizing unchanged — the condition
// for putting it in a command a person may copy.
func terminalSafe(s string) bool { return safeText(s, 4*maxFact) == s }

// approvalHint is the copy-pasteable re-read after a refusal. A trigger or run
// name that sanitizing would change is never put in a command (the copy would
// name a different target, or carry terminal controls): the hint then points
// at the list instead.
func approvalHint(sel *selection, app string) string {
	target := "--run " + shellQuote(sel.Trigger.Ref)
	raw := sel.Trigger.Ref
	if sel.Trigger.Name != "" {
		raw = kindWord(sel.Kind) + "/" + sel.Trigger.Name
		target = shellQuote(raw)
	}
	if !terminalSafe(raw) || !terminalSafe(app) {
		return "`hadron automation list` and review it again (the name contains characters that cannot be shown safely)"
	}
	return "`hadron automation approval " + target + " --app " + shellQuote(app) + "`"
}

// mutationError classifies a failed approve or decline. `partial` is true when
// the response carried a true result next to the error: the write may have
// committed, so it is outcome UNKNOWN (7) and never retried. The server's
// refusals keep its wording and the exit code api.MapError gives them.
func mutationError(err error, partial bool, verb string, sel *selection, app string) error {
	if partial {
		return exitcode.Newf(exitcode.Unavailable,
			"outcome unknown: %s. The %s may or may not have been applied and was not retried — run %s to check",
			api.MapError(err), verb, approvalHint(sel, app))
	}
	if api.IsSchemaSkew(err) {
		return exitcode.Newf(exitcode.Unavailable, "%s", skewMessage)
	}
	if verb == "approval" && api.HasErrorCode(err, "AUTOMATION_CHANGED") {
		return exitcode.Newf(exitcode.Conflict,
			"the automation changed since you looked, so nothing was approved and nothing was retried: run %s and approve again",
			approvalHint(sel, app))
	}
	return api.MapError(err)
}

// approvalTarget is the name argument for a copy-pasteable `approval` command
// (terminal-safe only; otherwise the placeholder).
func approvalTarget(sel *selection) string {
	raw := "--run " + sel.Trigger.Ref
	name := kindWord(sel.Kind) + "/" + sel.Trigger.Name
	if sel.Trigger.Name != "" {
		raw = name
	}
	if !terminalSafe(raw) {
		return "<name>"
	}
	if sel.Trigger.Name != "" {
		return shellQuote(name)
	}
	return "--run " + shellQuote(sel.Trigger.Ref)
}

// approvalAppFlag is " --app <app>" for a copy-pasteable command, or nothing when
// the App ref would not survive sanitizing.
func approvalAppFlag(app string) string {
	if !terminalSafe(app) {
		return ""
	}
	return " --app " + shellQuote(app)
}

func notConfirmed(verb string, sel *selection, app string) error {
	return exitcode.Newf(exitcode.Unavailable,
		"outcome unknown: the server did not confirm the %s. Run %s to check", verb, approvalHint(sel, app))
}

func newCmdApprove(f *cmdutil.Factory) *cobra.Command {
	var run string
	var yes bool
	cmd := &cobra.Command{
		Use:   "approve [<name>]",
		Short: "Approve an automation as shown",
		Long: `Approve one automation (--app, else the active App). The summary is shown first
(on stderr), you confirm, and the hash just shown is what is sent. If the
automation changed in the meantime the server refuses it (exit 5): nothing is
retried, and the next step is ` + "`hadron automation approval <name>`" + ` again.

--yes skips the prompt, never the display. Without --yes a non-interactive
caller is refused before anything is sent; "no" or end-of-input exits 6.
Display and prompt go to stderr, so stdout carries only the result.`,
		Example: `  hadron automation approve schedule/nightly
  hadron automation approve nightly --yes --json
  hadron automation approve --run <run-id> --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, source, err := cmdutil.TargetApp(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sel, err := resolveSelection(cmd.Context(), client, app, args, run, cmd.Flags().Changed("run"))
			if err != nil {
				return err
			}
			dto, err := fetchApproval(cmd.Context(), client, app, sel.Trigger, sel.Kind, false)
			if err != nil {
				return err
			}
			incomplete, err := writeProjection(f.IOStreams.ErrOut, sel.Display+inApp(sel, app), dto, false)
			if err != nil {
				return exitcode.Newf(exitcode.Error, "could not show the summary (%v); nothing was approved", err)
			}
			if incomplete {
				return exitcode.Newf(exitcode.Unavailable,
					"unavailable: the summary is too large to show in full, so nothing was approved (approval needs the whole summary on screen). Read all of it with `hadron automation approval %s --json%s`",
					approvalTarget(sel), approvalAppFlag(app))
			}
			where := inApp(sel, app)
			if sel.Kind != gen.AutomationTriggerKindManual {
				where += " (" + source + ")"
			}
			prompt := fmt.Sprintf("Approve %s%s as shown above?", sel.Display, where)
			switch dto.State {
			case string(gen.AutomationAdoptionStateAdopted):
				prompt = fmt.Sprintf("Approve %s%s again as shown above? (It is already approved; this records another approval.)", sel.Display, where)
			case string(gen.AutomationAdoptionStateChanged):
				prompt = fmt.Sprintf("Approve %s%s as it is now, shown above? (It changed since it was approved; its next run fails until you approve it again.)", sel.Display, where)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, prompt); err != nil {
				return err
			}
			resp, err := gen.ApproveAutomation(cmd.Context(), client, sel.Kind, sel.Trigger.Ref, dto.Hash)
			if err != nil {
				return mutationError(err, resp != nil && resp.ApproveAutomation, "approval", sel, app)
			}
			if resp == nil || !resp.ApproveAutomation {
				return notConfirmed("approval", sel, app)
			}
			res := resultDTO{App: app, Trigger: sel.Trigger, Status: "approved", Hash: dto.Hash}
			return output.Write(f.IOStreams, f.JSON, res, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ approved %s in %s\n", sel.Display, app)
				return err
			})
		},
	}
	addSelectionArgs(cmd, &run)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt (the summary is still shown)")
	return cmd
}

func declineWarning(sel *selection) string {
	switch {
	case sel.Kind == gen.AutomationTriggerKindManual:
		return "Declining withdraws the approval for this manual run: anything that later uses this run's approval is refused until it is approved again. Runs already started keep what they were approved with."
	case sel.State == string(gen.AutomationAdoptionStateAdopted):
		return "Declining switches this automation off: its next run will fail until it is approved again. This revokes the current approval, including a newer approval another person may have just given. Runs already started keep what they were approved with."
	default:
		return "This automation is not approved. Declining records your decline and changes nothing else — unless someone approves it before this runs: decline carries no hash, so it would revoke that approval too."
	}
}

func newCmdDecline(f *cmdutil.Factory) *cobra.Command {
	var run string
	var yes bool
	cmd := &cobra.Command{
		Use:   "decline [<name>]",
		Short: "Decline (or switch off) an automation",
		Long: `Decline one automation (--app, else the active App). On an approved automation this
REVOKES the approval — its next run fails until it is approved again — including
a newer approval someone else has just given: decline carries no hash, because it
only ever removes authority. On an automation that is not approved it only
records the decline. The confirmation says which.

Decline never needs the summary, so an automation you cannot read can still be
declined. --yes skips the prompt; without it a non-interactive caller is refused
before anything is sent, and "no" or end-of-input exits 6.`,
		Example: `  hadron automation decline schedule/nightly
  hadron automation decline --run <run-id> --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, source, err := cmdutil.TargetApp(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sel, err := resolveSelection(cmd.Context(), client, app, args, run, cmd.Flags().Changed("run"))
			if err != nil {
				return err
			}
			if sel.Kind == gen.AutomationTriggerKindManual {
				if err := requireSupport(cmd.Context(), client, app); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "Automation %s%s\n%s\n", sel.Display, inApp(sel, app), declineWarning(sel)); err != nil {
				return exitcode.Newf(exitcode.Error, "could not show the warning (%v); nothing was declined", err)
			}
			where := inApp(sel, app)
			if sel.Kind != gen.AutomationTriggerKindManual {
				where += " (" + source + ")"
			}
			question := fmt.Sprintf("Decline %s%s?", sel.Display, where)
			if sel.State == string(gen.AutomationAdoptionStateAdopted) && sel.Kind != gen.AutomationTriggerKindManual {
				question = fmt.Sprintf("Decline %s%s? This switches it off.", sel.Display, where)
			}
			if err := cmdutil.Confirm(f.IOStreams, yes, question); err != nil {
				return err
			}
			resp, err := gen.DeclineAutomation(cmd.Context(), client, sel.Kind, sel.Trigger.Ref)
			if err != nil {
				return mutationError(err, resp != nil && resp.DeclineAutomation, "decline", sel, app)
			}
			if resp == nil || !resp.DeclineAutomation {
				return notConfirmed("decline", sel, app)
			}
			res := resultDTO{App: app, Trigger: sel.Trigger, Status: "declined"}
			return output.Write(f.IOStreams, f.JSON, res, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ declined %s in %s\n", sel.Display, app)
				return err
			})
		},
	}
	addSelectionArgs(cmd, &run)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}
