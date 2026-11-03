package cmdutil

import (
	"fmt"
	"io"

	"github.com/hadron-memory/hadron-cli/internal/output"
)

// MintBlockerNotApproved is the mint-report blocker kind for a node whose
// current revision carries no approval.
const MintBlockerNotApproved = "NOT_APPROVED"

// WriteMintApproveHint prints, once, how to approve when any of a mint
// report's blocker kinds is NOT_APPROVED (cli#1150). Human output only: the
// --json report stays the server's report. It names the per-node path, whose
// --revision guard approves only what was reviewed, and the bulk path, which
// approves current content with no revision check (cor:dmo:120).
func WriteMintApproveHint(w io.Writer, memory string, blockerKinds ...string) {
	for _, k := range blockerKinds {
		if k == MintBlockerNotApproved {
			fmt.Fprintf(w, "  approve with: hadron node approve <node-urn> --revision <n> (approves only the revision you reviewed),\n"+
				"    or hadron memory approve-all -m %s (approves every unapproved node's current content, with no revision check)\n",
				output.SafeCell(memory))
			return
		}
	}
}
