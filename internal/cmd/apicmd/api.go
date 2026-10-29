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
		Long: `Send one GraphQL query or mutation to the Hadron server, as you, and print
the server's JSON response, indented. Use this when no other
hadron command does what you need. The fields you can ask for are listed in
the server's GraphQL schema (introspect it with a '__schema' query, or read
schema/schema.graphql in the hadron-cli repository).

A query only reads. A MUTATION CHANGES REAL DATA, on the server this command
talks to (--server, HADRON_SERVER, or 'hadron config get server'), with your
sign-in's permissions, and nothing asks you to confirm. There is no dry run:
try a mutation on a test server first. 'hadron api' sends no App context, so
--app has no effect here; an operation that needs one takes it as an argument.

Variables are passed with repeated -F key=value flags. Values are sent as
JSON when they parse as JSON (numbers, true/false, objects), otherwise as
strings. An id-typed ref such as memory(ref:) takes a memory id or URN
(hrn:mem:<root>:<slug>). Pass "-" as the query to read the document from a
pipe, or use --input <file>. "-" is refused when standard input is an
interactive terminal, which can silently cut a long document short.

When the server answers normally (HTTP 200), the output on stdout is its
JSON response, with or without --json. When it refuses with an HTTP error
status (400 or above), nothing is printed on stdout: the status and the
server's message go to stderr as the error (inside the CLI's JSON error
with --json). Read the exit code as well as the output:
  0  the server answered with no errors. A record you cannot see, or that
     does not exist, usually comes back as null with exit 0: check the data
     (signed out, 'me' is null too).
  3, 4, 8 and others: the server reported errors (3 not signed in, 4 not
     found or not visible to you, 8 forbidden). On an HTTP 200 answer the
     JSON, with its "errors", is still printed on stdout.
  2  the document is not valid GraphQL for this server (a typo, an unknown
     field): nothing ran, and the server's message is printed as the error.
  7  the server never answered (unreachable, a gateway error, a timeout).
     After a mutation that means it may or may not have been applied, so
     check before you retry.
The full table is in 'hadron agentic-usage'.`,
		Example: `  # Read: who am I signed in as?
  hadron api 'query { me { id email } }'

  # Read with a variable (a memory URN or id)
  hadron api 'query($ref: ID!) { memory(ref: $ref) { urn name } }' -F ref=hrn:mem:acme.com:kb

  # A longer document from a file or a pipe
  hadron api --input op.graphql
  cat op.graphql | hadron api -

  # WRITES: a mutation runs for real, with no confirmation
  hadron api 'mutation($id: String!, $d: String!) { updateNode(input: {id: $id, description: $d}) { id description } }' -F id='<node-id>' -F d="New description"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query, err := readQuery(args, inputFile, f.IOStreams.In, f.IOStreams.IsInputTerminal())
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

// readQuery resolves the GraphQL document. A query is a DOCUMENT (#648):
// multiline, and easily over a terminal's 4 KB line-discipline buffer. A
// truncated one usually fails to parse, but "usually" is not a guard, so "-"
// goes through cmdutil.ReadDocumentStdin, which refuses an interactive
// terminal rather than send whatever survived the line discipline.
func readQuery(args []string, inputFile string, stdin io.Reader, stdinIsTerminal bool) (string, error) {
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
		return cmdutil.ReadDocumentStdin(stdin, stdinIsTerminal, `"hadron api -"`, "--input")
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
