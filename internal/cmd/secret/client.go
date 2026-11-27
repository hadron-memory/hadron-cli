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
	var reason, worker, startsAt, expiresAt, mode, agent, bindingPrefix, bindingHeader string
	var maxReads int
	var autoDelete, wait, appHolder bool
	var waitTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "issue <secret> --reason <text> [--worker <worker-id> | --mode USE (--agent <agent-id> | --app-holder) --binding-prefix <https-prefix> --binding-header <name>] [--starts-at <ts>] [--expires-at <ts>] [--max-reads <n>] [--wait]",
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
server parses it).

USE mode (--mode USE, any case) issues an injection-only client: the holder's runs use the
secret by reference and its value is never shown. It needs a holder that is an
agent installed in the App (--agent <agent-id>) or the App itself
(--app-holder): give exactly one of the two, and not --worker. --binding-prefix
is the signed https origin and path prefix the value may be sent to and
--binding-header the header it goes in. USE needs both (exit 2 without either);
they are sent exactly as typed (the
server judges them; the person signs those bytes). Until a person signs, a USE
client is pending and allows nothing. The USE flags are refused without --mode
USE, and --agent and --app-holder never hold a client issued without it (that
one is held by you or --worker).

--auto-delete marks the client: the server then deletes the secret at the client's
last read or when its window ends, whichever comes first. It needs --expires-at
or --max-reads (the server refuses it without a window or a read budget, and the
CLI does not check that itself: the refusal comes back as the server wrote it).
Only whoever manages the App, or the writer of the secret's current version, may
set it; on a secret that does not exist or that you cannot see the refusal is the
same "not permitted" (exit 8), never "not found". The mark is one of
the grant's terms, and a grant's terms are what the person signs: nothing is
deleted until a person has signed the grant, and a client that is never signed
deletes nothing. When --auto-delete was sent the command prints a note on stderr stating the
requested deletion; the issue answer itself does not carry the mark, so
'secret client list' (AUTO-DELETE) is where to read it back.

What cancels the deletion depends on the window. With --expires-at the deletion
is a signed deadline: once signed, it happens at the end of the window whatever
becomes of the client, so revoking a marked client (or the holder leaving) does
not cancel the deletion. With only --max-reads there is no deadline: the secret
is deleted at the client's LAST READ, and a client revoked (or whose holder left)
before its last read never makes it, so revoking cancels the deletion. A deleted
secret is gone for good.

This is a person's act and never sends a worker session, even from a bound
worktree. Whoever manages the App, or wrote the secret's current version, may
issue; the server decides, and its refusal is reported as it came.

--wait polls the signing request every 2 seconds until it ends, up to
--wait-timeout (10 minutes; not the request's own 24-hour life). The URL goes to
stderr first so you can pass it on while waiting. Exit codes: 0 done (with
--wait: signed); 2 usage, a server without signing, or --auto-delete without
--expires-at or --max-reads (or a time the server cannot parse); 4 not found; 5 the request ended without a signature (DECLINED,
EXPIRED, STALE, WITHDRAWN), the issuer was superseded as writer, or the secret
has too many live clients; 7 the answer to the issue was lost; 8 not permitted
(including --auto-delete on a secret you neither manage nor wrote the current
version of); 13 --wait timed out. A timeout leaves the grant and the request as they are;
nothing is withdrawn. A signer the server refuses leaves the request OPEN, so
that wait ends by timeout: the request's lastRefusedAt, in 'hadron team grant
signing-request get <request-id>', may show it.

A lost answer to the issue is never resent: a pending read client may exist, so
check 'hadron secret client list' before issuing again. --json signingRequest is
null when no signing request was received (the server refused it, or its answer
was lost: the error says which, and how to ask again).`,
		Example: `  hadron secret client issue <secret-id> --reason "deploy window" --max-reads 1
  hadron secret client issue <secret-id> --worker <worker-id> --reason "nightly job" --wait
  hadron secret client issue <secret-id> --mode USE --agent <agent-id> --binding-prefix https://api.example.com/v1/ --binding-header X-Api-Key --reason "nightly sync"`,
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
			use, err := checkUseFlags(fl.Changed, mode, agent, appHolder)
			if err != nil {
				return err
			}
			in := gen.IssueSecretReadClientInput{
				SecretRef: args[0], Reason: reason, HolderKind: gen.GrantHolderKindUser, AutoDelete: autoDelete,
			}
			if fl.Changed("worker") {
				in.HolderKind = gen.GrantHolderKindWorker
				in.HolderRef = &worker
			}
			if use {
				useMode := gen.SecretReadClientModeUse
				in.Mode = &useMode
				if fl.Changed("agent") {
					in.HolderKind = gen.GrantHolderKindAgent
					in.HolderRef = &agent
				} else {
					in.HolderKind = gen.GrantHolderKindApp // no holderRef: the server holds it for the secret's App
				}
				if fl.Changed("binding-prefix") {
					in.BindingPrefix = &bindingPrefix
				}
				if fl.Changed("binding-header") {
					in.BindingHeader = &bindingHeader
				}
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
			if autoDelete {
				// The mutation's answer does not carry the mark, so this states what was ASKED
				// (never what the server recorded). It is printed as soon as the client exists,
				// before the signing step, so a failure after it (the client is left pending
				// with the mark armed) still shows it. stderr only: stdout and the --json shape
				// are unchanged. What revoking does depends on the window (the CLI knows which
				// flags it sent).
				effect := "revoking the client before its last read cancels it (no window)"
				if fl.Changed("expires-at") {
					effect = "revoking the client will not stop it (the window is a signed deadline)"
				}
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "note: --auto-delete was sent: once a person signs this grant, the secret %s is deleted at this client's last read or when its window ends; %s; read the mark back with `hadron secret client list`\n", output.SafeCell(issued.SecretName), effect)
			}

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
	fl.StringVar(&mode, "mode", "", "REVEAL (default) or USE, an injection-only client the holder's runs use by reference")
	fl.StringVar(&agent, "agent", "", "USE holder: an agent installed in the App (its id); exactly one of --agent and --app-holder")
	fl.BoolVar(&appHolder, "app-holder", false, "USE holder: the App itself; exactly one of --agent and --app-holder")
	fl.StringVar(&bindingPrefix, "binding-prefix", "", "USE: the https origin and path prefix the value may be sent to, sent exactly as typed")
	fl.StringVar(&bindingHeader, "binding-header", "", "USE: the header name the value is sent in, sent exactly as typed")
	fl.BoolVar(&autoDelete, "auto-delete", false, "the server deletes the secret at the client's last read or when its window ends (needs --expires-at or --max-reads; with --expires-at a signed deletion survives revoking the client, with only --max-reads revoking cancels it)")
	fl.BoolVar(&wait, "wait", false, "poll the signing request until it ends")
	fl.DurationVar(&waitTimeout, "wait-timeout", defaultSignWait, "max time to wait with --wait")
	return cmd
}

// checkUseFlags decides the mode and refuses (exit 2, before anything is sent)
// the flag combinations that cannot be a USE or a REVEAL issue. It judges which
// flags were given, plus one value rule: a USE --agent must not be blank (it
// would silently fall back to the App holder). The binding values and every
// holder are otherwise the server's to judge, so a blank binding value is sent
// as typed on purpose (cli#3014). --mode is trimmed and case-folded. It reports
// whether this is a USE issue.
func checkUseFlags(changed func(string) bool, mode, agent string, appHolder bool) (bool, error) {
	use := false
	if changed("mode") {
		switch strings.ToUpper(strings.TrimSpace(mode)) {
		case "USE":
			use = true
		case "REVEAL":
		default:
			return false, exitcode.Newf(exitcode.Usage, "--mode must be REVEAL or USE")
		}
	}
	if !use {
		for _, name := range []string{"agent", "app-holder", "binding-prefix", "binding-header"} {
			if changed(name) {
				return false, exitcode.Newf(exitcode.Usage, "--%s is a USE flag: add --mode USE (a REVEAL client is held by you or --worker)", name)
			}
		}
		return false, nil
	}
	if changed("agent") && strings.TrimSpace(agent) == "" {
		return false, exitcode.Newf(exitcode.Usage, "--agent needs a value; omit the flag to leave it unset")
	}
	if changed("worker") {
		return false, exitcode.Newf(exitcode.Usage, "--worker holds a REVEAL client; a USE client takes --agent <agent-id> or --app-holder")
	}
	// Exactly one holder: --agent and --app-holder are given together or not at all.
	if changed("agent") == appHolder {
		return false, exitcode.Newf(exitcode.Usage, "--mode USE needs exactly one holder: --agent <agent-id> or --app-holder")
	}
	// The server requires the signed origin/prefix and the header name for USE.
	if !changed("binding-prefix") || !changed("binding-header") {
		return false, exitcode.Newf(exitcode.Usage, "--mode USE needs both --binding-prefix <https-prefix> and --binding-header <name>")
	}
	return true, nil
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
	// DestroyOnEnd is the auto-delete mark: the secret is deleted at the client's last
	// read or when its window ends (a person must have signed the grant first). It is
	// null only when the server does not report the mark at all (a server older than
	// the mark; the list says so on stderr): never false for "unknown".
	DestroyOnEnd *bool `json:"destroyOnEnd"`
	// RevealURL is the server's own link for a person holder (cli#2758), null for any other viewer.
	RevealURL *string `json:"revealUrl"`
}

// listTier is how much of the list the server has shown it can answer: each field is a separate
// server change, so a server may have the link without the mark or the mark without the link.
type listTier int

const (
	tierBoth     listTier = iota // revealUrl and destroyOnEnd: the first try
	tierLinkOnly                 // the server rejected destroyOnEnd only: the link, the mark unknown
	tierMarkOnly                 // the server rejected revealUrl only: the mark, the link null
	tierNeither                  // the mark-free, link-free page (revoke's lookup)
)

// markKnown and linkKnown say what a page read at this tier carries.
func (t listTier) markKnown() bool { return t == tierBoth || t == tierMarkOnly }

// fetchClientPage reads one page of the caller's read clients (`client list` and the reveal link
// lookup) with as much as the server has: the auto-delete mark (cli#2720) and the server's
// revealUrl (cli#2758). The list is declared non-null with non-null rows: a missing or null list,
// a null row, or (where the mark is read) a row without the mark, is a malformed answer, never an
// empty success (and a dropped row would shift the --all offsets); a row answered without
// destroyOnEnd must not read as "no auto-delete" when the mark is the destructive one.
//
// A server that lacks a field rejects the document at validation naming only the rejected
// fields; then, and only then, the ladder steps down, to the document without exactly the field(s)
// it named: both rejected reads the page with neither (the mark unknown, the link null), only the
// mark leaves the link, only the link leaves the mark. tier is kept so later pages skip the
// documents already rejected.
func fetchClientPage(ctx context.Context, client graphql.Client, tier *listTier, limit, offset *int) ([]clientDTO, error) {
	for {
		var (
			page []clientDTO
			err  error
		)
		switch *tier {
		case tierBoth:
			page, err = fetchBothPage(ctx, client, limit, offset)
		case tierLinkOnly:
			page, err = fetchLinkOnlyPage(ctx, client, limit, offset)
		case tierMarkOnly:
			page, err = fetchMarkOnlyPage(ctx, client, limit, offset)
		default:
			return fetchLookupPage(ctx, client, limit, offset)
		}
		if err == nil {
			return page, nil
		}
		next, stepped := stepDown(*tier, err)
		if !stepped {
			var r rejectedErr
			if errors.As(err, &r) {
				return nil, api.MapErrorKeepingCode(r.err)
			}
			return nil, err
		}
		*tier = next
	}
}

// rejectedErr marks a list failure that is a validation rejection of the fields the list
// documents add, so the ladder may step down on it; it is mapped to the caller's error
// only when no step applies.
type rejectedErr struct{ err error }

func (e rejectedErr) Error() string { return e.err.Error() }

// stepDown is the next tier for a rejected document, or false when the failure is not a
// validation rejection of exactly the field(s) the document at this tier selects beyond the
// next tier's.
func stepDown(t listTier, err error) (listTier, bool) {
	var r rejectedErr
	if !errors.As(err, &r) {
		return t, false
	}
	raw := r.err
	switch t {
	case tierBoth:
		switch {
		case api.IsFrameworkValidationOnlyFor(raw, "destroyOnEnd"):
			return tierLinkOnly, true
		case api.IsFrameworkValidationOnlyFor(raw, "revealUrl"):
			return tierMarkOnly, true
		case api.IsFrameworkValidationOnlyFor(raw, "revealUrl", "destroyOnEnd"):
			return tierNeither, true
		}
	case tierLinkOnly:
		if api.IsFrameworkValidationOnlyFor(raw, "revealUrl") {
			return tierNeither, true
		}
	case tierMarkOnly:
		if api.IsFrameworkValidationOnlyFor(raw, "destroyOnEnd") {
			return tierNeither, true
		}
	}
	return t, false
}

// listFailure is the three list documents' shared failure handling: a GraphQL failure is
// returned as a rejectedErr when the ladder might step down on it, else as the mapped error.
func listFailure(err error) error {
	if api.IsFrameworkValidationOnlyFor(err, "revealUrl", "destroyOnEnd") {
		return rejectedErr{err}
	}
	return api.MapErrorKeepingCode(err)
}

func malformedList() error {
	return exitcode.Newf(exitcode.Error, "server returned no read-client list; the answer is malformed, not empty")
}

func malformedRow() error {
	return exitcode.Newf(exitcode.Error, "server returned an empty read-client row; the answer is malformed")
}

func markMissing() error {
	return exitcode.Newf(exitcode.Error, "server returned a read-client row without its auto-delete mark; the answer is malformed, and a missing mark is not \"no\"")
}

func fetchBothPage(ctx context.Context, client graphql.Client, limit, offset *int) ([]clientDTO, error) {
	resp, err := gen.ListSecretReadClients(ctx, client, limit, offset)
	if err != nil {
		return nil, listFailure(err)
	}
	if resp == nil || resp.MySecretReadClients == nil {
		return nil, malformedList()
	}
	page := make([]clientDTO, 0, len(resp.MySecretReadClients))
	for _, c := range resp.MySecretReadClients {
		if c == nil {
			return nil, malformedRow()
		}
		if c.DestroyOnEnd == nil {
			return nil, markMissing()
		}
		page = append(page, clientDTO{ClientID: c.ClientId, ClientPrefix: c.ClientPrefix, GrantID: c.GrantId, SecretID: c.SecretId,
			SecretName: c.SecretName, SelfIssued: c.SelfIssued, EndReason: c.EndReason, CreatedAt: c.CreatedAt,
			DestroyOnEnd: c.DestroyOnEnd, RevealURL: c.RevealUrl})
	}
	return page, nil
}

func fetchLinkOnlyPage(ctx context.Context, client graphql.Client, limit, offset *int) ([]clientDTO, error) {
	resp, err := gen.ListSecretReadClientsLink(ctx, client, limit, offset)
	if err != nil {
		return nil, listFailure(err)
	}
	if resp == nil || resp.MySecretReadClients == nil {
		return nil, malformedList()
	}
	page := make([]clientDTO, 0, len(resp.MySecretReadClients))
	for _, c := range resp.MySecretReadClients {
		if c == nil {
			return nil, malformedRow()
		}
		page = append(page, clientDTO{ClientID: c.ClientId, ClientPrefix: c.ClientPrefix, GrantID: c.GrantId, SecretID: c.SecretId,
			SecretName: c.SecretName, SelfIssued: c.SelfIssued, EndReason: c.EndReason, CreatedAt: c.CreatedAt, RevealURL: c.RevealUrl})
	}
	return page, nil
}

func fetchMarkOnlyPage(ctx context.Context, client graphql.Client, limit, offset *int) ([]clientDTO, error) {
	resp, err := gen.ListSecretReadClientsMark(ctx, client, limit, offset)
	if err != nil {
		return nil, listFailure(err)
	}
	if resp == nil || resp.MySecretReadClients == nil {
		return nil, malformedList()
	}
	page := make([]clientDTO, 0, len(resp.MySecretReadClients))
	for _, c := range resp.MySecretReadClients {
		if c == nil {
			return nil, malformedRow()
		}
		if c.DestroyOnEnd == nil {
			return nil, markMissing()
		}
		page = append(page, clientDTO{ClientID: c.ClientId, ClientPrefix: c.ClientPrefix, GrantID: c.GrantId, SecretID: c.SecretId,
			SecretName: c.SecretName, SelfIssued: c.SelfIssued, EndReason: c.EndReason, CreatedAt: c.CreatedAt, DestroyOnEnd: c.DestroyOnEnd})
	}
	return page, nil
}

// fetchLookupPage reads one page of the caller's read clients WITHOUT the mark and the link:
// `client revoke`'s exact-id lookup (a safety action must not depend on a display column a
// server may not have) and the fallback for a server that lacks either field. The rows carry
// neither: DestroyOnEnd stays nil (unknown), never false, and RevealURL stays nil.
func fetchLookupPage(ctx context.Context, client graphql.Client, limit, offset *int) ([]clientDTO, error) {
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
was self-issued, whether it carries the auto-delete mark (AUTO-DELETE yes: the
secret is deleted at the client's last read or when its window ends, see
'secret client issue --help'; --json destroyOnEnd), the end reason the server
reported (a dash means the client is live; otherwise SECRET_DELETED, PROTECTED,
REVOKED, PENDING, SPENT, EXPIRED, NOT_STARTED, ISSUER_GONE or ENDED) and when it
was created. The server returns nothing else:
there is no window, read budget or live/spent column here.

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
			// A server that lacks the auto-delete mark or the reveal link rejects the list
			// document: fetchClientPage steps down only as far as the rejection forces, so the
			// field the server does have is kept and the mark is unknown (never "no") when it
			// is missing; the check the recovery advice points at works on such a server.
			tier := tierBoth
			fetch := func(l, o *int) ([]clientDTO, error) { return fetchClientPage(sendCtx, client, &tier, l, o) }
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
			if !tier.markKnown() {
				_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "note: this server does not report the auto-delete mark, so AUTO-DELETE is unknown (? in the table, destroyOnEnd null in --json), not \"no\"")
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, err := fmt.Fprintln(w, "no read clients")
					return err
				}
				t := output.NewTable(w, "CLIENT", "SECRET", "SELF-ISSUED", "AUTO-DELETE", "END REASON", "CREATED", "GRANT")
				for _, r := range rows {
					t.Row(output.SafeCell(clientColumn(r)), output.SafeCell(r.SecretName), fmt.Sprint(r.SelfIssued), markCell(r.DestroyOnEnd),
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

// markCell renders the auto-delete mark: yes, no, or ? when the server did not report it.
func markCell(b *bool) string {
	switch {
	case b == nil:
		return "?"
	case *b:
		return "yes"
	default:
		return "no"
	}
}
