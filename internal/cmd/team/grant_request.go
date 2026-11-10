package team

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/Khan/genqlient/graphql"
	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
	"github.com/spf13/cobra"
)

// cli#1868: the worker's half of a boot-prompt grant: ask for a grant, ask for
// it to be signed, read or withdraw that signing request. Nothing here signs
// or activates a grant (cor:acl:140:07: a person signs, on the ceremony page),
// and the CLI never reproduces the server's worker gate (cor:api:240:01).

const grantRequestHelp = `The server answers only for a worker session driven by a person, in its own App.
This command sends that session when this worktree is bound. Run unbound it still
asks, without a worker session, and prints the server's refusal (it makes no local
refusal); a stderr line says so. A binding on another server, or --app naming
another App, is refused before anything is sent.

A requested grant is pending and allows nothing until a person signs it on the
ceremony page. This command never signs and never activates a grant. It prints
no grant state label, only the facts and allows.

These commands exist only where signing is switched on. On a server without it
the command says "does not offer" and exits 2.

Exit codes: 0 done; 2 usage (bad input, a binding for another server or App, or
a server without signing); 4 not found or not visible; 5 the grant or request is
no longer in a state that allows it (GRANT_NOT_PENDING, GRANT_EXPIRED,
GRANT_TERMS_UNREPRESENTABLE, SIGNING_REQUEST_ENDED); 8 not permitted
(GRANT_REQUESTER_NOT_WORKER, GRANT_HOLDER_NOT_ALLOWED,
SIGNING_REQUEST_NOT_REQUESTER, FORBIDDEN). --json errors carry serverCode.`

const unboundNote = "no worker binding in this worktree; sent without a worker session"

// grantWriteBinding resolves the binding for a grant request verb. It returns nil
// (and no error) when the worktree is unbound: the verb then sends without a
// session and the server answers. A binding for another server or another App
// still refuses here, so a session id never reaches a server or App it does
// not belong to (session hygiene, not a platform rule).
func grantWriteBinding(ctx context.Context, f *cmdutil.Factory) (*binding, error) {
	b, _, err := readBinding(ctx)
	if err != nil && !errors.Is(err, errNoWorktree) {
		return nil, err
	}
	if b == nil || b.SessionID == "" {
		return nil, nil
	}
	if !bindingServerMatches(f, b) {
		return nil, exitcode.Newf(exitcode.Usage, "this worktree's worker binding is for another server; omit the other --server")
	}
	if f.AppFlag != "" {
		same, e := bindingsAppIdentity(ctx, f, f.AppFlag, b.AppID)
		if e != nil {
			return nil, e
		}
		if !same {
			return nil, exitcode.Newf(exitcode.Usage, "this worktree's worker binding is for another App; omit the other --app")
		}
	}
	return b, nil
}

// workerGrantWriteClient is workerGrantClient for the request verbs: the
// binding's session when there is one, none (and one stderr note) when not.
func workerGrantWriteClient(ctx context.Context, f *cmdutil.Factory, b *binding) (context.Context, graphql.Client, error) {
	client, err := f.GraphQLClient()
	if err != nil {
		return ctx, nil, err
	}
	if b == nil {
		_, _ = fmt.Fprintln(f.IOStreams.ErrOut, unboundNote)
		return ctx, client, nil
	}
	return api.WithSession(ctx, b.SessionID), client, nil
}

// grantSigningFields are the schema names a grant-request operation can be
// refused by when the server has no signing surface (its switch off, or older).
var grantSigningFields = []string{
	"createGrant", "CreateGrantInput", "requestGrantSignature", "withdrawGrantSigningRequest",
	"grantSigningRequest", "GrantSigningRequest", "GrantSigningRequestState",
}

// mapGrantSigningError is api.MapErrorKeepingCode, except that a server whose
// every error is a missing signing name gets its own sentence: the generic
// skew advice (upgrade) is wrong where signing is simply not offered. A mixed
// envelope (a missing name beside a real refusal) is not swallowed.
func mapGrantSigningError(err error) error {
	if api.IsGraphQLValidationOnlyFor(err, grantSigningFields...) {
		return api.WithServerCode(exitcode.Newf(exitcode.Usage, "this server does not offer grant requests and grant signing"), err)
	}
	return api.MapErrorKeepingCode(err)
}

// grantSigningRequestDTO is the stable --json shape of a grant signing request.
type grantSigningRequestDTO struct {
	ID             string                   `json:"id"`
	GrantID        string                   `json:"grantId"`
	Version        int                      `json:"version"`
	TermsHash      string                   `json:"termsHash"`
	PayloadVersion string                   `json:"payloadVersion"`
	State          string                   `json:"state"`
	RequestedBy    grantSigningRequesterDTO `json:"requestedBy"`
	CreatedAt      string                   `json:"createdAt"`
	ExpiresAt      string                   `json:"expiresAt"`
	EndedAt        *string                  `json:"endedAt"`
	CeremonyURL    string                   `json:"ceremonyUrl"`
}

type grantSigningRequesterDTO struct {
	UserID    *string `json:"userId"`
	AppID     *string `json:"appId"`
	WorkerID  *string `json:"workerId"`
	SessionID *string `json:"sessionId"`
}

func grantSigningRequestFrom(r *gen.TeamGrantSigningRequestFields) grantSigningRequestDTO {
	d := grantSigningRequestDTO{
		ID: r.Id, GrantID: r.GrantId, Version: r.Version, TermsHash: r.TermsHash, PayloadVersion: r.PayloadVersion,
		State: string(r.State), CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, EndedAt: r.EndedAt, CeremonyURL: r.CeremonyUrl,
	}
	if rb := r.RequestedBy; rb != nil {
		d.RequestedBy = grantSigningRequesterDTO{rb.UserId, rb.AppId, rb.WorkerId, rb.SessionId}
	}
	return d
}

// unsafeForTerminal reports a character that could act on a terminal when
// printed: C0, DEL and C1 controls (Cc), format characters (Cf) and the line
// and paragraph separators (Zl, Zp).
func unsafeForTerminal(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return true
		}
	}
	return false
}

func grantOrDash(p *string) string {
	if p == nil {
		return "—"
	}
	return output.SafeCell(*p)
}

// renderGrantSigningRequest prints the request. ceremonyUrl is never routed
// through SafeCell (it would truncate or rewrite it): it is printed exactly as
// returned, or the text render is refused when it could act on a terminal.
func renderGrantSigningRequest(f *cmdutil.Factory, d grantSigningRequestDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		if unsafeForTerminal(d.CeremonyURL) {
			return exitcode.Newf(exitcode.Error, "the ceremony URL contains control or format characters and is not printed as text; use --json to read it exactly")
		}
		rows := [][2]string{
			{"id", output.SafeCell(d.ID)}, {"grantId", output.SafeCell(d.GrantID)}, {"version", fmt.Sprint(d.Version)},
			{"termsHash", output.SafeCell(d.TermsHash)}, {"payloadVersion", output.SafeCell(d.PayloadVersion)}, {"state", output.SafeCell(d.State)},
			{"requestedBy.userId", grantOrDash(d.RequestedBy.UserID)}, {"requestedBy.appId", grantOrDash(d.RequestedBy.AppID)},
			{"requestedBy.workerId", grantOrDash(d.RequestedBy.WorkerID)}, {"requestedBy.sessionId", grantOrDash(d.RequestedBy.SessionID)},
			{"createdAt", output.SafeCell(d.CreatedAt)}, {"expiresAt", output.SafeCell(d.ExpiresAt)}, {"endedAt", grantOrDash(d.EndedAt)},
			{"ceremonyUrl", d.CeremonyURL},
		}
		for _, r := range rows {
			if _, err := fmt.Fprintf(w, "%s: %s\n", r[0], r[1]); err != nil {
				return err
			}
		}
		return nil
	})
}

// requiredID refuses a blank positional id before any call.
func requiredID(what, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", exitcode.Newf(exitcode.Usage, "%s is required", what)
	}
	return v, nil
}

func newCmdTeamGrantRequest(f *cmdutil.Factory) *cobra.Command {
	var action, values, reason, holder, holderID, startsAt, expiresAt string
	var maxUses int
	cmd := &cobra.Command{
		Use:   "request --action <name> --values <json|@file> --reason <text>",
		Short: "Ask for a grant for this worker or its role agent",
		Long: `Ask for a pending grant for this worker (--holder worker, the default) or its
role agent (--holder agent). The holder id comes from this worktree's binding;
--holder-id overrides it (and is required when unbound). --values is the JSON
object of the action's parameters (or @file); whether it fits the action is the
server's to judge. --starts-at, --expires-at and --max-uses are passed through.

Prints the pending grant in the same fourteen fields as team grant get. Hand the
next step to a person: hadron team grant request-signature <id>.

` + grantRequestHelp,
		Example: "  hadron team grant request --action bootprompt.source.edit --values '{\"node\":\"<id>\"}' --reason 'fix a typo in my prompt'",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
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
			for _, fv := range []struct{ name, val string }{{"starts-at", startsAt}, {"expires-at", expiresAt}, {"holder-id", holderID}} {
				if fl.Changed(fv.name) && strings.TrimSpace(fv.val) == "" {
					return exitcode.Newf(exitcode.Usage, "--%s needs a value; omit the flag to leave it unset", fv.name)
				}
			}
			if holder != "worker" && holder != "agent" {
				return exitcode.Newf(exitcode.Usage, "--holder takes worker or agent")
			}
			b, err := grantWriteBinding(cmd.Context(), f)
			if err != nil {
				return err
			}
			id := strings.TrimSpace(holderID)
			if id == "" && b != nil {
				id = b.WorkerID
				if holder == "agent" {
					id = b.AgentID
				}
			}
			if id == "" {
				return exitcode.Newf(exitcode.Usage, "no %s id to ask for: this worktree has no binding that names one; pass --holder-id", holder)
			}
			in := gen.CreateGrantInput{Action: strings.TrimSpace(action), Values: raw, HolderKind: gen.GrantHolderKindWorker, HolderRef: id, Reason: reason}
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
			ctx, client, err := workerGrantWriteClient(cmd.Context(), f, b)
			if err != nil {
				return err
			}
			resp, err := gen.TeamCreateGrant(ctx, client, &in)
			if err != nil {
				return mapGrantSigningError(err)
			}
			if resp == nil || resp.CreateGrant == nil {
				return exitcode.Newf(exitcode.Error, "server returned no grant")
			}
			dto := workerGrantFrom(resp.CreateGrant)
			if !f.JSON {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "pending until a person signs it; next: hadron team grant request-signature %s\n", dto.ID)
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				names := []string{"id", "action", "values", "holderKind", "holderId", "startsAt", "expiresAt", "maxUses", "useCount", "reason", "createdAt", "activatedAt", "revokedAt", "allows"}
				for i, v := range dto.cells() {
					if _, e := fmt.Fprintf(w, "%s: %s\n", names[i], output.SafeCell(v)); e != nil {
						return e
					}
				}
				return nil
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&action, "action", "", "the action to ask a grant for (required)")
	fl.StringVar(&values, "values", "", "the action's parameters as a JSON object, or @file (required)")
	fl.StringVar(&reason, "reason", "", "why the grant is asked for, shown to the signer (required)")
	fl.StringVar(&holder, "holder", "worker", "who holds the grant: worker or agent (this worker's role agent)")
	fl.StringVar(&holderID, "holder-id", "", "the holder's id, overriding the binding's")
	fl.StringVar(&startsAt, "starts-at", "", "when the grant starts to allow, a timestamp the server parses")
	fl.StringVar(&expiresAt, "expires-at", "", "when the grant stops allowing, a timestamp the server parses")
	fl.IntVar(&maxUses, "max-uses", 0, "how many uses the grant allows")
	return cmd
}

// signingRequestVerb builds a verb that takes one id and prints a signing request.
func signingRequestVerb(f *cmdutil.Factory, use, short, what, example string, call func(ctx context.Context, c graphql.Client, id string) (*gen.TeamGrantSigningRequestFields, error)) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.ExactArgs(1), Example: example,
		Long: short + "\n\n" + what + "\n\n" + grantRequestHelp,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := requiredID("the id", args[0])
			if err != nil {
				return err
			}
			b, err := grantWriteBinding(cmd.Context(), f)
			if err != nil {
				return err
			}
			ctx, client, err := workerGrantWriteClient(cmd.Context(), f, b)
			if err != nil {
				return err
			}
			r, err := call(ctx, client, id)
			if err != nil {
				return mapGrantSigningError(err)
			}
			if r == nil {
				return exitcode.Newf(exitcode.NotFound, "signing request %q not found (or not visible to this caller)", id)
			}
			return renderGrantSigningRequest(f, grantSigningRequestFrom(r))
		},
	}
}

func newCmdTeamGrantRequestSignature(f *cmdutil.Factory) *cobra.Command {
	return signingRequestVerb(f, "request-signature <grant-id>", "Ask for a pending grant to be signed",
		"Prints the signing request and its ceremonyUrl, the page where a person signs the\ngrant. Hand the URL to that person as printed; it is never composed, shortened or\nrewritten here. Text mode refuses a URL carrying control or format characters\n(exit 1); --json always carries it byte for byte.",
		"  hadron team grant request-signature <grant-id>",
		func(ctx context.Context, c graphql.Client, id string) (*gen.TeamGrantSigningRequestFields, error) {
			resp, err := gen.TeamRequestGrantSignature(ctx, c, id)
			if err != nil || resp == nil {
				return nil, err
			}
			return &resp.RequestGrantSignature.TeamGrantSigningRequestFields, nil
		})
}

func newCmdTeamGrantSigningRequest(f *cmdutil.Factory) *cobra.Command {
	group := &cobra.Command{Use: "signing-request <command>", Short: "Read or withdraw a grant signing request", Long: "Read or withdraw a signing request this worker made.\n\n" + grantRequestHelp}
	get := signingRequestVerb(f, "get <request-id>", "Read a grant signing request",
		"Shows whether the URL you handed over was signed: state is the server's own state\nfor the request (OPEN, COMPLETED, DECLINED, EXPIRED, STALE, WITHDRAWN), printed as sent.",
		"  hadron team grant signing-request get <request-id> --json",
		func(ctx context.Context, c graphql.Client, id string) (*gen.TeamGrantSigningRequestFields, error) {
			resp, err := gen.TeamGrantSigningRequest(ctx, c, id)
			if err != nil || resp == nil || resp.GrantSigningRequest == nil {
				return nil, err
			}
			return &resp.GrantSigningRequest.TeamGrantSigningRequestFields, nil
		})
	withdraw := signingRequestVerb(f, "withdraw <request-id>", "Withdraw an open grant signing request",
		"Ends a signing request this worker made, so its URL can no longer be signed. It\nwithdraws the request, not the grant, and is not confirmed: it can be asked again\nonly as a new request.",
		"  hadron team grant signing-request withdraw <request-id>",
		func(ctx context.Context, c graphql.Client, id string) (*gen.TeamGrantSigningRequestFields, error) {
			resp, err := gen.TeamWithdrawGrantSigningRequest(ctx, c, id)
			if err != nil || resp == nil {
				return nil, err
			}
			return &resp.WithdrawGrantSigningRequest.TeamGrantSigningRequestFields, nil
		})
	group.AddCommand(get, withdraw)
	return group
}
