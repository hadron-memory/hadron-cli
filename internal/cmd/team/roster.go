package team

// `team roster` — the server-derived team roster (#830 over server #1707).
// The roster is RENDERED here, never re-derived: every state comes from the
// teamRoster query. The states are plan-derived (design:agent-tasks §7.4,
// cor:agt:050 Out Of Scope), which the help text says out loud per the
// CHECKED brief — they describe the plan's model, not platform law.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
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
in the first that holds (the server's roster order, most actionable
first): YOUR_MOVE, UNAVAILABLE, WAITING, WORKING, ASSIGNED_NOT_STARTED,
AVAILABLE, NO_FAMILY, NOT_LAUNCHABLE.

CHECK is the separate server-derived team-channel checking status:
NO_SESSION, PENDING (with its reason), OK or OVERDUE. A dash means the
server did not make the result available. This experiment adds no overdue
alarm, and the CLI does not calculate the status from local time or tasks.

TERMINAL is the worker's latest terminal report from a host (hadron-server
#2771): the status (busy, idle, locked or dark), the reason for locked or dark,
and the AGE of the report on every row — idle (3m), dark (no_turn_for_due_ticker,
12m) — so a stale report from a stopped sweep reads as stale. The age is from
checkedAt, for display only; a dash means never reported or not available to you.
--json carries the whole object (host, status, prompt, reason, lastTurnAt,
checkedAt, reportedAt). It is stored and advisory: it decides nothing.

MODEL is what the worker's newest open binding SELF-REPORTED and TIER is the
server's coarse high/mid/low mapping of it (a dash: unbound, unreported or
unmapped). Both are advisory display, like FAMILY — never authorization and
never inferred here. The "free for work by tier" line tallies the rows the
server marked freeForWork (not state AVAILABLE) by that tier; a null or
unrecognised tier counts as unknown. Needs a server that serves these fields.

GROUPS are the model groups the worker's model is CURRENTLY in, computed live
by the server (cor:agt:060:12) — live matching information shown for reading,
never authorization, and never computed here. A dash is a real empty answer:
the worker has no reported model, or its model is in no group. A row whose
group lookup failed on the server reads "unavailable (CODE)" — the server's
error code only, never its text — and --json gives modelGroups null plus
modelGroupsError for that row; the rest of the roster is still shown. That is
the only server error this command tolerates. Needs a server that serves
modelGroups (hadron-server#3026).

HELD BY / SINCE show who holds the worker's name and since when (heldBy /
heldAt; in --json heldBy is {id, handle, urn, name} or null). The server
withholds the holder's name from readers it does not trust with it: a
withheld name arrives as null, exactly like an unset one, and the CLI renders
it the same way (the handle) without masking or filling anything itself.

Read it with lastActiveAt: nothing clears a reportsTo claim automatically,
and a state is a fact about now, not a judgement. The as-of value uses
control-free display text capped at 256 runes; --json preserves the original.`,
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
			// The ONE tolerated field error: advisory failures of the per-row
			// model groups, on a successfully decoded roster (cli#1485 S3).
			// Anything else, mixed in or not, fails the command as before.
			var advisories map[int]string
			if err != nil && resp != nil && resp.TeamRoster != nil {
				rows := resp.TeamRoster.Workers
				// Every core row must be present: a null row elsewhere in the
				// list is a damaged roster, and the renderer would otherwise
				// skip it and succeed with partial output (Joost, cli#1505 P2).
				whole := true
				for _, r := range rows {
					if r == nil {
						whole = false
						break
					}
				}
				if whole {
					if adv, ok := api.RosterGroupAdvisories(err, len(rows), func(i int) bool {
						return rows[i].ModelGroups == nil
					}); ok {
						advisories, err = adv, nil
					}
				}
			}
			if err != nil {
				return api.MapError(err)
			}
			dto := rosterDTO{
				AppID:   resp.TeamRoster.AppId,
				AsOf:    resp.TeamRoster.AsOf,
				Workers: []rosterEntryDTO{},
			}
			for i, w := range resp.TeamRoster.Workers {
				if w == nil {
					continue
				}
				// A null membership is only ever the advisory error's partner.
				// Null with no matching error is outside the server contract:
				// fail closed rather than read it as "no groups" (cli#1485 S3).
				var groupsErr *string
				if w.ModelGroups == nil {
					code, ok := advisories[i]
					if !ok {
						return exitcode.Newf(exitcode.Error, "team roster row %d: modelGroups is null without a matching field error — not a state the server documents, so it is not shown as \"no groups\"", i)
					}
					groupsErr = &code
				}
				// Guarded like every sibling (cycle 2, Xylon P2-1): a null
				// worker entry renders an empty party rather than panicking —
				// this was the diff's one unguarded party dereference.
				worker := taskPartyDTO{}
				if party := partyDTO(w.Worker); party != nil {
					worker = *party
				}
				var check *channelCheckDTO
				if w.CheckStatus != nil {
					check = channelCheckFromFields(w.CheckStatus.ChannelCheckFields)
				}
				dto.Workers = append(dto.Workers, rosterEntryDTO{
					CheckStatus:         check,
					Worker:              worker,
					State:               string(w.State),
					Role:                w.Role,
					Bound:               w.Bound,
					LiveSession:         w.LiveSession,
					Family:              w.Family,
					Model:               w.Model,
					ModelReportedAt:     w.ModelReportedAt,
					Tier:                w.Tier,
					ModelGroups:         w.ModelGroups,
					ModelGroupsError:    groupsErr,
					FreeForWork:         w.FreeForWork,
					FreeFamilies:        strListOrEmpty(w.FreeFamilies),
					LaunchableFamilies:  strListOrEmpty(w.LaunchableFamilies),
					UnavailableFamilies: strListOrEmpty(w.UnavailableFamilies),
					UnavailableReason:   reasonPtr(w.UnavailableReason),
					UnavailableUntil:    w.UnavailableUntil,
					LastActiveAt:        w.LastActiveAt,
					Terminal:            rosterTerminal(w.Terminal),
					LastAssignedAt:      w.LastAssignedAt,
					PendingTasks:        strListOrEmpty(w.PendingTasks),
					StartedTasks:        strListOrEmpty(w.StartedTasks),
					WaitingOn:           strListOrEmpty(w.WaitingOn),
					WaitingSince:        w.WaitingSince,
					UnreactedChildTask:  w.UnreactedChildTask,
					Alarms:              strListOrEmpty(w.Alarms),
					ReportsTo:           partyDTO(w.ReportsTo),
					ReportsToRetired:    w.ReportsToRetired,
					HeldBy:              rosterHolder(w.HeldBy),
					HeldAt:              w.HeldAt,
				})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				// Which roster, from where (#470) — before the rows, so an
				// empty staff still names its App.
				if _, err := fmt.Fprintf(w, "app: %s\n", appLabel()); err != nil {
					return err
				}
				t := output.NewTable(w, "WORKER", "STATE", "CHECK", "TERMINAL", "ROLE", "FAMILY", "MODEL", "TIER", "GROUPS", "LIVE", "TASKS", "ALARMS", "REPORTS TO", "HELD BY", "SINCE")
				now := time.Now()
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
					t.Row(output.SafeCell(name), output.SafeCell(e.State), output.SafeCell(channelCheckLabel(e.CheckStatus)), output.SafeCell(terminalLabel(e.Terminal, now)), output.SafeCell(strOr(e.Role)), output.SafeCell(family), output.SafeCell(strOr(e.Model)), output.SafeCell(strOr(e.Tier)), output.SafeCell(modelGroupsCell(e.ModelGroups, e.ModelGroupsError)), output.SafeCell(live), output.SafeCell(tasks), output.SafeCell(alarms), output.SafeCell(rosterReportsToLabel(e)), output.SafeCell(holderLabel(e.HeldBy)), output.SafeCell(strOr(e.HeldAt)))
				}
				if err := t.Flush(); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(w, capacityLine(dto.Workers)); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "as of %s — states are plan-derived (design:agent-tasks §7.4), not a spec\n", output.SafeCell(dto.AsOf))
				return err
			})
		},
	}
	return cmd
}

// capacityLine tallies the roster rows the SERVER marked freeForWork, grouped
// by the server's tier (hadron-cli#993). It counts rendered rows only: it does
// not derive availability (state is not consulted — a worker can be AVAILABLE
// and still not free) and it does not infer a tier. Anything that is not
// exactly high, mid or low — a null, or a tier a newer server adds — lands in
// "unknown" rather than being folded into a real bucket.
func capacityLine(workers []rosterEntryDTO) string {
	var high, mid, low, unknown int
	for _, e := range workers {
		if !e.FreeForWork {
			continue
		}
		switch {
		case e.Tier != nil && *e.Tier == "high":
			high++
		case e.Tier != nil && *e.Tier == "mid":
			mid++
		case e.Tier != nil && *e.Tier == "low":
			low++
		default:
			unknown++
		}
	}
	return fmt.Sprintf("free for work by tier: high %d · mid %d · low %d · unknown %d (model is self-reported and tier is advisory, never authorization)", high, mid, low, unknown)
}

// rosterReportsToLabel shows the server's reportsTo claim, marked when that
// coordinator is retired. No claim is a dash; nothing is inferred (cli#1085).
func rosterReportsToLabel(e rosterEntryDTO) string {
	if e.ReportsTo == nil {
		return "—"
	}
	label := partyLabel(*e.ReportsTo)
	if e.ReportsToRetired {
		label += " (retired)"
	}
	return label
}

// holderLabel names the holder: the name when the server sent one, else the
// handle. A withheld name and an unset one both arrive as null and so render
// identically (cli#1032) — no local masking, no local fill.
func holderLabel(h *rosterHolderDTO) string {
	if h == nil {
		return "—"
	}
	if h.Name != nil && *h.Name != "" {
		return output.SafeCell(*h.Name)
	}
	if h.Handle != nil && *h.Handle != "" {
		return output.SafeCell("@" + *h.Handle)
	}
	return "—"
}

func rosterHolder(h *gen.TeamRosterTeamRosterWorkersTeamRosterEntryHeldByWorkerHolder) *rosterHolderDTO {
	if h == nil {
		return nil
	}
	return &rosterHolderDTO{ID: h.Id, Handle: h.Handle, URN: h.Urn, Name: h.Name}
}

func rosterTerminal(t *gen.TeamRosterTeamRosterWorkersTeamRosterEntryTerminalWorkerTerminal) *workerTerminalDTO {
	if t == nil {
		return nil
	}
	return workerTerminalFromFields(t.WorkerTerminalFields)
}

// modelGroupsCell renders the server's group membership for one roster row:
// the names, a dash for a real [] (no model, or no group matches), and
// "unavailable (CODE)" for a row whose lookup failed — never "none" for an
// answer the server did not give (cli#1485 S3).
func modelGroupsCell(groups []string, errCode *string) string {
	if groups == nil {
		if errCode != nil {
			return "unavailable (" + *errCode + ")"
		}
		return "unavailable"
	}
	if len(groups) == 0 {
		return "—"
	}
	return strings.Join(groups, ",")
}
