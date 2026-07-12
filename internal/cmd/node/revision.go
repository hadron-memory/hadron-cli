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

// revisionEditorDTO is the editor of a snapshot resolved to PUBLIC identifiers
// only (#617) — never name/email. Both fields are nullable.
type revisionEditorDTO struct {
	Handle *string `json:"handle"`
	URN    *string `json:"urn"`
}

// revisionDTO is the stable --json shape for a node revision snapshot in list
// output.
type revisionDTO struct {
	ID           string             `json:"id"`
	NodeID       string             `json:"nodeId"`
	Loc          string             `json:"loc"`
	Name         string             `json:"name"`
	Description  *string            `json:"description"`
	Tags         []string           `json:"tags"`
	CreatedAt    string             `json:"createdAt"`
	EditedBy     *string            `json:"editedBy"`
	EditedByInfo *string            `json:"editedByInfo"`
	EditedByUser *revisionEditorDTO `json:"editedByUser"`
	RevLabel     *string            `json:"revLabel"`
	Changes      []string           `json:"changes"`
}

// revisionDetailDTO adds the snapshot's content for single-revision display.
type revisionDetailDTO struct {
	revisionDTO
	Content *string `json:"content"`
}

// restoredNodeDTO is the stable --json shape returned by `revision restore`.
type restoredNodeDTO struct {
	ID        string `json:"id"`
	MemoryID  string `json:"memoryId"`
	Loc       string `json:"loc"`
	Name      string `json:"name"`
	NodeType  string `json:"nodeType"`
	UpdatedAt string `json:"updatedAt"`
	Truncated bool   `json:"truncated"`
}

func newCmdRevision(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "revision <command>",
		Aliases: []string{"revisions", "rev"},
		Short:   "Inspect and manage a node's revision history",
		Long: `Every node edit snapshots the previous state as a revision (#617/#620). These
commands list that history, display, label, or restore a revision, and prune it.

A revision is addressed by its revision id (from ` + "`revision list`" + `). A node is
addressed the same way as elsewhere — a fully-qualified <org>::<memory>::<loc>
URN, or a bare <loc> with -m/--memory — plus a bare node id, which is the only
way to reach a soft-deleted node's history for cleanup.`,
	}
	cmd.AddCommand(newCmdRevisionList(f))
	cmd.AddCommand(newCmdRevisionGet(f))
	cmd.AddCommand(newCmdRevisionRestore(f))
	cmd.AddCommand(newCmdRevisionLabel(f))
	cmd.AddCommand(newCmdRevisionDelete(f))
	cmd.AddCommand(newCmdRevisionClear(f))
	return cmd
}

func newCmdRevisionList(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var limit int
	cmd := &cobra.Command{
		Use:     "list <node-urn> | <loc> -m <memory> | <node-id>",
		Aliases: []string{"ls"},
		Short:   "List a node's revision history, most recent first",
		Example: `  hadron node revision list hadronmemory.com::dev::start-here
  hadron node revision list start-here -m hadronmemory.com::dev --limit 5 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 0 {
				return exitcode.Newf(exitcode.Usage, "limit must be non-negative")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref, err := revisionNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			var limitArg *int
			if cmd.Flags().Changed("limit") {
				limitArg = &limit
			}
			resp, err := gen.NodeRevisions(cmd.Context(), client, ref, limitArg)
			if err != nil {
				return api.MapError(err)
			}
			dtos := make([]revisionDTO, 0, len(resp.NodeRevisions))
			for _, v := range resp.NodeRevisions {
				dtos = append(dtos, revisionDTOFrom(v.RevisionFields))
			}
			return output.Write(f.IOStreams, f.JSON, dtos, func(w io.Writer) error {
				t := output.NewTable(w, "REVISION-ID", "CREATED", "NAME", "EDITED-BY", "LABEL")
				for _, d := range dtos {
					t.Row(d.ID, d.CreatedAt, d.Name, editorDisplay(d), output.Dash(d.RevLabel))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().IntVar(&limit, "limit", 0, "max revisions to return (server default when unset)")
	return cmd
}

func newCmdRevisionGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get <revision-id>",
		Aliases: []string{"show"},
		Short:   "Display one revision, including its content",
		Example: `  hadron node revision get clr8x2k9p0000
  hadron node revision get clr8x2k9p0000 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.NodeRevision(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			if resp.NodeRevision == nil {
				return exitcode.Newf(exitcode.NotFound,
					"revision %q not found (or its node is unreadable or soft-deleted)", args[0])
			}
			r := resp.NodeRevision
			dto := revisionDetailDTO{
				revisionDTO: revisionDTOFrom(r.RevisionFields),
				Content:     r.Content,
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeRevisionDetail(w, dto)
			})
		},
	}
	return cmd
}

func newCmdRevisionRestore(f *cmdutil.Factory) *cobra.Command {
	var truncate, yes bool
	cmd := &cobra.Command{
		Use:   "restore <revision-id>",
		Short: "Restore a node to a revision",
		Long: `Restore a node to a revision snapshot. By default this first snapshots the
current state, so the restore is itself undoable.

--truncate instead makes the selected revision the new baseline: it restores and
deletes every revision newer than it (the pre-restore snapshot is skipped), as
one atomic transaction. This discards the intervening history and is not
undoable, so it prompts for confirmation (gated by --yes non-interactively).`,
		Example: `  hadron node revision restore clr8x2k9p0000
  hadron node revision restore clr8x2k9p0000 --truncate --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if truncate {
				if err := cmdutil.Confirm(f.IOStreams, yes, fmt.Sprintf(
					"Restore revision %s and delete every newer revision? This discards the intervening history and cannot be undone.",
					args[0])); err != nil {
					return err
				}
			}
			var truncateArg *bool
			if truncate {
				truncateArg = &truncate
			}
			resp, err := gen.RestoreNodeRevision(cmd.Context(), client, args[0], truncateArg)
			if err != nil {
				return api.MapError(err)
			}
			n := resp.RestoreNodeRevision
			if n == nil {
				return exitcode.Newf(exitcode.NotFound, "revision %q not found", args[0])
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
				msg := fmt.Sprintf("✓ Restored node %s from revision %s", dto.Loc, args[0])
				if truncate {
					msg += " (newer revisions deleted)"
				}
				_, err := fmt.Fprintln(w, msg)
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&truncate, "truncate", false, "also delete every revision newer than the restored one (irreversible)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (--truncate only)")
	return cmd
}

func newCmdRevisionLabel(f *cmdutil.Factory) *cobra.Command {
	var label string
	cmd := &cobra.Command{
		Use:   "label <revision-id> --label <text>",
		Short: "Set a revision's label",
		Long: `Set a revision's user-facing label (500-char cap). Pass --label "" to clear it.
The label also captures the reason supplied with the edit that took the snapshot.`,
		Example: `  hadron node revision label clr8x2k9p0000 --label "before the auth refactor"`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// --label is required, so it's always sent (an explicit "" clears it).
			resp, err := gen.UpdateNodeRevision(cmd.Context(), client, args[0], &label)
			if err != nil {
				return api.MapError(err)
			}
			dto := revisionDTOFrom(resp.UpdateNodeRevision.RevisionFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Labeled revision %s: %s\n", dto.ID, output.Dash(dto.RevLabel))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&label, "label", "", `revision label (pass "" to clear)`)
	_ = cmd.MarkFlagRequired("label")
	return cmd
}

func newCmdRevisionDelete(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <revision-id>",
		Aliases: []string{"rm"},
		Short:   "Delete one revision from a node's history",
		Example: `  hadron node revision delete clr8x2k9p0000 --yes`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "revision "+args[0]); err != nil {
				return err
			}
			resp, err := gen.DeleteNodeRevision(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			// The server errors NOT_FOUND for an unknown id, so a false here would
			// be a contract surprise; treat it as not-found rather than printing a
			// success line that contradicts the --json `deleted:false`.
			if !resp.DeleteNodeRevision {
				return exitcode.Newf(exitcode.NotFound, "revision %q not found", args[0])
			}
			dto := map[string]any{"revisionId": args[0], "deleted": resp.DeleteNodeRevision}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted revision %s\n", args[0])
				return err
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func newCmdRevisionClear(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var yes bool
	cmd := &cobra.Command{
		Use:   "clear <node-urn> | <loc> -m <memory> | <node-id>",
		Short: "Delete all of a node's revision history",
		Long: `Delete every revision in a node's history, printing the count removed.
Reachable on a soft-deleted node (cleanup) by passing its node id, since the
read surfaces no longer resolve its URN.`,
		Example: `  hadron node revision clear hadronmemory.com::dev::start-here --yes
  hadron node revision clear clr8x2k9p0000 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			ref, err := revisionNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}
			if err := cmdutil.ConfirmDeletion(f.IOStreams, yes, "all revision history for "+args[0]); err != nil {
				return err
			}
			resp, err := gen.ClearNodeHistory(cmd.Context(), client, ref)
			if err != nil {
				return api.MapError(err)
			}
			dto := map[string]any{"nodeRef": ref, "deletedCount": resp.ClearNodeHistory}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ Deleted %d revision(s)\n", resp.ClearNodeHistory)
				return err
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

// revisionNodeRef resolves a user-supplied node reference to a value the server's
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
func revisionNodeRef(cmd *cobra.Command, client graphql.Client, memory, ref string) (string, error) {
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

// writeRevisionDetail renders a single revision for human output.
func writeRevisionDetail(w io.Writer, dto revisionDetailDTO) error {
	fmt.Fprintf(w, "%s\n  revision: %s\n  node: %s\n  loc: %s\n", dto.Name, dto.ID, dto.NodeID, dto.Loc)
	fmt.Fprintf(w, "  created: %s\n", dto.CreatedAt)
	fmt.Fprintf(w, "  edited by: %s\n", editorDisplay(dto.revisionDTO))
	if dto.RevLabel != nil && *dto.RevLabel != "" {
		fmt.Fprintf(w, "  label: %s\n", *dto.RevLabel)
	}
	if len(dto.Changes) > 0 {
		fmt.Fprintf(w, "  changed: %v\n", dto.Changes)
	}
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
}

// editorDisplay renders a snapshot's editor for human output: @handle when a
// user resolved, else its urn, else the raw editedByInfo/editedBy id, else a
// dash.
func editorDisplay(d revisionDTO) string {
	if e := d.EditedByUser; e != nil {
		if e.Handle != nil && *e.Handle != "" {
			return "@" + *e.Handle
		}
		if e.URN != nil && *e.URN != "" {
			return *e.URN
		}
	}
	if d.EditedByInfo != nil && *d.EditedByInfo != "" {
		return *d.EditedByInfo
	}
	if d.EditedBy != nil && *d.EditedBy != "" {
		return *d.EditedBy
	}
	return "—"
}

// revisionDTOFrom builds the stable DTO from the shared genqlient fragment
// embedded in every revision-returning operation.
func revisionDTOFrom(r gen.RevisionFields) revisionDTO {
	return revisionDTO{
		ID:           r.Id,
		NodeID:       r.NodeId,
		Loc:          r.Loc,
		Name:         r.Name,
		Description:  r.Description,
		Tags:         tagsOf(r.Tags),
		CreatedAt:    r.CreatedAt,
		EditedBy:     r.EditedBy,
		EditedByInfo: r.EditedByInfo,
		EditedByUser: editorFrom(r.EditedByUser),
		RevLabel:     r.RevLabel,
		Changes:      tagsOf(r.Changes),
	}
}

func editorFrom(e *gen.RevisionFieldsEditedByUserNodeRevisionEditor) *revisionEditorDTO {
	if e == nil {
		return nil
	}
	return &revisionEditorDTO{Handle: e.Handle, URN: e.Urn}
}

// tagsOf normalizes a possibly-nil string slice to a non-nil one so --json
// renders [] rather than null.
func tagsOf(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
