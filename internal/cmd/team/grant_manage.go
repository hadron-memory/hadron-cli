package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// cli#1846: the person-manager grant verbs under `hadron team grant`: create,
// app-list, revoke and withdraw (hadron-server#3711 / #3876, cor:acl:140:05).
// The server enforces who may call each (cor:acl:140:05, :03): no verb here
// refuses a caller on role, membership or binding, and none asks for a
// signature or activates a grant (cor:acl:140:07; a person signs on the
// ceremony page). `state` is printed exactly as the server sent it, beside
// `allows`: the two do not always agree, and neither is derived or corrected.

const managerGrantFieldsHelp = `Each grant is printed with the fields the App's manager reads: id, appId, action,
values (the action's parameters, a JSON object), holderKind and holderId (who
holds it), issuerUserId (who signed it), requestedByUserId, requestedByWorkerId
and requestedBySessionId (who asked), startsAt and expiresAt (the window it
allows in), maxUses and useCount, reason, createdAt, activatedAt (empty until a
person signs it), revokedAt, revokedByUserId (who revoked or withdrew it),
holderDepartedAt (when its holder went), allows and state. state is the server's
own word, printed as sent: PENDING, ACTIVE, SPENT, EXPIRED, REVOKED, WITHDRAWN
or HOLDER_GONE. It is never worked out here from the timestamps. allows says
whether the grant can be used right now, and it does not always agree with
state: a grant whose issuer can no longer issue stops allowing without changing
state, so an ACTIVE row can show allows: false. Both are shown as returned. A
dash means the value is unset. --json keeps the server values and nulls.`

const managerGrantAccessHelp = `Every call here is made as you, the signed-in person, never through a worker
session. The server decides who may do what and this command asks it without a
local check: create and app-list are for a person who manages the App; revoke
is for the grant's issuer (while still a member of the App's organization) or
whoever manages the App; withdraw is for the person or worker session that
requested the grant, and only while it is pending. A refusal comes back with its
code: GRANT_NOT_MANAGER (not allowed, unknown and not visible are one answer)
and GRANT_NOT_REQUESTER exit 8, GRANT_NOT_FOUND exits 4, GRANT_NOT_PENDING exits
5, GRANT_ACTION_UNKNOWN, GRANT_VALUES_INVALID and GRANT_TERMS_INVALID exit 2.
Nothing here signs or activates a grant: ask for a signature with 'hadron team
grant request-signature <grant-id>'.`

var managerGrantNames = []string{"id", "appId", "action", "values", "holderKind", "holderId", "issuerUserId", "requestedByUserId", "requestedByWorkerId", "requestedBySessionId",
	"startsAt", "expiresAt", "maxUses", "useCount", "reason", "createdAt", "activatedAt", "revokedAt", "revokedByUserId", "holderDepartedAt", "allows", "state"}

// managerGrantDTO is the stable --json shape of a grant as the App's manager
// reads it. It is a DTO, never the generated struct, so --json survives a
// regeneration.
type managerGrantDTO struct {
	ID                   string           `json:"id"`
	AppID                string           `json:"appId"`
	Action               string           `json:"action"`
	Values               *json.RawMessage `json:"values"`
	HolderKind           string           `json:"holderKind"`
	HolderID             string           `json:"holderId"`
	IssuerUserID         *string          `json:"issuerUserId"`
	RequestedByUserID    *string          `json:"requestedByUserId"`
	RequestedByWorkerID  *string          `json:"requestedByWorkerId"`
	RequestedBySessionID *string          `json:"requestedBySessionId"`
	StartsAt             *string          `json:"startsAt"`
	ExpiresAt            *string          `json:"expiresAt"`
	MaxUses              *int             `json:"maxUses"`
	UseCount             int              `json:"useCount"`
	Reason               string           `json:"reason"`
	CreatedAt            string           `json:"createdAt"`
	ActivatedAt          *string          `json:"activatedAt"`
	RevokedAt            *string          `json:"revokedAt"`
	RevokedByUserID      *string          `json:"revokedByUserId"`
	HolderDepartedAt     *string          `json:"holderDepartedAt"`
	Allows               bool             `json:"allows"`
	State                string           `json:"state"`
}

type managerGrantPage struct {
	Items []managerGrantDTO `json:"items"`
	Total int               `json:"total"`
}

func managerGrantFrom(g *gen.GrantManagerFields) managerGrantDTO {
	return managerGrantDTO{g.Id, g.AppId, g.Action, g.Values, string(g.HolderKind), g.HolderId, g.IssuerUserId, g.RequestedByUserId, g.RequestedByWorkerId,
		g.RequestedBySessionId, g.StartsAt, g.ExpiresAt, g.MaxUses, g.UseCount, g.Reason, g.CreatedAt, g.ActivatedAt, g.RevokedAt, g.RevokedByUserId,
		g.HolderDepartedAt, g.Allows, string(g.State)}
}

// cells are the printable values in managerGrantNames order.
func (g managerGrantDTO) cells() []string {
	value := "null"
	if g.Values != nil {
		value = string(*g.Values)
	}
	max := "—"
	if g.MaxUses != nil {
		max = strconv.Itoa(*g.MaxUses)
	}
	return []string{g.ID, g.AppID, g.Action, value, g.HolderKind, g.HolderID, dashIfNil(g.IssuerUserID), dashIfNil(g.RequestedByUserID), dashIfNil(g.RequestedByWorkerID),
		dashIfNil(g.RequestedBySessionID), dashIfNil(g.StartsAt), dashIfNil(g.ExpiresAt), max, strconv.Itoa(g.UseCount), g.Reason, g.CreatedAt, dashIfNil(g.ActivatedAt),
		dashIfNil(g.RevokedAt), dashIfNil(g.RevokedByUserID), dashIfNil(g.HolderDepartedAt), strconv.FormatBool(g.Allows), g.State}
}

// writeManagerGrant prints one grant as "field: value" lines, exactly the
// fields the server returned. Nothing is worked out from them: a grant that was
// already ended comes back with its first end and is printed as it is.
func writeManagerGrant(f *cmdutil.Factory, dto managerGrantDTO) error {
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		for i, v := range dto.cells() {
			if _, e := fmt.Fprintf(w, "%s: %s\n", managerGrantNames[i], output.SafeCell(v)); e != nil {
				return e
			}
		}
		return nil
	})
}

func newCmdTeamGrantCreate(f *cmdutil.Factory) *cobra.Command {
	var app, action, values, reason, holder, holderID, startsAt, expiresAt string
	var maxUses int
	cmd := &cobra.Command{
		Use:   "create --app <app> --action <name> --values <json|@file> --reason <text> --holder worker|agent --holder-id <id>",
		Short: "Create a pending grant in an App you manage",
		Long: `Create a grant in an App, as the person who manages it, for a worker of that
App (--holder worker) or an agent installed in it (--holder agent). The grant is
born pending: it allows nothing until a person signs it on the ceremony page,
and this command never signs or asks for a signature. A worker asks for its own
grant with 'hadron team grant request'; this is the person's verb.

--app and --holder-id are required: a person has no worker binding to take
either from. --action is an action name from 'hadron team grant actions'.
--values is the JSON object of that action's parameters (or @file); whether it
fits is the server's to judge. --reason is shown to the person who signs.
--starts-at and --expires-at take a timestamp such as 2026-12-01T09:00:00Z (the
server parses it) and --max-uses a count; leave them out to let the server apply
its defaults. A flag given empty is refused (exit 2) before anything is sent.

Prints the pending grant (see below). Next step: 'hadron team grant
request-signature <grant-id>', then give the URL to the person who signs.

` + managerGrantFieldsHelp + `

` + managerGrantAccessHelp + `

Needs a server with grant signing switched on. Without it the command says it
does not offer grant requests and grant signing, and exits 2.`,
		Example: "  hadron team grant create --app acme.com:eng --action node.read --values '{\"node\":[\"<node-id>\"]}' --reason 'read the plan' --holder worker --holder-id <worker-id>",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if strings.TrimSpace(app) == "" {
				return exitcode.Newf(exitcode.Usage, "--app is required: a person has no worker binding to take the App from")
			}
			if strings.TrimSpace(holderID) == "" {
				return exitcode.Newf(exitcode.Usage, "--holder-id is required: a person has no worker binding to take the holder from")
			}
			if strings.TrimSpace(action) == "" {
				return exitcode.Newf(exitcode.Usage, "--action is required")
			}
			if strings.TrimSpace(reason) == "" {
				return exitcode.Newf(exitcode.Usage, "--reason is required")
			}
			if !fl.Changed("values") {
				return exitcode.Newf(exitcode.Usage, "--values is required")
			}
			raw, err := cmdutil.ParseJSONValue(values, cmdutil.JSONValueSpelling{Name: "--values", FileExample: "--values @values.json", Clear: "a different --action"})
			if err != nil {
				return err
			}
			for _, fv := range []struct{ name, val string }{{"starts-at", startsAt}, {"expires-at", expiresAt}} {
				if fl.Changed(fv.name) && strings.TrimSpace(fv.val) == "" {
					return exitcode.Newf(exitcode.Usage, "--%s needs a value; omit the flag to leave it unset", fv.name)
				}
			}
			if holder != "worker" && holder != "agent" {
				return exitcode.Newf(exitcode.Usage, "--holder takes worker or agent")
			}
			appRef := strings.TrimSpace(app)
			in := gen.CreateGrantInput{Action: strings.TrimSpace(action), Values: raw, HolderKind: gen.GrantHolderKindWorker, HolderRef: strings.TrimSpace(holderID), Reason: reason, AppRef: &appRef}
			if holder == "agent" {
				in.HolderKind = gen.GrantHolderKindAgent
			}
			if fl.Changed("starts-at") {
				in.StartsAt = &startsAt
			}
			if fl.Changed("expires-at") {
				in.ExpiresAt = &expiresAt
			}
			if fl.Changed("max-uses") {
				in.MaxUses = &maxUses
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamCreateGrant(cmd.Context(), client, &in)
			if err != nil {
				return cmdutil.MapGrantSigningError(err)
			}
			if resp == nil || resp.CreateGrant == nil {
				return exitcode.Newf(exitcode.Error, "server returned no grant result")
			}
			dto := managerGrantFrom(&resp.CreateGrant.GrantManagerFields)
			if !f.JSON {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "created; it allows nothing until a person signs it; next: hadron team grant request-signature %s\n", output.SafeCell(dto.ID))
			}
			return writeManagerGrant(f, dto)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&app, "app", "", "the App to create the grant in, as hrn:app:<root>:<slug>, <root>:<slug> or an App id (required)")
	fl.StringVar(&action, "action", "", "the action to grant (required)")
	fl.StringVar(&values, "values", "", "the action's parameters as a JSON object, or @file (required)")
	fl.StringVar(&reason, "reason", "", "why the grant is made, shown to the signer (required)")
	fl.StringVar(&holder, "holder", "worker", "who holds the grant: worker, or agent (an agent installed in the App)")
	fl.StringVar(&holderID, "holder-id", "", "the holder's id or URN (required)")
	fl.StringVar(&startsAt, "starts-at", "", "when the grant starts to allow, a timestamp the server parses")
	fl.StringVar(&expiresAt, "expires-at", "", "when the grant stops allowing, a timestamp the server parses")
	fl.IntVar(&maxUses, "max-uses", 0, "how many uses the grant allows")
	return cmd
}

func newCmdTeamGrantAppList(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	cmd := &cobra.Command{
		Use:   "app-list <app>",
		Short: "List an App's grants, in every state",
		Long: `List one page of an App's grants, in every state, as the person who manages it:
pending, active, spent, expired, revoked, withdrawn and holder gone. ('hadron team
grant list' is different: it reads the bound worker's own grants.) <app> is an
App ref (hrn:app:<root>:<slug>, <root>:<slug> or an id).

` + managerGrantFieldsHelp + `

Text mode prints a table with one column per field, in the server's order (newest
first), and a "total/offset/limit" line. --limit 0 prints only the total count;
--offset selects the page start. The default page limit is 20; above 200 the
server judges it. No page is fetched automatically. --json returns
{items:[],total:N}, or {total:N} for count-only.

An empty page is not proof that the App has no grants. The server answers an
empty page, never an error, to a caller who does not manage the App, an
outsider, a worker session, an App key, a run, an impersonated session, and for
an App that does not exist. Text mode says so; --json stays the bare page.

` + managerGrantAccessHelp,
		Example: "  hadron team grant app-list acme.com:eng --limit 50\n  hadron team grant app-list acme.com:eng --limit 0 --json",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return exitcode.Newf(exitcode.Usage, "the App is required")
			}
			if limit < 0 || offset < 0 || offset > 2147483647 {
				return exitcode.Newf(exitcode.Usage, "--limit takes 0 or more and --offset takes 0..2147483647")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.TeamAppGrants(cmd.Context(), client, ref, limit, offset)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.AppGrants == nil {
				return exitcode.Newf(exitcode.Error, "server returned no grants page")
			}
			if limit == 0 {
				return output.Write(f.IOStreams, f.JSON, struct {
					Total int `json:"total"`
				}{resp.AppGrants.Total}, func(w io.Writer) error { _, e := fmt.Fprintf(w, "total: %d\n", resp.AppGrants.Total); return e })
			}
			dto := managerGrantPage{Items: []managerGrantDTO{}, Total: resp.AppGrants.Total}
			for _, g := range resp.AppGrants.Items {
				if g != nil {
					dto.Items = append(dto.Items, managerGrantFrom(&g.GrantManagerFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 && dto.Total > 0 {
					// The server counted grants but this page holds none, which is not the
					// empty answer of an unauthorised caller. Below the total the page
					// should have held rows, so it is not called past the end (cli#3193).
					if offset < dto.Total {
						_, e := fmt.Fprintf(w, "No rows on this page: the server counted %d grants but returned none on this page (offset %d).\n", dto.Total, offset)
						return e
					}
					_, e := fmt.Fprintf(w, "No rows on this page: offset %d is past the end of %d grants.\n", offset, dto.Total)
					return e
				}
				if len(dto.Items) == 0 {
					_, e := fmt.Fprintln(w, "No rows on this page. You may not be authorised to list this App's grants, or the App does not exist: the server answers the same empty page either way.")
					return e
				}
				heads := []string{"ID", "APP", "ACTION", "VALUES", "HOLDER KIND", "HOLDER ID", "ISSUER", "REQUESTED BY USER", "REQUESTED BY WORKER", "REQUESTED BY SESSION",
					"STARTS", "EXPIRES", "MAX USES", "USE COUNT", "REASON", "CREATED", "ACTIVATED", "REVOKED", "REVOKED BY", "HOLDER DEPARTED", "ALLOWS", "STATE"}
				table := output.NewTable(w, heads...)
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
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "page size; 0 prints only the total count")
	cmd.Flags().IntVar(&offset, "offset", 0, "page offset, 0..2147483647")
	return cmd
}

// instructionApp names the App an instructed revoke acts in: --app, else the
// worktree binding's App. A revoke names a grant, not an App, so this is the
// App the worker's session is checked against (the shared helper's input).
func instructionApp(ctx context.Context, f *cmdutil.Factory) (string, error) {
	if f.AppFlag != "" {
		return f.AppFlag, nil
	}
	b, _, err := readBinding(ctx)
	if err != nil && !errors.Is(err, errNoWorktree) {
		return "", err
	}
	if b == nil {
		return "", nil
	}
	return b.AppID, nil
}

func newCmdTeamGrantRevoke(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <grant-id> [--instruction <kind>:<ref>]",
		Short: "Revoke a grant now",
		Long: `Revoke a grant now, in any state but ended: it stops allowing at once. The first
end stands: a grant that was already revoked, withdrawn or whose holder went
comes back exactly as it is, with its first end and no second stamp, and that is
a success; this command prints the returned grant and does not say it revoked
anything. It reads revokedAt and state from the answer, never from a comparison
with what was there before. <grant-id> is the id printed by 'hadron team grant
create' or 'hadron team grant app-list'.

--instruction <kind>:<ref> is for an AI worker acting on its holder's say-so
(the holder is the person responsible for the worker). kind is team-chat-message,
task or live-chat; ref says where the holder gave it (a team-chat message URN, a
task URN, or an opaque client address), never its text. A bad kind or a blank ref
is refused (exit 2) before anything is sent. The server validates the claim and
whether the holder may revoke; a worker needs the claim from one who may. The full
rules are in 'hadron agentic-usage'.

` + managerGrantFieldsHelp + `

` + managerGrantAccessHelp,
		Example: "  hadron team grant revoke <grant-id>\n  hadron team grant revoke <grant-id> --instruction task:<task-URN>",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := requiredID("the grant id", args[0])
			if err != nil {
				return err
			}
			claim, instructed, err := managerInstruction(cmd, f)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var g *gen.GrantManagerFields
			if claim == nil {
				resp, e := gen.TeamRevokeGrant(cmd.Context(), client, id)
				if e != nil {
					return api.MapError(e)
				}
				if resp != nil && resp.RevokeGrant != nil {
					g = &resp.RevokeGrant.GrantManagerFields
				}
			} else {
				app, e := instructionApp(cmd.Context(), f)
				if e != nil {
					return e
				}
				ctx, e := managerWriteContext(cmd, app, claim, instructed)
				if e != nil {
					return e
				}
				resp, e := gen.TeamRevokeGrantInstructed(ctx, client, id, claim)
				if e != nil {
					return api.MapError(e)
				}
				if resp != nil && resp.RevokeGrant != nil {
					g = &resp.RevokeGrant.GrantManagerFields
				}
			}
			if g == nil {
				return exitcode.Newf(exitcode.Error, "server returned no grant result")
			}
			return writeManagerGrant(f, managerGrantFrom(g))
		},
	}
	addManagerInstruction(cmd)
	return cmd
}

func newCmdTeamGrantWithdraw(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw <grant-id>",
		Short: "Withdraw a pending grant you requested",
		Long: `Withdraw a grant you asked for, while it is still pending (not yet signed). It
withdraws the GRANT: 'hadron team grant signing-request withdraw <request-id>'
withdraws a signing request, which is a different thing. Only the recorded
requester may: a person, or the worker session that asked. A grant that is
already active is revoked instead ('hadron team grant revoke'), and one that has
already ended comes back as it is, with its first end, which is a success; this
command prints the returned grant and does not say it withdrew anything. An ended
grant keeps its first end.

` + managerGrantFieldsHelp + `

` + managerGrantAccessHelp,
		Example: "  hadron team grant withdraw <grant-id>",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := requiredID("the grant id", args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := withdrawGrant(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil {
				return exitcode.Newf(exitcode.Error, "server returned no grant result")
			}
			return writeManagerGrant(f, managerGrantFrom(resp))
		},
	}
}

func withdrawGrant(ctx context.Context, client graphql.Client, id string) (*gen.GrantManagerFields, error) {
	resp, err := gen.TeamWithdrawGrant(ctx, client, id)
	if err != nil || resp == nil || resp.WithdrawGrant == nil {
		return nil, err
	}
	return &resp.WithdrawGrant.GrantManagerFields, nil
}
