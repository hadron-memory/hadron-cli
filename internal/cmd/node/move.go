package node

import (
	"io"
	"math"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdMove(f *cmdutil.Factory) *cobra.Command {
	var (
		memory   string
		toURN    string
		toMemory string
		dryRun   bool
		limit    int
	)
	cmd := &cobra.Command{
		Use:     "move <node-urn> | <loc> -m <memory> (--to-urn <urn> | --to-memory <memory>)",
		Aliases: []string{"mv"}, // its siblings are list/ls and delete/rm (#450)
		Short:   "Move a node to a new loc and/or memory",
		Long: `Relocate a node — together with its whole subtree, so every descendant moves
with it — keeping each node's id, so every incoming and outgoing edge reference
stays valid. Name the source by its fully-qualified URN
(hrn:node:<root>:<slug>:<loc>) or by a bare <loc> with -m/--memory.

Give the destination exactly one of:

  --to-urn hrn:node:<root>:<slug>:<loc>   the node's new full URN (new memory
                                    and/or loc).
  --to-memory hrn:mem:<root>:<slug>         a destination memory; the node keeps its
                                    current loc, only its memory changes.

A CROSS-MEMORY move (a different destination memory) is refused for a safe
subset — a usage error (exit 2) whose message names the reason — when:
  - the source or destination memory is encrypted, or the source is git-backed;
  - the subtree contains a chat-root node (a private/shared conversation); or
  - a node would violate the destination memory's collection (property) schema.
A same-memory move is not subject to these refusals.

Any move — same-memory or cross-memory — is a conflict (exit 5) if a live node
already occupies the destination, or if the source changes concurrently while
the move is in flight.

--dry-run asks the server to plan the move without writing. The server runs the
move's own checks, so the plan lists each node that would move with its current
and new URN, and the content-link warnings a cross-memory move would report (or
says they are withheld when you can't read the destination memory). If the move
would be refused, --dry-run fails with the move's own error and exit code. The
plan is true when answered and reserves nothing, so a change before the move can
still end it with a conflict (exit 5). The server lists at most --limit nodes
(1 to 2147483647, the GraphQL Int range; its default 100; the server bounds the
value). A plan that does not list every node is labelled TRUNCATED and exits 1;
when the server lists fewer than --limit asked for, that is its bound, and a
larger --limit cannot list the subtree in full. In --json, status, complete and
truncated always agree: truncated is the CLI's verdict (the server flagged the
plan or it lists fewer nodes than it counts) and is always the opposite of
complete.`,
		Example: `  hadron node move hrn:node:acme.com:kb:findings:flaky-ci --to-urn hrn:node:acme.com:kb:archive:flaky-ci
  hadron node move findings:flaky-ci -m hrn:mem:acme.com:kb --to-memory hrn:mem:acme.com:archive
  hadron node move findings -m hrn:mem:acme.com:kb --to-memory hrn:mem:acme.com:archive --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun && wantsLintWarnings(cmd) {
				return exitcode.Newf(exitcode.Usage, "--lint-warnings cannot be combined with --dry-run")
			}
			limitSet := cmd.Flags().Changed("limit")
			if limitSet && !dryRun {
				return exitcode.Newf(exitcode.Usage, "--limit applies only to --dry-run")
			}
			if limitSet && limit < 1 {
				return exitcode.Newf(exitcode.Usage, "--limit must be at least 1")
			}
			// The request carries --limit as a GraphQL Int, a signed 32-bit
			// value; a larger number cannot be encoded, so it is refused
			// here. Every value in range is sent for the server to bound
			// (cli#2320, cor:api:240:01).
			if limitSet && limit > math.MaxInt32 {
				return exitcode.Newf(exitcode.Usage, "--limit must be at most 2147483647, the largest GraphQL Int")
			}
			// Validate the destination flags before touching the network, so a
			// bad flag combo is a usage error even when unauthenticated.
			targetUrn, targetMemoryRef, err := relocationDestination(toURN, toMemory)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if dryRun {
				var planLimit *int
				if limitSet {
					planLimit = &limit
				}
				return planMove(cmd, f, client, memory, args[0], targetUrn, targetMemoryRef, planLimit)
			}
			sourceRef, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			resp, err := gen.MoveNode(cmd.Context(), client, sourceRef, targetUrn, targetMemoryRef)
			if err != nil {
				return api.MapError(err)
			}
			dto := moveDTO(resp.MoveNode)
			dto.LintWarnings = loadLintWarnings(cmd, client, dto.ID)
			return writeWithLint(f, dto, dto.LintWarnings, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ moved", output.SafeCell("to: "+dto.URN))
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> source against")
	cmd.Flags().StringVar(&toURN, "to-urn", "", "destination node URN hrn:node:<root>:<slug>:<loc> (new memory and/or loc)")
	cmd.Flags().StringVar(&toMemory, "to-memory", "", "destination memory (hrn:mem:<root>:<slug>); keeps the current loc")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the server's plan for the move without writing; a truncated plan exits 1")
	cmd.Flags().IntVar(&limit, "limit", 0, "with --dry-run: most nodes the plan lists (server default 100)")
	enableLintWarnings(cmd, "--dry-run")
	return cmd
}

// relocationDestination validates and normalizes the shared destination flags
// of `node move` and `node clone`. It enforces the "exactly one destination"
// rule client-side (the server enforces it too, but a local error is
// friendlier) and is pure — no network — so callers run it before requiring an
// authenticated client. Inputs are trimmed first, so a whitespace-only flag is
// treated as unset (not smuggled through as an empty ref). Exactly one of the
// returned targetUrn / targetMemoryRef is non-nil.
func relocationDestination(toURN, toMemory string) (*string, *string, error) {
	toURN = strings.TrimSpace(toURN)
	toMemory = strings.TrimSpace(toMemory)
	switch {
	case toURN != "" && toMemory != "":
		return nil, nil, exitcode.Newf(exitcode.Usage, "--to-urn and --to-memory are mutually exclusive")
	case toURN == "" && toMemory == "":
		return nil, nil, exitcode.Newf(exitcode.Usage, "specify a destination: --to-urn hrn:node:<root>:<slug>:<loc> or --to-memory hrn:mem:<root>:<slug>")
	}
	if toURN != "" {
		urn, err := cmdutil.CanonicalNodeURN(toURN)
		if err != nil {
			return nil, nil, err
		}
		return &urn, nil, nil
	}
	memRef := cmdutil.CanonicalMemoryRef(toMemory)
	return nil, &memRef, nil
}

func moveDTO(n *gen.MoveNodeMoveNode) nodeDTO {
	return nodeDTO{
		ID:         n.Id,
		URN:        n.Urn,
		MemoryID:   n.MemoryId,
		Loc:        n.Loc,
		Name:       n.Name,
		NodeType:   n.NodeType,
		Tags:       n.Tags,
		Seq:        nil,
		IsRunnable: boolVal(n.IsRunnable),
		Role:       n.Role,
		UpdatedAt:  n.UpdatedAt,
	}
}
