package spec

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdLs(f *cmdutil.Factory) *cobra.Command {
	var memory, prefix string
	var limit, offset int
	var approved, unapproved, minted, unminted bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List spec nodes in a memory",
		Long: `List spec nodes, optionally scoped to a loc prefix.

--prefix filters by the citation prefix: --prefix msg:010 lists every spec
whose loc is msg:010 or starts with msg:010:, at any depth. A node is a spec
when it has the legacy "spec" tag or a "spec" / "spec.*" role.

By default every matching spec is listed (the query is paged to
exhaustion). Pass --limit (with optional --offset) to display one page
after merging both spec markers.`,
		Example: `  hadron spec list -m hrn:mem:micromentor.org:platform-specs
  hadron spec list -m hrn:mem:micromentor.org:platform-specs --prefix msg:010 --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if approved && unapproved {
				return exitcode.Newf(exitcode.Usage, "--approved and --unapproved are mutually exclusive")
			}
			if minted && unminted {
				return exitcode.Newf(exitcode.Usage, "--minted and --unminted are mutually exclusive")
			}
			if (minted || unminted) && (approved || unapproved) {
				return exitcode.Newf(exitcode.Usage, "combine either an approval filter or a mint filter, not both")
			}
			prefix, err := validateSpecPrefix(prefix, cmd.Flags().Changed("prefix"))
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// Honor the spec default (hadron spec use / active memory) even
			// when -m is omitted, so a bare `list` scopes to the configured
			// corpus instead of scanning every accessible memory. With nothing
			// configured, ref is "" and the list stays unscoped.
			var memoryArg *string
			ref, err := effectiveSpecMemoryOptional(f, memory)
			if err != nil {
				return err
			}
			if ref != "" {
				m, err := resolveSpecMemoryURN(cmd, client, ref)
				if err != nil {
					return err
				}
				memoryArg = &m
			}
			var prefixArg *string
			if prefix != "" {
				prefixArg = &prefix
			}
			// The two marker streams are each paged to exhaustion and deduped
			// before a user window is cut. Applying --limit/--offset to either
			// stream on the server would skip specs from the other (#684).
			var rawNodes []*api.ListNode
			switch {
			case minted || unminted:
				rawNodes, err = scanAllSpecNodesWithMint(cmd.Context(), client, memoryArg, prefixArg, minted)
			case approved:
				rawNodes, err = scanAllSpecNodesWithApproval(cmd.Context(), client, memoryArg, prefixArg, gen.NodeApprovalStateApproved)
			case unapproved:
				never, firstErr := scanAllSpecNodesWithApproval(cmd.Context(), client, memoryArg, prefixArg, gen.NodeApprovalStateNotApproved)
				if firstErr != nil {
					return firstErr
				}
				stale, secondErr := scanAllSpecNodesWithApproval(cmd.Context(), client, memoryArg, prefixArg, gen.NodeApprovalStateSuperseded)
				if secondErr != nil {
					return secondErr
				}
				rawNodes = unionSpecNodes(never, stale)
			default:
				rawNodes, err = scanAllSpecNodes(cmd.Context(), client, memoryArg, prefixArg)
			}
			if err != nil {
				return err
			}

			// Only a scoped listing can be a draft corpus; an unscoped one
			// spans memories and marks nothing.
			draft := draftInfo{}
			if memoryArg != nil {
				draft, err = loadDraftInfo(cmd.Context(), client, cmdutil.CanonicalMemoryRef(*memoryArg), prefix)
				if err != nil {
					return err
				}
				if draft.Missing {
					return exitcode.Newf(exitcode.NotFound,
						"no memory found for %q — expected a memory id or a URN: hrn:mem:<root>:<slug>", memory)
				}
			}
			rawNodes = pageBranch(rawNodes, prefix, limit, offset)
			ids := make([]string, 0, len(rawNodes))
			for _, n := range rawNodes {
				if n != nil && underPrefix(n.Loc, prefix) {
					ids = append(ids, n.Id)
				}
			}
			statuses, mintStatuses, _, _, err := specStatuses(cmd.Context(), client, ids)
			if err != nil {
				return err
			}
			specs := make([]specDTO, 0, len(rawNodes))
			for _, n := range rawNodes {
				if n == nil || !underPrefix(n.Loc, prefix) {
					continue // the server's prefix is character-wise; keep the branch
				}
				specs = append(specs, specDTO{
					Citation:       n.Loc,
					MemoryID:       n.MemoryId,
					Name:           n.Name,
					NodeType:       n.NodeType,
					Tags:           tagsOrEmpty(n.Tags),
					UpdatedAt:      n.UpdatedAt,
					Placeholder:    draft.Placeholders[n.Loc],
					ApprovalStatus: statuses[n.Id],
					MintStatus:     mintStatuses[n.Id],
				})
			}

			var scopedURN string
			if memoryArg != nil {
				scopedURN = *memoryArg
			}
			annotateMemoryURNs(cmd, client, f.IOStreams.ErrOut, specs, scopedURN)

			return output.Write(f.IOStreams, f.JSON, specs, func(w io.Writer) error {
				return writeSpecListTable(w, specs)
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "scope to a memory (ID or fully-qualified URN)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "filter by citation prefix (e.g. msg:010)")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of specs to display (default: all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset (implies a single page)")
	cmd.Flags().BoolVar(&approved, "approved", false, "show only specs whose current revision is approved")
	cmd.Flags().BoolVar(&unapproved, "unapproved", false, "show specs never approved or edited since approval")
	cmd.Flags().BoolVar(&minted, "minted", false, "show minted specs")
	cmd.Flags().BoolVar(&unminted, "unminted", false, "show unminted specs")
	return cmd
}

// Only this listing has an approval column. Other callers of writeSpecTable
// (notably `spec find`) keep their established columns and JSON shapes.
func writeSpecListTable(w io.Writer, specs []specDTO) error {
	withApproval, withMint := false, false
	for _, s := range specs {
		if s.ApprovalStatus != nil {
			withApproval = true
		}
		if s.MintStatus != nil {
			withMint = true
		}
	}
	if !withApproval && !withMint {
		return writeSpecTable(w, specs)
	}
	cols := []string{"CITATION"}
	multiple := spansMemories(specs)
	if multiple {
		cols = append(cols, "MEMORY")
	}
	cols = append(cols, "NAME")
	if withApproval {
		cols = append(cols, "APPROVAL")
	}
	if withMint {
		cols = append(cols, "MINT")
	}
	t := output.NewTable(w, cols...)
	for _, s := range specs {
		row := []string{s.Citation}
		if multiple {
			row = append(row, memoryLabel(s))
		}
		row = append(row, placeholderLabel(s))
		if withApproval {
			row = append(row, specApprovalLabel(s.ApprovalStatus))
		}
		if withMint {
			row = append(row, specMintLabel(s.MintStatus))
		}
		t.Row(row...)
	}
	return t.Flush()
}

func specMintLabel(s *specMintDTO) string {
	if s == nil {
		return "unknown"
	}
	if s.Minted {
		return "MINTED"
	}
	return "UNMINTED"
}

func specApprovalLabel(s *specApprovalDTO) string {
	if s == nil {
		return "unknown"
	}
	return s.State
}
