package node

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdUpdate(f *cmdutil.Factory) *cobra.Command {
	var (
		name         string
		content      string
		contentFile  string
		nodeType     string
		description  string
		abstract     string
		abstractFile string
		data         string
		dataFile     string
		tags         []string
	)
	cmd := &cobra.Command{
		Use:   "update <node-urn>",
		Short: "Update a node",
		Long: `Update an existing node by its fully-qualified URN
(<org>:<memory>:<loc>). Only the fields you pass change; everything
else is preserved (pass an explicit empty string, e.g.
--description "", to clear a field).`,
		Example: `  hadron node update acme.com:kb:findings:flaky-ci --name "Flaky CI (resolved)"
  cat updated.md | hadron node update acme.com:kb:findings:flaky-ci --content -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("name") && !changed("content") && !changed("content-file") &&
				!changed("type") && !changed("description") &&
				!changed("abstract") && !changed("abstract-file") &&
				!changed("data") && !changed("data-file") && !changed("tag") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}
			// Content and abstract can each read stdin via "-", but stdin can
			// only be consumed once.
			if changed("content") && content == "-" && changed("abstract") && abstract == "-" {
				return exitcode.Newf(exitcode.Usage, "--content - and --abstract - cannot both read stdin")
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

			// Resolve the node first: the upsert needs memoryId + loc
			// (and name is required), and this avoids splitting the
			// URN client-side.
			existing, err := fetchNode(cmd, client, args[0])
			if err != nil {
				return err
			}

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
			if changed("data") || changed("data-file") {
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

			dto := upsertDTO(resp.UpsertNode)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ updated", dto.Loc, dto.Name)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "new node name")
	cmd.Flags().StringVarP(&content, "content", "c", "", `new content ("-" reads stdin)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read new content from a file")
	cmd.Flags().StringVar(&nodeType, "type", "", "new node type")
	cmd.Flags().StringVar(&description, "description", "", "new one-line description")
	cmd.Flags().StringVar(&abstract, "abstract", "", `new paragraph-length summary ("-" reads stdin)`)
	cmd.Flags().StringVar(&abstractFile, "abstract-file", "", "read the new abstract from a file")
	cmd.Flags().StringVar(&data, "data", "", `new JSON data object (replaces it; "null" clears)`)
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read the new JSON data object from a file")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "replace tags (repeatable)")
	return cmd
}
