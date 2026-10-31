package memory

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

// verifyProblemDTO is one node that is not INTACT (the node verify shape).
type verifyProblemDTO struct {
	NodeID           string  `json:"nodeId"`
	URN              string  `json:"urn"`
	Loc              string  `json:"loc"`
	State            string  `json:"state"`
	Revision         int     `json:"revision"`
	ApprovedRevision *int    `json:"approvedRevision"`
	ExpectedHash     *string `json:"expectedHash"`
	ActualHash       string  `json:"actualHash"`
}

// verifyDTO is the stable --json shape of memory verify.
type verifyDTO struct {
	Memory      string             `json:"memory"`
	Checked     int                `json:"checked"`
	Intact      int                `json:"intact"`
	NotApproved int                `json:"notApproved"`
	Superseded  int                `json:"superseded"`
	Tampered    int                `json:"tampered"`
	Problems    []verifyProblemDTO `json:"problems"`
}

// memory verify (cli#1340): verifyMemoryNodes, the tamper check over every
// live node of a memory (#1591). The server decides each node's state.
func newCmdVerify(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify <memoryRef>",
		Short: "Check every node of a memory against its recorded approval",
		Long: `Check every live node of a memory against its recorded approval, as node
verify does for one node. Each node is one of:

  INTACT        the approval still covers the current text
  NOT_APPROVED  never approved: nothing to check
  SUPERSEDED    edited (a new revision) since the approval; not tampering
  TAMPERED      approved text changed outside revisioned editing

It prints the counts and every node that is not INTACT, ordered by loc. It
exits 5 when any node is TAMPERED, after printing; NOT_APPROVED and SUPERSEDED
nodes alone exit 0.`,
		Example: `  hadron memory verify acme.com:kb
  hadron memory verify hrn:mem:acme.com:kb --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateMemoryRef(args[0]); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref := cmdutil.CanonicalMemoryRef(args[0])
			resp, err := gen.VerifyMemoryNodes(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.VerifyMemoryNodes == nil {
				return exitcode.Newf(exitcode.Error, "server returned no integrity report")
			}
			r := resp.VerifyMemoryNodes
			dto := verifyDTO{Memory: ref, Checked: r.Checked, Intact: r.Intact, NotApproved: r.NotApproved,
				Superseded: r.Superseded, Tampered: r.Tampered, Problems: []verifyProblemDTO{}}
			for _, p := range r.Problems {
				if p == nil {
					continue
				}
				dto.Problems = append(dto.Problems, verifyProblemDTO{NodeID: p.NodeId, URN: p.Urn, Loc: p.Loc, State: string(p.State),
					Revision: p.Revision, ApprovedRevision: p.ApprovedRevision, ExpectedHash: p.ExpectedHash, ActualHash: p.ActualHash})
			}
			if err := output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if _, err := fmt.Fprintf(w, "%s: %d checked, %d intact, %d not approved, %d superseded, %d tampered\n",
					output.SafeCell(dto.Memory), dto.Checked, dto.Intact, dto.NotApproved, dto.Superseded, dto.Tampered); err != nil {
					return err
				}
				if len(dto.Problems) == 0 {
					return nil
				}
				t := output.NewTable(w, "STATE", "NODE", "REVISION", "APPROVED AT")
				for _, p := range dto.Problems {
					approved := "-"
					if p.ApprovedRevision != nil {
						approved = fmt.Sprint(*p.ApprovedRevision)
					}
					t.Row(output.SafeCell(p.State), output.SafeCell(p.URN), fmt.Sprint(p.Revision), approved)
				}
				return t.Flush()
			}); err != nil {
				return err
			}
			if dto.Tampered > 0 {
				return exitcode.Silent(exitcode.Conflict)
			}
			return nil
		},
	}
	return cmd
}
