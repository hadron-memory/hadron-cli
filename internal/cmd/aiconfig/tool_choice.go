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
		Long: `Read the server's decision on how a chat turn would offer its respond tool for
a provider and model pair that need not be saved anywhere: AUTO or FORCED. The
pair goes to the server exactly as typed (never trimmed, lower-cased or checked
against a list here), and an unknown pair answers FORCED, which is a normal
answer and not a warning. It reads no config rows and meters no usage, so it is
safe in a dry run or before saving a config. Any signed-in caller may ask; no
App or org is involved. The decision is Proposed in cor:dmo:050:10 and may
change.`,
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
