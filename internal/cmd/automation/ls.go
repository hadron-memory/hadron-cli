package automation

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// lsDTO is the stable --json shape of `automation ls`. Rows and Omitted are
// always arrays. A row's state is UNADOPTED or ADOPTED only: CHANGED exists on
// the detail projection (`approval`) alone.
type lsDTO struct {
	App     string       `json:"app"`
	Rows    []rowDTO     `json:"rows"`
	Omitted []omittedDTO `json:"omitted"`
}

func stateWord(state string) string {
	switch state {
	case "ADOPTED":
		return "approved"
	case "UNADOPTED":
		return "not approved"
	case "CHANGED":
		return "changed since it was approved"
	}
	return safeText(state, maxName)
}

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the automations of an App that you can approve",
		Long: `List the schedules and webhooks of the App (--app, else the active App) that you
may approve, with their state: "approved" or "not approved".

"approved" means approved at some point, not that nothing has changed since:
whether an approved automation has changed shows only on ` + "`hadron automation approval <name>`" + `.
Only an adopter sees any row, so an empty list never proves the App has no
automations. Manual runs are not listed: each is approved from the run that
needs it (--run).`,
		Example: `  hadron automation ls
  hadron automation ls --app hrn:app:acme.com:ops --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, _, err := cmdutil.TargetApp(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			l, err := collectListing(cmd.Context(), client, app)
			if err != nil {
				return err
			}
			dto := lsDTO{App: app, Rows: l.Rows, Omitted: l.Omitted}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Rows) == 0 {
					fmt.Fprintf(w, "No schedules or webhooks you can approve in %s.\n", app)
				} else {
					t := output.NewTable(w, "AUTOMATION", "STATE", "WHAT")
					for _, r := range dto.Rows {
						state := stateWord(r.State)
						if r.State == "ADOPTED" {
							state += " (approval may be stale)"
						}
						t.Row(displayName(r), state, safeText(r.Headline, maxLine))
					}
					if err := t.Flush(); err != nil {
						return err
					}
				}
				for _, o := range dto.Omitted {
					if o.TriggerKind == "MANUAL" {
						fmt.Fprintln(w, "Manual runs are approved from the run that needs them (--run <run-id>).")
					} else {
						fmt.Fprintf(w, "%s triggers are not listed: %s\n", safeText(o.TriggerKind, maxName), safeText(o.Reason, maxLine))
					}
				}
				fmt.Fprintln(w, "Approved means approved at some point; whether it changed since shows on `hadron automation approval <name>`.")
				return nil
			})
		},
	}
}
