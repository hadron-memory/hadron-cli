package chat

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// toolChoiceDTO is `chat tool-choice --json` (cli#2020). The decision only:
// nothing is joined to it.
type toolChoiceDTO struct {
	ChatID     string  `json:"chatId"`
	ToolChoice *string `json:"toolChoice"`
}

func newCmdToolChoice(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "tool-choice <chat-id>",
		Short: "Read the tool-choice decision a chat's next turn will use",
		Long: `Read the server's decision on how this chat's next turn offers its respond
tool: AUTO or FORCED. The server decides it once, by the chat's stored choice,
else the App's chatbot config, else the default; this command only reads it and
keeps no list of its own. The decision is Proposed and may
change.

"tool choice: not resolved" (--json: toolChoice null) means the server resolved
nothing; it is a normal answer, not a failure, and no reason is implied.

A chat you may not read answers exactly as a missing one: exit 4, the same
message, with no hint which. Only the decision comes back: never a key, an id,
a name, a provider or a model.`,
		Example: `  hadron chat tool-choice chat_123
  hadron chat tool-choice chat_123 --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ChatEffectiveToolChoice(cmd.Context(), client, args[0])
			if err != nil {
				return api.MapError(err)
			}
			dto := toolChoiceDTO{ChatID: args[0]}
			// chatEffectiveToolChoice is nullable: null is the server's "no
			// effective choice", a normal answer rendered as "not resolved",
			// so this check stays. resp itself is never nil: the generated
			// client allocates it before the request (hadron-cli#2282).
			if resp.ChatEffectiveToolChoice != nil {
				name, nerr := cmdutil.ToolChoiceName(*resp.ChatEffectiveToolChoice)
				if nerr != nil {
					return nerr
				}
				dto.ToolChoice = &name
			}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, werr := io.WriteString(w, cmdutil.ToolChoiceText(dto.ToolChoice)+"\n")
				return werr
			})
		},
	}
}
