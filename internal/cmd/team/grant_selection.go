package team

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// cli#3197: one signing request over a selection of one App's pending grants
// (hadron-server#5001, cor:acl:140:07). The CLI sends the ids as given and the
// server judges them, the cap included. Nothing here signs: no operation takes
// an assertion, and the ceremony URL is printed as the server returned it.

// The access and exit-code text is the selection's own: the one-grant help says
// only a worker session is answered, and the server also answers a person for
// grants they may read, so that sentence is not repeated here.
const grantSelectionHelp = `The call carries this worktree's worker session when it is bound. Unbound it is
sent without one, with a stderr line saying so, and the server decides whether
you may. A binding on another server, or --app naming another App, is refused
before anything is sent.

One touch of a person's passkey on the ceremony page signs the selection; each
grant activates or fails on its own. This command never signs and never
activates a grant. These calls need a server that offers selection signing; on
one without it the command says "does not offer" and exits 2.

A refusal prints the server's message and code, and the grantRef, reason, cap or
state it sent (--json: serverCode and extensions). A grant id is shown there
only when it is one you passed.

Exit codes: 0 done; 2 usage (a blank id, a binding for another server or App, a
server without selection signing, SELECTION_EMPTY, SELECTION_TOO_LARGE,
SELECTION_DUPLICATE_GRANT, SELECTION_MIXED_APPS); 4 GRANT_NOT_FOUND,
SIGNING_REQUEST_NOT_FOUND, or a selection that is not found or not visible; 5
GRANT_NOT_PENDING, GRANT_EXPIRED, GRANT_TERMS_UNREPRESENTABLE,
SIGNING_REQUEST_ENDED; 8 not permitted (SIGNING_REQUEST_NOT_REQUESTER, and
FORBIDDEN, which the server also answers to asking or withdrawing while
impersonating).`

// grantSigningSelectionDTO is the stable --json shape of a selection. Each item
// is a signing request in the one-grant shape.
type grantSigningSelectionDTO struct {
	ID             string                           `json:"id"`
	State          string                           `json:"state"`
	PayloadVersion string                           `json:"payloadVersion"`
	RequestedBy    cmdutil.GrantSigningRequesterDTO `json:"requestedBy"`
	CreatedAt      string                           `json:"createdAt"`
	ExpiresAt      string                           `json:"expiresAt"`
	EndedAt        *string                          `json:"endedAt"`
	LastRefusedAt  *string                          `json:"lastRefusedAt"`
	CeremonyURL    string                           `json:"ceremonyUrl"`
	Items          []cmdutil.GrantSigningRequestDTO `json:"items"`
}

func grantSigningSelectionFrom(r *gen.TeamGrantSigningSelectionFields) grantSigningSelectionDTO {
	d := grantSigningSelectionDTO{
		ID: r.Id, State: string(r.State), PayloadVersion: r.PayloadVersion, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		EndedAt: r.EndedAt, LastRefusedAt: r.LastRefusedAt, CeremonyURL: r.CeremonyUrl, Items: []cmdutil.GrantSigningRequestDTO{},
	}
	if rb := r.RequestedBy; rb != nil {
		d.RequestedBy = cmdutil.GrantSigningRequesterDTO{UserID: rb.UserId, AppID: rb.AppId, WorkerID: rb.WorkerId, SessionID: rb.SessionId}
	}
	for _, item := range r.Items {
		if item != nil {
			d.Items = append(d.Items, cmdutil.GrantSigningRequestFrom(&item.TeamGrantSigningRequestFields))
		}
	}
	return d
}

// renderGrantSigningSelection prints the selection and one row per item. Like the
// one-grant render, ceremonyUrl is printed exactly as returned or not at all. An
// item's own ceremonyUrl is the selection's page, so text prints it once.
func renderGrantSigningSelection(f *cmdutil.Factory, d grantSigningSelectionDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		if cmdutil.UnsafeForTerminal(d.CeremonyURL) {
			return cmdutil.CeremonyURLRefusal()
		}
		rows := [][2]string{
			{"id", output.SafeCell(d.ID)}, {"state", output.SafeCell(d.State)}, {"payloadVersion", output.SafeCell(d.PayloadVersion)},
			{"requestedBy.userId", cmdutil.DashOrCell(d.RequestedBy.UserID)}, {"requestedBy.appId", cmdutil.DashOrCell(d.RequestedBy.AppID)},
			{"requestedBy.workerId", cmdutil.DashOrCell(d.RequestedBy.WorkerID)}, {"requestedBy.sessionId", cmdutil.DashOrCell(d.RequestedBy.SessionID)},
			{"createdAt", output.SafeCell(d.CreatedAt)}, {"expiresAt", output.SafeCell(d.ExpiresAt)}, {"endedAt", cmdutil.DashOrCell(d.EndedAt)},
			{"lastRefusedAt", cmdutil.DashOrCell(d.LastRefusedAt)},
			{"ceremonyUrl", d.CeremonyURL},
			{"items", fmt.Sprint(len(d.Items))},
		}
		for _, r := range rows {
			if _, err := fmt.Fprintf(w, "%s: %s\n", r[0], r[1]); err != nil {
				return err
			}
		}
		if len(d.Items) == 0 {
			return nil
		}
		t := output.NewTable(w, "GRANT", "REQUEST", "VERSION", "STATE", "ENDED")
		for _, i := range d.Items {
			t.Row(output.SafeCell(i.GrantID), output.SafeCell(i.ID), fmt.Sprint(i.Version), output.SafeCell(i.State), cmdutil.DashOrCell(i.EndedAt))
		}
		return t.Flush()
	})
}

// runGrantSelection sends one selection operation with the worktree's binding and
// renders its answer. supplied are the caller's grant ids, the only ones a
// refusal may show. A nil answer with no error is the read's not-found.
func runGrantSelection(cmd *cobra.Command, f *cmdutil.Factory, supplied []string, call func(ctx context.Context, c graphql.Client) (*gen.TeamGrantSigningSelectionFields, error)) error {
	b, err := grantWriteBinding(cmd.Context(), f)
	if err != nil {
		return err
	}
	ctx, client, err := workerGrantWriteClient(cmd.Context(), f, b)
	if err != nil {
		return err
	}
	r, err := call(ctx, client)
	if err != nil {
		return cmdutil.MapGrantSelectionError(err, supplied)
	}
	if r == nil {
		return exitcode.Newf(exitcode.NotFound, "signing selection not found (or not visible to this caller); the id given is not shown")
	}
	return renderGrantSigningSelection(f, grantSigningSelectionFrom(r))
}

// errNoSigningSelection is a mutation that answered null where it must answer a
// selection.
func errNoSigningSelection() error {
	return exitcode.Newf(exitcode.Error, "server returned no signing selection")
}

func newCmdTeamGrantRequestSelectionSignature(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "request-selection-signature <grant-id>...",
		Short: "Ask for several pending grants to be signed at once",
		Long: `Ask the server for ONE signing request over a selection of pending grants of one
App, at most 25. It prints the selection, its ceremonyUrl (the ceremony page
where a person signs), the item count, the expiry and one row per grant. Give
the URL to that person as printed; it is never composed, shortened or rewritten
here. Each <grant-id> is an id that 'hadron team grant request' printed. The ids
are sent as given and the server judges the list, the cap included: it refuses
the whole selection, before opening anything.

Text mode refuses a URL carrying control or format characters (exit 1); --json
always carries it. Read it with 'hadron team grant signing-request get-selection
<selection-id>'.

` + grantSelectionHelp,
		Example: "  hadron team grant request-selection-signature <grant-id> <grant-id>",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]string, 0, len(args))
			for _, a := range args {
				id, err := requiredID("each grant id", a)
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			return runGrantSelection(cmd, f, ids, func(ctx context.Context, c graphql.Client) (*gen.TeamGrantSigningSelectionFields, error) {
				resp, err := gen.TeamRequestGrantSelectionSignature(ctx, c, ids)
				if err != nil {
					return nil, err
				}
				if resp == nil || resp.RequestGrantSelectionSignature == nil {
					return nil, errNoSigningSelection()
				}
				return &resp.RequestGrantSelectionSignature.TeamGrantSigningSelectionFields, nil
			})
		},
	}
}

// selectionVerb builds a verb that takes one selection id and prints the selection.
func selectionVerb(f *cmdutil.Factory, use, short, what, example string, call func(ctx context.Context, c graphql.Client, id string) (*gen.TeamGrantSigningSelectionFields, error)) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1), Example: example,
		Long: what + "\n\n" + grantSelectionHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := requiredID("the selection id", args[0])
			if err != nil {
				return err
			}
			return runGrantSelection(cmd, f, nil, func(ctx context.Context, c graphql.Client) (*gen.TeamGrantSigningSelectionFields, error) {
				return call(ctx, c, id)
			})
		},
	}
}

func newCmdTeamGrantSelectionGet(f *cmdutil.Factory) *cobra.Command {
	return selectionVerb(f, "get-selection <selection-id>", "Read a signing selection",
		`Shows a selection: its state, expiry, lastRefusedAt and one row per grant.
<selection-id> is the id printed by 'hadron team grant
request-selection-signature'. state is the server's own word, printed as sent;
OPEN means the person can still sign on the ceremony page. A worker reads what
became of one of its own grants with 'hadron team grant get <grant-id>'. An
unknown id and one you may not read get the same not-found answer.

lastRefusedAt is the latest signing attempt the server observed and refused as
ineligible, a timestamp only; a dash (null in --json) means none was observed.`,
		"  hadron team grant signing-request get-selection <selection-id> --json",
		func(ctx context.Context, c graphql.Client, id string) (*gen.TeamGrantSigningSelectionFields, error) {
			resp, err := gen.TeamGrantSigningSelection(ctx, c, id)
			if err != nil || resp == nil || resp.GrantSigningSelection == nil {
				return nil, err
			}
			return &resp.GrantSigningSelection.TeamGrantSigningSelectionFields, nil
		})
}

func newCmdTeamGrantSelectionWithdraw(f *cmdutil.Factory) *cobra.Command {
	return selectionVerb(f, "withdraw-selection <selection-id>", "Withdraw an open signing selection",
		`Ends a signing selection you asked for, so its ceremony page can no longer sign
it. It withdraws the request, not the grants, and is not confirmed: they can be
asked for again only in a new request. A selection that already ended is refused
with SIGNING_REQUEST_ENDED and its state.`,
		"  hadron team grant signing-request withdraw-selection <selection-id>",
		func(ctx context.Context, c graphql.Client, id string) (*gen.TeamGrantSigningSelectionFields, error) {
			resp, err := gen.TeamWithdrawGrantSigningSelection(ctx, c, id)
			if err != nil {
				return nil, err
			}
			if resp == nil || resp.WithdrawGrantSigningSelection == nil {
				return nil, errNoSigningSelection()
			}
			return &resp.WithdrawGrantSigningSelection.TeamGrantSigningSelectionFields, nil
		})
}
