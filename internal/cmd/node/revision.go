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

// revisionStampedDTO is a snapshot in `revision list|get`: the shared shape
// plus the snapshot's OWN server-authored stamps (hadron-server#1326,
// cli#752) — the authorship of the content it captured and the validation at
// that revision, as the server snapshotted them. Never reconstructed from the
// live node or task. See stamps.go for the null convention.
type revisionStampedDTO struct {
	revisionDTO
	Authorship        *authorshipDTO        `json:"authorship"`
	ContentValidation *contentValidationDTO `json:"contentValidation"`
}

// revisionDetailDTO adds the snapshot's content for single-revision display.
type revisionDetailDTO struct {
	revisionStampedDTO
	Content *string `json:"content"`
}

// stampsRefused reports a server that predates the stamps (#1326): it
// refuses the stamped read by name, and the plain read still works.
func stampsRefused(err error) bool {
	return isUnknownFieldErr(err, "authorship") || isUnknownFieldErr(err, "contentValidation") ||
		isUnknownFieldErr(err, "NodeAuthorship") || isUnknownFieldErr(err, "NodeContentValidationStatus")
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
		Short:   "See and restore a node's earlier versions",
		Long: `Each time a node is changed, the version it replaces is kept as a revision,
so you can see what it said before and go back to it:

  revision list      a node's earlier versions, newest first, with their ids
  revision get       show one version, including its content
  revision restore   make an earlier version current again (undoable)
  revision label     name a version, such as "before the rewrite"
  revision delete    permanently delete one earlier version
  revision clear     permanently delete all of a node's earlier versions

The current version is not a revision; node get shows it. A revision is named
by its id, from revision list. A node is named as elsewhere: its URN, or its
loc with -m; or by its node id, which is the only way to reach the history of
a deleted node.`,
		Example: `  hadron node revision list findings:flaky-ci -m hrn:mem:acme.com:kb
  hadron node revision restore 01a18a07c32479fc9b69bf75dd8567e7`,
	}
	cmd.AddCommand(newCmdRevisionList(f))
	cmd.AddCommand(newCmdRevisionGet(f))
	cmd.AddCommand(newCmdRevisionRestore(f))
	cmd.AddCommand(newCmdRevisionLabel(f))
	cmd.AddCommand(newCmdRevisionDelete(f))
	cmd.AddCommand(newCmdRevisionClear(f))
	return cmd
}

// revisionDefaultLimit is the page `revision list` asks for when --limit is
// unset (cli#967: hidden defaults stay at 20 or less).
const revisionDefaultLimit = 20

func newCmdRevisionList(f *cmdutil.Factory) *cobra.Command {
	var memory string
	var limit, offset int
	cmd := &cobra.Command{
		Use:     "list <node-urn> | <loc> -m <memory> | <node-id>",
		Aliases: []string{"ls"},
		Short:   "List a node's revision history, most recent first",
		Long: `List a node's earlier versions, newest first: each revision's id, when it was
made, the node's name then, who edited it, its label (--reason on node update
sets one; node revision label changes it) and its validation state. The
current version is not listed; node get shows it.

Use a revision id with node revision get to see that version's content, or
with node revision restore to go back to it. 20 are listed unless --limit
says otherwise. stderr says "showing a-b of N" with the real total and, when
more exist, "more: --offset N"; --offset pages on. --limit 0 only counts.`,
		Example: `  hadron node revision list hrn:node:hadronmemory.com:dev:start-here
  hadron node revision list start-here -m hrn:mem:hadronmemory.com:dev --limit 5 --json`,
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
			// cli#1339: nodeRevisionsPage carries the real total and an offset, so
			// the history can be paged to its end. An unset --limit still sends
			// the explicit default (cli#967); stdout and --json stay the bare array.
			if !cmd.Flags().Changed("limit") {
				limit = revisionDefaultLimit
			}
			if offset < 0 {
				return exitcode.Newf(exitcode.Usage, "offset must be non-negative")
			}
			limitArg, offsetArg := &limit, &offset
			dtos := []revisionStampedDTO{}
			var total int
			var hasMore bool
			stamped, err := gen.NodeRevisionsStampedPage(cmd.Context(), client, ref, limitArg, offsetArg)
			switch {
			case err == nil:
				page := stamped.NodeRevisionsPage
				total, hasMore = page.Total, page.HasMore
				for _, v := range page.Items {
					d := revisionStampedDTO{revisionDTO: revisionDTOFrom(v.RevisionFields)}
					if v.Authorship != nil {
						d.Authorship = authorshipFrom(&v.Authorship.NodeAuthorshipFields)
					}
					if v.ContentValidation != nil {
						d.ContentValidation = contentValidationFrom(&v.ContentValidation.NodeContentValidationFields)
					}
					dtos = append(dtos, d)
				}
			case stampsRefused(err):
				// A server without the stamps: the history, and null stamps.
				resp, err := gen.NodeRevisionsPage(cmd.Context(), client, ref, limitArg, offsetArg)
				if err != nil {
					return api.MapError(err)
				}
				page := resp.NodeRevisionsPage
				total, hasMore = page.Total, page.HasMore
				for _, v := range page.Items {
					dtos = append(dtos, revisionStampedDTO{revisionDTO: revisionDTOFrom(v.RevisionFields)})
				}
			default:
				return api.MapError(err)
			}
			if len(dtos) == 0 {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "No revisions at offset %d (total %d).\n", offset, total)
			} else {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "showing %d-%d of %d\n", offset+1, offset+len(dtos), total)
			}
			if hasMore {
				_, _ = fmt.Fprintf(f.IOStreams.ErrOut, "more: --offset %d\n", offset+len(dtos))
			}
			return output.Write(f.IOStreams, f.JSON, dtos, func(w io.Writer) error {
				t := output.NewTable(w, "REVISION-ID", "CREATED", "NAME", "EDITED-BY", "LABEL", "VALIDATION")
				for _, d := range dtos {
					t.Row(output.SafeCell(d.ID), output.SafeCell(d.CreatedAt), output.SafeCell(d.Name), output.SafeCell(editorDisplay(d.revisionDTO)), output.SafeCell(output.Dash(d.RevLabel)), output.SafeCell(validationCell(d.ContentValidation)))
				}
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().IntVar(&limit, "limit", 0, "max revisions to return (default 20; 0 counts only)")
	cmd.Flags().IntVar(&offset, "offset", 0, "revisions to skip (nonnegative), to reach the next page")
	return cmd
}

func newCmdRevisionGet(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "get <revision-id>",
		Aliases: []string{"show"},
		Short:   "Display one revision, including its content",
		Long: `Show one earlier version of a node, named by its revision id (from node
revision list): the node's name, loc and id, when the version was replaced,
who edited it, its label, which fields the edit changed, and the content as
it was. An unknown id exits 4.`,
		Example: `  hadron node revision get 01a18a07c32479fc9b69bf75dd8567e7
  hadron node revision get 01a18a07c32479fc9b69bf75dd8567e7 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			var dto revisionDetailDTO
			found := false
			stamped, err := gen.NodeRevisionStamped(cmd.Context(), client, args[0])
			switch {
			case err == nil:
				if r := stamped.NodeRevision; r != nil {
					found = true
					dto = revisionDetailDTO{revisionStampedDTO: revisionStampedDTO{revisionDTO: revisionDTOFrom(r.RevisionFields)}, Content: r.Content}
					if r.Authorship != nil {
						dto.Authorship = authorshipFrom(&r.Authorship.NodeAuthorshipFields)
					}
					if r.ContentValidation != nil {
						dto.ContentValidation = contentValidationFrom(&r.ContentValidation.NodeContentValidationFields)
					}
				}
			case stampsRefused(err):
				resp, err := gen.NodeRevision(cmd.Context(), client, args[0])
				if err != nil {
					return api.MapError(err)
				}
				if r := resp.NodeRevision; r != nil {
					found = true
					dto = revisionDetailDTO{revisionStampedDTO: revisionStampedDTO{revisionDTO: revisionDTOFrom(r.RevisionFields)}, Content: r.Content}
				}
			default:
				return api.MapError(err)
			}
			if !found {
				return exitcode.Newf(exitcode.NotFound,
					"revision %q not found (or its node is unreadable or soft-deleted)", args[0])
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
		Example: `  hadron node revision restore 01a18a07c32479fc9b69bf75dd8567e7
  hadron node revision restore 01a18a07c32479fc9b69bf75dd8567e7 --truncate --yes`,
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
		Example: `  hadron node revision label 01a18a07c32479fc9b69bf75dd8567e7 --label "before the auth refactor"`,
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
	cmd.Flags().StringVar(&label, "label", "", `revision label (pass "" to clear) (required)`)
	_ = cmd.MarkFlagRequired("label")
	return cmd
}

func newCmdRevisionDelete(f *cmdutil.Factory) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <revision-id>",
		Aliases: []string{"rm"},
		Short:   "Permanently delete one earlier version of a node",
		Long: `Permanently delete one earlier version of a node, named by its revision id
(from node revision list). The node itself and its current content are not
changed. The deleted version can no longer be shown or restored with node
revision restore, and this cannot be undone.

It asks "Delete revision <id>? This cannot be undone." on a terminal; --yes
skips the question and is required when input is not a terminal (otherwise
exit 2). On success it prints "✓ Deleted revision <id>"; an unknown id exits 4.`,
		Example: `  hadron node revision delete 01a18a07c32479fc9b69bf75dd8567e7 --yes`,
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
		Short: "Permanently delete all earlier versions of a node",
		Long: `Permanently delete every earlier version of a node, leaving only the current
one. The node and its current content are not changed, but node revision list
is empty afterwards: no earlier version can be shown or restored. This cannot
be undone. On success it prints how many were deleted ("✓ Deleted 3
revision(s)").

It asks "Delete all revision history for <node>? This cannot be undone." on a
terminal; --yes skips the question and is required when input is not a terminal
(otherwise exit 2).

A deleted node is named by its node id (shown by node recovery list), since
its URN no longer resolves. Clearing its history does not stop node restore
from bringing it back.`,
		Example: `  hadron node revision clear findings:flaky-ci -m hrn:mem:acme.com:kb --yes
  hadron node revision clear 01a18a0625eb76aaab18f053afd47205 --yes`,
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
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
				"%q is not a fully-qualified node URN — expected hrn:node:<root>:<slug>:<loc>, or pass -m hrn:mem:<root>:<slug> with a bare loc (or a node id to reach a soft-deleted node)", ref)
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
	renderStamps(w, "  ", dto.Authorship, dto.ContentValidation, "this snapshot's revision")
	if dto.Content != nil && *dto.Content != "" {
		fmt.Fprintf(w, "\n%s\n", *dto.Content)
	}
	return nil
}

// validationCell is a history table's VALIDATION column: the snapshot's
// state, or a dash when the server predates the stamps.
func validationCell(v *contentValidationDTO) string {
	if v == nil {
		return "—"
	}
	return v.State
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
