// Package apicmd implements `hadron api`, the raw GraphQL escape
// hatch (same role as `gh api`).
package apicmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
)

func NewCmdAPI(f *cmdutil.Factory) *cobra.Command {
	var rawFields []string
	var inputFile string
	cmd := &cobra.Command{
		Use:   "api <query-or-mutation>",
		Short: "Send a raw GraphQL query or mutation",
		Long: `Send an arbitrary GraphQL document to the Hadron server and print
the raw JSON response. Use this when a curated command doesn't cover
what you need.

Variables are passed with repeated -F key=value flags. Values are
sent as JSON when they parse as JSON, otherwise as strings. Pass "-"
as the query to read the document from standard input, or use
--input <file>.`,
		Example: `  hadron api 'query { me { id email } }'
  hadron api 'query($id: ID!) { memory(id: $id) { urn name } }' -F id=mem_123
  cat op.graphql | hadron api -`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query, err := readQuery(args, inputFile, f.IOStreams.In)
			if err != nil {
				return err
			}
			variables, err := parseFields(rawFields)
			if err != nil {
				return err
			}

			server, err := f.Server()
			if err != nil {
				return err
			}
			token, _, err := f.Token()
			if err != nil {
				return err
			}

			result, err := api.RawGraphQL(cmd.Context(), server, token, query, variables, f.HTTPClient)
			if err != nil {
				return err
			}

			// Print the verbatim response envelope, then reflect any
			// GraphQL errors in the exit code.
			var buf bytes.Buffer
			if json.Valid(result.Body) {
				_ = json.Indent(&buf, result.Body, "", "  ")
				fmt.Fprintln(f.IOStreams.Out, buf.String())
			} else {
				fmt.Fprintln(f.IOStreams.Out, string(result.Body))
			}
			if err := result.Err(); err != nil {
				return exitcode.Silent(exitcode.FromError(err))
			}
			return nil
		},
	}
	cmd.Flags().StringArrayVarP(&rawFields, "field", "F", nil, "set a GraphQL variable (key=value, repeatable)")
	cmd.Flags().StringVar(&inputFile, "input", "", "read the GraphQL document from a file")
	return cmd
}

func readQuery(args []string, inputFile string, stdin io.Reader) (string, error) {
	if inputFile != "" {
		data, err := os.ReadFile(inputFile)
		if err != nil {
			return "", exitcode.Newf(exitcode.Usage, "reading --input: %v", err)
		}
		return string(data), nil
	}
	if len(args) == 0 {
		return "", exitcode.Newf(exitcode.Usage, "provide a GraphQL document, \"-\" for stdin, or --input <file>")
	}
	if args[0] == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return args[0], nil
}

func parseFields(fields []string) (map[string]any, error) {
	if len(fields) == 0 {
		return nil, nil
	}
	variables := map[string]any{}
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" {
			return nil, exitcode.Newf(exitcode.Usage, "invalid -F %q: expected key=value", field)
		}
		var parsed any
		if err := json.Unmarshal([]byte(value), &parsed); err == nil {
			variables[key] = parsed
		} else {
			variables[key] = value
		}
	}
	return variables, nil
}
