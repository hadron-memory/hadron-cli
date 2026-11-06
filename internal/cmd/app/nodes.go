package app

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

const appNodesDefaultLimit = 20
const appNodesMaxLimit = 200

type appNodeDTO struct {
	ID       string `json:"id"`
	MemoryID string `json:"memoryId"`
	Loc      string `json:"loc"`
	Name     string `json:"name"`
	NodeType string `json:"nodeType"`
}

type appNodePageDTO struct {
	Items   []appNodeDTO `json:"items"`
	Total   int          `json:"total"`
	HasMore bool         `json:"hasMore"`
}

func newCmdNodes(f *cmdutil.Factory) *cobra.Command {
	var limit, offset int
	var memory, childrenOf, where string
	var memoryClasses []string
	var prefix, nodeType, objectType, createdBy, role, updatedAfter, updatedBefore string
	var tags, nodeTypes []string
	var runnable, includeDeleted bool
	cmd := &cobra.Command{
		Use:   "nodes <app-ref> [--limit N] [--offset N]",
		Short: "List one page of nodes across an App's readable memories",
		Long: `List nodes across the memories an App makes readable to you. The required
App reference is an ID, hrn:app:<root>:<slug>, or <root>:<slug>; it chooses
the App for this read instead of the active App. The server decides which
memories and nodes you may read. This command writes nothing.

Each row shows loc (the node's path), name, type and memory ID. Rows keep the
server's order: loc, then node ID, across all the App's memories. Use
node get -m <memory-id> <loc> to read a row's content, and app memories
<app-ref> to see the App's memory scope.

Returns ONE page: default 20, capped at 200 when you ask for more. --limit 0
(or a negative limit) counts without returning nodes. --offset skips that
many rows (negative means 0). The table's summary on stderr reports total
and whether more rows exist, with the next offset when needed. --json prints
{items, total, hasMore}; an empty page has items: []. A page can be empty
while hasMore is true if access changed during the read; use the suggested
page offset rather than counting displayed rows.

Filters combine: --prefix, --type, --types, --object-type, repeated --tag, --runnable
(or --runnable=false), --created-by, --role, --updated-after/--updated-before
(RFC 3339, e.g. 2026-10-01T00:00:00Z), and --include-deleted. The App chooses
the memories: --memory/-m and --memory-class are local usage errors (exit 2);
use node list for a memory-scoped read instead. --children-of and --where
are also unsupported here: use app memories to choose a memory, then node list
-m <memory> with the narrowing flag. No request is sent for these flags.
--types accepts a union (repeatable or comma-separated, with CSV quoting),
matching node list --types. Parsed values keep case, spaces, order and duplicates.
The server validates empty lists, --type conflicts, comment and the 20-entry cap;
these refusals are exit 2. Unset sends no nodeTypes key; --types "" sends [].
Deleted nodes still require the server's recovery permission. A missing or unreadable App keeps the
server's not-found response (exit 4); a permission refusal is exit 8.`,
		Example: `  hadron app nodes hrn:app:acme.com:team
  hadron app nodes acme.com:team --prefix tasks: --limit 20 --offset 20
  hadron app nodes acme.com:team --runnable --json
  hadron app nodes acme.com:team --limit 0 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("memory") || cmd.Flags().Changed("memory-class") {
				return exitcode.Newf(exitcode.Usage, "the App chooses the memory scope; --memory/-m and --memory-class are not supported here. Use node list for a memory-scoped read. Nothing was sent")
			}
			for _, flag := range []string{"children-of", "where"} {
				if cmd.Flags().Changed(flag) {
					return exitcode.Newf(exitcode.Usage, "--%s is not supported by App node listings; use app memories to choose a memory, then node list -m <memory> with that flag. Nothing was sent", flag)
				}
			}
			if offset > 1<<31-1 {
				return exitcode.Newf(exitcode.Usage, "--offset exceeds the server's integer range; nothing was sent")
			}
			if limit < 0 {
				limit = 0
			}
			if limit > appNodesMaxLimit {
				limit = appNodesMaxLimit
			}
			if offset < 0 {
				offset = 0
			}
			appRef, err := cmdutil.CanonicalAppRef("<app-ref>", args[0])
			if err != nil {
				return err
			}
			if appRef == "" {
				return exitcode.Newf(exitcode.Usage, "give an App ID or URN as <app-ref>")
			}
			filter := gen.NodeFilter{}
			set := false
			for _, v := range []struct {
				flag, value string
				dst         **string
			}{
				{"prefix", prefix, &filter.LocPrefix}, {"type", nodeType, &filter.NodeType}, {"object-type", objectType, &filter.ObjectType},
				{"created-by", createdBy, &filter.CreatedBy}, {"role", role, &filter.Role}, {"updated-after", updatedAfter, &filter.UpdatedAfter}, {"updated-before", updatedBefore, &filter.UpdatedBefore},
			} {
				if !cmd.Flags().Changed(v.flag) {
					continue
				}
				value := v.value
				if v.flag == "created-by" || v.flag == "role" || strings.HasPrefix(v.flag, "updated-") {
					value = strings.TrimSpace(value)
					if value == "" {
						return exitcode.Newf(exitcode.Usage, "--%s is empty; give a value or drop the flag. Nothing was sent", v.flag)
					}
				}
				if strings.HasPrefix(v.flag, "updated-") {
					if _, err := time.Parse(time.RFC3339, value); err != nil {
						return exitcode.Newf(exitcode.Usage, "--%s needs an RFC 3339 time, e.g. 2026-10-01T00:00:00Z. Nothing was sent", v.flag)
					}
				}
				*v.dst = &value
				set = true
			}
			if cmd.Flags().Changed("types") {
				filter.NodeTypes = &nodeTypes
				set = true
			}
			if cmd.Flags().Changed("tag") {
				filter.Tags = tags
				set = true
			}
			if cmd.Flags().Changed("runnable") {
				filter.IsRunnable = &runnable
				set = true
			}
			if cmd.Flags().Changed("include-deleted") {
				filter.IncludeDeleted = &includeDeleted
				set = true
			}
			var filterArg *gen.NodeFilter
			if set {
				filterArg = &filter
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.AppNodesPage(cmd.Context(), client, appRef, filterArg, limit, offset)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.AppNodesPage == nil || resp.AppNodesPage.Items == nil {
				return exitcode.Newf(exitcode.Error, "the server returned no complete App node page; nothing is shown")
			}
			page := appNodePageDTO{Items: []appNodeDTO{}, Total: resp.AppNodesPage.Total, HasMore: resp.AppNodesPage.HasMore}
			for _, n := range resp.AppNodesPage.Items {
				if n == nil || n.Id == "" || n.MemoryId == "" {
					return exitcode.Newf(exitcode.Error, "the server returned an incomplete App node row; nothing is shown")
				}
				page.Items = append(page.Items, appNodeDTO{ID: n.Id, MemoryID: n.MemoryId, Loc: n.Loc, Name: n.Name, NodeType: n.NodeType})
			}
			if err := output.Write(f.IOStreams, f.JSON, page, func(w io.Writer) error {
				table := output.NewTable(w, "LOC", "NAME", "TYPE", "MEMORY")
				for _, n := range page.Items {
					table.Row(output.SafeCell(n.Loc), output.SafeCell(n.Name), output.SafeCell(n.NodeType), output.SafeCell(n.MemoryID))
				}
				return table.Flush()
			}); err != nil {
				return err
			}
			if f.JSON {
				return nil
			}
			more := "no"
			if page.HasMore {
				more = "yes"
			}
			if _, err := fmt.Fprintf(f.IOStreams.ErrOut, "total: %d; more: %s\n", page.Total, more); err != nil {
				return err
			}
			if page.HasMore {
				nextLimit, nextOffset := limit, offset+limit
				if limit == 0 {
					nextLimit = appNodesDefaultLimit
					nextOffset = offset
				}
				_, err = fmt.Fprintf(f.IOStreams.ErrOut, "next page: re-run the same command with --limit %d --offset %d (keep the same App and filters)\n", nextLimit, nextOffset)
			}
			return err
		},
	}
	cmd.Flags().IntVar(&limit, "limit", appNodesDefaultLimit, "page size (cap 200; 0 counts only)")
	cmd.Flags().IntVar(&offset, "offset", 0, "rows to skip (negative means 0)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "only nodes whose loc starts with this text")
	cmd.Flags().StringVar(&nodeType, "type", "", "only this node type")
	cmd.Flags().StringSliceVar(&nodeTypes, "types", nil, "filter by a union of node types (repeatable or comma-separated CSV; server validates)")
	cmd.Flags().StringVar(&objectType, "object-type", "", "only this record collection (e.g. competitor)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "only nodes with these tags (repeatable)")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "only runnable nodes, or --runnable=false for explicitly non-runnable nodes")
	cmd.Flags().StringVar(&createdBy, "created-by", "", "only nodes created by this user ID")
	cmd.Flags().StringVar(&role, "role", "", "only this role family (spec includes spec.*)")
	cmd.Flags().StringVar(&updatedAfter, "updated-after", "", "only nodes updated after this RFC 3339 time")
	cmd.Flags().StringVar(&updatedBefore, "updated-before", "", "only nodes updated before this RFC 3339 time")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "include deleted nodes when recovery permission allows it")
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "unsupported: the App chooses its memory scope; use node list")
	cmd.Flags().StringArrayVar(&memoryClasses, "memory-class", nil, "unsupported: the App chooses its memory scope; use node list")
	cmd.Flags().StringVar(&childrenOf, "children-of", "", "unsupported here: use node list -m <memory> --children-of")
	cmd.Flags().StringVar(&where, "where", "", "unsupported here: use node list -m <memory> --where")
	return cmd
}
