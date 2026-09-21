package node

import (
	"encoding/json"
	"io"
	"os"
	"strings"

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
			if changed("content") && content == "-" {
				stdinReaders++
			}
			if changed("abstract") && abstract == "-" {
				stdinReaders++
			}
			if changed("data-merge") && dataMerge == "-" {
				stdinReaders++
			}
			if stdinReaders > 1 {
				return exitcode.Newf(exitcode.Usage, "only one of --content -, --abstract -, --data-merge - may read stdin")
			}
			// Refuse a terminal HERE rather than at the read (#643): this
			// command resolves the ref and fetches the node before building
			// its input, and an argument this invalid should not cost two
			// round trips first. Same refusal as resolveContent's, from the
			// one definition in cmdutil.
			if changed("content") && content == "-" {
				if err := cmdutil.RefuseDocumentStdinFromTerminal(
					f.IOStreams.IsInputTerminal(), "--content -", "--content-file"); err != nil {
					return err
				}
			}
			// --abstract and --abstract-file are mutually exclusive. Guard on
			// Changed() (not the resolved value): an explicit --abstract "" to
			// clear would otherwise slip past ResolveTextInput's value check and
			// let the file silently win.
			if changed("abstract") && changed("abstract-file") {
				return exitcode.Newf(exitcode.Usage, "--abstract and --abstract-file are mutually exclusive")
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
				// The node's CURRENT kind, read once and used twice: to choose
				// the door for the write (the gate reads the RESULTING state,
				// and an omitted field preserves the stored one), and to name
				// the right door in the `--role ""` refusal.
				//
				// INSIDE the field branch, not above it: a `--data-merge`-only
				// update writes through `updateNodeData` and needs no door, so
				// reading here would spend a round trip on every merge for an
				// answer nothing consults.
				//
				// Best-effort — a failed read leaves the zero value, which
				// routes to the generic surface and lets the server give the
				// authoritative refusal.
				cur := api.NodeKindState{}
				if existing, gerr := gen.GetNode(cmd.Context(), client, nodeID); gerr == nil && existing.Node != nil {
					cur.Role = existing.Node.Role
					cur.IsRunnable = existing.Node.IsRunnable != nil && *existing.Node.IsRunnable
				}

				input := gen.UpdateNodeInput{
					Id: &nodeID,
				}
				if changed("name") {
					input.Name = &name
				}
				if changed("content") || changed("content-file") {
					body, err := resolveContent(content, contentFile, f.IOStreams.In, f.IOStreams.IsInputTerminal())
					if err != nil {
						return err
					}
					input.Content = &body
				}
				if changed("type") {
					input.NodeType = &nodeType
				}
				if changed("object-type") {
					input.ObjectType = &objectType
				}
				// #1201. Sending this is what makes the node GOVERNED — and the
				// gate reads the resulting state, so an explicit role here wins
				// over the stored one when the door is chosen below.
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
							"this would leave the node as two governed kinds at once — %s — and each door is exempt from its OWN kind only, so every one of them refuses it. Clear the other first, or write it with `hadron api`",
							strings.Join(kinds, " AND "))
					}
					// REFUSED rather than sent. The schema says null clears and
					// omission preserves, and `*string` + omitempty can express
					// neither an explicit null nor a distinct "clear" — a nil
					// pointer is omitted. So `--role ""` would write the EMPTY
					// STRING, which the server does NOT normalize to null.
					//
					// Measured, because the neighbouring flag sets the opposite
					// expectation: `--object-type ""` really does clear (the
					// server normalizes that one). Role does not, so a node would
					// be left carrying role "" — neither governed nor cleanly
					// ungoverned, and matching no entry in the register.
					//
					// The remedy names the door for the node's CURRENT kind, not a
					// fixed one: clearing is an UPDATE of what the node is now, so
					// a review node needs updateReviewNode and a runnable one
					// updateTaskNode. Prescribing updateSpecNode for all of them
					// would be refused for most governed nodes (@copilot on #615).
					if role == "" {
						return exitcode.Newf(exitcode.Usage,
							`--role "" would write an EMPTY role, not clear it — the server normalizes an empty object-type but not an empty role, leaving the node in a state no kind recognizes. Pass a value, or clear it with an explicit null through the door for what this node is NOW: hadron api 'mutation($i: UpdateNodeInput!){ %s(input:$i){ id role } }' -F i='{"id":"%s","role":null}'`,
							clearDoorFor(cur), nodeID)
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

				// The gate reads the RESULTING state, and an omitted field
				// preserves the stored one — so a plain `--description` edit of
				// a task or a review check still produces a governed node and
				// the generic `updateNode` refuses it. Deciding from the input
				// alone would route exactly those edits wrong, which is why the
				// node's current kind is read first (@codex on #614).
				//
				// One extra read per update, on a command that is not a hot
				// loop. The alternative — write optimistically and retry on the
				// typed refusal — doubles latency on the governed path and turns
				// a routing decision into error handling. (Read above, so the
				// `--role ""` refusal can name the same door.)
				resp, err := api.UpdateNodeByKind(cmd.Context(), client, &input, cur)
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
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read new content from a file")
	cmd.Flags().StringVar(&nodeType, "type", "", "new node type")
	cmd.Flags().StringVar(&objectType, "object-type", "", `new structured-storage collection (#725; "" clears → ordinary node; omit to preserve)`)
	// NOT the same word as `memory member --role` / `memory share --role`, which
	// are MEMBERSHIP roles on a person. This is Node.role — what the node is FOR
	// — and the usage says what it is not, the condition @Holger attached to
	// there being three kind-ish fields at all (nodeType / objectType / role).
	cmd.Flags().StringVar(&role, "role", "",
		`what this node is FOR (#1201) — governed values "spec"/"review" route the write through that kind's door; omit to preserve (clearing needs an explicit null; "" is refused). NOT --type (the platform kind) and NOT a membership role`)
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

// clearDoorFor names the mutation that may clear a node's role — the door for
// what the node is NOW, not a fixed one.
//
// Clearing is an UPDATE, and the gate reads BOTH the prior and resulting state,
// so the write must go through the door of the kind the node currently carries:
// a review node needs `updateReviewNode`, a runnable one `updateTaskNode`.
// Naming `updateSpecNode` for all of them — as the first version did — is
// refused for most governed nodes (@copilot on #615).
//
// An ungoverned node needs no door and gets the generic surface; it also cannot
// reach this message, since there is nothing to clear.
func clearDoorFor(cur api.NodeKindState) string {
	switch {
	case cur.IsRunnable:
		return "updateTaskNode"
	case cur.Role != nil && *cur.Role == api.SpecNodeRole:
		return "updateSpecNode"
	case cur.Role != nil && *cur.Role == api.ReviewNodeRole:
		return "updateReviewNode"
	}
	return "updateNode"
}
