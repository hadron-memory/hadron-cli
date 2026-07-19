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
		memory         string
		loc            string
		name           string
		content        string
		contentFile    string
		nodeType       string
		objectType     string
		description    string
		abstract       string
		data           string
		dataFile       string
		properties     string
		propertiesFile string
		runnable       bool
		tags           []string
	)
	cmd := &cobra.Command{
		Use:     "create",
		Aliases: []string{"add"},
		Short:   "Create a node",
		Long: `Create a node in a memory. Fails if a node already exists at the
loc (use ` + "`hadron node update`" + ` to modify an existing node).

--content takes the content inline, or "-" to read it from standard
input; --content-file reads it from a file.

--object-type tags the node with its structured-storage collection (#725,
e.g. competitor), orthogonal to --type (nodeType). --properties /
--properties-file set the node's typed properties (the JSONB column the schema
governs and --where / --sort-property target) — distinct from --data. On a
schema-governed memory the server validates objectType + properties against the
schema and rejects a violation.`,
		Example: `  hadron node add -m acme.com::kb --loc findings:flaky-ci --name "Flaky CI" --content "..."
  cat finding.md | hadron node add -m acme.com::kb --loc findings:flaky-ci --name "Flaky CI" --content -`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateURNPath("--loc", loc); err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			body, err := resolveContent(content, contentFile, f.IOStreams.In)
			if err != nil {
				return err
			}

			input := gen.CreateNodeInput{
				MemoryId: memory,
				Loc:      loc,
				Name:     name,
				Tags:     tags,
			}
			if body != "" {
				input.Content = &body
			}
			if nodeType != "" {
				input.NodeType = &nodeType
			}
			if objectType != "" {
				input.ObjectType = &objectType
			}
			if description != "" {
				input.Description = &description
			}
			if abstract != "" {
				input.Abstract = &abstract
			}
			// Gate on Changed() (not the value): an explicit --data "" / --properties ""
			// is a deliberate write of an empty/invalid bag — it must reach
			// resolveJSONObject (and error as invalid JSON), matching node update, and
			// must still trip mutual exclusion against its -file companion.
			changed := cmd.Flags().Changed
			if changed("data") && changed("data-file") {
				return exitcode.Newf(exitcode.Usage, "--data and --data-file are mutually exclusive")
			}
			if changed("properties") && changed("properties-file") {
				return exitcode.Newf(exitcode.Usage, "--properties and --properties-file are mutually exclusive")
			}
			if changed("data") || changed("data-file") {
				raw, err := resolveJSONObject("--data", data, dataFile)
				if err != nil {
					return err
				}
				input.Data = raw
			}
			if changed("properties") || changed("properties-file") {
				raw, err := resolveJSONObject("--properties", properties, propertiesFile)
				if err != nil {
					return err
				}
				input.Properties = raw
			}
			if cmd.Flags().Changed("runnable") {
				input.IsRunnable = &runnable
			}

			resp, err := gen.CreateNode(cmd.Context(), client, &input)
			if err != nil {
				return api.MapError(err)
			}

			dto := createDTO(resp.CreateNode)
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
	cmd.Flags().StringVar(&objectType, "object-type", "", "collection this node belongs to (#725; e.g. competitor), orthogonal to --type")
	cmd.Flags().StringVar(&description, "description", "", "one-line description")
	cmd.Flags().StringVar(&abstract, "abstract", "", "paragraph-length summary")
	cmd.Flags().StringVar(&data, "data", "", "machine-readable JSON data object")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "read the JSON data object from a file")
	cmd.Flags().StringVar(&properties, "properties", "", "structured-storage JSON properties (#725; schema-governed on a schema'd memory)")
	cmd.Flags().StringVar(&propertiesFile, "properties-file", "", "read the JSON properties object from a file")
	cmd.Flags().BoolVar(&runnable, "runnable", false, "mark the node runnable by 'hadron task run'")
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

// resolveJSONObject reads a node JSON bag (data or properties) from an inline
// flag or its companion --*-file, validates it, and returns a raw message ready
// for a create/update input field. The value REPLACES the whole bag on write
// (the server preserves an omitted field and overwrites a supplied one); pass
// `null` to clear it. flag is the base flag name (e.g. "--data",
// "--properties") — the "-file" variant is derived for error messages.
//
// The two sources are mutually exclusive, but that's enforced at the call site
// on cmd.Flags().Changed() (an inline value of "" is a *set* empty string, which
// a value check here couldn't tell from unset) — so this only reads the source
// that's present, file winning when both values are non-empty. Callers gate the
// call on Changed() so an unset flag stays omitted from the wire, and an
// explicit "" reaches here to fail as invalid JSON.
func resolveJSONObject(flag, inline, file string) (*json.RawMessage, error) {
	raw := strings.TrimSpace(inline)
	name := flag
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, exitcode.Newf(exitcode.Usage, "reading %s-file: %v", flag, err)
		}
		raw = strings.TrimSpace(string(b))
		name = flag + "-file"
	}
	if !json.Valid([]byte(raw)) {
		return nil, exitcode.Newf(exitcode.Usage, "%s must contain valid JSON (use `null` to clear)", name)
	}
	msg := json.RawMessage(raw)
	return &msg, nil
}
