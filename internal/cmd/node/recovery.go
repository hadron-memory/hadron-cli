package node

import (
	"fmt"
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

// Deleted-node recovery (hadron-server#2234, cli#985). Its own command group on
// purpose: the ordinary node reads are live-only and stay so, so no flag on
// them can ever read a tombstone.

// recoveryDefaultLimit is the page size a recovery listing asks for when
// --limit is unset. Always sent explicitly: the server default is not relied
// on (cli#967).
const recoveryDefaultLimit = 20

// recoveryMissSentence is the ONE answer for a point read that returned
// nothing. The server conceals why (a live node, an absent one, a comment, a
// caller who is not a writer, a run or key principal all look identical), so
// the CLI says nothing that would tell them apart.
const recoveryMissSentence = "no recoverable deleted node at that reference; it may be live or absent, or this account may not recover it"

// recoveryNodeDTO is one deleted node in a listing and the identity part of a
// point read.
type recoveryNodeDTO struct {
	ID        string `json:"id"`
	URN       string `json:"urn"`
	MemoryID  string `json:"memoryId"`
	Loc       string `json:"loc"`
	Name      string `json:"name"`
	NodeType  string `json:"nodeType"`
	DeletedAt string `json:"deletedAt"`
}

// recoveryNodesDTO is the --json shape of `node recovery ls`. limit is null
// under --all (no single page size applied); hasMore is always false there.
type recoveryNodesDTO struct {
	Items   []recoveryNodeDTO `json:"items"`
	Total   int               `json:"total"`
	Limit   *int              `json:"limit"`
	Offset  int               `json:"offset"`
	HasMore bool              `json:"hasMore"`
}

// recoveryNodeDetailDTO is `node recovery get`. content is the raw stored body
// and contentIncluded says whether it was asked for, so "not requested" is
// never mistaken for "empty".
type recoveryNodeDetailDTO struct {
	recoveryNodeDTO
	Description     *string `json:"description"`
	Content         *string `json:"content"`
	ContentIncluded bool    `json:"contentIncluded"`
}

type recoveryEndpointDTO struct {
	ID        string  `json:"id"`
	URN       string  `json:"urn"`
	Loc       string  `json:"loc"`
	Name      string  `json:"name"`
	DeletedAt *string `json:"deletedAt"`
}

type recoveryEdgeDTO struct {
	ID     string              `json:"id"`
	Name   *string             `json:"name"`
	Source recoveryEndpointDTO `json:"source"`
	Target recoveryEndpointDTO `json:"target"`
}

// recoveryEdgesDTO mirrors recoveryNodesDTO for `node recovery graph`.
type recoveryEdgesDTO struct {
	Items   []recoveryEdgeDTO `json:"items"`
	Total   int               `json:"total"`
	Limit   *int              `json:"limit"`
	Offset  int               `json:"offset"`
	HasMore bool              `json:"hasMore"`
}

func newCmdRecovery(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recovery <command>",
		Short: "Read deleted nodes and the edges around them (writers only)",
		Long: `Read what was soft-deleted from a memory so it can be restored with
'hadron node restore'. This is a separate surface on purpose: 'node get|ls'
stay live-only and never return a deleted node.

Only a memory's writers can use it. For anyone else a deleted node and a node
that never existed are indistinguishable, so a miss here does not say which.`,
	}
	cmd.AddCommand(newCmdRecoveryLs(f))
	cmd.AddCommand(newCmdRecoveryGet(f))
	cmd.AddCommand(newCmdRecoveryGraph(f))
	return cmd
}

// pageFlags are the paging flags shared by the two listings.
type pageFlags struct {
	limit, offset int
	all           bool
}

func (p *pageFlags) bind(cmd *cobra.Command) {
	cmd.Flags().IntVar(&p.limit, "limit", recoveryDefaultLimit, "Page size, 1-200")
	cmd.Flags().IntVar(&p.offset, "offset", 0, "Rows to skip")
	cmd.Flags().BoolVar(&p.all, "all", false, "Read every page (exclusive with --limit/--offset); fails whole if any page fails")
}

// validate refuses a malformed paging request locally and returns the offset to
// send (nil when zero, per the omit-vs-null wire rule).
func (p *pageFlags) validate(cmd *cobra.Command) error {
	if p.all && (cmd.Flags().Changed("limit") || cmd.Flags().Changed("offset")) {
		return exitcode.Newf(exitcode.Usage, "--all cannot be combined with --limit or --offset")
	}
	if p.limit < 1 || p.limit > api.PageLimit {
		return exitcode.Newf(exitcode.Usage, "--limit must be between 1 and %d", api.PageLimit)
	}
	if p.offset < 0 {
		return exitcode.Newf(exitcode.Usage, "--offset must be non-negative")
	}
	return nil
}

func offsetArg(offset int) *int {
	if offset == 0 {
		return nil
	}
	return &offset
}

// pageResult is what the shared listing driver returns to the renderer.
type pageResult[T any] struct {
	items   []T
	total   int
	limit   *int
	offset  int
	hasMore bool
}

// listPage runs one deliberate page, or drains every page under --all. A page
// that fails mid-drain fails the whole command with nothing printed: a partial
// list read as a complete one is the failure this guards against.
func listPage[T any](f *cmdutil.Factory, p *pageFlags, fetch func(limit int, offset *int) ([]T, int, error)) (*pageResult[T], error) {
	if p.all {
		items, err := api.CollectAll(func(limit, offset int) ([]T, int, error) {
			return fetch(limit, offsetArg(offset))
		})
		if err != nil {
			return nil, err
		}
		return &pageResult[T]{items: items, total: len(items)}, nil
	}
	items, total, err := fetch(p.limit, offsetArg(p.offset))
	if err != nil {
		return nil, err
	}
	limit := p.limit
	res := &pageResult[T]{items: items, total: total, limit: &limit, offset: p.offset}
	// The exact total, not a full page, says whether more exist.
	next := p.offset + len(items)
	if next < total {
		res.hasMore = true
		_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "showing %d-%d of %d; more: --offset %d\n", p.offset+1, next, total, next)
	}
	return res, nil
}

func requireRecoveryMemory(memory string) (string, error) {
	if strings.TrimSpace(memory) == "" {
		return "", exitcode.Newf(exitcode.Usage, "-m/--memory hrn:mem:<root>:<slug> is required")
	}
	return cmdutil.CanonicalMemoryRef(memory), nil
}

func newCmdRecoveryLs(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var pf pageFlags
	cmd := &cobra.Command{
		Use:     "list -m <memory>",
		Aliases: []string{"ls"},
		Short:   "List deleted nodes in a memory",
		Long: `List the ordinary nodes deleted from a memory, ordered by loc.

One page of 20 by default. --limit/--offset read one deliberate page; when more
exist, stderr says so and names the next --offset (stdout and --json are
unchanged). --all reads every page and fails as a whole if any page fails, so a
partial list is never printed. Pages are read separately, so a node restored
or deleted while --all runs can move between pages.`,
		Example: `  hadron node recovery ls -m hrn:mem:acme.com:kb
  hadron node recovery ls -m hrn:mem:acme.com:kb --all --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			memRef, err := requireRecoveryMemory(memory)
			if err != nil {
				return err
			}
			if err := pf.validate(cmd); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			res, err := listPage(f, &pf, func(limit int, offset *int) ([]recoveryNodeDTO, int, error) {
				resp, err := gen.RecoveryNodes(cmd.Context(), client, memRef, limit, offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp.RecoveryNodes == nil {
					return nil, 0, exitcode.Newf(exitcode.Error, "recoveryNodes returned no result")
				}
				out := make([]recoveryNodeDTO, 0, len(resp.RecoveryNodes.Items))
				for _, it := range resp.RecoveryNodes.Items {
					if it == nil {
						continue
					}
					out = append(out, recoveryNodeDTO{ID: it.Id, URN: it.Urn, MemoryID: it.MemoryId, Loc: it.Loc,
						Name: it.Name, NodeType: it.NodeType, DeletedAt: it.DeletedAt})
				}
				return out, resp.RecoveryNodes.Total, nil
			})
			if err != nil {
				return err
			}
			dto := recoveryNodesDTO{Items: res.items, Total: res.total, Limit: res.limit, Offset: res.offset, HasMore: res.hasMore}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 {
					_, err := fmt.Fprintf(w, "No deleted nodes in %s.\n", memory)
					return err
				}
				t := output.NewTable(w, "LOC", "NAME", "TYPE", "DELETED", "URN")
				for _, n := range dto.Items {
					t.Row(n.Loc, n.Name, n.NodeType, n.DeletedAt, n.URN)
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "Memory to list (hrn:mem:<root>:<slug>)")
	pf.bind(cmd)
	return cmd
}

func newCmdRecoveryGraph(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var pf pageFlags
	cmd := &cobra.Command{
		Use:   "graph -m <memory>",
		Short: "List edges whose endpoints are live or deleted-but-recoverable",
		Long: `List the live edges in a memory whose two endpoints are each either live or a
deleted node you can recover — the edges a restore would reconnect. Paging is
the same as 'node recovery ls'.`,
		Example: `  hadron node recovery graph -m hrn:mem:acme.com:kb --json`,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			memRef, err := requireRecoveryMemory(memory)
			if err != nil {
				return err
			}
			if err := pf.validate(cmd); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			res, err := listPage(f, &pf, func(limit int, offset *int) ([]recoveryEdgeDTO, int, error) {
				resp, err := gen.RecoveryEdges(cmd.Context(), client, memRef, limit, offset)
				if err != nil {
					return nil, 0, api.MapError(err)
				}
				if resp.RecoveryEdges == nil {
					return nil, 0, exitcode.Newf(exitcode.Error, "recoveryEdges returned no result")
				}
				out := make([]recoveryEdgeDTO, 0, len(resp.RecoveryEdges.Items))
				for _, e := range resp.RecoveryEdges.Items {
					if e == nil || e.Source == nil || e.Target == nil {
						continue
					}
					out = append(out, recoveryEdgeDTO{
						ID: e.Id, Name: e.Name,
						Source: recoveryEndpointDTO{ID: e.Source.Id, URN: e.Source.Urn, Loc: e.Source.Loc, Name: e.Source.Name, DeletedAt: e.Source.DeletedAt},
						Target: recoveryEndpointDTO{ID: e.Target.Id, URN: e.Target.Urn, Loc: e.Target.Loc, Name: e.Target.Name, DeletedAt: e.Target.DeletedAt},
					})
				}
				return out, resp.RecoveryEdges.Total, nil
			})
			if err != nil {
				return err
			}
			dto := recoveryEdgesDTO{Items: res.items, Total: res.total, Limit: res.limit, Offset: res.offset, HasMore: res.hasMore}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if len(dto.Items) == 0 {
					_, err := fmt.Fprintf(w, "No recoverable edges in %s.\n", memory)
					return err
				}
				t := output.NewTable(w, "SOURCE", "EDGE", "TARGET")
				for _, e := range dto.Items {
					t.Row(endpointCell(e.Source), output.Dash(e.Name), endpointCell(e.Target))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "Memory to read (hrn:mem:<root>:<slug>)")
	pf.bind(cmd)
	return cmd
}

func endpointCell(e recoveryEndpointDTO) string {
	if e.DeletedAt != nil {
		return e.Loc + " (deleted)"
	}
	return e.Loc
}

func newCmdRecoveryGet(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var content bool
	cmd := &cobra.Command{
		Use:   "get <node-urn> | <node-id> | <loc> -m <memory> [--content]",
		Short: "Show one deleted node",
		Long: `Show one deleted node by id, URN, or a bare loc with -m. The reference goes to
the server as given: nothing is resolved first, because an ordinary resolve
omits deleted nodes and would refuse every one.

--content also reads the stored body, byte for byte: templates and includes are
never expanded. In text mode the body alone is written to stdout, with no
metadata header added to it; with --json it is the "content" string and
"contentIncluded" is true. A body that cannot be read (for instance, an
encrypted memory that is locked) is an error, never an empty body.

A miss prints one fixed sentence whatever the cause: the node may be live or
absent, or this account may not recover it.`,
		Example: `  hadron node recovery get hrn:node:acme.com:kb:findings:flaky-ci
  hadron node recovery get findings:flaky-ci -m hrn:mem:acme.com:kb --content`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := cmdutil.BatchNodeRef(memory, args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.RecoveryNode(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			n := resp.RecoveryNode
			if n == nil {
				return exitcode.Newf(exitcode.NotFound, "%s", recoveryMissSentence)
			}
			dto := recoveryNodeDetailDTO{
				recoveryNodeDTO: recoveryNodeDTO{ID: n.Id, URN: n.Urn, MemoryID: n.MemoryId, Loc: n.Loc,
					Name: n.Name, NodeType: n.NodeType, DeletedAt: n.DeletedAt},
				Description: n.Description,
			}
			if content {
				body, err := recoveryContent(cmd, client, n.Id)
				if err != nil {
					return err
				}
				dto.Content, dto.ContentIncluded = body, true
				if !f.JSON {
					// Text mode with --content is the body and nothing else.
					if body == nil {
						_, _ = fmt.Fprintln(f.IOStreams.ErrOut, "node has no stored body")
						return nil
					}
					_, err := io.WriteString(f.IOStreams.Out, *body)
					return err
				}
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "%s\n  urn: %s\n  type: %s\n  deleted: %s\n", dto.Name, dto.URN, dto.NodeType, dto.DeletedAt)
				if err == nil && dto.Description != nil && *dto.Description != "" {
					_, err = fmt.Fprintf(w, "  description: %s\n", *dto.Description)
				}
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "Memory, to name a bare <loc> (hrn:mem:<root>:<slug>)")
	cmd.Flags().BoolVar(&content, "content", false, "Also read the raw stored body")
	return cmd
}

// recoveryContent reads the body by the stable node id from the first read (not
// the mutable ref, which could re-resolve to another tombstone) in its own
// operation, so the default point read cannot fail on an unreadable key.
func recoveryContent(cmd *cobra.Command, client graphql.Client, id string) (*string, error) {
	resp, err := gen.RecoveryNodeContent(cmd.Context(), client, id)
	if err != nil {
		return nil, api.MapError(err)
	}
	if resp.RecoveryNode == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "%s", recoveryMissSentence)
	}
	return resp.RecoveryNode.Content, nil
}
