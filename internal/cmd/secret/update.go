package secret

import (
	"encoding/json"
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
		valueFile  string
		metaPairs  []string
		metaJSON   string
		authType   string
		username   string
		headerName string
		urlPrefix  string
	)
	cmd := &cobra.Command{
		Use:   "update <id|urn> [--value-file <src>] [metadata flags]",
		Short: "Replace a secret's value or metadata (rotation)",
		Long: `Replace a secret's value, its metadata, or both. Whatever you do not pass is
kept. This overwrites in place: there are no versions, and the old value cannot
be recovered. The new value is never printed.

A secret's name, kind and owner cannot change. To rename one, create a new
secret and delete the old one (runs and flows find a secret by its name).

Name it by the id from hadron secret list, or by URN:
hrn:secret:<org or user handle>:<name>. You can update your own user secrets,
and an org's if you are its ADMIN or OWNER (else exit 8). A secret you cannot
see exits 4.

New value (only with --value-file; never as an argument):
  --value-file -       read stdin (a no-echo prompt on a terminal)
  --value-file @path   read a file (a plain path works too)
Trailing newlines are trimmed; an empty value is refused (exit 2).

For a generic secret the value is the string read. Its metadata is replaced
with --meta key=value (repeatable) or --meta-json '<object>', not both.

For a webfetch-auth secret, a new value needs --type (bearer; basic with
--username; header with --header-name) together with --value-file, and
--url-prefix replaces the URL prefix the credential is sent to. --meta and
--meta-json are refused alongside these flags.

The server checks the resulting value and metadata against the secret's kind;
a mismatch (say, a plain value for a webfetch-auth secret) exits 2.

Prints "✓ updated secret <name>  <kind>/<id>  owner <type> <id>"; --json gives
the same fields as hadron secret get.`,
		Example: `  printf '%s' "$NEW_TOKEN" | hadron secret update hrn:secret:alice:github-token --value-file -
  hadron secret update hrn:secret:alice:github-token --meta env=prod
  hadron secret update hrn:secret:acme.com:poll-auth --type bearer --value-file @token.txt
  hadron secret update hrn:secret:acme.com:poll-auth --url-prefix https://api.example.com/v2/`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			webFetch := changed("type") || changed("username") || changed("header-name") || changed("url-prefix")
			generic := changed("meta") || changed("meta-json")
			if webFetch && generic {
				return exitcode.Newf(exitcode.Usage, "--meta/--meta-json are generic-secret metadata; they cannot be combined with --type/--username/--header-name/--url-prefix")
			}
			if (changed("username") || changed("header-name")) && !changed("type") {
				return exitcode.Newf(exitcode.Usage, "--username and --header-name describe a new webfetch-auth value; pass --type and --value-file too")
			}
			if changed("type") && !changed("value-file") {
				return exitcode.Newf(exitcode.Usage, "--type describes a new value; pass --value-file (- reads stdin, or prompts without echo on a terminal)")
			}
			if !changed("value-file") && !generic && !changed("url-prefix") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass --value-file, --meta/--meta-json, or --url-prefix")
			}

			var metadata *json.RawMessage
			switch {
			case generic:
				m, err := genericMetadata(metaPairs, metaJSON)
				if err != nil {
					return err
				}
				metadata = m
			case changed("url-prefix"):
				if urlPrefix == "" {
					return exitcode.Newf(exitcode.Usage, "--url-prefix is empty; pass the URL prefix the credential is sent to")
				}
				m, err := webFetchAuthMetadata(urlPrefix)
				if err != nil {
					return err
				}
				metadata = &m
			}

			var value *json.RawMessage
			if changed("value-file") {
				source := valueFile
				// "-" on a terminal would echo what is typed: prompt without
				// echo instead (ReadSecretInput's no-source branch).
				if source == "-" && f.IOStreams.IsInputTerminal() {
					source = ""
				}
				var v json.RawMessage
				var err error
				if changed("type") {
					v, err = webFetchAuthValue(f, source, authType, username, headerName)
				} else {
					var material string
					material, err = readSecretMaterial(f.IOStreams, source, "New secret value")
					if err == nil {
						v, err = jsonStringValue(material)
					}
				}
				if err != nil {
					return err
				}
				value = &v
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.UpdateSecret(cmd.Context(), client, args[0], metadata, value)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.UpdateSecret == nil {
				return exitcode.Newf(exitcode.Error, "server returned no secret")
			}
			dto := dtoFromFields(resp.UpdateSecret.SecretFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeSecretLine(w, "✓ updated", dto)
			})
		},
	}
	cmd.Flags().StringVar(&valueFile, "value-file", "", `new value from "-" (stdin; no-echo prompt on a terminal), "@file", or a file path`)
	cmd.Flags().StringArrayVar(&metaPairs, "meta", nil, "replace generic metadata: key=value (repeatable)")
	cmd.Flags().StringVar(&metaJSON, "meta-json", "", "replace generic metadata with a JSON object")
	cmd.Flags().StringVar(&authType, "type", "", "new webfetch-auth value type: bearer, basic, or header (needs --value-file)")
	cmd.Flags().StringVar(&username, "username", "", "webfetch-auth basic username (with --type basic)")
	cmd.Flags().StringVar(&headerName, "header-name", "", "webfetch-auth header name (with --type header)")
	cmd.Flags().StringVar(&urlPrefix, "url-prefix", "", "replace the webfetch-auth URL prefix")
	return cmd
}
