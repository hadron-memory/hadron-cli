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

func newCmdAdd(f *cmdutil.Factory) *cobra.Command {
	var (
		memory      string
		loc         string
		name        string
		content     string
		contentFile string
		nodeType    string
		description string
		abstract    string
		data        string
		dataFile    string
		tags        []string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create a node",
		Long: `Create a node in a memory. Fails if a node already exists at the
loc (use ` + "`hadron node update`" + ` to modify an existing node).

--content takes the content inline, or "-" to read it from standard
input; --content-file reads it from a file.`,
		Example: `  hadron node add -m acme.com:kb --loc findings:flaky-ci --name "Flaky CI" --content "..."
  cat finding.md | hadron node add -m acme.com:kb --loc findings:flaky-ci --name "Flaky CI" --content -`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			body, err := resolveContent(content, contentFile, f.IOStreams.In)
			if err != nil {
				return err
			}

			createOnly := true
			input := gen.NodeInput{
				MemoryId:   memory,
				Loc:        loc,
				Name:       name,
				CreateOnly: &createOnly,
				Tags:       tags,
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
			if data != "" || dataFile != "" {
				raw, err := resolveData(data, dataFile)
				if err != nil {
					return err
				}
				input.Data = raw
			}

			resp, err := gen.UpsertNode(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}

			dto := upsertDTO(resp.UpsertNode)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				t := output.NewTable(w)
				t.Row("✓ created", dto.Loc, dto.Name)
				return t.Flush()
			})
		},
	}
	cmd.Flags().StringVarP(&memory, "memory", "m", "", "memory ID or fully-qualified URN (required)")
	cmd.Flags().StringVar(&loc, "loc", "", "node location within the memory (required)")
	cmd.Flags().StringVar(&name, "name", "", "node name (required)")
	cmd.Flags().StringVarP(&content, "content", "c", "", `node content ("-" reads stdin)`)
	cmd.Flags().StringVar(&contentFile, "content-file", "", "read node content from a file")
	cmd.Flags().StringVar(&nodeType, "type", "", "node type (defaults to the server default)")
	cmd.Flags().StringVar(&description, "description", "", "one-line description")
	cmd.Flags().StringVar(&abstract, "abstract", "", "paragraph-length summary")
	cmd.Flags().StringVar(&data, "data", "", "machine-readable JSON data object")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read the JSON data object from a file")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "tag (repeatable)")
	_ = cmd.MarkFlagRequired("memory")
	_ = cmd.MarkFlagRequired("loc")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func resolveContent(content, contentFile string, stdin io.Reader) (string, error) {
	if content != "" && contentFile != "" {
		return "", exitcode.Newf(exitcode.Usage, "--content and --content-file are mutually exclusive")
	}
	if contentFile != "" {
		data, err := os.ReadFile(contentFile)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --content-file: %v", err)
		}
		return string(data), nil
	}
	if content == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return content, nil
}

// resolveData reads the node `data` JSON from --data (inline) or --data-file,
// validates it, and returns it as a raw message ready for NodeInput.Data.
// The value replaces the node's whole data object on write (the server's
// upsert preserves an omitted field and overwrites a supplied one); pass
// `--data null` to clear it. Callers gate the call so an unset flag stays
// omitted from the wire.
func resolveData(data, dataFile string) (*json.RawMessage, error) {
	if data != "" && dataFile != "" {
		return nil, exitcode.Newf(exitcode.Usage, "--data and --data-file are mutually exclusive")
	}
	raw := strings.TrimSpace(data)
	if dataFile != "" {
		b, err := os.ReadFile(dataFile)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading --data-file: %v", err)
		}
		raw = strings.TrimSpace(string(b))
	}
	if !json.Valid([]byte(raw)) {
		return nil, exitcode.Newf(exitcode.Usage, "--data must be valid JSON (use `null` to clear)")
	}
	msg := json.RawMessage(raw)
	return &msg, nil
}
