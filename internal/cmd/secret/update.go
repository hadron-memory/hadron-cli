package secret

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/spf13/cobra"

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
		protected  bool
	)
	cmd := &cobra.Command{
		Use:   "update <id|urn> [--value-file <src>] [metadata flags] [--protected|--protected=false]",
		Short: "Replace a secret's value or metadata (rotation)",
		Long: `Replace a secret's value, its metadata, or both. Whatever you do not pass is
kept. For an org or user secret a new value overwrites in place and the old
one cannot be recovered; an App secret keeps every value as a version. The new
value is never printed. To store a value, prefer hadron secret set: --value-file
here is the same operation, kept so existing scripts work.

A secret's name, kind and owner cannot change. To rename one, create a new
secret and delete the old one (runs and flows find a secret by its name).

Name it by the id from hadron secret list, or by URN:
hrn:secret:<org or user handle>:<name> (an App secret by its id). You can update your own user secrets,
and an org's if you are its ADMIN or OWNER (else exit 8). A secret you cannot
see exits 4.

New value (only with --value-file; never as an argument):
  --value-file -       read stdin (a no-echo prompt on a terminal)
  --value-file @path   read a file (a plain path works too)
Trailing newlines are trimmed; an empty value, or an empty --value-file or
--meta-json argument, is refused (exit 2).

For a generic secret the value is the string read. Its metadata is replaced
with --meta key=value (repeatable) or --meta-json '<object>', not both.

For a webfetch-auth secret, a new value needs --type (bearer; basic with
--username; header with --header-name) together with --value-file, and
--url-prefix replaces the URL prefix the credential is sent to. --meta and
--meta-json are refused alongside these flags.

--protected (App secrets only) changes protection in the same update:
--protected turns it on, --protected=false turns it off (an App manager only;
the server judges). The flag alone is a valid update, and any protection
change is staged for a passkey ceremony like every change to a protected
secret: the command prints the request id and the ceremony URL, never
"updated". Turning protection off when it is already off is a no-change ask
that servers answer differently: some stage the clear like any protection
change (exit 5: the request id and ceremony URL print); others recognize
the no change, stage nothing, and the rest of the update applies (or
nothing does) with the normal updated line. Without the flag no protected
field is sent — a value or metadata update does not touch protection.
Protection never changes without a signed request: update --protected stages
its change for the ceremony above, and hadron secret protect changes
protection and nothing else. The request names the protected argument, so
--protected needs a server from 2026-11-21, when the argument reached server
main; without the flag the request is unchanged from before the flag existed,
so older servers keep accepting this command. A server with passkey signing
off still accepts the command, refusing only an explicit protection change.

The server checks the resulting value and metadata against the secret's kind;
a mismatch (say, a plain value for a webfetch-auth secret) exits 2.

Prints "✓ updated secret <name>  <kind>/<id>  owner <type> <id>"; --json gives
the same fields as hadron secret get.`,
		Example: `  printf '%s' "$NEW_TOKEN" | hadron secret update hrn:secret:alice:github-token --value-file -
  hadron secret update hrn:secret:alice:github-token --meta env=prod
  hadron secret update hrn:secret:acme.com:poll-auth --type bearer --value-file @token.txt
  hadron secret update hrn:secret:acme.com:poll-auth --url-prefix https://api.example.com/v2/`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := refuseDetachedProtectedValue(cmd, args); err != nil {
				return err
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed
			webFetch := changed("type") || changed("username") || changed("header-name") || changed("url-prefix")
			generic := changed("meta") || changed("meta-json")
			// Flag presence decides, not the value: an explicitly empty
			// --meta-json or --value-file (an unset variable) is refused
			// rather than silently ignored (#1489 LOCAL P2, BLIND P3-4).
			// With the empty --meta-json refused here, genericMetadata's
			// both-non-empty check covers --meta with --meta-json.
			if changed("meta-json") && strings.TrimSpace(metaJSON) == "" {
				return exitcode.Newf(exitcode.Usage, "--meta-json is empty (an unset variable?); pass a JSON object")
			}
			if changed("value-file") && strings.TrimSpace(valueFile) == "" {
				return exitcode.Newf(exitcode.Usage, "--value-file is empty (an unset variable?); pass -, @path or a path")
			}
			if webFetch && generic {
				return exitcode.Newf(exitcode.Usage, "--meta/--meta-json are generic-secret metadata; they cannot be combined with --type/--username/--header-name/--url-prefix")
			}
			if (changed("username") || changed("header-name")) && !changed("type") {
				return exitcode.Newf(exitcode.Usage, "--username and --header-name describe a new webfetch-auth value; pass --type and --value-file too")
			}
			if changed("type") && !changed("value-file") {
				return exitcode.Newf(exitcode.Usage, "--type describes a new value; pass --value-file (- reads stdin, or prompts without echo on a terminal)")
			}
			if !changed("value-file") && !generic && !changed("url-prefix") && !changed("protected") {
				return exitcode.Newf(exitcode.Usage, "nothing to update — pass --value-file, --meta/--meta-json, --url-prefix, or --protected")
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
				v, err := readNewValue(f, valueFile, changed("type"), authType, username, headerName)
				if err != nil {
					return err
				}
				value = &v
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// cli#2815: tri-state by presence — omitted sends no field (a
			// value/metadata update does not touch protection and stays
			// member-allowed), --protected/--protected=false stage turning it
			// on/off on an App secret (false is manager-only; the server words
			// both refusals, the CLI never pre-checks). The flag also chooses
			// the DOCUMENT (Eden's ruling on the round-5 BLIND P2, chat
			// #137754): omitted (and secret set) ride the pre-#2815
			// UpdateSecret, so servers from before the argument reached
			// server main (2026-11-21) keep accepting plain updates; given, it
			// rides UpdateSecretProtected, which names the argument.
			var prot *bool
			if changed("protected") {
				p := protected
				prot = &p
			}
			var fields *gen.SecretFields
			if prot == nil {
				resp, err := gen.UpdateSecret(cmd.Context(), client, args[0], metadata, value)
				if err != nil {
					return mapChange(err, "the update of secret "+output.SafeCell(args[0]))
				}
				if resp == nil || resp.UpdateSecret == nil {
					return exitcode.Newf(exitcode.Error, "server returned no secret")
				}
				fields = &resp.UpdateSecret.SecretFields
			} else {
				resp, err := gen.UpdateSecretProtected(cmd.Context(), client, args[0], metadata, value, prot)
				if err != nil {
					return mapChange(err, "the update of secret "+output.SafeCell(args[0]))
				}
				if resp == nil || resp.UpdateSecret == nil {
					return exitcode.Newf(exitcode.Error, "server returned no secret")
				}
				fields = &resp.UpdateSecret.SecretFields
			}
			dto := dtoFromFields(*fields)
			// A protection change on an App secret stages (hadron-server#3999
			// S4): the answer is SECRET_PROTECTED_NEEDS_PASSKEY, mapped above.
			// A returned secret is therefore unconfirmed for --protected, and
			// for --protected=false that the answer still reports protected —
			// the server neither staged nor cleared; never a success line,
			// same shape as secret protect (BLIND P1 on #2932). The one
			// returned secret that IS the answer is the no-op clear
			// (hadron-server#4749): clearing protection that is not on asks
			// for no change, so the server sends the secret back — unchanged
			// alone, or with the rest of the update applied — reporting
			// exactly the unprotected state this CLI asked for. A server that
			// cleared protection directly, with no ceremony, answers the same
			// way and prints as confirmed: the server judges protection, and
			// the answer reports the state asked for either way.
			if prot != nil && (*prot || fields.Protected) {
				return exitcode.Newf(exitcode.Unavailable,
					"the server answered with the secret instead of staging the protection change, which updateSecret(protected: …) does not do; it reports protected: %t. The outcome is not confirmed — any value or metadata sent alongside the flag may have been applied; the protection change was not: check it with: hadron secret get %s",
					fields.Protected, cmdutil.ShellQuote(output.SafeCell(args[0])))
			}
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
	cmd.Flags().BoolVar(&protected, "protected", false, "change protection in this update (App secrets only; staged for a signed request)")
	return cmd
}

// readNewValue reads a secret's new value for set and update --value-file
// (cor:sec:010:01: file or stdin, never argv) and shapes it: the string read,
// or a webfetch-auth object when --type is given. One path for both commands,
// so the two cannot drift (cli#2013).
func readNewValue(f *cmdutil.Factory, valueFile string, typed bool, authType, username, headerName string) (json.RawMessage, error) {
	if strings.TrimSpace(valueFile) == "" {
		return nil, exitcode.Newf(exitcode.Usage, "--value-file is empty (an unset variable?); pass -, @path or a path")
	}
	source := valueFile
	// "-" on a terminal would echo what is typed: prompt without echo
	// instead (ReadSecretInput's no-source branch).
	if source == "-" && f.IOStreams.IsInputTerminal() {
		source = ""
	}
	if typed {
		return webFetchAuthValue(f, source, authType, username, headerName)
	}
	material, err := readSecretMaterial(f.IOStreams, source, "New secret value")
	if err != nil {
		return nil, err
	}
	return jsonStringValue(material)
}
