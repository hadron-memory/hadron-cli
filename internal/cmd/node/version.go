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

// versionEditorDTO is the editor of a snapshot resolved to PUBLIC identifiers
// only (#617) — never name/email. Both fields are nullable: the server returns
// this object only when editedBy is a user with a handle.
type versionEditorDTO struct {
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

// versionDTO is the stable --json shape for a node version snapshot in list
// output.
type versionDTO struct {
	ID           string            `json:"id"`
	NodeID       string            `json:"nodeId"`
	Loc          string            `json:"loc"`
	Name         string            `json:"name"`
	Description  *string           `json:"description"`
	Tags         []string          `json:"tags"`
	CreatedAt    string            `json:"createdAt"`
	EditedBy     *string           `json:"editedBy"`
	EditedByUser *versionEditorDTO `json:"editedByUser"`
}

// versionDetailDTO adds the snapshot's content for single-version display.
type versionDetailDTO struct {
	versionDTO
	Content *string `json:"content"`
}

// restoredNodeDTO is the stable --json shape returned by `version restore`.
type restoredNodeDTO struct {
	ID        string `json:"id"`
	MemoryID  string `json:"memoryId"`
	Loc       string `json:"loc"`
	Name      string `json:"name"`
	NodeType  string `json:"nodeType"`
	UpdatedAt string `json:"updatedAt"`
	Truncated bool   `json:"truncated"`
}

func newCmdVersion(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "version <command>",
		Aliases: []string{"versions"},
		Short:   "Inspect and manage a node's version history",
		Long: `Every node edit snapshots the previous state (#617). These commands list
that history, display or restore a snapshot, and prune it.

A snapshot is addressed by its version id (from ` + "`version list`" + `). A node is
addressed the same way as elsewhere — a fully-qualified <org>::<memory>::<loc>
URN, or a bare <loc> with -m/--memory — plus a bare node id, which is the only
way to reach a soft-deleted node's history for cleanup.`,
	}
	cmd.AddCommand(newCmdVersionList(f))
	cmd.AddCommand(newCmdVersionGet(f))
	cmd.AddCommand(newCmdVersionRestore(f))
	cmd.AddCommand(newCmdVersionDelete(f))
	cmd.AddCommand(newCmdVersionClear(f))
	return cmd
}

func newCmdVersionList(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var limit int
	cmd := &cobra.Command{
		Use:     "list <node-urn> | <loc> -m <memory> | <node-id>",
		Aliases: []string{"ls"},
		Short:   "List a node's version history, most recent first",
		Example: `  hadron node version list hadronmemory.com::dev::start-here
  hadron node version list start-here -m hadronmemory.com::dev --limit 5 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return exitcode.Newf(exitcode.Usage, "limit must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref, err := versionNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			var limitArg *int
			if cmd.Flags().Changed("limit") {
				limitArg = &limit
			}
			resp, err := gen.NodeVersions(cmd.Context(), client, ref, limitArg)
			if err != nil {
				return api.MapError(err)
			}
			dtos := make([]versionDTO, 0, len(resp.NodeVersions))
			for _, v := range resp.NodeVersions {
				dtos = append(dtos, versionDTO{
					ID:           v.Id,
					NodeID:       v.NodeId,
					Loc:          v.Loc,
					Name:         v.Name,
					Description:  v.Description,
					Tags:         tagsOf(v.Tags),
					CreatedAt:    v.CreatedAt,
					EditedBy:     v.EditedBy,
					EditedByUser: editorFromList(v.EditedByUser),
				})
			}
			return output.Write(f.IOStreams, f.JSON, dtos, func(w io.Writer) error {
				t := output.NewTable(w, "VERSION-ID", "CREATED", "NAME", "EDITED-BY")
				for _, d := range dtos {
					t.Row(d.ID, d.CreatedAt, d.Name, editorDisplay(d))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().IntVar(&limit, "limit", 0, "max snapshots to return (server default when unset)")
	return cmd
}

func newCmdVersionGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get <version-id>",
		Aliases: []string{"show"},
		Short:   "Display one snapshot, including its content",
		Example: `  hadron node version get clr8x2k9p0000
  hadron node version get clr8x2k9p0000 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.NodeVersion(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.NodeVersion == nil {
				return exitcode.Newf(exitcode.NotFound,
					"version %q not found (or its node is unreadable or soft-deleted)", args[0])
			}
			v := resp.NodeVersion
			dto := versionDetailDTO{
				versionDTO: versionDTO{
					ID:           v.Id,
					NodeID:       v.NodeId,
					Loc:          v.Loc,
					Name:         v.Name,
					Description:  v.Description,
					Tags:         tagsOf(v.Tags),
					CreatedAt:    v.CreatedAt,
					EditedBy:     v.EditedBy,
					EditedByUser: editorFromShow(v.EditedByUser),
				},
				Content: v.Content,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				fmt.Fprintf(w, "%s\n  version: %s\n  node: %s\n  loc: %s\n", dto.Name, dto.ID, dto.NodeID, dto.Loc)
				fmt.Fprintf(w, "  created: %s\n", dto.CreatedAt)
				fmt.Fprintf(w, "  edited by: %s\n", editorDisplay(dto.versionDTO))
				if dto.Description != nil && *dto.Description != "" {
					fmt.Fprintf(w, "  about: %s\n", *dto.Description)
				}
				if len(dto.Tags) > 0 {
					fmt.Fprintf(w, "  tags: %v\n", dto.Tags)
				}
				if dto.Content != nil && *dto.Content != "" {
					fmt.Fprintf(w, "\n%s\n", *dto.Content)
				}
				return nil
			})
		},
	}
	return cmd
}

func newCmdVersionRestore(f *cmdutil.Factory) *cobra.Command {
	var truncate, yes bool
	cmd := &cobra.Command{
		Use:   "restore <version-id>",
		Short: "Restore a node to a snapshot",
		Long: `Restore a node to a version snapshot. By default this first snapshots the
current state, so the restore is itself undoable.

--truncate instead makes the selected snapshot the new baseline: it restores and
deletes every snapshot newer than it (the pre-restore snapshot is skipped), as
one atomic transaction. This discards the intervening history and is not
undoable, so it prompts for confirmation (gated by --yes non-interactively).`,
		Example: `  hadron node version restore clr8x2k9p0000
  hadron node version restore clr8x2k9p0000 --truncate --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if truncate {
				if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
					"Restore version %s and delete every newer snapshot? This discards the intervening history and cannot be undone.",
					args[0])); err != nil {
					return err
				}
			}
			var truncateArg *bool
			if truncate {
				truncateArg = &truncate
			}
			resp, err := gen.RestoreNodeVersion(cmd.Context(), client, args[0], truncateArg)
			if err != nil {
				return api.MapError(err)
			}
			n := resp.RestoreNodeVersion
			if n == nil {
				return exitcode.Newf(exitcode.NotFound, "version %q not found", args[0])
			}
			dto := restoredNodeDTO{
				ID:        n.Id,
				MemoryID:  n.MemoryId,
				Loc:       n.Loc,
				Name:      n.Name,
				NodeType:  n.NodeType,
				UpdatedAt: n.UpdatedAt,
				Truncated: truncate,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				msg := fmt.Sprintf("✓ Restored node %s from version %s", dto.Loc, args[0])
				if truncate {
					msg += " (newer snapshots deleted)"
				}
				_, err := fmt.Fprintln(w, msg)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&truncate, "truncate", false, "also delete every snapshot newer than the restored one (irreversible)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (--truncate only)")
	return cmd
}

func newCmdVersionDelete(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <version-id>",
		Aliases: []string{"rm"},
		Short:   "Delete one snapshot from a node's history",
		Example: `  hadron node version delete clr8x2k9p0000 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "version "+args[0]); err != nil {
				return err
			}
			resp, err := gen.DeleteNodeVersion(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// The server errors NOT_FOUND for an unknown id, so a false here would
			// be a contract surprise; treat it as not-found rather than printing a
			// success line that contradicts the --json `deleted:false`.
			if !resp.DeleteNodeVersion {
				return exitcode.Newf(exitcode.NotFound, "version %q not found", args[0])
			}
			dto := map[string]any{"versionId": args[0], "deleted": resp.DeleteNodeVersion}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted version %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdVersionClear(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear <node-urn> | <loc> -m <memory> | <node-id>",
		Short: "Delete all of a node's version history",
		Long: `Delete every snapshot in a node's version history, printing the count removed.
Reachable on a soft-deleted node (cleanup) by passing its node id, since the
read surfaces no longer resolve its URN.`,
		Example: `  hadron node version clear hadronmemory.com::dev::start-here --yes
  hadron node version clear clr8x2k9p0000 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref, err := versionNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "all version history for "+args[0]); err != nil {
				return err
			}
			resp, err := gen.ClearNodeHistory(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			dto := map[string]any{"nodeRef": ref, "deletedCount": resp.ClearNodeHistory}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted %d snapshot(s)\n", resp.ClearNodeHistory)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// versionNodeRef resolves a user-supplied node reference to a value the server's
// nodeRef arg accepts (a node PK or URN). A fully-qualified URN, or a
// -m/--memory + bare loc, is resolved client-side to the node's PK — consistent
// with the other node commands and validating the URN grammar.
//
// A colon-free bare token with neither is taken as a node PK and passed through
// verbatim: the server's nodeRef accepts a PK, and it is the only way to reach a
// soft-deleted node's history, whose URN no longer resolves. A bare token that
// carries single colons is a namespaced loc (e.g. findings:flaky-ci), never a
// PK — reject it with the same guidance the other node commands give rather than
// letting it miss server-side as a bogus id.
func versionNodeRef(cmd *cobra.Command, client graphql.Client, memory, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.TrimSpace(memory) == "" &&
		!strings.Contains(ref, "::") &&
		!strings.HasPrefix(ref, "hrn:") &&
		!strings.HasPrefix(ref, "urn:") {
		if strings.Contains(ref, ":") {
			return "", exitcode.Newf(exitcode.Usage,
				"%q is not a fully-qualified node URN — expected <org>::<memory>::<loc>, or pass -m <org::memory> with a bare loc (or a node id to reach a soft-deleted node)", ref)
		}
		return ref, nil
	}
	return cmdutil.ResolveNodeRef(cmd, client, memory, ref)
}

// editorDisplay renders a snapshot's editor for human output: @handle when a
// user resolved, else its urn, else the raw editedBy id, else a dash.
func editorDisplay(d versionDTO) string {
	if e := d.EditedByUser; e != nil {
		if e.Handle != nil && *e.Handle != "" {
			return "@" + *e.Handle
		}
		if e.URN != nil && *e.URN != "" {
			return *e.URN
		}
	}
	if d.EditedBy != nil && *d.EditedBy != "" {
		return *d.EditedBy
	}
	return "—"
}

// The two genqlient selections yield distinct NodeVersionEditor types (list vs
// single-version), so alias each and give it a converter. Both carry the same
// handle/urn fields; the nil check keeps a typed-nil from masquerading as a
// present editor.
type (
	listEditor = gen.NodeVersionsNodeVersionsNodeVersionEditedByUserNodeVersionEditor
	showEditor = gen.NodeVersionNodeVersionEditedByUserNodeVersionEditor
)

func editorFromList(e *listEditor) *versionEditorDTO {
	if e == nil {
		return nil
	}
	return &versionEditorDTO{Handle: e.Handle, URN: e.Urn}
}

func editorFromShow(e *showEditor) *versionEditorDTO {
	if e == nil {
		return nil
	}
	return &versionEditorDTO{Handle: e.Handle, URN: e.Urn}
}

// tagsOf normalizes a possibly-nil tag slice to a non-nil one so --json renders
// [] rather than null.
func tagsOf(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
