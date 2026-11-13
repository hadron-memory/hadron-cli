package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

const workerGrantHelp = `Read grants through this worktree's worker-bound, person-driven session in its own App.
The server returns null or an empty page for other caller kinds, without an error.
An empty result is not proof that you have no grants. Start a binding with
hadron team session start before reading; --app cannot switch the bound App.

The list includes grants naming this worker directly as holder (any timestamps),
plus this worker's own requests that are neither activated nor revoked.
Grants held by a role agent, another worker, a person or an App are not listed.

Prints timestamps and allows (a yes/no fact about now), never a derived state label.
--json preserves the server values and nulls. list and get only read. request,
request-signature and signing-request ask for a grant and for its signing; a
person signs, and nothing here does. The separate hadron grant group reads and
manages organization action grants.`

func newCmdTeamGrant(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "grant <command>", Short: "Worker action grants and named App grants", Long: "add/rm manage named App grants. list/get/request manage worker action grants.\n\n" + workerGrantHelp}
	var limit, offset int
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "Read one page of worker grants", Long: workerGrantHelp + `

--limit 0 prints only the total count; --offset selects the page start.
The default page limit is 20. --json returns {items:[],total:N}, or {total:N}
for count-only. No page is fetched automatically.`, Example: "  hadron team grant list --limit 20\n  hadron team grant list --limit 0 --json", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if limit < 0 || limit > 200 || offset < 0 || offset > 2147483647 {
			return exitcode.Newf(exitcode.Usage, "--limit takes 0..200 and --offset takes 0..2147483647")
		}
		ctx, client, err := workerGrantClient(cmd.Context(), f)
		if err != nil {
			return err
		}
		resp, err := gen.TeamGrants(ctx, client, limit, offset)
		if err != nil {
			return api.MapError(err)
		}
		if resp == nil || resp.Grants == nil {
			return exitcode.Newf(exitcode.Error, "server returned no grants page")
		}
		if limit == 0 {
			return output.Write(f.IOStreams, f.JSON, struct {
				Total int `json:"total"`
			}{resp.Grants.Total}, func(w io.Writer) error { _, e := fmt.Fprintf(w, "total: %d\n", resp.Grants.Total); return e })
		}
		dto := workerGrantPage{Items: []workerGrantDTO{}, Total: resp.Grants.Total}
		for _, g := range resp.Grants.Items {
			if g != nil {
				dto.Items = append(dto.Items, workerGrantFrom(g))
			}
		}
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			if len(dto.Items) == 0 {
				_, e := fmt.Fprintln(w, "No rows returned on this worker-bound page; other caller kinds also receive an empty result.")
				return e
			}
			table := output.NewTable(w, "ID", "ACTION", "VALUES", "HOLDER KIND", "HOLDER ID", "STARTS", "EXPIRES", "MAX USES", "USE COUNT", "REASON", "CREATED", "ACTIVATED", "REVOKED", "ALLOWS")
			for _, g := range dto.Items {
				cells := g.cells()
				for i := range cells {
					cells[i] = output.SafeCell(cells[i])
				}
				table.Row(cells...)
			}
			if e := table.Flush(); e != nil {
				return e
			}
			_, e := fmt.Fprintf(w, "total: %d; offset: %d; limit: %d\n", dto.Total, offset, limit)
			return e
		})
	}}
	list.Flags().IntVar(&limit, "limit", 20, "page size, 0..200; 0 prints only the total count")
	list.Flags().IntVar(&offset, "offset", 0, "page offset, 0..2147483647")
	get := &cobra.Command{Use: "get <id>", Short: "Read one worker grant by ID", Long: workerGrantHelp + `

A missing or hidden row returns the same not-found line and exit 4.
--json returns the same fourteen grant fields as a list item.`, Example: "  hadron team grant get <id> --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, client, err := workerGrantClient(cmd.Context(), f)
		if err != nil {
			return err
		}
		resp, err := gen.TeamGrant(ctx, client, args[0])
		if err != nil {
			return api.MapError(err)
		}
		if resp == nil || resp.Grant == nil {
			return exitcode.Newf(exitcode.NotFound, "grant %q not found (or not visible to this worker-bound caller)", args[0])
		}
		dto := workerGrantFrom(resp.Grant)
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			names := []string{"id", "action", "values", "holderKind", "holderId", "startsAt", "expiresAt", "maxUses", "useCount", "reason", "createdAt", "activatedAt", "revokedAt", "allows"}
			for i, v := range dto.cells() {
				if _, e := fmt.Fprintf(w, "%s: %s\n", names[i], output.SafeCell(v)); e != nil {
					return e
				}
			}
			return nil
		})
	}}
	group.AddCommand(newCmdAppGrant(f, false), newCmdAppGrant(f, true), list, get, newCmdTeamGrantRequest(f), newCmdTeamGrantRequestSignature(f), newCmdTeamGrantSigningRequest(f))
	return group
}

// workerGrantClient refuses unbound/cross-server reads rather than rendering a
// server-empty result as a claim about some other identity's grants.
func workerGrantClient(ctx context.Context, f *cmdutil.Factory) (context.Context, graphql.Client, error) {
	b, _, err := readBinding(ctx)
	if err != nil && !errors.Is(err, errNoWorktree) {
		return ctx, nil, err
	}
	if b == nil || b.SessionID == "" || b.WorkerID == "" || b.AppID == "" {
		return ctx, nil, exitcode.Newf(exitcode.Usage, "worker-bound grants require a worker session; start one with hadron team session start")
	}
	if !bindingServerMatches(f, b) {
		return ctx, nil, exitcode.Newf(exitcode.Usage, "worker-bound grants require the binding's server; omit the other --server")
	}
	if f.AppFlag != "" {
		same, e := bindingsAppIdentity(ctx, f, f.AppFlag, b.AppID)
		if e != nil {
			return ctx, nil, e
		}
		if !same {
			return ctx, nil, exitcode.Newf(exitcode.Usage, "worker-bound grants read only the binding's App; omit the other --app")
		}
	}
	client, err := f.GraphQLClient()
	return api.WithSession(ctx, b.SessionID), client, err
}

type workerGrantDTO struct {
	ID          string           `json:"id"`
	Action      string           `json:"action"`
	Values      *json.RawMessage `json:"values"`
	HolderKind  string           `json:"holderKind"`
	HolderID    string           `json:"holderId"`
	StartsAt    *string          `json:"startsAt"`
	ExpiresAt   *string          `json:"expiresAt"`
	MaxUses     *int             `json:"maxUses"`
	UseCount    int              `json:"useCount"`
	Reason      string           `json:"reason"`
	CreatedAt   string           `json:"createdAt"`
	ActivatedAt *string          `json:"activatedAt"`
	RevokedAt   *string          `json:"revokedAt"`
	Allows      bool             `json:"allows"`
}
type workerGrantPage struct {
	Items []workerGrantDTO `json:"items"`
	Total int              `json:"total"`
}
type workerGrantFields interface {
	GetId() string
	GetAction() string
	GetValues() *json.RawMessage
	GetHolderKind() gen.GrantHolderKind
	GetHolderId() string
	GetStartsAt() *string
	GetExpiresAt() *string
	GetMaxUses() *int
	GetUseCount() int
	GetReason() string
	GetCreatedAt() string
	GetActivatedAt() *string
	GetRevokedAt() *string
	GetAllows() bool
}

func workerGrantFrom(g workerGrantFields) workerGrantDTO {
	return workerGrantDTO{g.GetId(), g.GetAction(), g.GetValues(), string(g.GetHolderKind()), g.GetHolderId(), g.GetStartsAt(), g.GetExpiresAt(), g.GetMaxUses(), g.GetUseCount(), g.GetReason(), g.GetCreatedAt(), g.GetActivatedAt(), g.GetRevokedAt(), g.GetAllows()}
}
func (g workerGrantDTO) cells() []string {
	value := "null"
	if g.Values != nil {
		value = string(*g.Values)
	}
	max := "—"
	if g.MaxUses != nil {
		max = strconv.Itoa(*g.MaxUses)
	}
	allows := "no"
	if g.Allows {
		allows = "yes"
	}
	return []string{g.ID, g.Action, value, g.HolderKind, g.HolderID, grantTime(g.StartsAt), grantTime(g.ExpiresAt), max, strconv.Itoa(g.UseCount), g.Reason, g.CreatedAt, grantTime(g.ActivatedAt), grantTime(g.RevokedAt), allows}
}
func grantTime(v *string) string {
	if v == nil {
		return "—"
	}
	return *v
}
