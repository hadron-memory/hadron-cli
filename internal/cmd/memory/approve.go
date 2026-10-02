package memory

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

type bulkApprovalDTO struct {
	Memory               string                `json:"memory"`
	ApprovedCount        int                   `json:"approvedCount"`
	AlreadyApprovedCount int                   `json:"alreadyApprovedCount"`
	Approved             []bulkApprovalNodeDTO `json:"approved"`
	SkippedPlaceholders  []string              `json:"skippedPlaceholders"`
}

type bulkApprovalNodeDTO struct {
	NodeID   string                `json:"nodeId"`
	URN      string                `json:"urn"`
	Loc      string                `json:"loc"`
	Approval bulkApprovalRecordDTO `json:"approval"`
}

type bulkApprovalRecordDTO struct {
	Revision       int                    `json:"revision"`
	ApprovedAt     string                 `json:"approvedAt"`
	ApprovedBy     *bulkApprovalEditorDTO `json:"approvedBy"`
	ApprovedByInfo *string                `json:"approvedByInfo"`
	Hash           string                 `json:"hash"`
}

type bulkApprovalEditorDTO struct {
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

func newCmdApproveAll(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var yes bool
	cmd := &cobra.Command{
		Use:   "approve-all -m <memoryRef>",
		Short: "Approve current revisions throughout a memory",
		Long: `Approve the current revision of every live, unminted node in a memory
that is not already approved. The server performs this as one transaction, returning
the nodes it approved and the reserved placeholders it skipped. An existing
approval of the current revision is kept unchanged. This command asks for
confirmation on a terminal and requires --yes in non-interactive use.`,
		Example: `  hadron memory approve-all -m hrn:mem:micromentor.org:specs-draft --yes --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(memory) == "" {
				return exitcode.Newf(exitcode.Usage, "-m/--memory is required; name the memory whose current node revisions to approve")
			}
			ref := cmdutil.CanonicalMemoryRef(memory)
			if err := cmdutil.Confirm(f.IOStreams, yes,
				fmt.Sprintf("Approve every eligible, unminted current node revision in %s?", ref)); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ApproveMemoryNodes(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp.ApproveMemoryNodes == nil {
				return exitcode.Newf(exitcode.Error, "server returned no memory approval result")
			}
			r := resp.ApproveMemoryNodes
			dto := bulkApprovalDTO{
				Memory: ref, ApprovedCount: r.ApprovedCount,
				AlreadyApprovedCount: r.AlreadyApprovedCount,
				Approved:             []bulkApprovalNodeDTO{}, SkippedPlaceholders: []string{},
			}
			dto.SkippedPlaceholders = append(dto.SkippedPlaceholders, r.SkippedPlaceholders...)
			for _, n := range r.Approved {
				if n == nil || n.Approval == nil {
					return exitcode.Newf(exitcode.Error, "server returned an incomplete node approval")
				}
				a := n.Approval.NodeApprovalFields
				approval := bulkApprovalRecordDTO{
					Revision: a.Revision, ApprovedAt: a.ApprovedAt,
					ApprovedByInfo: a.ApprovedByInfo, Hash: a.Hash,
				}
				if a.ApprovedBy != nil {
					approval.ApprovedBy = &bulkApprovalEditorDTO{Handle: a.ApprovedBy.Handle, URN: a.ApprovedBy.Urn}
				}
				dto.Approved = append(dto.Approved, bulkApprovalNodeDTO{
					NodeID: n.NodeId, URN: n.Urn, Loc: n.Loc, Approval: approval,
				})
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "approved %d node revision(s) in %s; %d already approved\n",
					dto.ApprovedCount, dto.Memory, dto.AlreadyApprovedCount)
				for _, n := range dto.Approved {
					fmt.Fprintf(w, "  %s  revision %d\n", n.URN, n.Approval.Revision)
				}
				if len(dto.SkippedPlaceholders) > 0 {
					fmt.Fprintf(w, "  skipped %d reserved placeholder(s): %s\n",
						len(dto.SkippedPlaceholders), strings.Join(dto.SkippedPlaceholders, ", "))
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN to approve")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (required in non-interactive use)")
	return cmd
}
