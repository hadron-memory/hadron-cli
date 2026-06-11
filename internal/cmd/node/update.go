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
		memory      string
		name        string
		content     string
		contentFile string
		nodeType    string
		description string
		abstract    string
		tags        []string
	)
	cmd := &cobra.Command{
		Use:   "update <loc>",
		Short: "Update a node",
		Long: `Update an existing node by its loc. Only the fields you pass
change; everything else is preserved. Without -m/--memory the loc is
resolved across every memory you can read; pass it to be precise.`,
		Example: `  hadron node update findings:flaky-ci -m acme.com:kb --name "Flaky CI (resolved)"
  cat updated.md | hadron node update findings:flaky-ci -m acme.com:kb --content -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" && content == "" && contentFile == "" && nodeType == "" &&
				description == "" && abstract == "" && len(tags) == 0 {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass at least one field flag")
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			// Resolve the node first: the upsert needs memoryId + loc
			// (and name is required), and this avoids splitting the
			// URN client-side.
			existing, err := fetchNode(cmd, client, args[0], memory)
			if err != nil {
				return err
			}

			input := gen.NodeInput{
				MemoryId: existing.MemoryID,
				Loc:      existing.Loc,
				Name:     existing.Name,
			}
			if name != "" {
				input.Name = name
			}
			body, err := resolveContent(content, contentFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			if body != "" {
				input.Content = &body
			}
			if nodeType != "" {
				input.NodeType = &nodeType
			}
			if description != "" {
				input.Description = &description
			}
			if abstract != "" {
				input.Abstract = &abstract
			}
			if len(tags) > 0 {
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
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "resolve the loc within this memory (ID or URN)")
	cmd.Flags().StringVar(&name, "name", "", "new node name")
	cmd.Flags().StringVarP(&content, "content", "c", "", `new content ("-" reads stdin)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read new content from a file")
	cmd.Flags().StringVar(&nodeType, "type", "", "new node type")
	cmd.Flags().StringVar(&description, "description", "", "new one-line description")
	cmd.Flags().StringVar(&abstract, "abstract", "", "new paragraph-length summary")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "replace tags (repeatable)")
	return cmd
}
