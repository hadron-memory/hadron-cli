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
		memory        string
		name          string
		content       string
		contentFile   string
		nodeType      string
		description   string
		abstract      string
		abstractFile  string
		data          string
		dataFile      string
		dataMerge     string
		dataMergeFile string
		runnable      bool
		reason        string
		tags          []string
	)
	cmd := &cobra.Command{
		Use:   "update <node-urn> | <loc> -m <memory>",
		Short: "Update a node",
		Long: `Update an existing node by its fully-qualified URN
(<org>::<memory>::<loc>), or by a bare <loc> with -m/--memory. Only the
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
mutually exclusive.`,
		Example: `  hadron node update acme.com:kb:findings:flaky-ci --name "Flaky CI (resolved)"
  cat updated.md | hadron node update findings:flaky-ci -m acme.com:kb --content -
  hadron node update acme.com:kb:findings:flaky-ci --data-merge '{"status":"closed"}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			replaceData := changed("data") || changed("data-file")
			mergeData := changed("data-merge") || changed("data-merge-file")
			anyField := changed("name") || changed("content") || changed("content-file") ||
				changed("type") || changed("description") ||
				changed("abstract") || changed("abstract-file") ||
				replaceData || changed("runnable") || changed("tag")
			if !anyField && !mergeData {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			// Replace and merge are different operations (distinct mutations).
			if replaceData && mergeData {
				return exitcode.Newf(exitcode.Usage, "--data (replace) and --data-merge (merge) are mutually exclusive")
			}
			// --data-merge and --data-merge-file are mutually exclusive. Guard
			// on Changed() (not the resolved value): an explicit --data-merge ""
			// would otherwise slip past resolveMergeData's value check and let
			// the file silently win.
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
				input := gen.UpdateNodeInput{
					Id: &nodeID,
				}
				if changed("name") {
					input.Name = &name
				}
				if changed("content") || changed("content-file") {
					body, err := resolveContent(content, contentFile, f.IOStreams.In)
					if err != nil {
						return err
					}
					input.Content = &body
				}
				if changed("type") {
					input.NodeType = &nodeType
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
					raw, err := resolveData(data, dataFile)
					if err != nil {
						return err
					}
					input.Data = raw
				}
				if changed("runnable") {
					input.IsRunnable = &runnable
				}
				if changed("tag") {
					input.Tags = tags
				}
				input.Reason = reasonPtr

				resp, err := gen.UpdateNode(cmd.Context(), client, &input)
				if err != nil {
					return api.MapError(err)
				}
				dto = updateDTO(resp.UpdateNode)
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory (org::memory) to resolve a bare <loc> against")
	cmd.Flags().StringVar(&name, "name", "", "new node name")
	cmd.Flags().StringVarP(&content, "content", "c", "", `new content ("-" reads stdin)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read new content from a file")
	cmd.Flags().StringVar(&nodeType, "type", "", "new node type")
	cmd.Flags().StringVar(&description, "description", "", "new one-line description")
	cmd.Flags().StringVar(&abstract, "abstract", "", `new paragraph-length summary ("-" reads stdin)`)
	cmd.Flags().StringVar(&abstractFile, "abstract-file", "", "read the new abstract from a file")
	cmd.Flags().StringVar(&data, "data", "", `replace the JSON data object ("null" clears; merge with --data-merge)`)
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read the replacement JSON data object from a file")
	cmd.Flags().StringVar(&dataMerge, "data-merge", "", `merge a JSON object into data, preserving unmentioned keys ("-" reads stdin)`)
	cmd.Flags().StringVar(&dataMergeFile, "data-merge-file", "", "read the JSON object to merge into data from a file")
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
