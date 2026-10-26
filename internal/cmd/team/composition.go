package team

// `team composition` — the server's direct-report lanes (#2061 over server
// #2475), rendered as returned. A thin read: no second roster join, no role
// exclusions, no balance or capacity judgment. Lane membership, order, null
// buckets and the retired-coordinator flag are the server's.

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

type compositionDTO struct {
	AppID string               `json:"appId"`
	AsOf  string               `json:"asOf"`
	Lanes []compositionLaneDTO `json:"lanes"`
}

type compositionLaneDTO struct {
	Coordinator        *taskPartyDTO               `json:"coordinator"`
	CoordinatorRetired bool                        `json:"coordinatorRetired"`
	CountsByRole       []compositionRoleCountDTO   `json:"countsByRole"`
	CountsByRoleFamily []compositionFamilyCountDTO `json:"countsByRoleFamily"`
	Workers            []compositionWorkerDTO      `json:"workers"`
}

type compositionRoleCountDTO struct {
	Role  *string `json:"role"`
	Count int     `json:"count"`
}

type compositionFamilyCountDTO struct {
	Role   *string `json:"role"`
	Family *string `json:"family"`
	Count  int     `json:"count"`
}

type compositionWorkerDTO struct {
	Worker      taskPartyDTO `json:"worker"`
	Role        *string      `json:"role"`
	Family      *string      `json:"family"`
	Bound       bool         `json:"bound"`
	LiveSession bool         `json:"liveSession"`
	State       string       `json:"state"`
}

func newCmdTeamComposition(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "composition",
		Short: "The team's direct-report lanes and their headcounts",
		Long: `The team App's composition: its active workers grouped by the coordinator
they directly report to — the server's teamComposition query, rendered as
returned. The CLI joins and judges nothing.

Each lane names its coordinator (a retired coordinator stays named and is
marked), lists the lane's non-retired workers in the server's order, and gives
plain headcounts by role and by role and family. A worker with no role or no
family is counted in its own bucket (shown as a dash). The lane with no
coordinator holds workers who report to nobody. A lane is a direct-report
group, not a hierarchy rollup: a coordinator's own row sits in the lane of the
worker it reports to.

This is a read, not a capacity policy: it does not say whether the team is
balanced. A refused read, or a server that does not serve teamComposition, is a
failure — never an empty team.`,
		Args: cobra.NoArgs,
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
			resp, err := gen.TeamComposition(ctx, client, scope.Ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.TeamComposition == nil {
				return exitcode.Newf(exitcode.Unavailable, "the server returned no composition; this is not an empty team — try again")
			}
			dto := compositionFromGen(resp.TeamComposition)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return renderComposition(w, appLabel(), dto)
			})
		},
	}
}

func compositionFromGen(c *gen.TeamCompositionTeamComposition) compositionDTO {
	dto := compositionDTO{AppID: c.AppId, AsOf: c.AsOf, Lanes: []compositionLaneDTO{}}
	for _, l := range c.Lanes {
		if l == nil {
			continue
		}
		lane := compositionLaneDTO{
			Coordinator:        partyDTO(l.Coordinator),
			CoordinatorRetired: l.CoordinatorRetired,
			CountsByRole:       []compositionRoleCountDTO{},
			CountsByRoleFamily: []compositionFamilyCountDTO{},
			Workers:            []compositionWorkerDTO{},
		}
		for _, n := range l.CountsByRole {
			if n != nil {
				lane.CountsByRole = append(lane.CountsByRole, compositionRoleCountDTO{Role: n.Role, Count: n.Count})
			}
		}
		for _, n := range l.CountsByRoleFamily {
			if n != nil {
				lane.CountsByRoleFamily = append(lane.CountsByRoleFamily, compositionFamilyCountDTO{Role: n.Role, Family: n.Family, Count: n.Count})
			}
		}
		for _, w := range l.Workers {
			if w == nil {
				continue
			}
			worker := taskPartyDTO{}
			if p := partyDTO(w.Worker); p != nil {
				worker = *p
			}
			lane.Workers = append(lane.Workers, compositionWorkerDTO{
				Worker: worker, Role: w.Role, Family: w.Family,
				Bound: w.Bound, LiveSession: w.LiveSession, State: string(w.State),
			})
		}
		dto.Lanes = append(dto.Lanes, lane)
	}
	return dto
}

func partyLabel(p taskPartyDTO) string {
	if p.Name != nil && *p.Name != "" {
		return cellSafe(*p.Name)
	}
	return cellSafe(p.ID)
}

// partyURNLabel is the server's canonical URN, or a dash when it has none
// (a name-less or URN-less party is shown as given, never guessed).
func partyURNLabel(p taskPartyDTO) string {
	return cellSafe(strOr(p.Urn))
}

func renderComposition(w io.Writer, app string, dto compositionDTO) error {
	if _, err := fmt.Fprintf(w, "app: %s\n", app); err != nil {
		return err
	}
	for _, l := range dto.Lanes {
		head := "unassigned (reports to nobody)"
		if l.Coordinator != nil {
			head = "reports to " + partyLabel(*l.Coordinator)
			if l.CoordinatorRetired {
				head += " (retired)"
			}
			head += " [" + partyURNLabel(*l.Coordinator) + "]"
		}
		if _, err := fmt.Fprintf(w, "\n%s — %d worker(s)\n", head, len(l.Workers)); err != nil {
			return err
		}
		t := output.NewTable(w, "WORKER", "URN", "STATE", "ROLE", "FAMILY", "LIVE")
		for _, m := range l.Workers {
			live := "quiet"
			if m.LiveSession {
				live = "live"
			} else if !m.Bound {
				live = "unbound"
			}
			t.Row(partyLabel(m.Worker), partyURNLabel(m.Worker), m.State, cellSafe(strOr(m.Role)), cellSafe(strOr(m.Family)), live)
		}
		if err := t.Flush(); err != nil {
			return err
		}
		for _, n := range l.CountsByRole {
			if _, err := fmt.Fprintf(w, "  role %s: %d\n", cellSafe(strOr(n.Role)), n.Count); err != nil {
				return err
			}
		}
		for _, n := range l.CountsByRoleFamily {
			if _, err := fmt.Fprintf(w, "  role %s · family %s: %d\n", cellSafe(strOr(n.Role)), cellSafe(strOr(n.Family)), n.Count); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintf(w, "\nas of %s — lanes are direct-report groups as the server returned them; no balance is judged\n", dto.AsOf)
	return err
}
