package node

import (
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
	ID             string              `json:"id"`
	NodeID         string              `json:"nodeId"`
	NodeURN        *string             `json:"nodeUrn"`
	Revision       int                 `json:"revision"`
	ApprovalHash   string              `json:"approvalHash"`
	PayloadVersion string              `json:"payloadVersion"`
	State          string              `json:"state"`
	CreatedAt      string              `json:"createdAt"`
	ExpiresAt      string              `json:"expiresAt"`
	EndedAt        *string             `json:"endedAt"`
	NamedSigner    *revisionEditorDTO  `json:"namedSigner"`
	RequestedBy    signingRequesterDTO `json:"requestedBy"`
	Signature      *signatureDTO       `json:"signature"`
}

type signingRequestListDTO struct {
	NodeID   string              `json:"nodeId"`
	URN      string              `json:"urn"`
	Loc      string              `json:"loc"`
	Requests []signingRequestDTO `json:"requests"`
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
		CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, EndedAt: r.EndedAt,
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

// mapSigningError is api.MapError, except that a server without the signing
// surface gets its own message: the generic schema-skew advice (upgrade the
// CLI or the server) is wrong when the server is current and signing is simply
// not offered there.
func mapSigningError(err error) error {
	for _, name := range signingFields {
		if api.IsGraphQLValidationFor(err, name) {
			return exitcode.Newf(exitcode.Usage, "this server does not offer node signing")
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
signing page, and agents may ask but never sign.

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
				return mapSigningError(err)
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

With --check the command exits 5 after printing unless the roll-up state is
CURRENT and there are no integrity problems.`,
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
				return mapSigningError(err)
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
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				state := "none"
				if dto.State != nil {
					state = *dto.State
				}
				fmt.Fprintf(w, "%s (revision %d)\n  signatures: %s\n", dto.URN, dto.Revision, state)
				for _, s := range dto.Signatures {
					renderSignature(w, "    ", s)
				}
				if dto.IntegrityProblems > 0 {
					fmt.Fprintf(w, "  integrity problems: %d (signature records that failed verification)\n", dto.IntegrityProblems)
				}
				return nil
			}); err != nil {
				return err
			}
			if check && (dto.State == nil || *dto.State != string(gen.SignatureStateCurrent) || dto.IntegrityProblems > 0) {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare node loc against")
	cmd.Flags().BoolVar(&check, "check", false, "exit 5 unless a current signature exists and no record fails verification")
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.GetSigningRequest(cmd.Context(), client, args[0])
			if err != nil {
				return mapSigningError(err)
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
	var memory, state string
	var limit int
	cmd := &cobra.Command{
		Use:     "list <node-ref>",
		Aliases: []string{"ls"},
		Short:   "List a node's signing requests, newest first",
		Long: `List the node's signing requests, newest first, including ended ones.
--state filters on the effective state: an open request past its deadline
reads EXPIRED. --limit is 1-100 (server default 20).`,
		Example: `  hadron node signing-request list hrn:node:hadronmemory.com:dev:decisions:dk-001
  hadron node signing-request list decisions:dk-001 -m hrn:mem:hadronmemory.com:dev --state open --json`,
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
			var first *int
			if cmd.Flags().Changed("limit") {
				if limit < 1 || limit > 100 {
					return exitcode.Newf(exitcode.Usage, "--limit must be between 1 and 100")
				}
				first = &limit
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.NodeSigningRequests(cmd.Context(), client, id, statePtr, first)
			if err != nil {
				return mapSigningError(err)
			}
			if resp.Node == nil {
				return exitcode.Newf(exitcode.NotFound, "node %s not found or not readable by you", args[0])
			}
			n := resp.Node
			dto := signingRequestListDTO{NodeID: n.Id, URN: n.Urn, Loc: n.Loc, Requests: []signingRequestDTO{}}
			for _, r := range n.SigningRequests {
				if r != nil {
					dto.Requests = append(dto.Requests, signingRequestFrom(&r.SigningRequestFields))
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Requests) == 0 {
					fmt.Fprintf(w, "%s: no signing requests\n", dto.URN)
					return nil
				}
				t := output.NewTable(w, "ID", "STATE", "REVISION", "SIGNER", "CREATED", "EXPIRES")
				for _, r := range dto.Requests {
					signer := "any writer"
					if r.NamedSigner != nil {
						signer = signerLabel(r.NamedSigner)
					}
					t.Row(output.SafeCell(r.ID), output.SafeCell(r.State), fmt.Sprint(r.Revision), output.SafeCell(signer), output.SafeCell(r.CreatedAt), output.SafeCell(r.ExpiresAt))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare node loc against")
	cmd.Flags().StringVar(&state, "state", "", "only requests in this effective state (open, completed, declined, expired, stale, withdrawn)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum requests to return, 1-100 (server default 20)")
	return cmd
}

func newCmdSigningRequestWithdraw(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "withdraw <request-id>",
		Short: "Withdraw an open signing request you made",
		Long: `Withdraw an open signing request. Only the account that made it may
withdraw it, from any of its sessions. An already-ended request is refused
(exit 5) and stays as it ended.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.WithdrawSigningRequest(cmd.Context(), client, args[0])
			if err != nil {
				return mapSigningError(err)
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
