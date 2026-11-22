package secret

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		name, scope, owner, kind string
		valueFile                string
		metaPairs                []string
		metaJSON                 string
		authType                 string
		username                 string
		headerName               string
		urlPrefix                string
		protected                bool
	)
	cmd := &cobra.Command{
		Use:   "create --name <name> --scope <user|org|app> [--owner <ref>] --kind <generic|webfetch-auth> [--protected|--protected=false]",
		Short: "Create an owner-scoped encrypted secret",
		Long: `Create an encrypted secret and print its id. The value is never taken as an
argument:
  --value-file -       read stdin
  --value-file @path   read a file (a plain path works too)
  (omitted)            read piped stdin, or prompt without echo on a terminal
Trailing newlines are trimmed; an empty value is refused (exit 2).

--name is lowercase letters, digits and "-", up to 64 characters, not starting
with "-". It must be unique within the owner: a second secret with the same
name exits 2; delete the old one first.

--scope user makes it yours (--owner may be left out). --scope org needs
--owner <org id or URN> and an org ADMIN or OWNER role (else exit 8).
--scope app needs --owner <app id or URN>; the server decides who may create
it, and the value is stored as its first version. See
hadron secret for who can then use it.

--kind generic stores any string, which the agent's model can read (for a
user or org secret; a run never resolves an App secret). Add
optional metadata with --meta key=value (repeatable) or --meta-json '<object>',
not both.

--kind webfetch-auth stores a credential that is only injected into web
requests whose URL starts with --url-prefix (required; http or https, without
user info, query or fragment). --type says what the value is:
  bearer   a bearer token
  basic    a password; also pass --username
  header   a header value; also pass --header-name
--meta and --meta-json are refused for this kind.

--protected (App secrets only) asks for a protected secret from the start: the
creation itself is staged for a passkey ceremony, so the command prints the
request id and the ceremony URL and never says "created" — nothing exists
until a person signs it. --protected=false asks for the default outright
(unprotected). Protection never changes without a signed request: the
--protected creation above stages one, and hadron secret protect changes
protection for an existing secret. The request names the protected argument,
so a server from before the argument shipped (2026-11-14) refuses the request
even without the flag; a server with passkey signing off still accepts the
command, refusing only an explicit protection change.

Prints "✓ created secret <name>  <kind>/<id>  owner <type> <id>"; --json gives
id, ownerType, ownerId, name, kind, metadata and the created/updated fields.`,
		Example: `  printf '%s' "$GITHUB_TOKEN" | hadron secret create --name github-token --scope user --kind generic --value-file -
  hadron secret create --name poll-auth --scope org --owner hrn:org:acme.com \
    --kind webfetch-auth --type bearer --url-prefix https://api.example.com/ --value-file @token.txt`,
		Args: func(cmd *cobra.Command, args []string) error {
			if err := refuseDetachedProtectedValue(cmd, args); err != nil {
				return err
			}
			return cobra.NoArgs(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateName(name); err != nil {
				return err
			}
			ownerType, ownerRef, err := validateOwner(scope, owner)
			if err != nil {
				return err
			}
			if err := validateKind(kind); err != nil {
				return err
			}

			value, metadata, err := buildCreatePayload(f, kind, valueFile, metaPairs, metaJSON, authType, username, headerName, urlPrefix)
			if err != nil {
				return err
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// cli#2815: tri-state by presence — omitted sends no field (the
			// default unprotected create), --protected stages the whole creation
			// for a passkey (App scope only; the server words the refusal for
			// other scopes), --protected=false is the default shape said aloud.
			var prot *bool
			if cmd.Flags().Changed("protected") {
				p := protected
				prot = &p
			}
			resp, err := gen.CreateSecret(cmd.Context(), client, ownerType, ownerRef, name, kind, metadata, value, prot)
			if err != nil {
				return mapChange(err, "the creation of secret "+output.SafeCell(name))
			}
			if resp == nil || resp.CreateSecret == nil {
				return exitcode.Newf(exitcode.Error, "server returned no secret")
			}
			dto := dtoFromFields(resp.CreateSecret.SecretFields)
			// A protected create is staged, never applied (hadron-server#3999
			// S4): the answer is SECRET_PROTECTED_NEEDS_PASSKEY, mapped above.
			// A returned secret means the server did not do what this CLI
			// asked for, so the requested protection is unconfirmed — never a
			// success line; same shape as secret protect (BLIND P1 on #2932)
			// and the update guard. Both arms (BLIND P3 fold, round 4): a
			// true ask must stage, so ANY returned secret is wrong; a false
			// ask expects the secret back, and an answer reporting protected
			// did not get the unprotected create asked for.
			if prot != nil && *prot {
				return exitcode.Newf(exitcode.Unavailable,
					"the server answered with the created secret instead of staging it for a passkey, which createSecret(protected: true) does not do; it reports protected: %t. The secret exists, and the protection you asked for is not confirmed — it may be unprotected: check it with: hadron secret get %s",
					resp.CreateSecret.Protected, cmdutil.ShellQuote(output.SafeCell(dto.ID)))
			}
			if prot != nil && !*prot && resp.CreateSecret.Protected {
				return exitcode.Newf(exitcode.Unavailable,
					"the server answered with the created secret reporting protected: true, which createSecret(protected: false) does not do. The secret exists, and the protection you asked for is not confirmed — it may be protected: check it with: hadron secret get %s",
					cmdutil.ShellQuote(output.SafeCell(dto.ID)))
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeSecretLine(w, "✓ created", dto)
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "secret name (lowercase [a-z0-9-], max 64, not starting with -; required)")
	cmd.Flags().StringVar(&scope, "scope", "", "owner scope: user, org or app (required)")
	cmd.Flags().StringVar(&owner, "owner", "", "owner ID or URN (required except --scope user, where empty means caller)")
	cmd.Flags().StringVar(&kind, "kind", "", "secret kind: generic or webfetch-auth (required)")
	cmd.Flags().StringVar(&valueFile, "value-file", "", `read secret material from "-" stdin, "@file", or file path`)
	cmd.Flags().StringArrayVar(&metaPairs, "meta", nil, "generic metadata key=value (repeatable)")
	cmd.Flags().StringVar(&metaJSON, "meta-json", "", "generic metadata JSON object")
	cmd.Flags().StringVar(&authType, "type", "", "webfetch-auth type: bearer, basic, or header")
	cmd.Flags().StringVar(&username, "username", "", "webfetch-auth basic username")
	cmd.Flags().StringVar(&headerName, "header-name", "", "webfetch-auth header name")
	cmd.Flags().StringVar(&urlPrefix, "url-prefix", "", "webfetch-auth URL origin/path scope")
	cmd.Flags().BoolVar(&protected, "protected", false, "create it protected (App secrets only; staged for a signed request)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("scope")
	_ = cmd.MarkFlagRequired("kind")
	return cmd
}

func buildCreatePayload(
	f *cmdutil.Factory,
	kind, valueFile string,
	metaPairs []string,
	metaJSON, authType, username, headerName, urlPrefix string,
) (json.RawMessage, *json.RawMessage, error) {
	switch kind {
	case kindGeneric:
		if authType != "" || username != "" || headerName != "" || urlPrefix != "" {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--type/--username/--header-name/--url-prefix apply only to --kind webfetch-auth")
		}
		metadata, err := genericMetadata(metaPairs, metaJSON)
		if err != nil {
			return nil, nil, err
		}
		material, err := readSecretMaterial(f.IOStreams, valueFile, "Secret value")
		if err != nil {
			return nil, nil, err
		}
		value, err := jsonStringValue(material)
		return value, metadata, err
	case kindWebFetchAuth:
		if len(metaPairs) > 0 || metaJSON != "" {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--meta/--meta-json apply only to --kind generic; use --url-prefix for webfetch-auth metadata")
		}
		return webFetchAuthPayload(f, valueFile, authType, username, headerName, urlPrefix)
	default:
		return nil, nil, exitcode.Newf(exitcode.Usage, "--kind must be one of generic, webfetch-auth")
	}
}

func genericMetadata(pairs []string, raw string) (*json.RawMessage, error) {
	if len(pairs) == 0 && raw == "" {
		return nil, nil
	}
	if len(pairs) > 0 && raw != "" {
		return nil, exitcode.Newf(exitcode.Usage, "--meta and --meta-json are mutually exclusive")
	}
	if raw != "" {
		msg, err := cmdutil.ParseJSONArg(raw, "meta-json")
		if err != nil {
			return nil, err
		}
		return ensureJSONObject(msg, "meta-json")
	}
	msg, err := cmdutil.KeyValsToJSON(pairs, "meta")
	if err != nil {
		return nil, err
	}
	return ensureJSONObject(msg, "meta")
}

func webFetchAuthPayload(f *cmdutil.Factory, valueFile, authType, username, headerName, urlPrefix string) (json.RawMessage, *json.RawMessage, error) {
	if urlPrefix == "" {
		return nil, nil, exitcode.Newf(exitcode.Usage, "--url-prefix is required for --kind webfetch-auth")
	}
	valueJSON, err := webFetchAuthValue(f, valueFile, authType, username, headerName)
	if err != nil {
		return nil, nil, err
	}
	metadataJSON, err := webFetchAuthMetadata(urlPrefix)
	if err != nil {
		return nil, nil, err
	}
	return valueJSON, &metadataJSON, nil
}

// webFetchAuthMetadata is a webfetch-auth secret's metadata: the URL prefix
// its credential is injected into. The server validates it.
func webFetchAuthMetadata(urlPrefix string) (json.RawMessage, error) {
	return jsonObjectValue(map[string]string{"urlPrefix": urlPrefix})
}

// webFetchAuthValue reads the credential material for --type and builds the
// webfetch-auth value object. Shared by create and update.
func webFetchAuthValue(f *cmdutil.Factory, valueFile, authType, username, headerName string) (json.RawMessage, error) {
	var label string
	value := map[string]string{"type": authType}
	switch authType {
	case "bearer":
		label = "Bearer token"
		token, err := readSecretMaterial(f.IOStreams, valueFile, label)
		if err != nil {
			return nil, err
		}
		value["token"] = token
	case "basic":
		if username == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--username is required for --type basic")
		}
		label = fmt.Sprintf("Password for %s", username)
		password, err := readSecretMaterial(f.IOStreams, valueFile, label)
		if err != nil {
			return nil, err
		}
		value["username"] = username
		value["password"] = password
	case "header":
		if headerName == "" {
			return nil, exitcode.Newf(exitcode.Usage, "--header-name is required for --type header")
		}
		label = "Header value for " + headerName
		headerValue, err := readSecretMaterial(f.IOStreams, valueFile, label)
		if err != nil {
			return nil, err
		}
		value["name"] = headerName
		value["value"] = headerValue
	default:
		return nil, exitcode.Newf(exitcode.Usage, "--type must be one of bearer, basic, header")
	}
	return jsonObjectValue(value)
}
