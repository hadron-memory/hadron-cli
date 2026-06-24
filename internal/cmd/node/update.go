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
				replaceData || changed("tag")
			if !anyField && !mergeData {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			// Replace and merge are different operations (distinct mutations).
			if replaceData && mergeData {
				return exitcode.Newf(exitcode.Usage, "--data (replace) and --data-merge (merge) are mutually exclusive")
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

			// Resolve the node first: the upsert needs memoryId + loc (and name
			// is required), the merge needs the id, and this avoids splitting
			// the URN client-side.
			existing, err := fetchNode(cmd, client, memory, args[0])
			if err != nil {
				return err
			}

			var dto nodeDTO
			// Field updates (including a --data replace) go through the upsert.
			if anyField {
				input := gen.NodeInput{
					MemoryId: existing.MemoryId,
					Loc:      existing.Loc,
					Name:     existing.Name,
				}
				if changed("name") {
					input.Name = name
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
				if changed("tag") {
					input.Tags = tags
				}

				resp, err := gen.UpsertNode(cmd.Context(), client, &input)
				if err != nil {
					return api.MapError(err)
				}
				dto = upsertDTO(resp.UpsertNode)
			}

			// A --data-merge runs last (a separate mutation), so its result is
			// the final state we render.
			if mergeData {
				patch, err := resolveMergeData(dataMerge, dataMergeFile, f.IOStreams.In)
				if err != nil {
					return err
				}
				resp, err := gen.UpdateNodeData(cmd.Context(), client, existing.Id, patch)
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
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "replace tags (repeatable)")
	return cmd
}

// resolveMergeData reads the JSON patch for a --data-merge from inline text
// ("-" reads stdin) or --data-merge-file and validates it is JSON. The server
// shallow-merges this patch into the node's existing data (patch wins on
// top-level key collisions) and rejects a non-object patch with BAD_USER_INPUT.
func resolveMergeData(data, dataFile string, stdin io.Reader) (json.RawMessage, error) {
	if data != "" && dataFile != "" {
		return nil, exitcode.Newf(exitcode.Usage, "--data-merge and --data-merge-file are mutually exclusive")
	}
	raw := strings.TrimSpace(data)
	switch {
	case dataFile != "":
		b, err := os.ReadFile(dataFile)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading --data-merge-file: %v", err)
		}
		raw = strings.TrimSpace(string(b))
	case data == "-":
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}
		raw = strings.TrimSpace(string(b))
	}
	if !json.Valid([]byte(raw)) {
		flag := "--data-merge"
		if dataFile != "" {
			flag = "--data-merge-file"
		}
		return nil, exitcode.Newf(exitcode.Usage, "%s must contain a valid JSON object", flag)
	}
	return json.RawMessage(raw), nil
}
