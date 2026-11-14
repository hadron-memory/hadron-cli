package secret

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdSet(f *cmdutil.Factory) *cobra.Command {
	var (
		valueFile  string
		authType   string
		username   string
		headerName string
	)
	cmd := &cobra.Command{
		Use:   "set <id|urn> --value-file <src>",
		Short: "Store a new value for a secret",
		Long: `Store a new value for a secret. This is the value path: it sends exactly the
new value and nothing else, and prints the secret's current version number as
the server answers it. The value is never printed.

What it does is the server's call. An org or user secret is overwritten in
place (no versions; the current version is shown as "-"). An App secret gets a
new version and the old ones are kept. Nothing is checked here: a value over
the size limit, a protected secret (SECRET_PROTECTED) or a secret you may not
change come back as the server's refusal.

The value comes from a file or stdin, never an argument:
  --value-file -       read stdin (a no-echo prompt on a terminal)
  --value-file @path   read a file (a plain path works too)
Trailing newlines are trimmed; an empty value is refused (exit 2).

For a webfetch-auth secret give the value's shape with --type (bearer; basic
with --username; header with --header-name). To change metadata only, use
hadron secret update. hadron secret update --value-file is the same operation,
kept for existing scripts.

Name it by the id from hadron secret list, or by URN where the server resolves
one. --json gives the secret fields, including currentVersion and protected.`,
		Example: `  printf '%s' "$NEW_TOKEN" | hadron secret set 01a18cf643247d0fa261f320ec74d878 --value-file -
  hadron secret set hrn:secret:acme.com:poll-auth --type bearer --value-file @token.txt`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			if !changed("value-file") {
				return exitcode.Newf(exitcode.Usage, "--value-file is required (- reads stdin, or prompts without echo on a terminal)")
			}
			if (changed("username") || changed("header-name")) && !changed("type") {
				return exitcode.Newf(exitcode.Usage, "--username and --header-name describe a webfetch-auth value; pass --type too")
			}
			v, err := readNewValue(f, valueFile, changed("type"), authType, username, headerName)
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateSecret(cmd.Context(), client, args[0], nil, &v)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateSecret == nil {
				return exitcode.Newf(exitcode.Error, "server returned no secret")
			}
			dto := dtoFromFields(resp.UpdateSecret.SecretFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				if err := writeSecretLine(w, "✓ set", dto); err != nil {
					return err
				}
				_, err := fmt.Fprintf(w, "current version: %s\n", versionCell(dto.CurrentVersion))
				return err
			})
		},
	}
	cmd.Flags().StringVar(&valueFile, "value-file", "", `new value from "-" (stdin; no-echo prompt on a terminal), "@file", or a file path (required)`)
	cmd.Flags().StringVar(&authType, "type", "", "webfetch-auth value type: bearer, basic, or header")
	cmd.Flags().StringVar(&username, "username", "", "webfetch-auth basic username (with --type basic)")
	cmd.Flags().StringVar(&headerName, "header-name", "", "webfetch-auth header name (with --type header)")
	return cmd
}

// versionCell is the server's currentVersion as given: "-" when it answers
// null (org and user secrets), never inferred from the owner type.
func versionCell(v *int) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(*v)
}
