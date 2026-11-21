package secret

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#2631 slice 1: `secret client issue` and `secret client ls`. A read client
// is one secret.reveal grant (cor:sec:060): issuing one is a person's act that
// waits for the issuer's passkey signature, and revealing is a worker session's
// act (slice 2, cli#2664). Neither is done here: this slice never reads a value.
// The server owns who may issue and every refusal; the CLI adds no gate of its
// own (cor:api:240:01).

const (
	// signPollInterval is how often --wait re-reads the signing request.
	signPollInterval = 2 * time.Second
	// defaultSignWait bounds --wait. It is not the request's own 24-hour life.
	defaultSignWait = 10 * time.Minute
	// clientsPageSize is the page --all asks for; the loop stops on an empty page,
	// so a server cap below it only costs more requests.
	clientsPageSize = 200
	maxClientPages  = 1000
)

func newCmdClient(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client <command>",
		Short: "Issue, list and revoke read clients for an App secret",
		Long: `A read client lets one holder read an App secret's value, within a window and a
read budget you choose. It is one grant that a person signs: nothing reads until
then. The holder is you, or a live worker of the App.

Issuing is a person's act. The server refuses a worker session, a run, an
impersonated session and an App key, and decides who may issue (whoever manages
the App, or wrote the secret's current version). Revealing the value is a worker
session's act and is not part of these commands.`,
	}
	cmd.AddCommand(newCmdClientIssue(f), newCmdClientList(f), newCmdClientRevoke(f))
	return cmd
}

// signingDTO is the signing request as `client issue` prints it.
type signingDTO struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	ExpiresAt   string  `json:"expiresAt"`
	EndedAt     *string `json:"endedAt"`
	CeremonyURL string  `json:"ceremonyUrl"`
}

// issueDTO is the --json shape of `client issue`. signingRequest is null only
// when the signing step was refused after the issue: the issue is still printed
// and the exit code carries the refusal.
type issueDTO struct {
	ClientID       string      `json:"clientId"`
	GrantID        string      `json:"grantId"`
	SecretName     string      `json:"secretName"`
	SelfIssued     bool        `json:"selfIssued"`
	SigningRequest *signingDTO `json:"signingRequest"`
}

func signingFrom(r *gen.TeamGrantSigningRequestFields) *signingDTO {
	d := cmdutil.GrantSigningRequestFrom(r)
	return &signingDTO{ID: d.ID, State: d.State, ExpiresAt: d.ExpiresAt, EndedAt: d.EndedAt, CeremonyURL: d.CeremonyURL}
}

// renderIssue prints the issued client and its signing request. The ceremony
// URL is never routed through SafeCell: it is printed exactly as returned, or the
// text render is refused when it could act on a terminal.
func renderIssue(f *cmdutil.Factory, d issueDTO) error {
	return output.Write(f.IOStreams, f.JSON, d, func(w io.Writer) error {
		// The client and grant already exist, so an unsafe URL withholds only
		// itself: every other field is printed first (the caller needs the ids to
		// recover), and the text render then refuses with the URL's own error.
		unsafeURL := d.SigningRequest != nil && cmdutil.UnsafeForTerminal(d.SigningRequest.CeremonyURL)
		rows := [][2]string{
			{"clientId", output.SafeCell(d.ClientID)}, {"secret", output.SafeCell(d.SecretName)},
			{"selfIssued", fmt.Sprint(d.SelfIssued)}, {"grantId", output.SafeCell(d.GrantID)},
		}
		if s := d.SigningRequest; s != nil {
			rows = append(rows,
				[2]string{"signingRequest", output.SafeCell(s.ID)}, [2]string{"state", output.SafeCell(s.State)},
				[2]string{"expiresAt", output.SafeCell(s.ExpiresAt)})
			if !unsafeURL {
				rows = append(rows, [2]string{"ceremonyUrl", s.CeremonyURL})
			}
		}
		for _, r := range rows {
			if _, err := fmt.Fprintf(w, "%s: %s\n", r[0], r[1]); err != nil {
				return err
			}
		}
		if unsafeURL {
			return cmdutil.CeremonyURLRefusal()
		}
		return nil
	})
}

func newCmdClientIssue(f *cmdutil.Factory) *cobra.Command {
	var reason, worker, startsAt, expiresAt string
	var maxReads int
	var autoDelete, wait bool
	var waitTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "issue <secret> --reason <text> [--worker <worker-id>] [--starts-at <ts>] [--expires-at <ts>] [--max-reads <n>] [--wait]",
		Short: "Issue a read client for an App secret, pending a person's signature",
		Long: `Issue a read client for an App secret. <secret> is the App secret's ID or URN.
It sends the issue, then asks for the grant's signing request and prints the
ceremony URL: hand it to the person who must sign on that page with a passkey.
Until they do the grant is pending and allows nothing. The CLI never composes or
rewrites the URL, and text mode refuses one carrying control or format
characters (--json carries it exactly).

The holder is you (the default, marked selfIssued) or, with --worker, a live
worker of the App by its id. The server refuses any other holder. --reason is
required and is shown to the signer. --max-reads 1 makes a one-use client.
--starts-at and --expires-at take a timestamp such as 2026-12-01T09:00:00Z (the
server parses it). --auto-delete is passed as given: the server refuses it until
it is built.

This is a person's act and never sends a worker session, even from a bound
worktree. Whoever manages the App, or wrote the secret's current version, may
issue; the server decides, and its refusal is reported as it came.

--wait polls the signing request every 2 seconds until it ends, up to
--wait-timeout (10 minutes; not the request's own 24-hour life). The URL goes to
stderr first so you can pass it on while waiting. Exit codes: 0 done (with
--wait: signed); 2 usage, a server without signing, or auto-delete not yet
available; 4 not found; 5 the request ended without a signature (DECLINED,
EXPIRED, STALE, WITHDRAWN), the issuer was superseded as writer, or the secret
has too many live clients; 7 the answer to the issue was lost; 8 not permitted;
13 --wait timed out. A timeout leaves the grant and the request as they are;
nothing is withdrawn. A signer the server refuses leaves the request OPEN, so
that wait ends by timeout: the request's lastRefusedAt, in 'hadron team grant
signing-request get <request-id>', may show it.

A lost answer to the issue is never resent: a pending read client may exist, so
check 'hadron secret client list' before issuing again. --json signingRequest is
null when no signing request was received (the server refused it, or its answer
was lost: the error says which, and how to ask again).`,
		Example: `  hadron secret client issue <secret-id> --reason "deploy window" --max-reads 1
  hadron secret client issue <secret-id> --worker <worker-id> --reason "nightly job" --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if strings.TrimSpace(args[0]) == "" {
				return exitcode.Newf(exitcode.Usage, "the secret reference is empty")
			}
			if strings.TrimSpace(reason) == "" {
				return exitcode.Newf(exitcode.Usage, "--reason is required")
			}
			for _, fv := range []struct{ name, val string }{{"worker", worker}, {"starts-at", startsAt}, {"expires-at", expiresAt}} {
				if fl.Changed(fv.name) && strings.TrimSpace(fv.val) == "" {
					return exitcode.Newf(exitcode.Usage, "--%s needs a value; omit the flag to leave it unset", fv.name)
				}
			}
			if waitTimeout <= 0 {
				return exitcode.Newf(exitcode.Usage, "--wait-timeout must be positive")
			}
			in := gen.IssueSecretReadClientInput{
				SecretRef: args[0], Reason: reason, HolderKind: gen.GrantHolderKindUser, AutoDelete: autoDelete,
			}
			if fl.Changed("worker") {
				in.HolderKind = gen.GrantHolderKindWorker
				in.HolderRef = &worker
			}
			if fl.Changed("starts-at") {
				in.StartsAt = &startsAt
			}
			if fl.Changed("expires-at") {
				in.ExpiresAt = &expiresAt
			}
			if fl.Changed("max-reads") {
				in.MaxReads = &maxReads
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// A person's act: the plain context, never a worker session.
			ctx := cmd.Context()

			resp, err := gen.IssueSecretReadClient(ctx, client, &in)
			if err != nil {
				return issueError(err)
			}
			if resp == nil || resp.IssueSecretReadClient == nil {
				return exitcode.Newf(exitcode.Error, "server returned no read client; a pending read client may exist: check `hadron secret client list` before issuing again")
			}
			issued := resp.IssueSecretReadClient
			if strings.TrimSpace(issued.ClientId) == "" || strings.TrimSpace(issued.GrantId) == "" {
				return exitcode.Newf(exitcode.Error, "server returned an unusable read client (no client or grant id); no signing request was made, and a pending read client may exist: check `hadron secret client list`")
			}
			dto := issueDTO{ClientID: issued.ClientId, GrantID: issued.GrantId, SecretName: issued.SecretName, SelfIssued: issued.SelfIssued}

			sr, err := gen.TeamRequestGrantSignature(ctx, client, issued.GrantId)
			var req *gen.TeamGrantSigningRequestFields
			if err == nil {
				if sr == nil || sr.RequestGrantSignature == nil {
					err = cmdutil.ErrNoSigningRequest
				} else if r := &sr.RequestGrantSignature.TeamGrantSigningRequestFields; strings.TrimSpace(r.Id) == "" || strings.TrimSpace(r.CeremonyUrl) == "" {
					// An answer with no request id or no ceremony URL names nothing a
					// person can sign: not a success. The issue is kept and the same
					// recovery as an empty answer is given.
					err = cmdutil.ErrNoSigningRequest
				} else {
					req = r
				}
			}
			if err != nil {
				// The grant exists and is pending. Print it (signingRequest null), then
				// surface the refusal with the exit code it earned.
				if perr := renderIssue(f, dto); perr != nil {
					return perr
				}
				return signingStepError(err, issued.GrantId, issued.ClientId)
			}
			dto.SigningRequest = signingFrom(req)
			if !f.JSON {
				_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "pending: the grant allows nothing until a person signs it on the ceremony page")
			}
			if !wait {
				return renderIssue(f, dto)
			}
			if cmdutil.UnsafeForTerminal(dto.SigningRequest.CeremonyURL) {
				// The signer could not be shown this URL while we wait, so nothing is
				// polled: print the issue (text refuses the URL, --json carries it
				// exactly) and stop at once; the request stays open.
				if err := renderIssue(f, dto); err != nil {
					return err
				}
				return exitcode.Newf(exitcode.Error, "not waiting: the ceremony URL contains control or format characters and cannot be shown while waiting; the signing request %s stays open (read it with `hadron team grant signing-request get %s`)",
					output.SafeCell(dto.SigningRequest.ID), output.SafeCell(dto.SigningRequest.ID))
			}
			if _, werr := fmt.Fprintf(f.IOStreams.ErrOut, "waiting for a signature at %s\n", dto.SigningRequest.CeremonyURL); werr != nil {
				// The signer's link could not be delivered here, so do not wait for a
				// signature nobody can be asked for: print the issue (its URL is on
				// stdout) and stop; the request stays open.
				if err := renderIssue(f, dto); err != nil {
					return err
				}
				return exitcode.Newf(exitcode.Error, "not waiting: the ceremony URL could not be written to stderr (%v); the signing request %s stays open (read it with `hadron team grant signing-request get %s`)",
					werr, output.SafeCell(dto.SigningRequest.ID), output.SafeCell(dto.SigningRequest.ID))
			}
			last, werr := pollSigning(ctx, client, req.Id, issued.ClientId, waitTimeout)
			if last != nil {
				dto.SigningRequest = last
			}
			if err := renderIssue(f, dto); err != nil {
				return err
			}
			return werr
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&reason, "reason", "", "why the client is issued, shown to the signer (required)")
	fl.StringVar(&worker, "worker", "", "make a live worker of the App (its id) the holder; default: you")
	fl.StringVar(&startsAt, "starts-at", "", "when the client starts to allow reads, a timestamp the server parses")
	fl.StringVar(&expiresAt, "expires-at", "", "when the client stops allowing reads, a timestamp the server parses")
	fl.IntVar(&maxReads, "max-reads", 0, "how many reads the client allows (1 = one-use)")
	fl.BoolVar(&autoDelete, "auto-delete", false, "ask the server to delete the secret at the client's last read (the server refuses until it is built)")
	fl.BoolVar(&wait, "wait", false, "poll the signing request until it ends")
	fl.DurationVar(&waitTimeout, "wait-timeout", defaultSignWait, "max time to wait with --wait")
	return cmd
}

// issueError maps a failed issue. A lost answer (the 7 class) says a pending
// read client may exist and names `client list`; the call is never resent.
func issueError(err error) error {
	mapped := api.MapErrorKeepingCode(err)
	if exitcode.FromError(mapped) == exitcode.Unavailable {
		msg := mapped.Error() + "; a pending read client may exist: check `hadron secret client list` before issuing again"
		return api.WithServerCode(exitcode.Newf(exitcode.Unavailable, "%s", msg), err)
	}
	return mapped
}

// withoutClientID projects a mapped signing error so the issued client's id never
// reaches the terminal or the --json error envelope, even if the server echoes it
// in its message or its code (the id is half of a read: it appears in issue and
// list output only). The exit code is kept; a server code that carries the id is
// dropped, as setTokenError drops a code that carries a token.
func withoutClientID(mapped, raw error, clientID string) error {
	msg := mapped.Error()
	if clientID != "" {
		msg = strings.ReplaceAll(msg, clientID, "[client id]")
	}
	safe := exitcode.Newf(exitcode.FromError(mapped), "%s", msg)
	if clientID != "" && strings.Contains(api.ServerCodeOf(raw), clientID) {
		return safe
	}
	return api.WithServerCode(safe, raw)
}

// signingStepError maps a refused signing request after a successful issue. It
// names the grant that was created and never repeats the client id.
func signingStepError(err error, grantID, clientID string) error {
	mapped := cmdutil.MapGrantSigningError(err)
	msg := fmt.Sprintf("%s; the read client's grant %s was created and is pending, and allows nothing until a person signs it",
		mapped.Error(), output.SafeCell(grantID))
	if exitcode.FromError(mapped) == exitcode.Unavailable || errors.Is(err, cmdutil.ErrNoSigningRequest) {
		// The answer was lost or empty: a signing request may or may not exist.
		// Asking again is safe for a pending grant, so name that, never resend.
		msg += fmt.Sprintf("; a signing request may exist: run `hadron team grant request-signature %s` to ask again", output.SafeCell(grantID))
	}
	return withoutClientID(api.WithServerCode(exitcode.Newf(exitcode.FromError(mapped), "%s", msg), err), err, clientID)
}

// pollSigning re-reads the signing request until its state is not OPEN or the
// timeout elapses. The deadline context also bounds a single hung request. It
// returns the last request it read (nil if none) and the exit-coded outcome: nil
// for COMPLETED, 13 on timeout, 5 for an ended request, the mapped error for a
// failed read. A timeout never withdraws the request.
func pollSigning(parent context.Context, client graphql.Client, id, clientID string, timeout time.Duration) (*signingDTO, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var last *signingDTO
	timedOut := func() error {
		// The parent (Ctrl-C, a caller's cancel or deadline) is not the command's
		// own wait: say what happened, and leave everything as it is.
		if parent.Err() != nil {
			return exitcode.Newf(exitcode.Cancelled,
				"cancelled while waiting for a signature (%v); the grant and its signing request %s are left as they are (nothing is withdrawn): read it with `hadron team grant signing-request get %s`",
				parent.Err(), output.SafeCell(id), output.SafeCell(id))
		}
		return exitcode.Newf(exitcode.WaitTimeout,
			"not signed within %s; the grant and its signing request %s are left as they are (nothing is withdrawn): read it with `hadron team grant signing-request get %s`",
			timeout, output.SafeCell(id), output.SafeCell(id))
	}
	ticker := time.NewTicker(signPollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return last, timedOut()
		}
		resp, err := gen.TeamGrantSigningRequest(ctx, client, id)
		if err != nil {
			if ctx.Err() != nil {
				return last, timedOut()
			}
			return last, withoutClientID(cmdutil.MapGrantSigningError(err), err, clientID)
		}
		if resp == nil || resp.GrantSigningRequest == nil {
			return last, exitcode.Newf(exitcode.NotFound, "signing request %s not found (or not visible to this caller)", output.SafeCell(id))
		}
		last = signingFrom(&resp.GrantSigningRequest.TeamGrantSigningRequestFields)
		switch last.State {
		case "OPEN":
		case "COMPLETED":
			return last, nil
		default:
			return last, exitcode.Newf(exitcode.Conflict, "the signing request %s ended %s without a signature; nothing was signed", output.SafeCell(id), output.SafeCell(last.State))
		}
		select {
		case <-ctx.Done():
			return last, timedOut()
		case <-ticker.C:
		}
	}
}

// clientDTO is the --json shape of one row of `client list`.
type clientDTO struct {
	// ClientID is the full id only for the current issuer or holder: the server
	// shows a manager who is neither the prefix alone, so it is null then.
	ClientID     *string `json:"clientId"`
	ClientPrefix string  `json:"clientPrefix"`
	GrantID      string  `json:"grantId"`
	SecretID     string  `json:"secretId"`
	SecretName   string  `json:"secretName"`
	SelfIssued   bool    `json:"selfIssued"`
	EndReason    *string `json:"endReason"`
	CreatedAt    string  `json:"createdAt"`
}

// fetchClientPage reads one page of the caller's read clients. The list is declared
// non-null with non-null rows: a missing or null list, or a null row, is a
// malformed answer, never an empty success (and a dropped row would shift the --all
// offsets).
func fetchClientPage(ctx context.Context, client graphql.Client, limit, offset *int) ([]clientDTO, error) {
	resp, err := gen.MySecretReadClients(ctx, client, limit, offset)
	if err != nil {
		return nil, api.MapErrorKeepingCode(err)
	}
	if resp == nil || resp.MySecretReadClients == nil {
		return nil, exitcode.Newf(exitcode.Error, "server returned no read-client list; the answer is malformed, not empty")
	}
	page := make([]clientDTO, 0, len(resp.MySecretReadClients))
	for _, c := range resp.MySecretReadClients {
		if c == nil {
			return nil, exitcode.Newf(exitcode.Error, "server returned an empty read-client row; the answer is malformed")
		}
		page = append(page, clientDTO{ClientID: c.ClientId, ClientPrefix: c.ClientPrefix, GrantID: c.GrantId, SecretID: c.SecretId,
			SecretName: c.SecretName, SelfIssued: c.SelfIssued, EndReason: c.EndReason, CreatedAt: c.CreatedAt})
	}
	return page, nil
}

func newCmdClientList(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	var all, asWorker bool
	cmd := &cobra.Command{
		Use:     "list [--limit <n>] [--offset <n> | --all] [--as-worker]",
		Aliases: []string{"ls"},
		Short:   "List the read clients you issued or hold",
		Long: `List the read clients the caller issued or holds: client id, secret, whether it
was self-issued, the end reason the server reported (SECRET_DELETED or PROTECTED;
a dash means no reason reported, not that the client is live) and when it was
created. The server returns nothing else: there is no window, read budget or
live/spent column here.

Acting as you (the default, no worker session) lists the clients you issued or
hold. --as-worker sends this worktree's bound worker session and so lists the
clients that worker holds. An App key, a run or an impersonated session gets an
empty list from the server, not an error, so an empty list is not proof that no
client exists. The server pages the list; --limit and --offset pass straight
through and --all walks every page (it stops with an error after 1000 pages;
use --limit and --offset beyond that).`,
		Example: `  hadron secret client list
  hadron secret client list --as-worker --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fl := cmd.Flags()
			if all && (fl.Changed("limit") || fl.Changed("offset")) {
				return exitcode.Newf(exitcode.Usage, "--all cannot be combined with --limit/--offset")
			}
			if limit < 0 || offset < 0 {
				return exitcode.Newf(exitcode.Usage, "--limit and --offset cannot be negative")
			}
			var asCtx cmdutil.WorkerWriteContext
			if asWorker {
				m, err := f.ResolveWorkerWrite(cmd.Context())
				if err != nil {
					return err
				}
				asCtx = m
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			sendCtx := cmd.Context()
			if asCtx != nil {
				sendCtx, err = asCtx(sendCtx)
				if err != nil {
					return err
				}
			}
			fetch := func(l, o *int) ([]clientDTO, error) { return fetchClientPage(sendCtx, client, l, o) }
			rows := []clientDTO{}
			if all {
				for off, pages := 0, 0; ; pages++ {
					l, o := clientsPageSize, off
					page, err := fetch(&l, &o)
					if err != nil {
						return err
					}
					if len(page) == 0 {
						break
					}
					// The cap is checked on a page that still has rows, so exactly
					// maxClientPages pages plus the empty terminator succeed.
					if pages >= maxClientPages {
						return exitcode.Newf(exitcode.Error, "stopped after %d pages; use --limit and --offset", maxClientPages)
					}
					rows = append(rows, page...)
					off += len(page)
				}
			} else {
				page, err := fetch(pagePtr(cmd, "limit", limit), pagePtr(cmd, "offset", offset))
				if err != nil {
					return err
				}
				rows = append(rows, page...)
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, err := fmt.Fprintln(w, "no read clients")
					return err
				}
				t := output.NewTable(w, "CLIENT", "SECRET", "SELF-ISSUED", "END REASON", "CREATED", "GRANT")
				for _, r := range rows {
					t.Row(output.SafeCell(clientColumn(r)), output.SafeCell(r.SecretName), fmt.Sprint(r.SelfIssued),
						output.SafeCell(output.Dash(r.EndReason)), output.SafeCell(r.CreatedAt), output.SafeCell(r.GrantID))
				}
				return t.Flush()
			})
		},
	}
	fl := cmd.Flags()
	fl.IntVar(&limit, "limit", 0, "rows per page (default: the server's)")
	fl.IntVar(&offset, "offset", 0, "rows to skip")
	fl.BoolVar(&all, "all", false, "read every page")
	fl.BoolVar(&asWorker, "as-worker", false, "send this worktree's bound worker session, so the list is the clients that worker holds (default: act as you, no session header)")
	return cmd
}

// clientColumn is the CLIENT cell: the full id when the server gave it, else the
// prefix with an ellipsis (a manager who is neither the issuer nor the holder
// sees only a prefix, so it is never mistaken for an id).
func clientColumn(r clientDTO) string {
	if r.ClientID != nil {
		return *r.ClientID
	}
	return r.ClientPrefix + "…"
}
