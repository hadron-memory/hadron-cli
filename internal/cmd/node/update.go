package node

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aymanbagabas/go-udiff"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		memory         string
		name           string
		content        string
		contentFile    string
		nodeType       string
		objectType     string
		role           string
		description    string
		abstract       string
		abstractFile   string
		data           string
		dataFile       string
		dataMerge      string
		dataMergeFile  string
		properties     string
		propertiesFile string
		runnable       bool
		force          bool
		dryRun         bool
		reason         string
		tags           []string
	)
	cmd := &cobra.Command{
		Use:   "update <node-urn> | <loc> -m <memory>",
		Short: "Update a node",
		Long: `Update an existing node by its fully-qualified URN
(hrn:node:<root>:<slug>:<loc>), or by a bare <loc> with -m/--memory. Only the
fields you pass change; everything else is preserved (pass an explicit
empty string, e.g. --description "", to clear a field).

The data bag can be written two ways:

  --data / --data-file       REPLACE the whole data object (pass "null" to
                             clear it).
  --data-merge / --data-merge-file
                             MERGE a JSON object into the existing data: its
                             top-level keys overwrite, unmentioned keys are
                             preserved (a shallow merge — nested object values
                             are replaced wholesale, not deep-merged). The
                             patch must be an object.

Replace and merge are different operations, so --data and --data-merge are
mutually exclusive.

EDITING EXISTING CONTENT: read it with ` + "`node get <ref> --body-only`" + `, which
prints the stored body byte for byte (#873). A plain ` + "`node get`" + ` compiles the
node's Mustache templates, so writing that text back deletes every {{…}}
placeholder (#736); a text-mode ` + "`node get --raw`" + ` prints a metadata header above
the body, which would be saved INTO it.

Two guards stand on a new body (--content / --content-file). One that starts
with that header is REFUSED (exit 2, nothing written) unless --force. One that
drops a {{…}} tag the stored body has — a placeholder or a {{=…=}} delimiter
change — is written, with a warning on stderr: removing a placeholder can be
the edit.

--dry-run writes nothing: it reads the node, runs every client-side check a
write would (the header guard, the role and kind refusals, input parsing), and
shows the fields that would change, with a diff of the stored body against the
new one. The server's own validation (a schema-governed memory, the door's
refusals) runs only on the write. It previews the field update only, so
--data-merge is refused with it. Empty or whitespace --role clears via literal
null in the current kind's door; --data-merge cannot be combined with a role
clear because it is a separate write (use --data for one atomic replacement).

--content - (or --content-file -), --abstract - and --data-merge - read
standard input, and are for
a PIPE (cat file | hadron node update ...). Each is REFUSED when stdin is an
interactive terminal, which can truncate large input before the CLI sees it;
use --content-file, --abstract-file or --data-merge-file instead.

--object-type sets the node's structured-storage collection (#725); pass "" to
clear it (→ ordinary node), omit to preserve. --properties / --properties-file
REPLACE the typed properties bag the schema governs (pass "null" to clear) —
distinct from --data. (There is no --properties-merge yet; a shallow merge needs
the server-side updateNodeProperties mutation, tracked as hadron-server#742. On a
schema-governed memory the server validates the result and rejects a violation.)`,
		Example: `  hadron node update hrn:node:acme.com:kb:findings:flaky-ci --name "Flaky CI (resolved)"
  cat updated.md | hadron node update findings:flaky-ci -m hrn:mem:acme.com:kb --content -
  hadron node update hrn:node:acme.com:kb:findings:flaky-ci --data-merge '{"status":"closed"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			clearRole := changed("role") && strings.TrimSpace(role) == ""
			replaceData := changed("data") || changed("data-file")
			mergeData := changed("data-merge") || changed("data-merge-file")
			replaceProps := changed("properties") || changed("properties-file")
			anyField := changed("name") || changed("content") || changed("content-file") ||
				changed("type") || changed("object-type") || changed("description") ||
				changed("abstract") || changed("abstract-file") ||
				replaceData || replaceProps || changed("runnable") || changed("tag") ||
				changed("role")
			if !anyField && !mergeData {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			if clearRole && mergeData {
				return exitcode.Newf(exitcode.Usage, "clearing --role and --data-merge cannot share one mutation; use --data to replace the bag atomically with the clear")
			}
			if dryRun && mergeData {
				return exitcode.Newf(exitcode.Usage,
					"--dry-run previews the field update only; --data-merge is a separate write it does not preview — drop one")
			}
			contentChanged := changed("content") || changed("content-file")
			// Replace and merge are different operations (distinct mutations).
			if replaceData && mergeData {
				return exitcode.Newf(exitcode.Usage, "--data (replace) and --data-merge (merge) are mutually exclusive")
			}
			// The inline/-file pairs are mutually exclusive. Guard on Changed()
			// (not the resolved value): an explicit --X "" would otherwise slip past
			// the value check and let the file silently win.
			if changed("data") && changed("data-file") {
				return exitcode.Newf(exitcode.Usage, "--data and --data-file are mutually exclusive")
			}
			if changed("properties") && changed("properties-file") {
				return exitcode.Newf(exitcode.Usage, "--properties and --properties-file are mutually exclusive")
			}
			if changed("data-merge") && changed("data-merge-file") {
				return exitcode.Newf(exitcode.Usage, "--data-merge and --data-merge-file are mutually exclusive")
			}
			// content, abstract, and the merge patch can each read stdin via
			// "-", but stdin can only be consumed once.
			stdinReaders := 0
			if (changed("content") && content == "-") || (changed("content-file") && contentFile == "-") {
				stdinReaders++
			}
			if changed("abstract") && abstract == "-" {
				stdinReaders++
			}
			if changed("data-merge") && dataMerge == "-" {
				stdinReaders++
			}
			if stdinReaders > 1 {
				return exitcode.Newf(exitcode.Usage, "only one of --content - (--content-file -), --abstract -, --data-merge - may read stdin")
			}
			// Refuse a terminal HERE rather than at the read (#643): this
			// command resolves the ref and fetches the node before building
			// its input, and an argument this invalid should not cost two
			// round trips first. Same refusal as resolveContent's, from the
			// one definition in cmdutil. All three stdin readers are
			// DOCUMENTS (#648) — content, a paragraph abstract, a JSON merge
			// patch — none a secret, so all three take the guard.
			for _, doc := range []struct{ flag, value, fileFlag string }{
				{"content", content, "--content-file"},
				{"content-file", contentFile, "--content-file"},
				{"abstract", abstract, "--abstract-file"},
				{"data-merge", dataMerge, "--data-merge-file"},
			} {
				if changed(doc.flag) && doc.value == "-" {
					if err := cmdutil.RefuseDocumentStdinFromTerminal(
						f.IOStreams.IsInputTerminal(), "--"+doc.flag+" -", doc.fileFlag); err != nil {
						return err
					}
				}
			}
			// --abstract and --abstract-file are mutually exclusive. Guard on
			// Changed() (not the resolved value): an explicit --abstract "" to
			// clear would otherwise slip past ResolveTextInput's value check and
			// let the file silently win.
			if changed("abstract") && changed("abstract-file") {
				return exitcode.Newf(exitcode.Usage, "--abstract and --abstract-file are mutually exclusive")
			}
			// The new body is read and checked BEFORE any request: a body
			// carrying the get header is refused without a round trip (#873).
			var body string
			if contentChanged {
				b, err := resolveContent(content, contentFile, f.IOStreams.In, f.IOStreams.IsInputTerminal())
				if err != nil {
					return err
				}
				if !force {
					if err := cmdutil.RefuseGetHeaderPrefix(b, "--force"); err != nil {
						return err
					}
				}
				body = b
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// --reason rides along on whichever mutation runs; the server records
			// it in version history (editedBy). A blank/whitespace-only reason is
			// treated as unset — otherwise it would override the server's
			// clientId/userId fallback with an empty editedBy. nil = omit/preserve.
			var reasonPtr *string
			if r := strings.TrimSpace(reason); r != "" {
				reasonPtr = &r
			}

			// Both the field update and the data merge address the node by id,
			// so resolve the ref (full URN, or bare loc + -m) once up front.
			// updateNode targets `id` XOR (memoryId, loc) and preserves every
			// omitted field — name included — so no full-node pre-fetch is
			// needed anymore.
			nodeID, err := cmdutil.ResolveNodeRef(cmd, client, memory, args[0])
			if err != nil {
				return err
			}

			var dto nodeDTO
			if anyField {
				// The node's CURRENT kind, read once to choose
				// the door for the write (the gate reads the kind the node is
				// now AND the kind it will be; an omitted field preserves
				// the stored one). Clearing requires this read to succeed.
				//
				// INSIDE the field branch, not above it: a `--data-merge`-only
				// update writes through `updateNodeData` and needs no door, so
				// reading here would spend a round trip on every merge for an
				// answer nothing consults.
				//
				// Except for clears/previews, a failed read leaves the zero value, which
				// routes to the generic surface and lets the server give the
				// authoritative refusal.
				//
				// A new body or a preview needs the STORED body as well, to
				// compare against (#873), so that read is the raw one; it
				// carries the same kind fields.
				cur := api.NodeKindState{}
				var stored *gen.GetNodeNode
				if contentChanged || dryRun {
					existing, gerr := gen.GetNodeRaw(cmd.Context(), client, nodeID)
					switch {
					case gerr == nil && existing.Node != nil:
						stored = existing.Node
					case (dryRun || clearRole) && gerr != nil:
						return api.MapError(gerr)
					case dryRun || clearRole:
						return exitcode.Newf(exitcode.NotFound, "node %q not found, or not readable by you", args[0])
					}
				} else {
					existing, gerr := gen.GetNode(cmd.Context(), client, nodeID)
					if gerr != nil && clearRole {
						return api.MapError(gerr)
					}
					if gerr == nil {
						stored = existing.Node
					}
					if clearRole && stored == nil {
						return exitcode.Newf(exitcode.NotFound, "node %q not found, or not readable by you", args[0])
					}
				}
				if stored != nil {
					cur.Role = stored.Role
					cur.IsRunnable = stored.IsRunnable != nil && *stored.IsRunnable
				}

				input := gen.UpdateNodeInput{
					Id: &nodeID,
				}
				if changed("name") {
					input.Name = &name
				}
				warnings := []string{}
				if contentChanged {
					input.Content = &body
					if stored != nil {
						if w := cmdutil.TemplateDropWarning(strVal(stored.Content), body); w != "" {
							warnings = append(warnings, w)
						}
					}
				}
				if changed("type") {
					input.NodeType = &nodeType
				}
				if changed("object-type") {
					input.ObjectType = &objectType
				}
				// #1201. Sending this is what makes the node GOVERNED — and the
				// gate reads the kind the node will be as well as the one it
				// is, so an explicit role here counts when the door is chosen.
				if changed("role") {
					// Two governed kinds cannot coexist, and an UPDATE can
					// produce that pair from a node that is already runnable —
					// the resulting state is what the gate reads (@codex on
					// #615).
					resultRunnable := cur.IsRunnable
					if changed("runnable") {
						resultRunnable = runnable
					}
					if kinds := api.GovernedKindConflict(&role, resultRunnable); kinds != nil {
						return exitcode.Newf(exitcode.Usage,
							"this would leave the node as two governed kinds at once — %s — and each door is exempt from its OWN kind only, so every one of them refuses it. Clear the other first, or write it with `hadron api` if the server's register has changed",
							strings.Join(kinds, " AND "))
					}

					input.Role = &role
				}
				if changed("description") {
					input.Description = &description
				}
				if changed("abstract") || changed("abstract-file") {
					abs, err := cmdutil.ResolveTextInput("abstract", abstract, abstractFile, f.IOStreams.In)
					if err != nil {
						return err
					}
					input.Abstract = &abs
				}
				if replaceData {
					raw, err := resolveJSONObject("--data", data, dataFile)
					if err != nil {
						return err
					}
					input.Data = raw
				}
				if replaceProps {
					raw, err := resolveJSONObject("--properties", properties, propertiesFile)
					if err != nil {
						return err
					}
					input.Properties = raw
				}
				if changed("runnable") {
					input.IsRunnable = &runnable
				}
				if changed("tag") {
					input.Tags = tags
				}
				input.Reason = reasonPtr

				if clearRole {
					// A preview must make the same current ∪ resulting kind
					// check as the literal-null writer. The blank routing value
					// denotes no resulting role; it is never sent to the server.
					if err := api.CheckUpdateKinds(&input, cur); err != nil {
						return err
					}
				}
				if dryRun {
					return writeUpdatePreview(f, stored, changed, body, contentChanged, warnings)
				}
				for _, w := range warnings {
					fmt.Fprintf(f.IOStreams.ErrOut, "warning: %s\n", w)
				}

				// The gate reads the kind the node is now AND the kind it will
				// be (before ∪ after, cli#714), and an omitted field preserves
				// the stored one — so a plain `--description` edit of
				// a task or a review check still produces a governed node and
				// the generic `updateNode` refuses it. Deciding from the input
				// alone would route exactly those edits wrong, which is why the
				// node's current kind is read first (@codex on #614).
				//
				// One extra read per update, on a command that is not a hot
				// loop. The alternative — write optimistically and retry on the
				// typed refusal — doubles latency on the governed path and turns
				// a routing decision into error handling.
				var resp *api.AuthoredNode
				var err error
				if clearRole {
					resp, err = api.UpdateNodeClearingRoleByKind(cmd.Context(), client, &input, cur)
				} else {
					resp, err = api.UpdateNodeByKind(cmd.Context(), client, &input, cur)
				}
				if err != nil {
					return api.MapError(err)
				}
				dto = updateDTO(resp)
			}

			// A --data-merge runs last (a separate mutation), so its result is
			// the final state we render.
			if mergeData {
				patch, err := resolveMergeData(dataMerge, dataMergeFile, f.IOStreams.In)
				if err != nil {
					return err
				}
				resp, err := gen.UpdateNodeData(cmd.Context(), client, nodeID, patch, reasonPtr)
				if err != nil {
					return api.MapError(err)
				}
				dto = mergeDTO(resp.UpdateNodeData)
			}

			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ updated", dto.Loc, dto.Name)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (hrn:mem:<root>:<slug>) to resolve a bare <loc> against")
	cmd.Flags().StringVar(&name, "name", "", "new node name")
	cmd.Flags().StringVarP(&content, "content", "c", "", `new content ("-" reads stdin)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", `read new content from a file ("-" reads piped stdin)`)
	cmd.Flags().BoolVar(&force, "force", false, "write a new body even though it starts with the metadata header a text-mode node get prints")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change, with a diff of the body, without writing")
	cmd.Flags().StringVar(&nodeType, "type", "", "new node type (comments cannot be retyped; use hadron comment edit)")
	cmd.Flags().StringVar(&objectType, "object-type", "", `new structured-storage collection (#725; "" clears → ordinary node; omit to preserve)`)
	// NOT the same word as `memory member --role` / `memory share --role`, which
	// are MEMBERSHIP roles on a person. This is Node.role — what the node is FOR
	// — and the usage says what it is not, the condition @Holger attached to
	// there being three kind-ish fields at all (nodeType / objectType / role).
	cmd.Flags().StringVar(&role, "role", "",
		`what this node is FOR (#1201) — "spec"/"spec.*" and "review"/"review.*" use their governed doors; omit to preserve; empty or whitespace clears through an explicit null. NOT --type (the platform kind) and NOT a membership role`)
	cmd.Flags().StringVar(&description, "description", "", "new one-line description")
	cmd.Flags().StringVar(&abstract, "abstract", "", `new paragraph-length summary ("-" reads stdin)`)
	cmd.Flags().StringVar(&abstractFile, "abstract-file", "", "read the new abstract from a file")
	cmd.Flags().StringVar(&data, "data", "", `replace the JSON data object ("null" clears; merge with --data-merge)`)
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read the replacement JSON data object from a file")
	cmd.Flags().StringVar(&dataMerge, "data-merge", "", `merge a JSON object into data, preserving unmentioned keys ("-" reads stdin)`)
	cmd.Flags().StringVar(&dataMergeFile, "data-merge-file", "", "read the JSON object to merge into data from a file")
	cmd.Flags().StringVar(&properties, "properties", "", `replace the structured-storage JSON properties (#725; "null" clears)`)
	cmd.Flags().StringVar(&propertiesFile, "properties-file", "", "read the replacement JSON properties object from a file")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "mark the node runnable by 'hadron task run' (--runnable=false clears it; omit to preserve)")
	cmd.Flags().StringVar(&reason, "reason", "", "why this change was made (recorded in version history)")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "replace tags (repeatable)")
	return cmd
}

// resolveMergeData reads the JSON patch for a --data-merge from inline text
// ("-" reads stdin) or --data-merge-file and validates it is JSON. Object-only
// enforcement is left to the server (a non-object patch is rejected with
// BAD_USER_INPUT); it shallow-merges this patch into the node's existing data,
// patch winning on top-level key collisions. The caller has already enforced
// that the two flags are mutually exclusive.
func resolveMergeData(dataMerge, dataMergeFile string, stdin io.Reader) (json.RawMessage, error) {
	raw := strings.TrimSpace(dataMerge)
	switch {
	case dataMergeFile != "":
		b, err := os.ReadFile(dataMergeFile)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading --data-merge-file: %v", err)
		}
		raw = strings.TrimSpace(string(b))
	case dataMerge == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSpace(string(b))
	}
	if !json.Valid([]byte(raw)) {
		flag := "--data-merge"
		if dataMergeFile != "" {
			flag = "--data-merge-file"
		}
		return nil, exitcode.Newf(exitcode.Usage, "%s must contain valid JSON", flag)
	}
	return json.RawMessage(raw), nil
}

// updateFields is the order the --dry-run preview lists fields in, each with
// the flags that set it.
var updateFields = []struct {
	field string
	flags []string
}{
	{"name", []string{"name"}},
	{"content", []string{"content", "content-file"}},
	{"type", []string{"type"}},
	{"object-type", []string{"object-type"}},
	{"role", []string{"role"}},
	{"description", []string{"description"}},
	{"abstract", []string{"abstract", "abstract-file"}},
	{"data", []string{"data", "data-file"}},
	{"properties", []string{"properties", "properties-file"}},
	{"runnable", []string{"runnable"}},
	{"tags", []string{"tag"}},
}

// updatePreviewDTO is `node update --dry-run --json` (#873). content is null
// unless the body changes; warnings is always a list.
type updatePreviewDTO struct {
	DryRun   bool               `json:"dryRun"`
	ID       string             `json:"id"`
	URN      string             `json:"urn"`
	Loc      string             `json:"loc"`
	Fields   []string           `json:"fields"`
	Content  *contentPreviewDTO `json:"content"`
	Warnings []string           `json:"warnings"`
}

// contentPreviewDTO is the body change: stored (raw) → proposed, and their
// unified diff ("" when identical).
type contentPreviewDTO struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Diff   string `json:"diff"`
}

func writeUpdatePreview(f *cmdutil.Factory, stored *gen.GetNodeNode, changed func(string) bool, body string, contentChanged bool, warnings []string) error {
	dto := updatePreviewDTO{DryRun: true, ID: stored.Id, URN: stored.Urn, Loc: stored.Loc, Fields: []string{}, Warnings: warnings}
	for _, uf := range updateFields {
		for _, fl := range uf.flags {
			if changed(fl) {
				dto.Fields = append(dto.Fields, uf.field)
				break
			}
		}
	}
	if contentChanged {
		before := strVal(stored.Content)
		dto.Content = &contentPreviewDTO{Before: before, After: body}
		if before != body {
			dto.Content.Diff = udiff.Unified("content (stored)", "content (proposed)", before, body)
		}
	}
	return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
		fmt.Fprintf(w, "dry run: nothing was written. Would update %s: %s\n", dto.Loc, strings.Join(dto.Fields, ", "))
		if dto.Content != nil {
			if dto.Content.Diff == "" {
				fmt.Fprintln(w, "content: unchanged")
			} else {
				fmt.Fprintf(w, "\n%s", dto.Content.Diff)
			}
		}
		for _, warn := range dto.Warnings {
			fmt.Fprintf(w, "\nwarning: %s\n", warn)
		}
		return nil
	})
}
