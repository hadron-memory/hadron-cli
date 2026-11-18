package aiconfig

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/hadron-memory/hadron-cli/internal/api"
	"github.com/hadron-memory/hadron-cli/internal/api/gen"
	"github.com/hadron-memory/hadron-cli/internal/cmdutil"
	"github.com/hadron-memory/hadron-cli/internal/output"
)

// pairToolChoiceDTO is `ai-config tool-choice --json` (cli#2020).
type pairToolChoiceDTO struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	ToolChoice string `json:"toolChoice"`
}

func newCmdToolChoice(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "tool-choice <provider> <model>",
		Short: "Read the tool-choice decision for a provider and model pair",
		Long: `Ask the server how a chat turn would hand the model its "respond" tool (the
tool a chat reply is delivered through) for a provider and model pair. The pair
need not be saved anywhere. The answer is one word:
  AUTO    the model may decide to call the tool
  FORCED  the model is required to call it
An unknown pair answers FORCED; that is a normal answer, not a warning. The
pair goes to the server exactly as typed (not trimmed, lower-cased or checked
against a list). The command only reads the server's rule: no config is read,
no provider is called and nothing is metered, so it is safe before you save a
config. Any signed-in caller may ask; no App or org is involved. The rule may
change.

This predicts; it does not check. To see whether a saved config's model really
answers a chat turn, run 'hadron ai-config test <id> --chat-probe'. Text mode
prints the one word; --json prints {provider, model, toolChoice}.`,
		Example: `  hadron ai-config tool-choice anthropic claude-opus-4-8
  hadron ai-config tool-choice openai gpt-5 --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := f.GraphQLClient()
			if err != nil {
				return err
			}
			resp, err := gen.ChatToolChoiceFor(cmd.Context(), client, args[0], args[1])
			if err != nil {
				return api.MapError(err)
			}
			// chatToolChoiceFor is non-null, so there is no "no choice" answer
			// to handle; resp itself is never nil, because the generated client
			// allocates it before the request (hadron-cli#2282).
			name, nerr := cmdutil.ToolChoiceName(resp.ChatToolChoiceFor)
			if nerr != nil {
				return nerr
			}
			dto := pairToolChoiceDTO{Provider: args[0], Model: args[1], ToolChoice: name}
			return output.Write(f.IOStreams, f.JSON, dto, func(w io.Writer) error {
				_, werr := io.WriteString(w, cmdutil.ToolChoiceText(&name)+"\n")
				return werr
			})
		},
	}
}
