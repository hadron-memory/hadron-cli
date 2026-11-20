package node

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// Passkey-signed node revisions (hadron-server#1632, contract #1864; cli#818).
// The CLI asks for a signature and reads the outcome; it never produces one.
// No operation here takes an assertion or key material: a person signs on the
// server-hosted page, and only a passkey's label is ever shown.

type signatureDTO struct {
	ID               string             `json:"id"`
	State            string             `json:"state"`
	Revision         int                `json:"revision"`
	VerifiedAt       string             `json:"verifiedAt"`
	Signer           *revisionEditorDTO `json:"signer"`
	PasskeyLabel     string             `json:"passkeyLabel"`
	PasskeyRevokedAt *string            `json:"passkeyRevokedAt"`
	PayloadVersion   string             `json:"payloadVersion"`
	SigningRequestID string             `json:"signingRequestId"`
	// LocalCheck is the result of the local v3 payload rebuild (cli#879).
	// Present only with --check; it never claims verification.
	LocalCheck *string `json:"localCheck,omitempty"`
}

type signatureStatusDTO struct {
	NodeID            string         `json:"nodeId"`
	URN               string         `json:"urn"`
	Loc               string         `json:"loc"`
	Revision          int            `json:"revision"`
	State             *string        `json:"state"`
	IntegrityProblems int            `json:"integrityProblems"`
	Signatures        []signatureDTO `json:"signatures"`
}

type signingRequesterDTO struct {
	UserID    *string `json:"userId"`
	AppID     *string `json:"appId"`
	WorkerID  *string `json:"workerId"`
	SessionID *string `json:"sessionId"`
}

type signingRequestDTO struct {
	ID             string  `json:"id"`
	NodeID         string  `json:"nodeId"`
	NodeURN        *string `json:"nodeUrn"`
	Revision       int     `json:"revision"`
	ApprovalHash   string  `json:"approvalHash"`
	PayloadVersion string  `json:"payloadVersion"`
	State          string  `json:"state"`
	// CeremonyURL is the server's own link to the ceremony page, copied
	// byte for byte and never composed (cli#878 part 2). Carried for every
	// state in --json; text prints it only while the request is OPEN.
	CeremonyURL string              `json:"ceremonyUrl"`
	CreatedAt   string              `json:"createdAt"`
	ExpiresAt   string              `json:"expiresAt"`
	EndedAt     *string             `json:"endedAt"`
	NamedSigner *revisionEditorDTO  `json:"namedSigner"`
	RequestedBy signingRequesterDTO `json:"requestedBy"`
	Signature   *signatureDTO       `json:"signature"`
}

type signingRequestListDTO struct {
	NodeID   string              `json:"nodeId"`
	URN      string              `json:"urn"`
	Loc      string              `json:"loc"`
	Requests []signingRequestDTO `json:"requests"`
	// HasMore and NextCursor are the page facts the server returned (cli#2032).
	HasMore    bool    `json:"hasMore"`
	NextCursor *string `json:"nextCursor"`
}

func signerFrom(handle, urn *string) *revisionEditorDTO {
	if handle == nil && urn == nil {
		return nil
	}
	return &revisionEditorDTO{Handle: handle, URN: urn}
}

func signatureFrom(s *gen.NodeSignatureFields) signatureDTO {
	d := signatureDTO{
		ID: s.Id, State: string(s.State), Revision: s.Revision, VerifiedAt: s.VerifiedAt,
		PasskeyLabel: s.PasskeyLabel, PasskeyRevokedAt: s.PasskeyRevokedAt,
		PayloadVersion: s.PayloadVersion, SigningRequestID: s.SigningRequestId,
	}
	if s.Signer != nil {
		d.Signer = signerFrom(s.Signer.Handle, s.Signer.Urn)
	}
	return d
}

func signingRequestFrom(r *gen.SigningRequestFields) signingRequestDTO {
	d := signingRequestDTO{
		ID: r.Id, NodeID: r.NodeId, NodeURN: r.NodeUrn, Revision: r.Revision,
		ApprovalHash: r.ApprovalHash, PayloadVersion: r.PayloadVersion, State: string(r.State),
		CeremonyURL: r.CeremonyUrl, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, EndedAt: r.EndedAt,
	}
	if r.NamedSigner != nil {
		d.NamedSigner = signerFrom(r.NamedSigner.Handle, r.NamedSigner.Urn)
	}
	if r.RequestedBy != nil {
		d.RequestedBy = signingRequesterDTO{
			UserID: r.RequestedBy.UserId, AppID: r.RequestedBy.AppId,
			WorkerID: r.RequestedBy.WorkerId, SessionID: r.RequestedBy.SessionId,
		}
	}
	if r.Signature != nil {
		s := signatureFrom(&r.Signature.NodeSignatureFields)
		d.Signature = &s
	}
	return d
}

// signingFields are the schema names a signing operation can be refused by
// when the server has no signing surface: one that predates #1632, or one
// with its signing switch off (hadron-server#1895), which answers exactly as
// if the feature did not exist.
var signingFields = []string{
	"requestNodeSignature", "withdrawSigningRequest", "signingRequest", "signingRequests",
	"signatureStatus", "SigningRequest", "SigningRequestState", "NodeSignature", "NodeSignatureStatus",
}

// ceremonyTokenProbe answers whether the server advertises the `node-signing`
// switch (GET /capabilities), for mapSigningError's old-server branch. It is
// built per command and runs only when a ceremonyUrl selection has already
// failed validation — nowhere else — so a switched-off server pays no extra
// round trip (cli#878 part 2, token-first per cor:api:020:01).
type ceremonyTokenProbe func() (bool, error)

// newCeremonyTokenProbe wires the probe to the command's server and the
// package's injectable SigningCapabilityProbe (the node get fold's seam).
func newCeremonyTokenProbe(ctx context.Context, f *cmdutil.Factory) ceremonyTokenProbe {
	return func() (bool, error) {
		server, err := f.Server()
		if err != nil {
			return false, err
		}
		return SigningCapabilityProbe(ctx, server)
	}
}

// The three mapSigningError refusals. Named so the tests assert against the
// message itself, not a hard-coded copy of its most distinctive fragment:
// the wording lives here, once. Exported on purpose: package cmd's tests read
// them, and Go has no cross-package export_test. They reach exitcode.Newf as
// "%s" arguments, never as format strings, so a future "%" in the wording is
// safe.
const (
	PagedListRefusal     = "this server does not offer the paged signing-request list; upgrade the server (hadron-server#4123)"
	CeremonyPagesRefusal = "this server does not offer signing-request ceremony pages; upgrade the server (hadron-server#3120)"
	SigningOffRefusal    = "this server does not offer node signing"
)

// requestIsOpen reports the one state whose link still leads somewhere: an
// ended request's link only explains that it ended (the schema's own words),
// so text prints it while OPEN only.
func requestIsOpen(state string) bool {
	return state == string(gen.SigningRequestStateOpen)
}

// ceremonyText renders the ceremony URL for text mode. The rule is
// "display-clean => verbatim, at any length": a value whose runes are all
// display-neutral-clean (the same classification as table cells) prints as
// is. Anything else is a server string that must not reach the terminal raw
// (an OSC 8 hyperlink can hide the real target, OSC 52 writes the clipboard,
// an embedded newline forges lines; bidi and other format characters are
// neutralised too), so it goes through the same neutralisation as a table
// cell: still readable, never executable. There is deliberately no url.Parse
// and no scheme test: a clean non-web value (javascript:alert(1), ftp://h/x)
// is byte-identical through SafeCell, so classifying it would change no
// output. No signing command opens a ceremony URL (sign.go has no exec or
// browser path); auth/passkey_revoke_page.go is only the platform's other
// URL-opening surface, where a scheme gate acts. "Copied verbatim, never
// composed" is about not building the URL; --json carries the raw value
// either way.
func ceremonyText(u string) string {
	if !strings.ContainsFunc(u, output.Neutralised) {
		return u
	}
	return output.SafeCell(u)
}

// mapSigningError is api.MapError, except that a server without the signing
// surface gets its own message: the generic schema-skew advice (upgrade the
// CLI or the server) is wrong when the server is current and signing is simply
// not offered there.
func mapSigningError(err error, ceremonyToken ceremonyTokenProbe) error {
	// A server that has signing but not the paged list (it predates
	// hadron-server#4123) is told so: "does not offer node signing" would be
	// wrong, and there is no fallback to the unpaged read (cli#2032).
	if api.IsGraphQLValidationFor(err, "signingRequestsPage") {
		return exitcode.Newf(exitcode.Usage, "%s", PagedListRefusal)
	}
	// SigningRequest.ceremonyUrl is newer than the rest of the surface
	// (hadron-server#3120), so a server can have signing and still refuse
	// this one field. The refusal alone cannot tell an older server from a
	// switched-off one — off removes the whole surface, and its errors can
	// quote "SigningRequest" in the type position of the very same sentence —
	// so the capability token decides first (cor:api:020:01: test for a
	// token, never compare versions). The rejected-field position is read,
	// never any quoted name, so a surface refusal cannot land here. Token
	// present: the bounded old-server answer. No token, or discovery failed
	// (which reads as not-advertised): the shipped signing-off answer — a
	// server that may simply be off never gets upgrade text. A nil probe
	// (no factory at hand) gets the generic mapping, never a guess between
	// the two answers.
	if api.IsFrameworkValidationOnlyFor(err, "ceremonyUrl") {
		if ceremonyToken == nil {
			return api.MapError(err)
		}
		if advertised, perr := ceremonyToken(); perr == nil && advertised {
			return exitcode.Newf(exitcode.Usage, "%s", CeremonyPagesRefusal)
		}
		return exitcode.Newf(exitcode.Usage, "%s", SigningOffRefusal)
	}
	for _, name := range signingFields {
		if api.IsGraphQLValidationFor(err, name) {
			return exitcode.Newf(exitcode.Usage, "%s", SigningOffRefusal)
		}
	}
	return api.MapError(err)
}

func signerLabel(e *revisionEditorDTO) string {
	if e != nil {
		if e.Handle != nil && *e.Handle != "" {
			return "@" + *e.Handle
		}
		if e.URN != nil && *e.URN != "" {
			return *e.URN
		}
	}
	return "an unidentified signer"
}

func renderSignature(w io.Writer, indent string, s signatureDTO) {
	fmt.Fprintf(w, "%s%s  r%d  %s  passkey %q  verified %s\n", indent, s.State, s.Revision, signerLabel(s.Signer), s.PasskeyLabel, s.VerifiedAt)
	if s.PasskeyRevokedAt != nil {
		fmt.Fprintf(w, "%s  passkey revoked at %s\n", indent, *s.PasskeyRevokedAt)
	}
}

func renderSigningRequest(w io.Writer, r signingRequestDTO) {
	node := r.NodeID
	if r.NodeURN != nil && *r.NodeURN != "" {
		node = *r.NodeURN
	}
	fmt.Fprintf(w, "%s  %s\n", r.ID, r.State)
	fmt.Fprintf(w, "  node: %s revision %d\n", node, r.Revision)
	if r.NamedSigner != nil {
		fmt.Fprintf(w, "  signer: %s\n", signerLabel(r.NamedSigner))
	} else {
		fmt.Fprintln(w, "  signer: any writer of the node's memory")
	}
	fmt.Fprintf(w, "  created: %s\n  expires: %s\n", r.CreatedAt, r.ExpiresAt)
	// The handoff: the server's own link, verbatim, only while the request
	// can still be signed (cli#878 part 2).
	if requestIsOpen(r.State) && r.CeremonyURL != "" {
		fmt.Fprintf(w, "  ceremony: %s\n", ceremonyText(r.CeremonyURL))
	}
	if r.EndedAt != nil {
		fmt.Fprintf(w, "  ended: %s\n", *r.EndedAt)
	}
	if r.Signature != nil {
		renderSignature(w, "  signature: ", *r.Signature)
	}
}

func newCmdSign(f *cmdutil.Factory) *cobra.Command {
	var memory, signer string
	var revision int
	cmd := &cobra.Command{
		Use:   "sign <node-ref>",
		Short: "Ask for a passkey signature on a node's current content",
		Long: `Create a signing request for the node's current revision. The CLI never
signs: a person completes the request with their passkey on the server's
signing page — the request prints that page's link, the server's own URL,
while it is open — and agents may ask but never sign.

Any reader of the node may ask. Without --signer, any writer of the node's
memory may sign; --signer names one user (by id), pinned for the life of the
request. Pass --revision from a prior node read to refuse if the node changed
since you saw it. The request ends exactly once: completed, declined,
withdrawn, expired, or stale when the title, abstract or content changes.
Follow it with ` + "`node signing-request get <id>`" + `.`,
		Example: `  hadron node sign hrn:node:hadronmemory.com:dev:decisions:dk-001 --revision 7
  hadron node sign decisions:dk-001 -m hrn:mem:hadronmemory.com:dev --signer <user-id> --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("revision") && revision < 1 {
				return exitcode.Newf(exitcode.Usage, "--revision must be a positive node revision")
			}
			// Passed-but-blank is not absent: --signer="$UNSET" must not
			// silently widen the request to any writer of the memory.
			signer = strings.TrimSpace(signer)
			if cmd.Flags().Changed("signer") && signer == "" {
				return exitcode.Newf(exitcode.Usage, "--signer was given an empty value (an unset variable?); name a user id, or omit it to let any writer sign")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			var expected *int
			if cmd.Flags().Changed("revision") {
				expected = &revision
			}
			var signerRef *string
			if cmd.Flags().Changed("signer") {
				signerRef = &signer
			}
			resp, err := gen.RequestNodeSignature(cmd.Context(), client, id, expected, signerRef)
			if err != nil {
				return mapSigningError(err, newCeremonyTokenProbe(cmd.Context(), f))
			}
			if resp.RequestNodeSignature == nil {
				return exitcode.Newf(exitcode.Error, "server returned no signing request")
			}
			dto := signingRequestFrom(&resp.RequestNodeSignature.SigningRequestFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprint(w, "signing request ")
				renderSigningRequest(w, dto)
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare node loc against")
	cmd.Flags().IntVar(&revision, "revision", 0, "request only if this previously read revision is still current")
	cmd.Flags().StringVar(&signer, "signer", "", "the one user (id) who may sign; default any writer of the memory")
	return cmd
}

func newCmdSignatures(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var check bool
	cmd := &cobra.Command{
		Use:   "signatures <node-ref>",
		Short: "Show a node's passkey signatures, re-verified on read",
		Long: `List the node's signatures and their states, current signers first. The
server re-verifies every record on each read; a record that fails is counted
as an integrity problem and never listed as a signature.

States: CURRENT (verifies, content unchanged since), EARLIER (verifies, but
the content changed since), PENDING (a first passkey still in its cooling-off
window), CANCELLED (that passkey was revoked inside its window),
NOT_APPLICABLE (an encrypted memory). The roll-up state is empty when the node
has no signatures.

With --check the command first rebuilds each signature's v3 payload and
challenge locally and checks that its inputs agree (localCheck:
PAYLOAD_CONSISTENT, HASH_MISMATCH, UNSUPPORTED_VERSION for any record that is
not v3, UNAVAILABLE when its inputs cannot be read). The server's state is
shown unchanged. It then exits 5 after printing unless the roll-up state is
CURRENT and there are no integrity problems, or if any record is HASH_MISMATCH
or UNSUPPORTED_VERSION.`,
		Example: `  hadron node signatures hrn:node:hadronmemory.com:dev:decisions:dk-001
  hadron node signatures decisions:dk-001 -m hrn:mem:hadronmemory.com:dev --check --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.NodeSignatureStatus(cmd.Context(), client, id)
			if err != nil {
				return mapSigningError(err, newCeremonyTokenProbe(cmd.Context(), f))
			}
			if resp.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node %s not found or not readable by you", args[0])
			}
			n := resp.Node
			dto := signatureStatusDTO{NodeID: n.Id, URN: n.Urn, Loc: n.Loc, Revision: n.Revision, Signatures: []signatureDTO{}}
			if n.SignatureStatus != nil {
				s := n.SignatureStatus.NodeSignatureStatusFields
				if s.State != nil {
					st := string(*s.State)
					dto.State = &st
				}
				dto.IntegrityProblems = s.IntegrityProblems
				for _, sig := range s.Signatures {
					if sig != nil {
						dto.Signatures = append(dto.Signatures, signatureFrom(&sig.NodeSignatureFields))
					}
				}
			}
			localFailed := false
			if check && len(dto.Signatures) > 0 {
				lc := newLocalChecker(cmd.Context(), client, id, n.Id)
				lc.ceremonyToken = newCeremonyTokenProbe(cmd.Context(), f)
				for i := range dto.Signatures {
					res, err := lc.check(dto.Signatures[i])
					if err != nil {
						return err
					}
					dto.Signatures[i].LocalCheck = &res
					localFailed = localFailed || localFails(res)
				}
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				state := "none"
				if dto.State != nil {
					state = *dto.State
				}
				fmt.Fprintf(w, "%s (revision %d)\n  signatures: %s\n", dto.URN, dto.Revision, state)
				for _, s := range dto.Signatures {
					renderSignature(w, "    ", s)
					if s.LocalCheck != nil {
						fmt.Fprintf(w, "      local check: %s", *s.LocalCheck)
						if *s.LocalCheck == localPayloadConsistent {
							fmt.Fprintf(w, "; %s", localCheckHumanLine)
						}
						fmt.Fprintln(w)
					}
				}
				if dto.IntegrityProblems > 0 {
					fmt.Fprintf(w, "  integrity problems: %d (signature records that failed verification)\n", dto.IntegrityProblems)
				}
				return nil
			}); err != nil {
				return err
			}
			if check && (localFailed || dto.State == nil || *dto.State != string(gen.SignatureStateCurrent) || dto.IntegrityProblems > 0) {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare node loc against")
	cmd.Flags().BoolVar(&check, "check", false, checkFlagUsage+" Exits 5 unless a current signature exists and no record fails verification, or if a record is HASH_MISMATCH or UNSUPPORTED_VERSION.")
	return cmd
}

func newCmdSigningRequest(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "signing-request <command>",
		Aliases: []string{"signing-requests"},
		Short:   "Inspect and withdraw a node's signing requests",
		Long: `A signing request (created by ` + "`node sign`" + `) asks a person to sign one node
revision with their passkey. These commands read a request, list a node's
requests, and withdraw an open one. A request is addressed by its id.`,
	}
	cmd.AddCommand(newCmdSigningRequestGet(f))
	cmd.AddCommand(newCmdSigningRequestList(f))
	cmd.AddCommand(newCmdSigningRequestWithdraw(f))
	return cmd
}

func newCmdSigningRequestGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <request-id>",
		Short: "Show one signing request and, once completed, its signature",
		Long: `Show one signing request, named by its id (from node signing-request list or
the output of node sign): its node and revision, its state (OPEN, COMPLETED,
DECLINED, WITHDRAWN, EXPIRED or STALE) and, once a person has completed it,
the signature. An OPEN request also prints "ceremony: <url>" — the server's
own link to the page where a person completes it, printed only while the
request can still be signed (--json carries ceremonyUrl for every state).`,
		Example: `  hadron node signing-request get 01a18a0b2c3d7e8f9a0b1c2d3e4f5a6b --json`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetSigningRequest(cmd.Context(), client, args[0])
			if err != nil {
				return mapSigningError(err, newCeremonyTokenProbe(cmd.Context(), f))
			}
			if resp.SigningRequest == nil {
				return exitcode.Newf(exitcode.NotFound, "signing request %s not found or not readable by you", args[0])
			}
			dto := signingRequestFrom(&resp.SigningRequest.SigningRequestFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				renderSigningRequest(w, dto)
				return nil
			})
		},
	}
}

// signingRequestStates are the --state values, in the server's enum order.
var signingRequestStates = []gen.SigningRequestState{
	gen.SigningRequestStateOpen, gen.SigningRequestStateCompleted, gen.SigningRequestStateDeclined,
	gen.SigningRequestStateExpired, gen.SigningRequestStateStale, gen.SigningRequestStateWithdrawn,
}

func parseSigningRequestState(v string) (gen.SigningRequestState, bool) {
	for _, s := range signingRequestStates {
		if strings.EqualFold(v, string(s)) {
			return s, true
		}
	}
	return "", false
}

func newCmdSigningRequestList(f *cmdutil.Factory) *cobra.Command {
	var memory, state, after string
	var limit int
	cmd := &cobra.Command{
		Use:     "list <node-ref> [--state <s>] [--limit <n>] [--after <cursor>]",
		Aliases: []string{"ls"},
		Short:   "List one page of a node's signing requests, newest first",
		Long: `List one page of the node's signing requests, newest first, including ended
ones. One call shows one page: 20 requests by default, at most 100 (a larger
--limit is clamped by the server, not refused here). --state filters on the
effective state: an open request past its deadline reads EXPIRED.

When there are more, the output ends with "has more: true" and a "next cursor"
line; pass that value to --after to read the next page, exactly as printed
(--json carries the same value in nextCursor), and give the same --state again.
Each page is a snapshot at the time of the call; an empty page means no
requests on that page. Each OPEN row's ceremony link follows the table as
"<id> ceremony: <url>" (an ended row prints none); --json adds ceremonyUrl on
every row, and hasMore and nextCursor (null when there is no further
page) beside requests.`,
		Example: `  hadron node signing-request list hrn:node:hadronmemory.com:dev:decisions:dk-001
  hadron node signing-request list decisions:dk-001 -m hrn:mem:hadronmemory.com:dev --state open --json
  hadron node signing-request list decisions:dk-001 -m hrn:mem:hadronmemory.com:dev --state open --after <next cursor from the previous page>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var statePtr *gen.SigningRequestState
			if cmd.Flags().Changed("state") {
				s, ok := parseSigningRequestState(state)
				if !ok {
					names := make([]string, len(signingRequestStates))
					for i, s := range signingRequestStates {
						names[i] = strings.ToLower(string(s))
					}
					return exitcode.Newf(exitcode.Usage, "--state must be one of %s", strings.Join(names, ", "))
				}
				statePtr = &s
			}
			first, err := cmdutil.PageLimit(cmd, limit)
			if err != nil {
				return err
			}
			afterPtr, err := cmdutil.PageAfter(cmd, after)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.NodeSigningRequests(cmd.Context(), client, id, statePtr, first, afterPtr)
			if err != nil {
				return mapSigningError(err, newCeremonyTokenProbe(cmd.Context(), f))
			}
			if resp.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node %s not found or not readable by you", args[0])
			}
			n := resp.Node
			if n.SigningRequestsPage == nil {
				return exitcode.Newf(exitcode.Error, "server returned no signing-request page")
			}
			page := n.SigningRequestsPage
			if err := cmdutil.CheckPage(page.HasMore, page.NextCursor, afterPtr); err != nil {
				return err
			}
			dto := signingRequestListDTO{NodeID: n.Id, URN: n.Urn, Loc: n.Loc, Requests: []signingRequestDTO{}, HasMore: page.HasMore, NextCursor: page.NextCursor}
			for _, r := range page.Items {
				if r != nil {
					dto.Requests = append(dto.Requests, signingRequestFrom(&r.SigningRequestFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Requests) == 0 {
					// With --state the message names the filter: an empty filtered
					// page does not mean no requests exist (cli#2032 ruling).
					what := "no signing requests"
					if statePtr != nil {
						what = "no " + strings.ToLower(string(*statePtr)) + " signing requests"
					}
					if _, err := fmt.Fprintf(w, "%s: %s on this page\n", dto.URN, what); err != nil {
						return err
					}
				} else {
					t := output.NewTable(w, "ID", "STATE", "REVISION", "SIGNER", "CREATED", "EXPIRES")
					for _, r := range dto.Requests {
						signer := "any writer"
						if r.NamedSigner != nil {
							signer = signerLabel(r.NamedSigner)
						}
						t.Row(output.SafeCell(r.ID), output.SafeCell(r.State), fmt.Sprint(r.Revision), output.SafeCell(signer), output.SafeCell(r.CreatedAt), output.SafeCell(r.ExpiresAt))
					}
					if err := t.Flush(); err != nil {
						return err
					}
					// The table cannot hold URLs: each OPEN row's ceremony link
					// follows under it, id-qualified, verbatim — ended rows print
					// none (their link only explains that they ended).
					for _, r := range dto.Requests {
						if requestIsOpen(r.State) && r.CeremonyURL != "" {
							if _, err := fmt.Fprintf(w, "%s ceremony: %s\n", output.SafeCell(r.ID), ceremonyText(r.CeremonyURL)); err != nil {
								return err
							}
						}
					}
				}
				if _, err := fmt.Fprintf(w, "has more: %t\n", dto.HasMore); err != nil {
					return err
				}
				if dto.NextCursor != nil {
					if _, err := fmt.Fprintf(w, "next cursor: %s\n", output.SafeCell(*dto.NextCursor)); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare node loc against")
	cmd.Flags().StringVar(&state, "state", "", "only requests in this effective state (open, completed, declined, expired, stale, withdrawn)")
	cmd.Flags().IntVar(&limit, "limit", 0, "requests per page (default 20; the server clamps to 100)")
	cmd.Flags().StringVar(&after, "after", "", "continue after this cursor, exactly as a previous page printed it")
	return cmd
}

func newCmdSigningRequestWithdraw(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw <request-id>",
		Short: "Withdraw an open signing request you made",
		Long: `Withdraw an open signing request. Only the account that made it may
withdraw it, from any of its sessions. An already-ended request is refused
(exit 5) and stays as it ended. The output shows the ended request; no
ceremony line prints (an ended request's link only explains that it ended),
and --json carries ceremonyUrl as the server returned it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.WithdrawSigningRequest(cmd.Context(), client, args[0])
			if err != nil {
				return mapSigningError(err, newCeremonyTokenProbe(cmd.Context(), f))
			}
			if resp.WithdrawSigningRequest == nil {
				return exitcode.Newf(exitcode.Error, "server returned no signing request")
			}
			dto := signingRequestFrom(&resp.WithdrawSigningRequest.SigningRequestFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprint(w, "withdrew signing request ")
				renderSigningRequest(w, dto)
				return nil
			})
		},
	}
}
