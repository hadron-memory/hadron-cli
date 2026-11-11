package app

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Khan/genqlient/graphql"
	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/exitcode"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// cli#1913 (hadron-server#3957, cor:dmo:050:10, cor:dmo:050:12): the App's
// chatbot AI config. The pin is an id of one of THIS App's own configs; what a
// chat may choose from is the server's narrow chatAiConfigChoices list (the
// App's own enabled configs plus a `default` marker), never the inherited
// App -> Agent -> Org -> Host ai-config list. Nothing here asks for, or
// prints, a key, a key preview, params or an endpoint.

// chatbotChoiceDTO is one row of the choices list, as the server returns it.
// The `default` row is a walk marker, not a config: it has value "default" and
// no provider, and the CLI never invents an id for it.
type chatbotChoiceDTO struct {
	Value              string  `json:"value"`
	Name               string  `json:"name"`
	IsDefault          bool    `json:"isDefault"`
	IsAppChatbotConfig bool    `json:"isAppChatbotConfig"`
	Provider           *string `json:"provider"`
	Model              *string `json:"model"`
}

// chatbotPinDTO is the pin as `app get` and `chatbot-ai-config get` show it.
// Name is the server-returned name of the config, taken from the choices list
// and null when the pin is not among the App's enabled choices (a stored
// disabled pin is skipped until it is enabled again): the CLI then says so and
// never claims the config is in use. Listed says whether it was found; it is
// nil when the choices read failed, because then membership is UNKNOWN and an
// outage must not read as a confirmed absence.
type chatbotPinDTO struct {
	ID     *string
	Name   *string
	Listed *bool
}

// readChatbotPin reads App.chatbotAiConfigId in its own operation so a server
// that predates #3957 still serves `app get`: with tolerateOldServer only that
// documented capability absence (every error a validation error on the field)
// comes back as nil. The explicit `chatbot-ai-config get` passes false: it must
// not claim a selection is unset when the capability was never read. A null App
// is NotFound for both, never an unset pin.
func readChatbotPin(ctx context.Context, client graphql.Client, ref, shown string, tolerateOldServer bool) (*string, error) {
	resp, err := gen.GetAppChatbotAiConfig(ctx, client, ref)
	if err != nil {
		if tolerateOldServer && api.IsGraphQLValidationOnlyFor(err, "chatbotAiConfigId") {
			return nil, nil
		}
		return nil, api.MapErrorKeepingCode(err)
	}
	if resp.App == nil {
		return nil, exitcode.Newf(exitcode.NotFound, "no App found for %q", shown)
	}
	return resp.App.ChatbotAiConfigId, nil
}

func readChatbotChoices(ctx context.Context, client graphql.Client, ref string) ([]chatbotChoiceDTO, error) {
	resp, err := gen.ChatAiConfigChoices(ctx, client, ref)
	if err != nil {
		return nil, api.MapErrorKeepingCode(err)
	}
	rows := []chatbotChoiceDTO{}
	for _, c := range resp.ChatAiConfigChoices {
		rows = append(rows, chatbotChoiceDTO{
			Value: c.Value, Name: c.Name, IsDefault: c.IsDefault,
			IsAppChatbotConfig: c.IsAppChatbotConfig, Provider: c.Provider, Model: c.Model,
		})
	}
	return rows, nil
}

// resolveChatbotPin names a pin from the choices list. A failed choices read
// leaves the name and Listed unknown (nil, with a note on stderr) rather than
// failing a read whose pin was already answered.
func resolveChatbotPin(ctx context.Context, f *cmdutil.Factory, client graphql.Client, ref string, id *string) chatbotPinDTO {
	pin := chatbotPinDTO{ID: id}
	f0 := false
	if id == nil {
		pin.Listed = &f0
		return pin
	}
	rows, err := readChatbotChoices(ctx, client, ref)
	if err != nil {
		fmt.Fprintf(f.IOStreams.ErrOut, "note: could not read the App's chatbot choices, so the config's name is not shown: %v\n", err)
		return pin
	}
	pin.Listed = &f0
	for _, r := range rows {
		if !r.IsDefault && r.Value == *id {
			name, t := r.Name, true
			pin.Name, pin.Listed = &name, &t
			break
		}
	}
	return pin
}

// chatbotPinText is the one text line of a pin.
func chatbotPinText(p chatbotPinDTO) string {
	switch {
	case p.ID == nil:
		return "-"
	case p.Listed == nil:
		return fmt.Sprintf("%s (name unavailable: the App's chatbot choices could not be read)", output.SafeCell(*p.ID))
	case *p.Listed && p.Name != nil:
		return fmt.Sprintf("%s (%s)", output.SafeCell(*p.Name), output.SafeCell(*p.ID))
	default:
		return fmt.Sprintf("%s (not among the App's enabled choices; a disabled config is skipped until enabled)", output.SafeCell(*p.ID))
	}
}

const chatbotAiConfigLong = `The App's chatbot AI config is one of the App's OWN AI configs, used for its
chats unless a chat stored its own choice (otherwise the default resolution
applies). It is stored as the config's id; the name shown is the one the
server returns in the App's chatbot choices, never looked up through the
Agent/Org/Host walk. A config that is disabled may be stored but is skipped
until it is enabled again, so a pin that is not among the enabled choices is
shown by id and is not called in use.

Whoever manages the App may set or clear it (a platform admin included, even
on a user-owned App: choosing a config reads, sets and tests no key). The
server decides; the CLI pre-judges nothing. No key, key preview or endpoint is
ever requested or printed. Every command takes the App explicitly (an App ID,
hrn:app:<root>:<slug> or <root>:<slug>); the global --app is not used.`

func newCmdChatbotAiConfig(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chatbot-ai-config <command>",
		Short: "Show, list, set or clear an App's chatbot AI config",
		Long:  chatbotAiConfigLong,
	}
	cmd.AddCommand(newCmdChatbotAiConfigGet(f))
	cmd.AddCommand(newCmdChatbotAiConfigList(f))
	cmd.AddCommand(newCmdChatbotAiConfigSet(f))
	cmd.AddCommand(newCmdChatbotAiConfigClear(f))
	return cmd
}

type chatbotAiConfigDTO struct {
	App    string  `json:"app"`
	ID     *string `json:"chatbotAiConfigId"`
	Name   *string `json:"chatbotAiConfigName"`
	Listed *bool   `json:"chatbotAiConfigListed"`
}

func newCmdChatbotAiConfigGet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <app-ref>",
		Short: "Show the App's chatbot AI config (id and name)",
		Long: `Show the App's chatbot AI config: its id and the server-returned name. JSON is
{app, chatbotAiConfigId, chatbotAiConfigName, chatbotAiConfigListed}; an unset
pin is null, and the name is null (chatbotAiConfigListed false) when the config
is not among the App's enabled choices; chatbotAiConfigListed is null when the
choices could not be read, so membership is unknown.

` + chatbotAiConfigLong,
		Example: `  hadron app chatbot-ai-config get acme.com:support
  hadron app chatbot-ai-config get acme.com:support --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			id, err := readChatbotPin(cmd.Context(), client, ref, args[0], false)
			if err != nil {
				return err
			}
			pin := resolveChatbotPin(cmd.Context(), f, client, ref, id)
			dto := chatbotAiConfigDTO{App: ref, ID: pin.ID, Name: pin.Name, Listed: pin.Listed}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "chatbot AI config: %s\n", chatbotPinText(pin))
				return err
			})
		},
	}
}

func newCmdChatbotAiConfigList(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:     "list <app-ref>",
		Aliases: []string{"ls"},
		Short:   "List what the App's chatbot AI config can be chosen from",
		Long: `List the App's own enabled AI configs plus one "default" entry, which stands
for the App -> Agent -> Org -> Host resolution at turn time and is not a config
(it has no provider and no id of its own: its value is the word default).
Any member of the App may read this. JSON is an array of
{value, name, isDefault, isAppChatbotConfig, provider, model} ([] when empty);
value is the config id to use with set. This is NOT hadron ai-config list,
which lists every config resolvable through the whole walk.

` + chatbotAiConfigLong,
		Example: `  hadron app chatbot-ai-config list acme.com:support
  hadron app chatbot-ai-config list acme.com:support --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			rows, err := readChatbotChoices(cmd.Context(), client, ref)
			if err != nil {
				return err
			}
			return output.Write(f.IOStreams, f.JSON, rows, func(w io.Writer) error {
				if len(rows) == 0 {
					_, err := fmt.Fprintln(w, "No chatbot AI config choices.")
					return err
				}
				t := output.NewTable(w, "VALUE", "NAME", "PROVIDER", "MODEL", "CHATBOT")
				for _, r := range rows {
					chatbot := ""
					if r.IsAppChatbotConfig {
						chatbot = "selected"
					}
					t.Row(output.SafeCell(r.Value), output.SafeCell(r.Name), output.SafeCell(optionalReadText(r.Provider)), output.SafeCell(optionalReadText(r.Model)), chatbot)
				}
				return t.Flush()
			})
		},
	}
}

func newCmdChatbotAiConfigSet(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "set <app-ref> <name-or-id>",
		Short: "Select one of the App's own AI configs as its chatbot config",
		Long: `Select the App's chatbot AI config. <name-or-id> is matched ONLY among the
App's own enabled choices (hadron app chatbot-ai-config list): an exact id, else
an exact name (an ambiguous name is a usage error listing the ids). A name never
matches an Agent, Org or Host config. A value the list does not carry is sent
as given and the server answers: a missing config, another App's and a deleted
one all get the same refusal. To store a config that is currently disabled, pass
its id. The word default is not a config and never matches by name: use clear to follow the default (a config that is itself named default is set by its id).

One mutation is sent and never retried. An empty value is a usage error with no
request. JSON is {app, chatbotAiConfigId, chatbotAiConfigName,
chatbotAiConfigListed} as stored.

` + chatbotAiConfigLong,
		Example: `  hadron app chatbot-ai-config set acme.com:support "Support bot"
  hadron app chatbot-ai-config set acme.com:support 01a1cae02ac9797eadf8f0a55273da70`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			want := args[1]
			if strings.TrimSpace(want) == "" {
				return exitcode.Newf(exitcode.Usage, "a config name or id is required; use clear to remove the selection")
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			rows, err := readChatbotChoices(cmd.Context(), client, ref)
			choicesRead := err == nil
			if err != nil {
				// The mutation is the authority; a manager who cannot read the
				// list can still set by id, so say so and carry on.
				fmt.Fprintf(f.IOStreams.ErrOut, "note: could not read the App's chatbot choices, so %q is sent as given: %v\n", want, err)
				rows = nil
			}
			id, err := matchOwnChoice(rows, want)
			if err != nil {
				return err
			}
			resp, err := gen.SetAppChatbotAiConfig(cmd.Context(), client, ref, &id)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp.SetAppChatbotAiConfig == nil {
				return exitcode.Newf(exitcode.Error, "the server did not return the App; read it with app chatbot-ai-config get before retrying")
			}
			pin := chatbotPinDTO{ID: resp.SetAppChatbotAiConfig.ChatbotAiConfigId}
			if choicesRead {
				f0 := false
				pin.Listed = &f0
				for _, r := range rows {
					if !r.IsDefault && pin.ID != nil && r.Value == *pin.ID {
						name, t := r.Name, true
						pin.Name, pin.Listed = &name, &t
					}
				}
			}
			dto := chatbotAiConfigDTO{App: ref, ID: pin.ID, Name: pin.Name, Listed: pin.Listed}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ set chatbot AI config of app %s\n  chatbot AI config: %s\n", output.SafeCell(ref), chatbotPinText(pin))
				return err
			})
		},
	}
}

// matchOwnChoice resolves want among the App's own choices: an exact id, else
// an exact name. The default row is a marker and never matches. No match
// returns want itself for the server to refuse (or accept, for a stored
// disabled config); the word default with no such config is a usage error.
func matchOwnChoice(rows []chatbotChoiceDTO, want string) (string, error) {
	for _, r := range rows {
		if !r.IsDefault && r.Value == want {
			return r.Value, nil
		}
	}
	// The word default is reserved: it never selects a config by NAME, even
	// when an own config is called that. Such a config is set by its id.
	if strings.EqualFold(want, "default") {
		return "", exitcode.Newf(exitcode.Usage, "default is the resolution marker, not a config; use clear to follow the default (a config named default is set by its id)")
	}
	var ids []string
	for _, r := range rows {
		if !r.IsDefault && r.Name == want {
			ids = append(ids, r.Value)
		}
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return want, nil
	default:
		return "", exitcode.Newf(exitcode.Usage, "%q names more than one of the App's configs; pass an id: %s", want, strings.Join(ids, ", "))
	}
}

func newCmdChatbotAiConfigClear(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "clear <app-ref>",
		Short: "Remove the App's chatbot AI config selection",
		Long: `Clear the App's chatbot AI config selection, so its chats follow the default
resolution again. Only the selection is removed: no AI config and no key is
deleted or changed. One mutation is sent and never retried. JSON is {app,
chatbotAiConfigId, chatbotAiConfigName, chatbotAiConfigListed} with a null id.

` + chatbotAiConfigLong,
		Example: `  hadron app chatbot-ai-config clear acme.com:support`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := requiredReadAppRef(args[0])
			if err != nil {
				return err
			}
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			// A nil configId is sent as an explicit null: that is the clear.
			resp, err := gen.SetAppChatbotAiConfig(cmd.Context(), client, ref, nil)
			if err != nil {
				return api.MapErrorKeepingCode(err)
			}
			if resp.SetAppChatbotAiConfig == nil {
				return exitcode.Newf(exitcode.Error, "the server did not return the App; read it with app chatbot-ai-config get before retrying")
			}
			notListed := false
			dto := chatbotAiConfigDTO{App: ref, ID: resp.SetAppChatbotAiConfig.ChatbotAiConfigId, Listed: &notListed}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, err := fmt.Fprintf(w, "✓ cleared chatbot AI config of app %s\n", output.SafeCell(ref))
				return err
			})
		},
	}
}
