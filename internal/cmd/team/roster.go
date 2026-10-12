package team

// `team roster` — the server-derived team roster (#830 over server #1707).
// The roster is RENDERED here, never re-derived: every state comes from the
// teamRoster query. The states are plan-derived (design:agent-tasks §7.4,
// cor:agt:050 Out Of Scope), which the help text says out loud per the
// CHECKED brief — they describe the plan's model, not platform law.

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdTeamRoster(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "roster",
		Short: "The team's workers and their derived states",
		Long: `The team App's roster: every worker with its derived state, session,
model family, tasks and open alarms — the server's teamRoster query,
rendered as returned. The CLI derives nothing.

The STATES are plan-derived (design:agent-tasks §7.4), not a spec — they are
the plan's model of a worker's situation, and cor:agt:050 keeps them out of
the law corpus on purpose. A worker can be in several at once; it is shown
in the first that holds: ASSIGNED_NOT_STARTED, AVAILABLE, NOT_LAUNCHABLE,
NO_FAMILY, UNAVAILABLE, WAITING, YOUR_MOVE…

Read it with lastActiveAt: nothing clears a reportsTo claim automatically,
and a state is a fact about now, not a judgement.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			b, err := readBindingOrNilWithApp(ctx, f)
			if err != nil {
				return err
			}
			scope, err := resolveTeamAppScope(ctx, f, b)
			if err != nil {
				return err
			}
			appLabel := lazyAppLabel(ctx, f, scope)
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamRoster(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			dto := rosterDTO{
				AppID:   resp.TeamRoster.AppId,
				AsOf:    resp.TeamRoster.AsOf,
				Workers: []rosterEntryDTO{},
			}
			for _, w := range resp.TeamRoster.Workers {
				if w == nil {
					continue
				}
				// Guarded like every sibling (cycle 2, Xylon P2-1): a null
				// worker entry renders an empty party rather than panicking —
				// this was the diff's one unguarded party dereference.
				worker := taskPartyDTO{}
				if party := partyDTO(w.Worker); party != nil {
					worker = *party
				}
				dto.Workers = append(dto.Workers, rosterEntryDTO{
					Worker:              worker,
					State:               string(w.State),
					Role:                w.Role,
					Bound:               w.Bound,
					LiveSession:         w.LiveSession,
					Family:              w.Family,
					FreeForWork:         w.FreeForWork,
					FreeFamilies:        strListOrEmpty(w.FreeFamilies),
					LaunchableFamilies:  strListOrEmpty(w.LaunchableFamilies),
					UnavailableFamilies: strListOrEmpty(w.UnavailableFamilies),
					UnavailableReason:   reasonPtr(w.UnavailableReason),
					UnavailableUntil:    w.UnavailableUntil,
					LastActiveAt:        w.LastActiveAt,
					LastAssignedAt:      w.LastAssignedAt,
					PendingTasks:        strListOrEmpty(w.PendingTasks),
					StartedTasks:        strListOrEmpty(w.StartedTasks),
					WaitingOn:           strListOrEmpty(w.WaitingOn),
					WaitingSince:        w.WaitingSince,
					UnreactedChildTask:  w.UnreactedChildTask,
					Alarms:              strListOrEmpty(w.Alarms),
					ReportsTo:           partyDTO(w.ReportsTo),
					ReportsToRetired:    w.ReportsToRetired,
				})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// Which roster, from where (#470) — before the rows, so an
				// empty staff still names its App.
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				t := output.NewTable(w, "WORKER", "STATE", "ROLE", "FAMILY", "LIVE", "TASKS", "ALARMS")
				for _, e := range dto.Workers {
					name := e.Worker.ID
					if e.Worker.Name != nil {
						name = *e.Worker.Name
					}
					family := strOr(e.Family)
					live := "quiet"
					if e.LiveSession {
						live = "live"
					} else if !e.Bound {
						live = "unbound"
					}
					tasks := fmt.Sprintf("P%d S%d", len(e.PendingTasks), len(e.StartedTasks))
					alarms := fmt.Sprintf("%d", len(e.Alarms))
					t.Row(name, e.State, strOr(e.Role), family, live, tasks, alarms)
				}
				if err := t.Flush(); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "as of %s — states are plan-derived (design:agent-tasks §7.4), not a spec\n", dto.AsOf)
				return err
			})
		},
	}
	return cmd
}
