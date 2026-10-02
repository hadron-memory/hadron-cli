package node

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

// approvalDTO is the latest server-recorded approval. It is deliberately
// separate from a content-validation stamp: an approval covers exactly one
// node revision, and a newer revision never inherits it.
type approvalDTO struct {
	Revision       int                `json:"revision"`
	ApprovedAt     string             `json:"approvedAt"`
	ApprovedBy     *revisionEditorDTO `json:"approvedBy"`
	ApprovedByInfo *string            `json:"approvedByInfo"`
	Hash           string             `json:"hash"`
}

type approvalStatusDTO struct {
	State    string       `json:"state"`
	Approval *approvalDTO `json:"approval"`
}

type mintStatusDTO struct {
	Minted       bool               `json:"minted"`
	MintedAt     *string            `json:"mintedAt"`
	MintedBy     *revisionEditorDTO `json:"mintedBy"`
	MintedByInfo *string            `json:"mintedByInfo"`
	Revision     *int               `json:"revision"`
	Hash         *string            `json:"hash"`
}

func mintStatusFrom(s *gen.NodeMintStatusFields) *mintStatusDTO {
	if s == nil {
		return nil
	}
	d := &mintStatusDTO{Minted: s.Minted, MintedAt: s.MintedAt, MintedByInfo: s.MintedByInfo, Revision: s.Revision, Hash: s.Hash}
	if s.MintedBy != nil {
		d.MintedBy = &revisionEditorDTO{Handle: s.MintedBy.Handle, URN: s.MintedBy.Urn}
	}
	return d
}

func renderMintStatus(w io.Writer, indent string, s *mintStatusDTO) {
	if s == nil {
		fmt.Fprintf(w, "%smint: unknown (server predates per-node minting)\n", indent)
		return
	}
	if !s.Minted {
		fmt.Fprintf(w, "%smint: unminted\n", indent)
		return
	}
	if s.Revision == nil {
		fmt.Fprintf(w, "%smint: minted (backfilled; revision unknown)\n", indent)
		return
	}
	fmt.Fprintf(w, "%smint: minted revision %d\n", indent, *s.Revision)
}

type approvalReceiptDTO struct {
	NodeID   string      `json:"nodeId"`
	URN      string      `json:"urn"`
	Loc      string      `json:"loc"`
	Approval approvalDTO `json:"approval"`
}

type integrityDTO struct {
	NodeID           string  `json:"nodeId"`
	URN              string  `json:"urn"`
	Loc              string  `json:"loc"`
	State            string  `json:"state"`
	Revision         int     `json:"revision"`
	ApprovedRevision *int    `json:"approvedRevision"`
	ExpectedHash     *string `json:"expectedHash"`
	ActualHash       string  `json:"actualHash"`
}

func approvalFrom(a *gen.NodeApprovalFields) *approvalDTO {
	if a == nil {
		return nil
	}
	d := &approvalDTO{
		Revision: a.Revision, ApprovedAt: a.ApprovedAt,
		ApprovedByInfo: a.ApprovedByInfo, Hash: a.Hash,
	}
	if a.ApprovedBy != nil {
		d.ApprovedBy = &revisionEditorDTO{Handle: a.ApprovedBy.Handle, URN: a.ApprovedBy.Urn}
	}
	return d
}

func approvalStatusFrom(s *gen.NodeApprovalStatusFields) *approvalStatusDTO {
	if s == nil {
		return nil
	}
	d := &approvalStatusDTO{State: string(s.State)}
	if s.Approval != nil {
		d.Approval = approvalFrom(&s.Approval.NodeApprovalFields)
	}
	return d
}

func newCmdApprove(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var revision int
	cmd := &cobra.Command{
		Use:   "approve <node-ref>",
		Short: "Approve a node's current revision",
		Long: `Record approval of the node's current revision. The server records the
approver, time and full SHA-256 of the title, abstract and content. A later
edit creates a new revision and leaves that new revision unapproved.

Pass --revision from a prior node read to refuse if the node changed since
you saw it. Without that flag, the server approves the revision current when
it acquires the node lock. Repeating approval of the same revision leaves the
existing approval unchanged.`,
		Example: `  hadron node approve hrn:node:hadronmemory.com:dev:instructions --revision 14
  hadron node approve instructions -m hrn:mem:hadronmemory.com:dev --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("revision") && revision < 1 {
				return exitcode.Newf(exitcode.Usage, "--revision must be a positive node revision")
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
			resp, err := gen.ApproveNode(cmd.Context(), client, id, expected)
			if err != nil {
				return api.MapError(err)
			}
			if resp.ApproveNode == nil || resp.ApproveNode.Approval == nil {
				return exitcode.Newf(exitcode.Error, "server returned no node approval")
			}
			r := resp.ApproveNode
			dto := approvalReceiptDTO{NodeID: r.NodeId, URN: r.Urn, Loc: r.Loc, Approval: *approvalFrom(&r.Approval.NodeApprovalFields)}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "approved %s revision %d by %s at %s\n  hash: %s\n",
					dto.URN, dto.Approval.Revision, approverLabel(dto.Approval), dto.Approval.ApprovedAt, dto.Approval.Hash)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare node loc against")
	cmd.Flags().IntVar(&revision, "revision", 0, "approve only this previously read node revision")
	return cmd
}

func newCmdVerify(f *cmdutil.Factory) *cobra.Command {
	var memory string
	cmd := &cobra.Command{
		Use:   "verify <node-ref>",
		Short: "Check a node's current content against its recorded approval",
		Long: `Recompute the server's approval hash over the current title, abstract
and content. INTACT means the current revision is approved and still matches.
TAMPERED means an approved revision changed outside revisioned editing;
SUPERSEDED means a legitimate edit advanced the revision after approval.
NOT_APPROVED means no approval exists. The command exits 5 for every state
other than INTACT, after printing the result.`,
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
			resp, err := gen.VerifyNode(cmd.Context(), client, id)
			if err != nil {
				return api.MapError(err)
			}
			if resp.VerifyNode == nil {
				return exitcode.Newf(exitcode.Error, "server returned no node integrity check")
			}
			r := resp.VerifyNode
			dto := integrityDTO{
				NodeID: r.NodeId, URN: r.Urn, Loc: r.Loc, State: string(r.State),
				Revision: r.Revision, ApprovedRevision: r.ApprovedRevision,
				ExpectedHash: r.ExpectedHash, ActualHash: r.ActualHash,
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "%s: %s (revision %d)\n", dto.URN, dto.State, dto.Revision); err != nil {
					return err
				}
				if dto.ApprovedRevision != nil {
					fmt.Fprintf(w, "  approved revision: %d\n", *dto.ApprovedRevision)
				}
				if dto.ExpectedHash != nil {
					fmt.Fprintf(w, "  approved hash: %s\n", *dto.ExpectedHash)
				}
				_, err := fmt.Fprintf(w, "  current hash:  %s\n", dto.ActualHash)
				return err
			}); err != nil {
				return err
			}
			if r.State != gen.NodeIntegrityStateIntact {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory to resolve a bare node loc against")
	return cmd
}

func approverLabel(a approvalDTO) string {
	if a.ApprovedBy != nil {
		if a.ApprovedBy.Handle != nil && *a.ApprovedBy.Handle != "" {
			return "@" + *a.ApprovedBy.Handle
		}
		if a.ApprovedBy.URN != nil && *a.ApprovedBy.URN != "" {
			return *a.ApprovedBy.URN
		}
	}
	if a.ApprovedByInfo != nil && *a.ApprovedByInfo != "" {
		return *a.ApprovedByInfo
	}
	return "an unidentified principal"
}

func renderApprovalStatus(w io.Writer, indent string, s *approvalStatusDTO) {
	if s == nil {
		fmt.Fprintf(w, "%sapproval: unknown (the server predates node approvals)\n", indent)
		return
	}
	switch s.State {
	case string(gen.NodeApprovalStateApproved):
		if s.Approval != nil {
			fmt.Fprintf(w, "%sapproval: APPROVED revision %d by %s at %s\n", indent,
				s.Approval.Revision, approverLabel(*s.Approval), s.Approval.ApprovedAt)
			return
		}
	case string(gen.NodeApprovalStateSuperseded):
		if s.Approval != nil {
			fmt.Fprintf(w, "%sapproval: SUPERSEDED (last approved revision %d by %s at %s)\n", indent,
				s.Approval.Revision, approverLabel(*s.Approval), s.Approval.ApprovedAt)
			return
		}
	}
	fmt.Fprintf(w, "%sapproval: %s\n", indent, s.State)
}
