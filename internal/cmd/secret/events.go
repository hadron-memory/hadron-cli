package secret

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

type eventDTO struct {
	ID          string  `json:"id"`
	At          string  `json:"at"`
	Kind        string  `json:"kind"`
	Outcome     string  `json:"outcome"`
	SecretID    string  `json:"secretId"`
	SecretName  string  `json:"secretName"`
	VersionNo   *int    `json:"versionNo"`
	ActorUserID *string `json:"actorUserId"`
	WorkerID    *string `json:"workerId"`
	SessionID   *string `json:"sessionId"`
}

type eventsDTO struct {
	Total  int        `json:"total"`
	Events []eventDTO `json:"events"`
}

func newCmdEvents(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	var all bool
	cmd := &cobra.Command{
		Use:   "events <id|urn> [--limit N] [--offset N] [--all]",
		Short: "Show an App secret's audit events, newest first",
		Long: `Show the audit events of one App secret, newest first: when, what happened, the
outcome, the version it touched and who did it. No value is ever shown.

Which events you see is the server's call. A secret that does not exist, and
one whose events the server will not show you, both give an empty list, and
this command prints it as empty: it never says "forbidden", because that would
reveal that the secret exists.

The server pages the list (default 20, at most 200 a page). --limit and
--offset pass straight through; --all walks the pages until the server's total.

--json gives total and events (id, at, kind, outcome, secretId, secretName,
versionNo, actorUserId, workerId, sessionId); an empty list is [].`,
		Example: `  hadron secret events 01a18cf643247d0fa261f320ec74d878
  hadron secret events 01a18cf643247d0fa261f320ec74d878 --all --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && (cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset")) {
				return exitcode.Newf(exitcode.Usage, "--all cannot be combined with --limit/--offset")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			out := eventsDTO{Events: []eventDTO{}}
			fetch := func(l, o *int) ([]eventDTO, int, error) {
				resp, err := gen.SecretEvents(cmd.Context(), client, args[0], l, o)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp == nil || resp.SecretEvents == nil {
					return []eventDTO{}, 0, nil
				}
				page := make([]eventDTO, 0, len(resp.SecretEvents.Items))
				for _, e := range resp.SecretEvents.Items {
					if e == nil {
						continue
					}
					page = append(page, eventDTO{ID: e.Id, At: e.At, Kind: e.Kind, Outcome: e.Outcome, SecretID: e.SecretId, SecretName: e.SecretName, VersionNo: e.VersionNo, ActorUserID: e.ActorUserId, WorkerID: e.WorkerId, SessionID: e.SessionId})
				}
				out.Total = resp.SecretEvents.Total
				return page, resp.SecretEvents.Total, nil
			}
			if all {
				items, err := api.CollectAll(func(l, o int) ([]eventDTO, int, error) { return fetch(&l, &o) })
				if err != nil {
					return err
				}
				out.Events = items
			} else {
				items, _, err := fetch(pagePtr(cmd, "limit", limit), pagePtr(cmd, "offset", offset))
				if err != nil {
					return err
				}
				out.Events = items
			}
			return output.Write(f.IOStreams, f.JSON, out, func(w io.Writer) error {
				if len(out.Events) == 0 {
					_, err := fmt.Fprintln(w, "no events")
					return err
				}
				t := output.NewTable(w, "AT", "KIND", "OUTCOME", "VERSION", "BY")
				for _, e := range out.Events {
					v := "-"
					if e.VersionNo != nil {
						v = fmt.Sprint(*e.VersionNo)
					}
					by := output.Dash(e.ActorUserID)
					if e.ActorUserID == nil {
						by = output.Dash(e.WorkerID)
					}
					t.Row(output.SafeCell(e.At), output.SafeCell(e.Kind), output.SafeCell(e.Outcome), v, output.SafeCell(by))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if offset+len(out.Events) < out.Total {
					_, err := fmt.Fprintf(w, "showing %d of %d from offset %d; more with --offset or --all\n", len(out.Events), out.Total, offset)
					return err
				}
				return nil
			})
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 0, "events per page (the server's default and cap apply)")
	cmd.Flags().IntVar(&offset, "offset", 0, "events to skip")
	cmd.Flags().BoolVar(&all, "all", false, "walk every page")
	return cmd
}
