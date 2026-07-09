package aiconfig

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// createFileSpec is the JSON object accepted by `ai-config create --file`.
// Every field is optional and mirrors a create flag; an explicit flag on the
// command line overrides the file's value. Its point is to keep the API key
// (and the rest of the config) out of argv and shell history.
type createFileSpec struct {
	App      *string          `json:"app"`
	Agent    *string          `json:"agent"`
	Org      *string          `json:"org"`
	Name     *string          `json:"name"`
	Provider *string          `json:"provider"`
	Model    *string          `json:"model"`
	APIKey   *string          `json:"apiKey"`
	Params   *json.RawMessage `json:"params"`
	Enabled  *bool            `json:"enabled"`
}

func newCmdCreate(f *cmdutil.Factory) *cobra.Command {
	var (
		app, agent, org       string
		name, provider, model string
		apiKey, file          string
		params                []string
		disabled              bool
	)
	cmd := &cobra.Command{
		Use:   "create ((--app | --agent | --org <id-or-urn>) --name <n> --provider <p> --model <m> | --file <path>)",
		Short: "Create an AI service config",
		Long: `Create an AI service config owned by an App, Agent, or Organization.

The API key is a secret. To keep it out of argv and shell history you can:
  - pass it on stdin with --api-key - ; or
  - put the whole config, key included, in a JSON file and pass --file <path>
    (--file - reads the JSON from stdin).

--file seeds every field; an explicit flag overrides the file's value. The file
keys mirror the flags: app, agent, org, name, provider, model, apiKey, params
(an object), enabled. Omit the key entirely to store a key-less config and set
it later with 'ai-config update'. --param sets provider knobs (repeatable) and,
when given, replaces the file's params object.`,
		Example: `  printf '%s' "$KEY" | hadron ai-config create --app acme.com:juno-app \
    --name default --provider anthropic --model claude-opus-4-8 --api-key -
  hadron ai-config create --file config.json
  hadron ai-config create --org acme.com --name fast --provider openai \
    --model gpt-4o-mini --param maxTokens=4096`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			changed := cmd.Flags().Changed

			var spec createFileSpec
			if changed("file") {
				if file == "-" && changed("api-key") && apiKey == "-" {
					return exitcode.Newf(exitcode.Usage, "--file - and --api-key - both read stdin; put the key in the file")
				}
				s, err := loadCreateFileSpec(file, f.IOStreams.In)
				if err != nil {
					return err
				}
				spec = *s
			}

			// Owner is a single choice (exactly one of app/agent/org), so treat
			// it as a unit: any owner flag on the command line replaces the
			// file's owner selection wholesale. Merging field-by-field could
			// pair a file `agent` with a flag `--app` and trip the
			// mutual-exclusion check.
			ownerApp, ownerAgent, ownerOrg := deref(spec.App), deref(spec.Agent), deref(spec.Org)
			if changed("app") || changed("agent") || changed("org") {
				ownerApp, ownerAgent, ownerOrg = app, agent, org
			}
			ownerType, ownerID, err := resolveOwner(ownerApp, ownerAgent, ownerOrg)
			if err != nil {
				return err
			}

			nameVal := strOr(spec.Name, name, changed("name"))
			providerVal := strOr(spec.Provider, provider, changed("provider"))
			modelVal := strOr(spec.Model, model, changed("model"))
			if nameVal == "" || providerVal == "" || modelVal == "" {
				return exitcode.Newf(exitcode.Usage, "--name, --provider, and --model are required (via flags or --file)")
			}

			// --param, when given, replaces the file's params object wholesale.
			var paramsJSON *json.RawMessage
			if changed("param") {
				paramsJSON, err = cmdutil.KeyValsToJSON(params, "param")
				if err != nil {
					return err
				}
			} else if spec.Params != nil {
				paramsJSON = spec.Params
			}

			// --api-key overrides the file's key; either way the key stays out
			// of argv. An empty value stores a key-less config.
			var keyArg *string
			switch {
			case changed("api-key"):
				k, err := resolveSecret(apiKey, f.IOStreams.In)
				if err != nil {
					return err
				}
				if k != "" {
					keyArg = &k
				}
			case spec.APIKey != nil && *spec.APIKey != "":
				keyArg = spec.APIKey
			}

			// enabled: default true; the file may disable; --disabled forces it.
			enabled := true
			if spec.Enabled != nil {
				enabled = *spec.Enabled
			}
			if changed("disabled") {
				enabled = !disabled
			}

			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}

			resp, err := gen.CreateAiServiceConfig(cmd.Context(), client, nameVal, providerVal, modelVal, ownerID, ownerType, keyArg, &enabled, paramsJSON)
			if err != nil {
				return api.MapError(err)
			}
			if resp.CreateAiServiceConfig == nil {
				return exitcode.Newf(exitcode.Error, "server returned no config")
			}
			dto := dtoFromFields(resp.CreateAiServiceConfig.AiServiceConfigFields)
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				return writeConfigLine(w, "✓ created", dto)
			})
		},
	}
	cmd.Flags().StringVar(&app, "app", "", "owning App (ID or URN)")
	cmd.Flags().StringVar(&agent, "agent", "", "owning Agent (ID or URN)")
	cmd.Flags().StringVar(&org, "org", "", "owning Organization (ID or URN)")
	cmd.Flags().StringVar(&name, "name", "", "config name (1-64 chars, [a-z0-9_-], unique per owner)")
	cmd.Flags().StringVar(&provider, "provider", "", "provider id (anthropic, openai, glm, bedrock)")
	cmd.Flags().StringVar(&model, "model", "", "model identifier")
	cmd.Flags().StringVar(&apiKey, "api-key", "", `provider API key ("-" reads stdin)`)
	cmd.Flags().StringVar(&file, "file", "", `read the config (key included) from a JSON file ("-" reads stdin)`)
	cmd.Flags().StringArrayVar(&params, "param", nil, "provider param key=value (repeatable; value parsed as JSON or string)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "create the config disabled")
	return cmd
}

// loadCreateFileSpec reads and parses the --file JSON object. "-" reads stdin
// so the whole config (API key included) can be piped in, never touching argv.
// Unknown keys are rejected to catch typos (e.g. "api_key" for "apiKey" — note
// Go matches field names case-insensitively, so "apikey" would still bind).
func loadCreateFileSpec(path string, stdin io.Reader) (*createFileSpec, error) {
	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "reading --file: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var spec createFileSpec
	if err := dec.Decode(&spec); err != nil {
		return nil, exitcode.Newf(exitcode.Usage, "parsing --file: %v", err)
	}
	// The file must be exactly one JSON object — reject trailing content (e.g.
	// two concatenated objects) rather than silently ignoring it.
	if _, err := dec.Token(); err != io.EOF {
		return nil, exitcode.Newf(exitcode.Usage, "parsing --file: unexpected trailing data after the JSON object")
	}
	// params, when present, must be a JSON object (the help says so, and it maps
	// to the provider-params object). A JSON null means "unset" → drop it.
	if spec.Params != nil {
		p := bytes.TrimSpace(*spec.Params)
		switch {
		case len(p) == 0, bytes.Equal(p, []byte("null")):
			spec.Params = nil
		case p[0] != '{':
			return nil, exitcode.Newf(exitcode.Usage, `parsing --file: "params" must be a JSON object`)
		}
	}
	return &spec, nil
}

// strOr resolves a create field: the flag value when it was set on the command
// line, else the file's value, else empty.
func strOr(fileVal *string, flagVal string, flagChanged bool) string {
	if flagChanged {
		return flagVal
	}
	if fileVal != nil {
		return *fileVal
	}
	return ""
}

// deref returns the pointed-to string, or "" for a nil pointer.
func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
