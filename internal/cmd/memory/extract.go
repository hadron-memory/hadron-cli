package memory

import (
	"errors"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdExtract(f *cmdutil.Factory) *cobra.Command {
	var (
		memoryRef string
		move      bool
	)
	cmd := &cobra.Command{
		Use:   "extract <parentRef> <targetUrn> [--move]",
		Short: "Extract a node subtree into a new memory",
		Long: `Extract a parent node and its loc-subtree into a new memory, making
the parent the new memory's root.

The subtree is the node at the parent's loc plus every live descendant.
Locs are REBASED so the parent becomes the root — "findings:auth"
becomes the memory slug and "findings:auth:oauth" becomes "<slug>:oauth".
Edges wholly inside the subtree carry over; boundary-crossing edges and
unresolved pending edges are dropped.

<parentRef> is the parent node's ID or fully-qualified URN
(<org>::<memory>::<loc>); pass -m <org::memory> with a bare loc instead.
<targetUrn> is a fully-qualified "<org>::<slug>" URN naming the new
memory — its org may differ from the source's, dropping the extract into
another organization.

Without --move the subtree is COPIED, leaving the source intact. With
--move it is relocated, soft-deleting the source subtree; --move needs
source write access and cannot target the source root.

The new memory PRESERVES the source's class, so an extract never widens
who can read the content (a member-restricted group stays group, a
personal/private source stays owner-owned).

v1 limitation: node content is copied verbatim — because both the slug
and node locs change, URN references among the moved nodes will break.`,
		Example: `  hadron memory extract acme.com::kb::findings:auth acme.com::auth-kb
  hadron memory extract -m acme.com::kb findings:auth other-org::auth-kb --move`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parentRef, targetURN := args[0], args[1]
			// Coarse client-side gate: a fully-qualified memory URN carries the
			// canonical "::" org→slug separator. Reject an obviously-relative
			// value before the round-trip; the server does the full validation.
			if !strings.Contains(targetURN, "::") {
				return errors.New("<targetUrn> must be a fully-qualified \"org::slug\" memory URN")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			parentID, err := resolveExtractParentRef(cmd, client, memoryRef, parentRef)
			if err != nil {
				return err
			}
			// Only send `move` when set — the server defaults it to false, and
			// omitempty keeps a false flag off the wire (preserving that default).
			var movePtr *bool
			if move {
				movePtr = &move
			}
			resp, err := gen.ExtractParentNodeToMemory(cmd.Context(), client, parentID, targetURN, movePtr)
			if err != nil {
				return api.MapError(err)
			}
			extracted := resp.ExtractParentNodeToMemory
			if extracted == nil {
				return errors.New("server returned an empty extractParentNodeToMemory response")
			}
			m := memoryDTO{
				ID: extracted.Id, URN: extracted.Urn, Name: extracted.Name,
				ShortDescription: extracted.ShortDescription, Class: string(extracted.Class),
				OrganizationID: extracted.OrganizationId, IsEncrypted: extracted.IsEncrypted,
				MaxRevCount: extracted.MaxRevCount, UpdatedAt: extracted.UpdatedAt,
			}
			if extracted.Visibility != nil {
				v := string(*extracted.Visibility)
				m.Visibility = &v
			}
			verb := "copied"
			if move {
				verb = "moved"
			}
			return output.Write(f.IOStreams, f.JSON, m, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ extracted ("+verb+")", parentRef, "→", m.URN)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memoryRef, "memory", "m", "", "memory (org::memory) for a bare-loc <parentRef>")
	cmd.Flags().BoolVar(&move, "move", false, "relocate the subtree (soft-delete the source) instead of copying")
	return cmd
}

// resolveExtractParentRef turns the <parentRef> arg into a node id the mutation
// accepts. A fully-qualified URN, or a -m/--memory + bare loc, is resolved
// client-side (validating the URN grammar) — same as the node commands. A
// colon-free bare token with neither is taken as a node PK and passed through
// verbatim: the server's parentRef accepts a PK, and the command + server
// contract both advertise a raw id. A bare token carrying single colons is a
// namespaced loc (e.g. findings:auth), never a PK — reject it with the shared
// node-ref guidance rather than letting it miss server-side as a bogus id.
// Mirrors node's revisionNodeRef (#229 review: Codex/Copilot).
func resolveExtractParentRef(cmd *cobra.Command, client graphql.Client, memory, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.TrimSpace(memory) == "" &&
		!strings.Contains(ref, "::") &&
		!strings.HasPrefix(ref, "hrn:") &&
		!strings.HasPrefix(ref, "urn:") {
		if strings.Contains(ref, ":") {
			return "", exitcode.Newf(exitcode.Usage,
				"%q is not a fully-qualified node URN — expected <org>::<memory>::<loc>, or pass -m <org::memory> with a bare loc (or a node id)", ref)
		}
		return ref, nil
	}
	return cmdutil.ResolveNodeRef(cmd, client, memory, ref)
}
