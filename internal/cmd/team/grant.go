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

const workerGrantAccess = `Reads worker action grants through this worktree's worker-bound session. With
no binding (start one with 'hadron team session start') it refuses locally:
exit 2, "worker-bound grants require a worker session", and sends nothing.
Bound, the server answers only for a person-driven worker session (one a person
is driving), in its own App, not an unattended run, an App key or a plain person
login: any other caller gets null or an empty page, with no error. An empty
result is therefore not proof that you have no grants. --app cannot switch the
bound App.`

const workerGrantFieldsHelp = `Each grant has fifteen fields: id, action, values (the action's parameters, a
JSON object), holderKind and holderId (who holds it), startsAt and expiresAt
(the window it allows in), maxUses and useCount, reason, createdAt, activatedAt
(empty until a person signs it), revokedAt, allows and state. allows is yes only
while the grant can be used right now: signed, not revoked, inside its window
and not used up. A pending (unsigned) grant allows nothing. state is the
server's own word, printed as sent (PENDING, ACTIVE, SPENT, EXPIRED, REVOKED,
WITHDRAWN or HOLDER_GONE), never worked out here; it can disagree with allows (a
grant whose issuer can no longer issue stops allowing without changing state),
and both are shown as returned. A dash means the value is unset. --json keeps the
server values and nulls. list, get and request need a server released on or
after 2026-11-17 (the state field; every other field request selects shipped by
2026-11-10, and list and get select only some of those); an older server refuses
the read. list and get only read; to ask for a grant see
'hadron team grant request'. Organization action grants are a different thing:
see 'hadron grant' ('hadron grant list'); 'hadron auth grants' reads the grants
held by you personally across Apps.`

var workerGrantNames = []string{"id", "action", "values", "holderKind", "holderId", "startsAt", "expiresAt", "maxUses", "useCount", "reason", "createdAt", "activatedAt", "revokedAt", "allows", "state"}

func newCmdTeamGrant(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "grant <command>", Short: "Worker action grants and named App grants", Long: `Grants let a worker (or its role agent) do something a platform rule otherwise
withholds. Three groups of commands live here:

  named App grants      add, rm: give or take away one named permission
                        (TICKER_SUBSCRIBE_WORKERS, WORKERS_STAFF, APP_MEMBERS)
                        for a worker in an App. Done by a person who manages
                        the App.
  worker action grants  request, request-signature, request-selection-signature,
                        signing-request, list, get: a worker asks for a grant of
                        one action, a PERSON signs it (one grant, or a selection
                        at once), and the worker reads the result.
  manager verbs         create, app-list, revoke, withdraw: a person who manages
                        an App creates, lists and revokes its grants, and the
                        requester withdraws a pending one.
  the action catalog    actions, action: the actions an App registers for
                        grants and the parameters each one needs.

To get a worker grant, in order: (1) 'hadron team grant actions' finds the
action and its PARAMETERS; (2) 'hadron team grant request --action <name>
--values <json> --reason <text>' creates a pending grant, which allows nothing;
(3) 'hadron team grant request-signature <grant-id>' prints a ceremony URL: give
it to a person, who signs on that page (nothing here signs); (4) 'hadron team
grant signing-request get <request-id>' shows whether it was signed, and
'hadron team grant get <grant-id>' shows the grant, where allows says whether it
can be used now. To have several pending grants signed at once, step (3) is
'hadron team grant request-selection-signature <grant-id>...'. 'list' and 'get' read the worker's own grants, so they need a
worker session that a person is driving (a worktree bound with 'hadron team
session start'). Organization action grants are managed with 'hadron grant'
('hadron grant list'), not here; 'hadron auth grants' reads the grants you hold
personally across Apps.`}
	var limit, offset int
	list := &cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "Read one page of worker grants", Long: "List this worker's own grants: what it holds, and its requests still waiting\nto be signed.\n\n" + workerGrantAccess + `

The list includes grants naming this worker directly as holder (any timestamps),
plus this worker's own requests that are neither activated nor revoked, and the
grants held by this worker's role agent in its own App while it is installed and
licensed (every state). Grants held by another worker's, a person's or an App's
are not listed.

` + workerGrantFieldsHelp + `

Text mode prints a table with one column per field (ID, ACTION, VALUES, HOLDER
KIND, HOLDER ID, STARTS, EXPIRES, MAX USES, USE COUNT, REASON, CREATED,
ACTIVATED, REVOKED, ALLOWS, STATE) and a "total/offset/limit" line. --limit 0 prints
only the total count; --offset selects the page start. The default page limit is
20. --json returns {items:[],total:N}, or {total:N} for count-only. No page is
fetched automatically. If the page is empty and you expected rows, check that
you are not asking for a grant held by someone else and that the bound session
is a person-driven worker session in its own App; each gives the same empty
page (an unbound worktree is refused instead, see above).`, Example: "  hadron team grant list --limit 20\n  hadron team grant list --limit 0 --json", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
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
			table := output.NewTable(w, "ID", "ACTION", "VALUES", "HOLDER KIND", "HOLDER ID", "STARTS", "EXPIRES", "MAX USES", "USE COUNT", "REASON", "CREATED", "ACTIVATED", "REVOKED", "ALLOWS", "STATE")
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
	get := &cobra.Command{Use: "get <id>", Short: "Read one worker grant by ID", Long: `Read one worker grant by its id: the ID column of 'hadron team grant list', or
the id printed when you ran 'hadron team grant request'. Text mode prints one
"field: value" line per field; --json prints the same fifteen fields as a list
item. A missing or hidden row returns the same not-found line and exit 4: get
sees the grants naming this worker as holder, plus this worker's own requests
that are neither activated nor revoked, and the grants held by this worker's
role agent in its own App; another worker's, a person's or an App's grants are
not visible here.

` + workerGrantAccess + `

` + workerGrantFieldsHelp, Example: "  hadron team grant get <id> --json", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, client, err := workerGrantClient(cmd.Context(), f)
		if err != nil {
			return err
		}
		resp, err := gen.TeamGrant(ctx, client, args[0])
		if err != nil {
			return api.MapError(err)
		}
		if resp == nil || resp.Grant == nil {
			return exitcode.Newf(exitcode.NotFound, "grant not found, or not visible to this worker-bound caller (the id given is not shown)")
		}
		dto := workerGrantFrom(resp.Grant)
		return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
			names := workerGrantNames
			for i, v := range dto.cells() {
				if _, e := fmt.Fprintf(w, "%s: %s\n", names[i], output.SafeCell(v)); e != nil {
					return e
				}
			}
			return nil
		})
	}}
	group.AddCommand(newCmdAppGrant(f, false), newCmdAppGrant(f, true), list, get, newCmdTeamGrantRequest(f), newCmdTeamGrantCreate(f), newCmdTeamGrantAppList(f), newCmdTeamGrantRevoke(f), newCmdTeamGrantWithdraw(f), newCmdTeamGrantRequestSignature(f), newCmdTeamGrantRequestSelectionSignature(f), newCmdTeamGrantSigningRequest(f), newCmdTeamGrantActions(f), newCmdTeamGrantAction(f))
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
	State       string           `json:"state"`
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
	GetState() gen.GrantState
}

func workerGrantFrom(g workerGrantFields) workerGrantDTO {
	return workerGrantDTO{g.GetId(), g.GetAction(), g.GetValues(), string(g.GetHolderKind()), g.GetHolderId(), g.GetStartsAt(), g.GetExpiresAt(), g.GetMaxUses(), g.GetUseCount(), g.GetReason(), g.GetCreatedAt(), g.GetActivatedAt(), g.GetRevokedAt(), g.GetAllows(), string(g.GetState())}
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
	return []string{g.ID, g.Action, value, g.HolderKind, g.HolderID, dashIfNil(g.StartsAt), dashIfNil(g.ExpiresAt), max, strconv.Itoa(g.UseCount), g.Reason, g.CreatedAt, dashIfNil(g.ActivatedAt), dashIfNil(g.RevokedAt), allows, g.State}
}
func dashIfNil(v *string) string {
	if v == nil {
		return "—"
	}
	return *v
}
