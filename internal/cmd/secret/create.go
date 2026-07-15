package secret

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
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
	)
	cmd := &cobra.Command{
		Use:   "create --name <name> --scope <user|org|app|memory> [--owner <ref>] --kind <generic|webfetch-auth>",
		Short: "Create an owner-scoped encrypted secret",
		Long: `Create an encrypted secret. The secret value is never accepted as an argv
flag. Use --value-file - to read stdin, --value-file @path to read a file, or
omit --value-file and run interactively for a no-echo prompt.

For --kind generic, the value is stored as an opaque string. Metadata is
optional via --meta key=value or --meta-json '{"key":"value"}'.

For --kind webfetch-auth, pass --type bearer|basic|header and --url-prefix.
The secret material is the bearer token, basic password, or header value; the
server derives metadata.type from the payload.`,
		Example: `  printf '%s' "$TOKEN" | hadron secret create --name github-token --scope user --kind generic --value-file -
  hadron secret create --name poll-auth --scope app --owner acme.com::monitor \
    --kind webfetch-auth --type bearer --url-prefix https://api.example.com/`,
		Args: cobra.NoArgs,
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
			resp, err := gen.CreateSecret(cmd.Context(), client, ownerType, ownerRef, name, kind, metadata, value)
			if err != nil {
				return api.MapError(err)
			}
			if resp == nil || resp.CreateSecret == nil {
				return exitcode.Newf(exitcode.Error, "server returned no secret")
			}
			dto := dtoFromFields(resp.CreateSecret.SecretFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeSecretLine(w, "✓ created", dto)
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "secret name (lowercase [a-z0-9-], max 64; required)")
	cmd.Flags().StringVar(&scope, "scope", "", "owner scope: user, org, app, or memory (required)")
	cmd.Flags().StringVar(&owner, "owner", "", "owner ID or URN (required except --scope user, where empty means caller)")
	cmd.Flags().StringVar(&kind, "kind", "", "secret kind: generic or webfetch-auth (required)")
	cmd.Flags().StringVar(&valueFile, "value-file", "", `read secret material from "-" stdin, "@file", or file path`)
	cmd.Flags().StringArrayVar(&metaPairs, "meta", nil, "generic metadata key=value (repeatable)")
	cmd.Flags().StringVar(&metaJSON, "meta-json", "", "generic metadata JSON object")
	cmd.Flags().StringVar(&authType, "type", "", "webfetch-auth type: bearer, basic, or header")
	cmd.Flags().StringVar(&username, "username", "", "webfetch-auth basic username")
	cmd.Flags().StringVar(&headerName, "header-name", "", "webfetch-auth header name")
	cmd.Flags().StringVar(&urlPrefix, "url-prefix", "", "webfetch-auth URL origin/path scope")
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
	var label string
	value := map[string]string{"type": authType}
	switch authType {
	case "bearer":
		label = "Bearer token"
		token, err := readSecretMaterial(f.IOStreams, valueFile, label)
		if err != nil {
			return nil, nil, err
		}
		value["token"] = token
	case "basic":
		if username == "" {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--username is required for --type basic")
		}
		label = fmt.Sprintf("Password for %s", username)
		password, err := readSecretMaterial(f.IOStreams, valueFile, label)
		if err != nil {
			return nil, nil, err
		}
		value["username"] = username
		value["password"] = password
	case "header":
		if headerName == "" {
			return nil, nil, exitcode.Newf(exitcode.Usage, "--header-name is required for --type header")
		}
		label = "Header value for " + headerName
		headerValue, err := readSecretMaterial(f.IOStreams, valueFile, label)
		if err != nil {
			return nil, nil, err
		}
		value["name"] = headerName
		value["value"] = headerValue
	default:
		return nil, nil, exitcode.Newf(exitcode.Usage, "--type must be one of bearer, basic, header")
	}
	valueJSON, err := jsonObjectValue(value)
	if err != nil {
		return nil, nil, err
	}
	metadataJSON, err := jsonObjectValue(map[string]string{"urlPrefix": urlPrefix})
	if err != nil {
		return nil, nil, err
	}
	return valueJSON, &metadataJSON, nil
}
