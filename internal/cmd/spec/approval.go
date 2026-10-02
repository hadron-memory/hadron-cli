package spec

import (
	"context"
	"fmt"
	"io"

	"github.com/Khan/genqlient/graphql"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

// A separate status read leaves all established spec reads usable on servers
// predating hadron-server#1591. A nil status means the server could not say;
// NOT_APPROVED is a positive answer from an approval-aware server.
type specApprovalDTO struct {
	State    string                 `json:"state"`
	Approval *specApprovalRecordDTO `json:"approval"`
}

type specApprovalRecordDTO struct {
	Revision       int                   `json:"revision"`
	ApprovedAt     string                `json:"approvedAt"`
	ApprovedBy     *specApprovalActorDTO `json:"approvedBy"`
	ApprovedByInfo *string               `json:"approvedByInfo"`
	Hash           string                `json:"hash"`
}

type specApprovalActorDTO struct {
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

type specMintDTO struct {
	Minted       bool                  `json:"minted"`
	MintedAt     *string               `json:"mintedAt"`
	MintedBy     *specApprovalActorDTO `json:"mintedBy"`
	MintedByInfo *string               `json:"mintedByInfo"`
	Revision     *int                  `json:"revision"`
	Hash         *string               `json:"hash"`
}

func specMintFrom(s *gen.NodeMintStatusFields) *specMintDTO {
	if s == nil {
		return nil
	}
	d := &specMintDTO{Minted: s.Minted, MintedAt: s.MintedAt, MintedByInfo: s.MintedByInfo, Revision: s.Revision, Hash: s.Hash}
	if s.MintedBy != nil {
		d.MintedBy = &specApprovalActorDTO{Handle: s.MintedBy.Handle, URN: s.MintedBy.Urn}
	}
	return d
}

func renderSpecMint(w io.Writer, s *specMintDTO) {
	if s == nil {
		fmt.Fprintln(w, "Mint: unknown (server predates per-node minting)")
		return
	}
	if !s.Minted {
		fmt.Fprintln(w, "Mint: unminted")
		return
	}
	if s.Revision == nil {
		fmt.Fprintln(w, "Mint: minted (backfilled; revision unknown)")
		return
	}
	fmt.Fprintf(w, "Mint: minted revision %d", *s.Revision)
	if s.MintedAt != nil {
		fmt.Fprintf(w, " at %s", *s.MintedAt)
	}
	fmt.Fprintln(w)
}

// specStatuses uses one status probe per batch on a mint-aware server. It
// falls back to approval-only and then no-status on older servers, without
// dropping the underlying spec read. The nodeBatch cap and spillover apply to
// each mode; other GraphQL and transport failures remain errors.
func specStatuses(ctx context.Context, client graphql.Client, ids []string) (map[string]*specApprovalDTO, map[string]*specMintDTO, bool, bool, error) {
	approvals := make(map[string]*specApprovalDTO, len(ids))
	mints := make(map[string]*specMintDTO, len(ids))
	approvalSupported, mintSupported := true, true
	pending := append([]string(nil), ids...)
	for len(pending) > 0 {
		end := min(len(pending), api.NodeBatchCap)
		batch := pending[:end]
		pending = pending[end:]
		if mintSupported {
			resp, err := gen.NodeLiveRevisionsMinted(ctx, client, batch, nil, nil)
			switch {
			case err == nil && resp.NodeBatch != nil:
				for _, n := range resp.NodeBatch.Nodes {
					if n == nil {
						continue
					}
					if n.ApprovalStatus != nil {
						approvals[n.Id] = specApprovalFrom(&n.ApprovalStatus.NodeApprovalStatusFields)
					}
					if n.MintStatus != nil {
						mints[n.Id] = specMintFrom(&n.MintStatus.NodeMintStatusFields)
					}
				}
				if resp.NodeBatch.Truncated {
					if len(resp.NodeBatch.Omitted) == 0 || len(resp.NodeBatch.Omitted) == len(batch) {
						return nil, nil, false, false, exitcode.Newf(exitcode.Error, "node status batch was truncated without progress")
					}
					pending = append(pending, resp.NodeBatch.Omitted...)
				}
				continue
			case err == nil:
				return nil, nil, false, false, exitcode.Newf(exitcode.Error, "server returned no node mint batch")
			case api.IsGraphQLValidationFor(err, "mintStatus") || api.IsGraphQLValidationFor(err, "NodeMintStatus"):
				mintSupported = false
			case api.IsGraphQLValidationFor(err, "approvalStatus") || api.IsGraphQLValidationFor(err, "NodeApprovalStatus"):
				return map[string]*specApprovalDTO{}, map[string]*specMintDTO{}, false, false, nil
			case oldNodeStatusField(err):
				return map[string]*specApprovalDTO{}, map[string]*specMintDTO{}, false, false, nil
			default:
				return nil, nil, false, false, api.MapError(err)
			}
		}
		resp, err := gen.NodeLiveRevisionsApproved(ctx, client, batch, nil, nil)
		if err != nil {
			if api.IsGraphQLValidationFor(err, "approvalStatus") || api.IsGraphQLValidationFor(err, "NodeApprovalStatus") || oldNodeStatusField(err) {
				return map[string]*specApprovalDTO{}, map[string]*specMintDTO{}, false, false, nil
			}
			return nil, nil, false, false, api.MapError(err)
		}
		if resp.NodeBatch == nil {
			return nil, nil, false, false, exitcode.Newf(exitcode.Error, "server returned no node approval batch")
		}
		for _, n := range resp.NodeBatch.Nodes {
			if n != nil && n.ApprovalStatus != nil {
				approvals[n.Id] = specApprovalFrom(&n.ApprovalStatus.NodeApprovalStatusFields)
			}
		}
		if resp.NodeBatch.Truncated {
			if len(resp.NodeBatch.Omitted) == 0 || len(resp.NodeBatch.Omitted) == len(batch) {
				return nil, nil, false, false, exitcode.Newf(exitcode.Error, "node approval batch was truncated without progress")
			}
			pending = append(pending, resp.NodeBatch.Omitted...)
		}
	}
	return approvals, mints, approvalSupported, mintSupported, nil
}

func oldNodeStatusField(err error) bool {
	for _, field := range []string{"authorship", "contentValidation", "NodeAuthorship", "NodeContentValidationStatus", "revision", "nodeBatch"} {
		if api.IsGraphQLValidationFor(err, field) {
			return true
		}
	}
	return false
}

func specApprovalFrom(s *gen.NodeApprovalStatusFields) *specApprovalDTO {
	if s == nil {
		return nil
	}
	d := &specApprovalDTO{State: string(s.State)}
	if s.Approval != nil {
		a := s.Approval.NodeApprovalFields
		r := &specApprovalRecordDTO{
			Revision: a.Revision, ApprovedAt: a.ApprovedAt,
			ApprovedByInfo: a.ApprovedByInfo, Hash: a.Hash,
		}
		if a.ApprovedBy != nil {
			r.ApprovedBy = &specApprovalActorDTO{Handle: a.ApprovedBy.Handle, URN: a.ApprovedBy.Urn}
		}
		d.Approval = r
	}
	return d
}

func renderSpecApproval(w io.Writer, s *specApprovalDTO) {
	if s == nil {
		fmt.Fprintln(w, "Approval: unknown (the server predates node approvals)")
		return
	}
	switch s.State {
	case string(gen.NodeApprovalStateApproved):
		if s.Approval != nil {
			fmt.Fprintf(w, "Approval: APPROVED revision %d by %s at %s\n", s.Approval.Revision, specApprover(*s.Approval), s.Approval.ApprovedAt)
			return
		}
	case string(gen.NodeApprovalStateSuperseded):
		if s.Approval != nil {
			fmt.Fprintf(w, "Approval: SUPERSEDED (last approved revision %d by %s at %s)\n", s.Approval.Revision, specApprover(*s.Approval), s.Approval.ApprovedAt)
			return
		}
	}
	fmt.Fprintf(w, "Approval: %s\n", s.State)
}

func specApprover(a specApprovalRecordDTO) string {
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
